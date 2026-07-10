# Draft: code-review

## Intent
- **intent**: CLEAR
- **review_required**: false
- **request**: 代码审查 — 功能可用性优先，安全性其次。个人局域网平板控制 Windows PC 场景。

## Components
1. **ffmpeg/capture.go** — H264 NAL 聚合 + ffmpeg 生命周期管理
2. **webrtc/manager.go** — PeerConnection 生命周期 + H264 codec 注册
3. **websocket/handler.go** — WS 信令 + 命令分发 + ffmpeg 生命周期引用计数
4. **control/** — Win32 输入注入（鼠标/键盘/电源）
5. **web/** — 前端（WS 客户端 + WebRTC + Canvas 渲染 + 触控映射）
6. **main.go + config/** — 组合根 + 配置加载

## Findings (from full codebase read)

### CRITICAL — 功能阻断
- **F1 [websocket/handler.go:117-128]**: `captureStarted` 是 per-connection 局部变量，不是 atomic。多个并发连接时，`startCapture` 闭包每个连接各自一份，互不可见 → 可能重复 Start ffmpeg。但 `capture.IsRunning()` 挡了第一层。真正的问题是：第二个连接的 `captureStarted=false`，看到 `IsRunning()==true` 就跳过 `Start()`，但从未调用 `ResendParameterSets` 给第二个 peer —— 等等，看代码：`ResendParameterSets` 在 `PeerConnectionStateConnected` 里无条件调用，所以这个路径是 OK 的。重新审视。
- **F2 [webrtc.js:2 + webrtc.js:36]**: `var pc = null` 是模块级变量，但 `app.js:36` 和 `app.js:85` 检查的是 `window.pc`。`startWebRTC()` 里 `pc = new RTCPeerConnection(...)` 赋值给模块级 `pc`，但没有 `window.pc = pc`。所以 `app.js` 里的 `window.pc` 检查永远是 `null/undefined` → watchdog 和重连清理逻辑失效。

### HIGH — 功能缺陷
- **F3 [touch.js:78-85]**: `onEnd` 里的点击逻辑 — `Date.now()-st < getLongPressMs()` 但 longTimer 在 `onMove` 里可能已经 clearTimeout 并设了 dragging=true，正常移动后松手不会发 click。但如果 longTimer 没被触发且没移动（纯点击），逻辑是对的。拖拽场景下 `dragging=true` → 不发 click → 正确。**但 `mouse_drag` 命令从未被前端发送** — `MouseDrag` 方法在 control/ 里存在但 websocket handler switch 里没有 `mouse_drag` case。

### MEDIUM — 健壮性
- **F4 [ffmpeg/capture.go:264-303]**: `process()` 里 `nalBuf = append(c.nalBuf, data...)) 在每次 process 开头把残留拼到 data 前面。但 `data` 是传入的 buf[:n]，append 后 data 变成了 nalBuf + buf[:n] 的拼接。下一行 `c.nalBuf = c.nalBuf[:0]` 重置了，但 `data` 本身是新的 slice。这看起来正确。
- **F5 [websocket/handler.go:130-151]**: connCount 引用计数 + captureStarted 局部变量。`startCapture` 只在 `PeerConnectionStateConnected` 时调用。如果第一个连接断了（connCount→0 → Stop），第二个连接的 `captureStarted=false` 且 `IsRunning()==false` → 会重新 Start。看起来 OK。
- **F6 [main.go:59-63]**: `http.ListenAndServe` 在 goroutine 里跑，失败时 `log.Fatalf` 会 `os.Exit(1)`，但不会触发 SIGINT 路径的清理。`capture.Stop()` 和 `rtcManager.Close()` 不会被执行。

### LOW — 改进建议
- **F7 [control/mouse.go:103-131]**: MouseScroll 每次 mouse_event 发一个 WHEEL_DELTA。如果 deltaY=50，要调 50 次 mouse_event，可能很慢。
- **F8 [web/controls.js:54-60]**: textInput keydown handler 里 `e.stopPropagation()` 在所有按键上都调，不仅是 Enter —— 会阻止键盘按键传递到 touchLayer，这是故意的。
- **F9 [config.yaml]**: `config.local.yaml` 在 .gitignore 但代码不读它。如果用户想本地覆盖配置，无法做到。

## Decision
- **scope**: F1-F4 修复（代码改动）+ F5-F9 改进建议（文档）
- **user_choice**: 全修 + 重构建议

## Status
- **status**: approved — scaffold + write plan
