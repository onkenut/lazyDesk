# lazyDesk WebSocket 视频流中断根因分析

> 分析日期：2026-07-09 · 代码版本：当前 main 分支 · 分析范围：全部 17 个源文件

---

## 目录

1. [现象描述](#1-现象描述)
2. [架构回顾：WS + WebRTC 视频管线](#2-架构回顾ws--webrtc-视频管线)
3. [根因一（P0）：60 秒 WebSocket 读取超时 —— 无心跳保活](#3-根因一p060-秒-websocket-读取超时--无心跳保活)
4. [根因二（P0）：ffmpeg 崩溃后 IsRunning() 返回过期 true](#4-根因二p0ffmpeg-崩溃后-isrunning-返回过期-true)
5. [根因三（P0）：新 WebRTC 对等端缺少 SPS/PPS/IDR 关键帧](#5-根因三p0新-webrtc-对等端缺少-spsppsidr-关键帧)
6. [根因四（P1）：CreatePeerConnection 中的赛道条件](#6-根因四p1createpeerconnection-中的赛道条件)
7. [根因五（P1）：WebRTC Disconnected 立即关闭 WS，阻止 ICE 重启](#7-根因五p1webrtc-disconnected-立即关闭-ws阻止-ice-重启)
8. [根因六（P2）：客户端无心跳机制](#8-根因六p2客户端无心跳机制)
9. [根因七（P2）：多连接共享 Capture，轨道被替换](#9-根因七p2多连接共享-capture轨道被替换)
10. [总结与修复优先级](#10-总结与修复优先级)
11. [附录：关键代码引用](#11-附录关键代码引用)

---

## 1. 现象描述

**用户症状**：WebSocket 连接建立后，视频流播放一段时间后停止（画面冻结），不再有新的视频帧到达浏览器。有时可恢复（重连后），有时需要刷新页面。

**触发条件**：
- 纯观看模式（无鼠标/键盘操作）下，约 60 秒后断流
- ffmpeg 进程异常退出后，重连也无法恢复视频
- 网络短暂波动后，视频永久中断

---

## 2. 架构回顾：WS + WebRTC 视频管线

```
浏览器                          Go 服务端
  |                                |
  |── WS /ws ────────────────────►|  HTTP Upgrade
  |◄── server_ready ──────────────|  CreatePeerConnection
  |── WS offer(SDP) ────────────►|  handleOffer → CreateAnswer
  |◄── WS answer(SDP) ───────────|
  |── WS candidate(ICE) ────────►|
  |◄── WS candidate(ICE) ────────|
  |       WebRTC Connected         |
  |                                |  startCapture() → ffmpeg 启动
  |◄══ RTP H264 视频流 ═══════════|  ffmpeg → NAL 解析 → sendCh → sendLoop → Pion Track
  |── WS mouse/key commands ─────►|  控制指令
  |                                |
  [如果客户端 60s 不发消息]         |
  |       ReadDeadline 超时        |  ReadMessage 返回 error
  |       WS 关闭                  |  closeConn() → defer 清理
  |                                |  connCount-- → 0 → capture.Stop()
```

**关键文件**：
| 文件 | 职责 |
|------|------|
| `websocket/handler.go` | WS 信令 + 控制指令分发 + 生命周期管理 |
| `ffmpeg/capture.go` | ffmpeg 子进程管理 + H264 NAL 解析 + 帧缓冲发送 |
| `webrtc/manager.go` | PeerConnection 创建/销毁 + 视频轨道管理 |
| `web/ws.js` | 客户端 WS（含指数退避重连） |
| `web/webrtc.js` | 客户端 WebRTC + Canvas 渲染 |
| `main.go` | 启动引导 + 模块连接 |

---

## 3. 根因一（P0）：60 秒 WebSocket 读取超时 —— 无心跳保活

### 3.1 问题代码

**`websocket/handler.go:216-223`**：
```go
// 主消息循环 — I3: 60s 读超时防 goroutine 泄漏
for {
    conn.SetReadDeadline(time.Now().Add(60 * time.Second))
    _, message, err := conn.ReadMessage()
    if err != nil {
        if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
            log.Printf("WebSocket error: %v", err)
        }
        break
    }
    // ... 消息处理
}
```

### 3.2 触发路径

```
时间线：
T+0s:   用户连接成功，视频开始播放
T+1~59s: 用户纯观看，不发送任何控制指令（无触摸、无键盘、无鼠标）
T+60s:  ReadDeadline 到期，ReadMessage() 返回 i/o timeout 错误
         → for 循环 break
         → defer closeConn() 触发
         → defer pc.Close()  触发
         → defer connCount-- → 0 → capture.Stop()
         → 视频流永久中断
```

### 3.3 根因分析

1. **服务端无心跳发送**：`gorilla/websocket` 库支持 `SetPingHandler` 和 `WriteMessage(websocket.PingMessage, ...)` 来发送协议级 Ping 帧，但代码中**完全没有配置**。浏览器收到 Ping 帧后会自动回复 Pong，不会触发 ReadDeadline。

2. **客户端无心跳发送**：`web/ws.js` 中 `WSClient` 类没有任何周期性消息发送逻辑。浏览器 WebSocket API 的 `onping` 事件也未被监听。

3. **ReadDeadline 设计意图与副作用**：注释标注 "I3: 防 goroutine 泄漏"，即防止死连接永久占用 goroutine。但 60 秒对于纯视频观看场景（无交互）来说太短——在实际使用中，用户可能长时间观看而不操作。

4. **关闭链路级联**：
   ```
   ReadMessage 超时
   → WS 关闭 (closeConn)
   → WebRTC PeerConnection.Close() (defer)
   → connCount 减 1
   → 如果 connCount == 0 → capture.Stop()
   → ffmpeg 进程被 kill
   → 所有视频数据终止
   ```

### 3.4 证据

- 服务端日志会出现 `"WebSocket error: ..."` 或正常关闭提示
- 客户端日志会出现 `"WebSocket closed (code: 1006)"`（异常关闭码）
- 客户端会触发 `ws.js:80` 的重连逻辑（指数退避）
- 重连后如果 ffmpeg 已被 stop，且 `IsRunning()` 返回 true（见根因二），则无视频

---

## 4. 根因二（P0）：ffmpeg 崩溃后 IsRunning() 返回过期 true

### 4.1 问题代码

**`ffmpeg/capture.go:357-365`**：
```go
func (c *Capture) IsRunning() bool {
    c.mu.Lock()
    defer c.mu.Unlock()
    select {
    case <-c.stopCh:
        return false
    default:
        return c.cmd != nil  // ⚠️ 仅检查 cmd 是否为 nil，不检查进程是否存活
    }
}
```

**`ffmpeg/capture.go:165-201` (readLoop)**：
```go
func (c *Capture) readLoop(gen int64) {
    defer func() {
        c.flushAU(gen)
        if c.stdout != nil {
            c.stdout.Close()
        }
        if atomic.LoadInt64(&c.generation) == gen {
            c.mu.Lock()
            if c.cmd != nil {
                c.cmd.Process.Kill()  // ⚠️ Kill 进程但不设置 c.cmd = nil
            }
            c.mu.Unlock()
        }
    }()
    // ...
    n, err := reader.Read(buf)
    if err != nil {
        // ffmpeg 崩溃/管道断开 → return → defer 执行
        return
    }
}
```

### 4.2 触发路径

```
ffmpeg 进程因任何原因崩溃（GPU 驱动问题、编码器错误、内存不足等）
→ stdout 管道断开
→ readLoop 中 reader.Read() 返回错误
→ readLoop return
→ defer: c.cmd.Process.Kill() 执行（对已死进程无害）
→ ❌ c.cmd 字段仍为非 nil
→ ❌ c.stopCh 仍为 open 状态
→ IsRunning() 返回 true ← 状态过期！

客户端重连：
→ HandleWebSocket 被调用
→ WebRTC Connected → startCapture()
→ startCapture 中 if !h.capture.IsRunning() → false（过期 true）
→ 跳过 c.capture.Start()
→ NO FFMPEG，NO VIDEO！
```

### 4.3 根因分析

`IsRunning()` 使用两个条件判断运行状态：
1. `stopCh` 是否已关闭（`Stop()` 调用后关闭）
2. `c.cmd != nil`（是否有过 ffmpeg 进程对象）

但这两个条件都**不反映进程的实际存活状态**。在 Windows 上，`os.Process` 没有直接的健康检查方法。正确的做法是：
- 在 `readLoop` 的 defer 中设置 `c.cmd = nil` 来标记进程已退出
- 或者使用一个独立的 `atomic.Bool` 标志位 `running`，在 `readLoop` defer 中设为 false

### 4.4 影响范围

- ffmpeg 的**任何**异常退出（包括 GPU 驱动崩溃、编码器内部错误、管道写入错误）都会触发此问题
- 一旦触发，**所有后续重连都无法恢复视频**，必须重启服务端
- 由于 `IsRunning()` 返回 true，`startCapture()` 是空操作，用户看到的症状是：连接成功但视频黑屏/卡住

---

## 5. 根因三（P0）：新 WebRTC 对等端缺少 SPS/PPS/IDR 关键帧

### 5.1 问题场景

```
场景 A：重连时 ffmpeg 仍在运行（第一个连接没断）
  ffmpeg 已运行 30 秒，正在输出第 900 帧
  → 新 WebSocket 连接建立
  → 新 PeerConnection 创建，新 videoTrack 创建
  → startCapture() → IsRunning() → true → 跳过
  → ffmpeg 当前输出的 H264 帧是 P 帧（增量帧）
  → 新 PeerConnection 收到的第一个帧是 P 帧
  → 浏览器没有 SPS/PPS/IDR → 无法解码 → 黑屏

场景 B：重连时 ffmpeg 重新启动
  ffmpeg 启动 → 第一帧输出 SPS/PPS/IDR
  → sendLoop 发送到 WebRTC track
  → 但 WebRTC 连接可能还未完全建立（ICE 仍在协商）
  → 浏览器错过初始 SPS/PPS/IDR
  → 后续帧无法解码
```

### 5.2 根因分析

1. **ffmpeg 启动时输出 SPS/PPS 仅一次**：H264 编码器在启动时输出 SPS（Sequence Parameter Set）和 PPS（Picture Parameter Set），随后开始输出 IDR（即时解码刷新）帧和 P 帧。如果新的对等端在 SPS/PPS 之后才加入，缺少解码所需的参数集。

2. **无 SPS/PPS 缓存**：服务端 Go 代码（`ffmpeg/capture.go`）在 `access unit aggregation` 中将 SPS（NAL type 7）作为 AU 边界处理，但**不缓存 SPS/PPS 用于重发给新对等端**。

3. **无 IDR 请求机制**：Pion WebRTC 的 `TrackLocalStaticSample` 是"静态"轨道——它只是将给定的 sample 写入，没有请求关键帧的机制。WebRTC 协议中的 PLI（Picture Loss Indication）可以请求 IDR，但当前代码**未注册 RTCP PLI 拦截器**。

4. **`-g` 参数未设置**：ffmpeg 命令中未指定 `-g`（GOP 大小），默认值为 250 帧（约 8 秒）。如果重连刚好发生在两个 IDR 帧之间，最长需要等 8 秒才能开始解码。但实际上由于缺少 SPS/PPS，即使等到 IDR 也可能无法解码。

### 5.3 证据

- 浏览器控制台可能出现解码错误
- 浏览器 WebRTC internals (`chrome://webrtc-internals`) 显示 `bytesReceived` 增加但无解码输出
- 画面表现为：连接成功、状态正常、但 canvas 全黑

---

## 6. 根因四（P1）：CreatePeerConnection 中的赛道条件

### 6.1 问题代码

**`webrtc/manager.go:29-39`**：
```go
func (m *Manager) CreatePeerConnection() (*webrtc.PeerConnection, error) {
    m.mu.Lock()
    defer m.mu.Unlock()

    // N5: 先关闭旧 PeerConnection，防止引用泄漏
    if m.peerConn != nil {
        m.peerConn.Close()
        m.peerConn = nil
        m.videoTrack = nil    // ⚠️ 将 track 置空
        m.audioTrack = nil
    }
    // ... 创建新 track ...
    m.videoTrack = videoTrack  // ⚠️ 设置新 track
}
```

**`webrtc/manager.go:144-153`**：
```go
func (m *Manager) WriteVideoSample(sample media.Sample) error {
    m.mu.Lock()
    track := m.videoTrack
    m.mu.Unlock()

    if track == nil {
        return nil  // ⚠️ 静默丢帧！
    }
    return track.WriteSample(sample)
}
```

### 6.2 触发路径

```
ffmpeg sendLoop 正在发送第 N 帧
→ 调用 m.WriteVideoSample(sample)
→ m.mu.Lock() → track = m.videoTrack → m.mu.Unlock()
                                         ↑
              此时重连发生：CreatePeerConnection() 获取 m.mu
              → m.videoTrack = nil     ← 旧 track 被置空
              → 创建新 PC，设置新 m.videoTrack
                                         ↓
→ if track == nil → return nil  ← 第 N 帧静默丢失
→ 第 N+1 帧 → m.mu.Lock() → track = m.videoTrack (新 track) → WriteSample
```

### 6.3 根因分析

这是一个**窄窗口赛道条件**：
1. ffmpeg `sendLoop` 持续调用 `WriteVideoSample()`
2. `CreatePeerConnection()` 先销毁旧轨道再创建新轨道
3. 在 `m.videoTrack = nil` 和 `m.videoTrack = newTrack` 之间的窗口内，所有帧都被静默丢弃（`return nil` 无日志）
4. 新轨道收到的是**中途的 H264 流**，缺少 SPS/PPS/IDR（见根因三）

**严重程度**：中等。窗口时间很短（微秒级），但如果在窗口内丢失的是 IDR 帧，会导致后续所有帧无法解码。

---

## 7. 根因五（P1）：WebRTC Disconnected 立即关闭 WS，阻止 ICE 重启

### 7.1 问题代码

**`websocket/handler.go:202-213`**：
```go
pc.OnConnectionStateChange(func(state pionwebrtc.PeerConnectionState) {
    log.Printf("WebRTC state: %s", state.String())
    switch state {
    case pionwebrtc.PeerConnectionStateConnected:
        startCapture()
    case pionwebrtc.PeerConnectionStateFailed,
         pionwebrtc.PeerConnectionStateDisconnected,  // ⚠️ 立即关闭
         pionwebrtc.PeerConnectionStateClosed:
        closeConn()
    }
})
```

### 7.2 触发路径

```
正常视频播放中...
→ 网络短暂波动（WiFi 干扰、切换 AP 等）
→ WebRTC ICE 状态变为 "disconnected"
→ ⚠️ OnConnectionStateChange 立即调用 closeConn()
→ WS 关闭
→ pc.Close()
→ connCount-- → 0 → capture.Stop()
→ ffmpeg 停止

本可以自动恢复：
→ ICE 层会在 5-15 秒内尝试 ICE 重启
→ 如果网络恢复，状态会变回 "connected"
→ 视频继续，用户无感知
→ ❌ 但由于 WS 已被关闭，ICE 重启无法完成
```

### 7.3 根因分析

WebRTC 的 `PeerConnectionState` 有三种"不正常"状态：

| 状态 | 含义 | 是否可恢复 |
|------|------|-----------|
| `disconnected` | ICE 暂时失去连接 | ✅ 是的，ICE 会自动重启 |
| `failed` | ICE 彻底失败 | ❌ 不可恢复 |
| `closed` | 主动关闭 | ❌ 不可恢复 |

**当前代码将三种状态等同处理**——全部立即关闭 WS。正确做法是：
- `disconnected`：等待一段时间（如 30 秒），给 ICE 重启机会
- `failed`：立即关闭
- `closed`：立即关闭（主动关闭，无需重试）

更多细节参见 [Pion ICE 重连文档](https://pion-webrtc.mintlify.app/advanced/connection-lifecycle)。

---

## 8. 根因六（P2）：客户端无心跳机制

### 8.1 问题代码

**`web/ws.js`**：整个 `WSClient` 类没有任何周期性消息发送逻辑。

```javascript
// ws.js 完整代码中：
// - 无 setInterval 定时器
// - 无 WebSocket ping/pong 处理
// - 无应用层心跳消息
```

### 8.2 根因分析

虽然根本问题是服务端的 60 秒 ReadDeadline（根因一），但客户端缺少心跳机制**加剧了问题**：

1. **浏览器 WebSocket API 支持 Ping/Pong**：`ws.onping` 事件可以接收服务端 Ping 并自动回复 Pong。但当前服务端不发送 Ping，所以这个机制无效。

2. **应用层心跳可作为替代**：客户端可以每 30 秒发送一个 `{"type":"ping"}` 消息来重置 ReadDeadline。当前没有这个机制。

3. **与重连逻辑的交互**：客户端的 `_scheduleReconnect()` 使用指数退避（1s→2s→4s→8s→10s max），如果网络问题导致 WS 断开但客户端不知道（因为 ReadDeadline 是服务端行为），重连延迟会增加。

---

## 9. 根因七（P2）：多连接共享 Capture，轨道被替换

### 9.1 问题代码

**`main.go:33-41`**：
```go
capture := ffmpeg.NewCapture(cfg)           // 单例
rtcManager := webrtc.NewManager(cfg)        // 单例
capture.SetVideoTrack(rtcManager)           // 全局绑定
wsHandler := websocket.NewHandler(rtcManager, capture, cmdHandler)
```

**`webrtc/manager.go:144-153`** (WriteVideoSample)：
```go
func (m *Manager) WriteVideoSample(sample media.Sample) error {
    m.mu.Lock()
    track := m.videoTrack  // ← 始终是最新的 track
    m.mu.Unlock()
    // ...
}
```

### 9.2 触发路径

```
客户端 A 连接 → PC_A 创建 → m.videoTrack = track_A → 视频正常
客户端 B 连接 → PC_B 创建 → m.videoTrack = track_B → B 视频正常
→ ffmpeg 的所有帧现在写入 track_B
→ track_A 不再接收任何数据
→ 客户端 A 画面冻结
```

### 9.3 根因分析

这是**有意为之的设计**——lazyDesk 面向单用户场景，只有一个 Capture 实例和一个 Manager 实例，所有连接共享同一个 ffmpeg 进程。

当第二个客户端连接时：
1. `CreatePeerConnection` 创建新的 PC 和 track
2. 旧的 `m.videoTrack` 被替换为新的
3. ffmpeg 的所有后续帧写入新 track
4. 旧客户端失去视频数据

这本身不是 bug，但**如果用户期望多客户端同时观看**，这就成为问题。此外，在重连场景中，旧的 WebSocket 连接可能尚未完全清理（defer 还未执行），导致新连接和旧连接争用 track。

---

## 10. 总结与修复优先级

### 10.1 根因汇总

| # | 根因 | 严重度 | 影响 | 频率 |
|---|------|--------|------|------|
| 1 | WS 读取超时 60s，无心跳 | **P0** | 纯观看场景必现 | 每次 60s 无操作 |
| 2 | ffmpeg 崩溃后 IsRunning() 过期 | **P0** | 一旦崩溃永久无法恢复 | ffmpeg 异常退出时 |
| 3 | 新 PeerConnection 缺少 SPS/PPS/IDR | **P0** | 重连后黑屏 | 每次重连 |
| 4 | CreatePeerConnection 赛道丢帧 | **P1** | 重连瞬间丢帧 | 重连时概率性 |
| 5 | Disconnected 立即关闭 WS | **P1** | 网络波动导致断连 | 网络不稳定时 |
| 6 | 客户端无心跳 | **P2** | 加剧根因 1 | 持续 |
| 7 | 多连接轨道替换 | **P2** | 旧连接视频冻结 | 多客户端时 |

### 10.2 推荐修复顺序

#### 第一步：修复心跳保活（解决根因 1 + 6）

**方案**：服务端使用 gorilla/websocket 的协议级 Ping/Pong。

```go
// 在 HandleWebSocket 中添加：
const (
    pingPeriod = 30 * time.Second
    writeWait  = 10 * time.Second
)

// 设置 Pong 处理器（客户端自动回复 Pong 时重置读取超时）
conn.SetPongHandler(func(string) error {
    conn.SetReadDeadline(time.Now().Add(60 * time.Second))
    return nil
})

// 启动周期性 Ping goroutine
go func() {
    ticker := time.NewTicker(pingPeriod)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            writeMu.Lock()
            conn.SetWriteDeadline(time.Now().Add(writeWait))
            if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
                writeMu.Unlock()
                return
            }
            writeMu.Unlock()
        case <-closeCh:  // 连接关闭时退出
            return
        }
    }
}()
```

#### 第二步：修复 IsRunning() 状态准确性（解决根因 2）

**方案**：在 `readLoop` 的 defer 中标记 `c.cmd = nil`。

```go
// ffmpeg/capture.go readLoop defer 中添加：
c.mu.Lock()
c.cmd = nil  // 标记进程已退出
c.mu.Unlock()
```

同时可选增加 `running` 原子标志位作为双重保险。

#### 第三步：缓存并重发 SPS/PPS（解决根因 3）

**方案**：在 `Capture` 中缓存最近的 SPS 和 PPS NAL 单元，在创建新 PeerConnection 时优先发送。

```go
// 新增字段
type Capture struct {
    // ...
    cachedSPS []byte  // 缓存的 SPS NAL
    cachedPPS []byte  // 缓存的 PPS NAL
}

// 在 onNalComplete 中缓存
if nalType == 7 { c.cachedSPS = append([]byte(nil), nal...) }
if nalType == 8 { c.cachedPPS = append([]byte(nil), nal...) }

// 提供方法供 websocket handler 在新连接建立后调用
func (c *Capture) ResendParameterSets() {
    // 将缓存的 SPS/PPS 作为独立 sample 写入 track
}
```

#### 第四步：延迟响应 Disconnected（解决根因 5）

**方案**：检测到 `disconnected` 时，等待 15-30 秒再决定是否关闭。

```go
case pionwebrtc.PeerConnectionStateDisconnected:
    // 不立即关闭，给 ICE 重启时间
    go func() {
        select {
        case <-time.After(30 * time.Second):
            if pc.ConnectionState() == pionwebrtc.PeerConnectionStateDisconnected {
                closeConn()
            }
        case <-closeCh:
            // 连接已通过其他方式关闭
        }
    }()
```

---

## 11. 附录：关键代码引用

### 11.1 涉及的文件清单

| 文件 | 行数 | 关键区域 |
|------|------|----------|
| `websocket/handler.go` | 264 | L202-213 (状态变更), L216-223 (读取超时) |
| `ffmpeg/capture.go` | 365 | L165-201 (readLoop), L357-365 (IsRunning) |
| `webrtc/manager.go` | 170 | L29-39 (CreatePeerConnection), L144-153 (WriteVideoSample) |
| `web/ws.js` | 121 | 全文 (无心跳) |
| `web/webrtc.js` | 163 | L12-92 (startWebRTC), L140-153 (信令处理) |
| `main.go` | 73 | L33-41 (模块初始化与连接) |

### 11.2 依赖版本

| 依赖 | 版本 |
|------|------|
| gorilla/websocket | v1.5.3 |
| pion/webrtc/v4 | v4.2.16 |
| Go | 1.25.0 |

### 11.3 参考来源

| # | 来源 | 链接 |
|---|------|------|
| 1 | gorilla/websocket Ping/Pong 示例 | https://pkg.go.dev/github.com/gorilla/websocket#example-IsUnexpectedCloseError |
| 2 | Pion WebRTC 连接生命周期 | https://pion-webrtc.mintlify.app/advanced/connection-lifecycle |
| 3 | WebRTC ICE 重启机制 | https://developer.mozilla.org/en-US/docs/Web/API/WebRTC_API/Session_lifetime#ice_restart |
| 4 | Pion TrackLocalStaticSample 文档 | https://pkg.go.dev/github.com/pion/webrtc/v4#TrackLocalStaticSample |
