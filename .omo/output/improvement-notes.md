# 改进建议文档 (F5-F10)

> 本文档为代码审查发现的 6 条改进建议，**仅记录不改动代码**。
> 来源: `.omo/plans/code-review-fixes.md` (Todo 5)

---

## F5 — sendLoop 不检查 generation

| 字段 | 值 |
|------|-----|
| 位置 | `ffmpeg/capture.go:180` |
| 问题 | `sendLoop(gen int64)` 接收 `gen` 参数但从未使用。`sendLoop` 内部不检查 `atomic.LoadInt64(&c.generation) != gen`，因此即使 `Start()` 被再次调用（generation+1），旧 goroutine 仍会继续从 `sendCh` 取帧写入 track。虽然 `stopCh` 会在 `Stop()` 时关闭来终止旧 goroutine，但 `gen` 参数成了 dead code。 |
| 建议方案 | 两种选择：(A) 在 `sendLoop` 开头加 `if atomic.LoadInt64(&c.generation) != gen { return }`，并在每次取帧后检查；(B) 直接去掉 `gen` 参数，仅依赖 `stopCh`。方案 A 更安全（双重保险），方案 B 更简洁。 |
| 优先级 | Low |
| 是否可逆 | Yes |

---

## F6 — captureStarted 局部变量语义不清

| 字段 | 值 |
|------|-----|
| 位置 | `websocket/handler.go:133-146` |
| 问题 | `captureStarted` 是 per-connection 的局部 `bool` 变量。每个 WS 连接各自维护一份，连接之间互不可见。第二个连接看到 `IsRunning()==true` 就跳过 `Start()`，但 `captureStarted` 仍为 `false`。实际逻辑靠 `capture.IsRunning()` 兜底正确，但 `captureStarted` 的语义令人困惑——它防止的是"同一连接内重复 Start"，而非"全局唯一 Start"。 |
| 建议方案 | (A) 将 `captureStarted` 提升为 `Handler` 字段（`bool` + `sync.Mutex`），语义清晰为"全局已启动"；(B) 直接移除 `captureStarted`，完全依赖 `IsRunning()`（已有原子性保证）。方案 B 更简单。 |
| 优先级 | Low |
| 是否可逆 | Yes |

---

## F7 — MouseScroll 多步循环慢

| 字段 | 值 |
|------|-----|
| 位置 | `control/mouse.go:120-131` |
| 问题 | `MouseScroll` 用 `for i := 0; i < steps; i++` 循环，每步调一次 `procMouseEvent.Call(flags, 0, 0, dwData, 0)`。如果 `deltaY=50`，要调 50 次 `mouse_event`，每次只发一个 `WHEEL_DELTA`（120）。这在极端情况下可能很慢，且 50 次系统调用开销大。已有限制 `steps > 100` 时截断为 100 (B5)。 |
| 建议方案 | (A) 用单次 `SendInput` + 将 `dwData` 设为 `steps * WHEEL_DELTA`（正负方向），一次调用滚动多格；(B) 使用 `WH_MOUSE_LL` hook 批量提交。方案 A 最简单且向后兼容。注意：`mouse_event` 的 `dwData` 支持任意刻度值（不限于 120 的倍数），所以 `steps * WHEEL_DELTA` 合法。 |
| 优先级 | Medium |
| 是否可逆 | Yes |

---

## F8 — config.local.yaml 不生效

| 字段 | 值 |
|------|-----|
| 位置 | `config/config.go:64` (Load 函数) + `config.yaml` (.gitignore) |
| 问题 | `.gitignore` 中有 `config.local.yaml` 条目（暗示用户可本地覆盖配置），但 `config.Load("config.yaml")` 硬编码只读 `config.yaml`。用户无法通过 `config.local.yaml` 覆盖配置。 |
| 建议方案 | 在 `config.Load` 中，加载 `config.yaml` 后，检查 `config.local.yaml` 是否存在；若存在则用其 override 基础配置。可用 `gopkg.in/yaml.v3` 的 `MapSlice` 或二次 unmarshal + merge 实现。示例：```go if local, err := os.ReadFile("config.local.yaml"); err == nil { yaml.Unmarshal(local, cfg) // override }```。注意：二次 unmarshal 需处理零值覆盖非零值的问题（map 结构更安全）。 |
| 优先级 | Low |
| 是否可逆 | Yes |

---

## F9 — connectOverlay() inline script 重复

| 字段 | 值 |
|------|-----|
| 位置 | `web/index.html:91-108` (inline `<script>`) |
| 问题 | `index.html` 底部有一段 inline script 定义了 `connectOverlay()` 函数，它做的事与 `app.js` 的 `window.connect()` 高度重复：读取 `overlayIP` 值、写 `localStorage`、调用 `connect()`/`wsClient.connect()`。inline script 还有自己的 `pc.close()` 清理逻辑（第 105 行引用全局 `pc`，而非 `window.pc`——这与 F1 修复前的脱钩问题同源）。 |
| 建议方案 | 将 `connectOverlay()` 逻辑统一到 `app.js` 的 `window.connect`（已有 IP 读取 + localStorage + `window.pc` 清理），删除 inline script 中的函数定义，改用 `onclick="connect()"` 或在 app.js 绑定事件。 |
| 优先级 | Low |
| 是否可逆 | Yes |

---

## F10 — OnConnectionStateChange 重复注册

| 字段 | 值 |
|------|-----|
| 位置 | `webrtc/manager.go:136-138` + `websocket/handler.go:226-251` |
| 问题 | `manager.go` 的 `CreatePeerConnection` 在返回 `pc` 前注册了一个 `pc.OnConnectionStateChange` 回调（仅日志）。`handler.go` 拿到同一个 `pc` 后又注册了第二个 `OnConnectionStateChange` 回调（含 startCapture/ResendParameterSets/closeConn 等实质逻辑）。Pion 允许多个回调，两个都会被调用，但 `manager.go` 的纯日志回调与 `handler.go` 的日志行重复（`log.Printf("WebRTC state: ...")` vs `log.Printf("WebRTC connection state: ...")`），造成日志冗余。 |
| 建议方案 | 去掉 `manager.go:136-138` 的纯日志回调（实质逻辑在 `handler.go` 中），或去掉 `handler.go:227` 的 `log.Printf` 日志行（保留 manager.go 的统一日志）。推荐前者——manager 的职责是创建 PC，状态处理属于 handler。 |
| 优先级 | Low |
| 是否可逆 | Yes |
