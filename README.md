# lazyDesk v2

局域网远程桌面工具：平板/手机浏览器控制 Windows PC。
Go + Pion WebRTC + ffmpeg 采集 + 原生 ESM 前端（单 exe 分发）。

## 功能

- 🖥️ → 📱 屏幕实时推送（WebRTC H.264，NVENC GPU 硬编码，60fps 低延迟）
- 🎵 系统音频推送（立体声混音 → Opus）
- 👆 触控映射：单指鼠标 / 长按右键 / 双指滚轮
- ⌨️ 虚拟按键面板：修饰键 / 功能键 / 快捷键宏 / 中文文本输入
- 🔌 电源控制：熄屏 / 锁屏 / 睡眠 / 关机
- 📺 多客户端同时观看（每连接独立 PeerConnection，互不影响）
- 📊 实时状态：帧率 / 客户端数 / 采集管线 / 音频状态（状态栏 + `/api/status`）

## 环境要求

| 组件 | 要求 |
|---|---|
| 操作系统 | Windows 10/11（采集与控制均使用 Windows 专有 API） |
| ffmpeg | **≥ 5.1**（推荐 6.x+），需含 `h264_nvenc` / `ddagrab`（GPU 管线）或 `libx264`（CPU 回退）。可从 [gyan.dev](https://www.gyan.dev/ffmpeg/builds/) 下载，加入 PATH 或在 `config.yaml` 指定路径 |
| 浏览器 | 平板/手机 Chrome、Edge、Safari（支持 WebRTC 即可） |

## 使用

```bash
go build -o lazyDesk.exe .
lazyDesk.exe
```

1. PC 上运行 `lazyDesk.exe`，控制台显示 `http://<本机IP>:8080`
2. 平板浏览器打开 `http://<PC的局域网IP>:8080`（可扫二维码/手动输入 IP）
3. 连接成功后自动进入桌面控制

### 首次连接失败排查

1. **防火墙放行 UDP**：首次运行弹窗时允许访问，或手动放行
2. **同一局域网**：平板与 PC 同子网，无 VPN
3. **浏览器"隐藏本地IP"**：Chrome 若开启 mDNS 混淆导致连接失败，请关闭该选项
4. **状态页**：PC 上访问 `http://localhost:8080/api/status` 查看采集管线/帧率/客户端/音频错误

## 配置（config.yaml）

```yaml
server:
  port: 8080            # 监听端口
  host: "0.0.0.0"       # 监听地址

ffmpeg:
  path: ""              # ffmpeg 路径（空 = 使用 PATH）
  screen:
    framerate: 60       # 采集帧率
    codec: "h264_nvenc" # h264_nvenc（GPU）| libx264（CPU）
    fallback: true      # GPU 不可用时自动降级 CPU 编码
    profile: "baseline" # baseline 兼容性最好
    bitrate: "15M"      # 视频码率（LAN 8-20M）
    gop_size: 60        # 关键帧间隔（1秒@60fps）
  audio:
    device: "立体声混音"  # 音频设备关键词（按括号前主名模糊匹配）
    bitrate: "128k"

control:
  dpi_scale: 1.0        # 已废弃（坐标按实际采集分辨率精确映射）

power:
  enabled: true         # 电源控制开关
```

## 架构

```
web/                # 前端（原生 ESM，无构建工具，go:embed 打包）
  modules/          # ws / webrtc / render / input / ui
capture/            # 采集层
  nal.go            # H.264 Annex B NAL 解析器（跨 chunk 边界）
  assembler.go      # 帧聚合：SPS/PPS 前缀 + 4 字节起始码规范化 + 丢弃 SEI/AUD/filler
  video.go          # ffmpeg 视频进程：崩溃自动重启（≤3 次）+ 分辨率解析
  audio.go          # 音频：dshow → libopus → RTP/UDP → 扇出
  hub.go            # 扇出 Hub：多客户端，慢客户端只丢自己的帧
  diag.go           # ffmpeg 探测 + 降级链（ddagrab+NVENC → gdigrab+x264）
webrtc/             # 每客户端 PeerConnection 工厂（H264+Opus，UDP4 优先）
server/             # HTTP + WS 信令 + 控制指令 + /api/status
control/            # Windows API：鼠标 / 键盘（SendInput）/ 剪贴板 / 电源
e2e/                # 端到端测试（Pion 作为真实 WebRTC 客户端）
tools/              # Edge headless 诊断脚本（验证浏览器端解码）
```

## 测试

```bash
go test ./...                  # 单元测试（NAL 解析 / 帧聚合 / 配置）
go test -tags e2e ./e2e/       # 端到端（真实采集 + 信令 + 视频/音频流 + 多客户端）
python tools/edge_diag.py      # 浏览器端验证（需先启动 Edge headless + 服务）
```

## v2 相比 v1 的修复

| 问题 | 修复 |
|---|---|
| 浏览器 0 帧解码（**核心不可用根因**） | 帧聚合时丢弃 SEI —— SEI 参与 RTP 打包会导致 Chromium 无法解码 P 帧（仅能解 IDR） |
| 单客户端（新连接踢旧连接） | Hub 扇出，每连接独立 PeerConnection |
| 混合 3/4 字节起始码损坏 RTP 载荷 | 全部规范化为 4 字节起始码 |
| 客户端断开导致服务端 panic | pump 固定引用，修复 nil 竞态 |
| 音频半成品（配置了但从未发送） | 完整实现：dshow 设备匹配 → Opus → RTP → 扇出 |
| ffmpeg 缺失/能力不足时黑屏无提示 | 启动前探测 + 自动降级 + 明确中文错误 |
| 鼠标坐标偏移（DPI 猜测） | 按 ffmpeg 实际采集分辨率精确映射 |
| 前端全局变量乱飞 | 原生 ESM 模块化 |
| 测试脚本 observe.py 无法验证任何东西 | 重写为真实 WebRTC 端到端测试 |

## 已知限制

- 仅 Windows 主机（采集与控制依赖 Windows API）
- 无跨公网支持（面向局域网；公网需自建 STUN/TURN）
- 文本输入通过剪贴板粘贴实现（`ctrl+v`），要求 PC 剪贴板可用
