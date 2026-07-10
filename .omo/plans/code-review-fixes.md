# code-review-fixes - Work Plan

## TL;DR (For humans)
<!-- Fill this LAST, after the detailed plan below is written, so it summarizes the REAL plan. -->

**What you'll get:** 修复4个功能阻断/缺陷 bug（重连时旧连接不清理导致视频卡死、拖拽功能缺失、端口冲突残留ffmpeg进程、首次点击播放时机错误），并附6条改进建议文档（代码不改动，仅记录）。

**Why this approach:** 前端 `window.pc` 与模块级 `pc` 脱钩是最严重的问题——重连后 watchdog 失效+旧 PeerConnection 泄漏，直接导致"连不上"或"画面卡住"。拖拽缺失是第二高频投诉场景。这两个修完，核心可用性就稳了。

**What it will NOT do:** 不做安全加固（用户明确说安全性其次）。不做架构重构（HandleWebSocket 328行单体不改）。不做测试编写（无现有测试框架，不引入）。不实施 F5-F9 改进建议（仅文档记录）。

**Effort:** Short
**Risk:** Low — 所有修改都是小范围精确修复，不触碰核心 NAL 聚合/WebRTC codec 注册逻辑
**Decisions to sanity-check:** 拖拽用「前端发 mouse_drag 命令」而非「模拟 mousedown+move+up」——前者更简单，复用已有 MouseDrag 方法

Your next move: 批准后运行 `/start-work`。完整执行细节见下方。

---

> TL;DR (machine): Short effort, Low risk, 4 bug fixes (F1-F4) + 6 improvement notes (F5-F9)

## Scope
### Must have
- F1: 修复 `web/webrtc.js` 中 `window.pc` 与模块级 `pc` 脱钩 — watchdog 和重连清理恢复工作
- F2: 实现拖拽功能 — 前端 `touch.js` 发送 `mouse_drag` 命令 + `websocket/handler.go` 添加 case + 确认 `control/mouse.go` MouseDrag 正确
- F3: 修复 `main.go` 中 `log.Fatalf` 绕过清理 — 改为优雅关闭
- F4: 修复 `web/app.js` 首次点击播放时机 — 移除独立 play 逻辑，统一到 webrtc.js ontrack
- F5-F9: 写改进建议到 `.omo/output/improvement-notes.md`（不改代码）

### Must NOT have (guardrails, anti-slop, scope boundaries)
- 不做安全加固（无 auth、无 TLS、无 CORS 限制 — 用户明确不需要）
- 不拆分 HandleWebSocket（328行单体不动 — 功能可用，重构是额外风险）
- 不引入测试框架（项目无测试，不在此次范围引入）
- 不改 ffmpeg NAL 聚合逻辑（capture.go process/onNalComplete/emitAU 不动 — 核心路径已稳定）
- 不改 WebRTC codec 注册（manager.go H264 profile 列表不动 — 已修复的 anti-pattern 不碰）
- 不实施 F5-F9（仅文档建议，不改代码）
- 不添加 config.local.yaml 支持（仅记录为建议）

## Verification strategy
> Zero human intervention - all verification is agent-executed.
- Test decision: none + `go vet ./...`（项目无测试，不引入框架；用 go vet 确认编译+静态检查）
- 前端验证：`go build -o lazyDesk.exe .` 确认 embed 前端编译通过
- Evidence: .omo/evidence/task-<N>-code-review-fixes.<ext>

## Execution strategy
### Parallel execution waves
> Target 5-8 todos per wave. Fewer than 3 (except the final) means under-split.

Wave 1 (并行 — 4个独立修复):
- Todo 1: F1 webrtc.js window.pc 修复
- Todo 2: F2 拖拽功能实现（前端+后端）
- Todo 3: F3 main.go 优雅关闭
- Todo 4: F4 app.js 首次点击播放修复

Wave 2 (串行 — 依赖 Wave 1 完成):
- Todo 5: F5-F9 改进建议文档

Wave 3 (验证 — 依赖全部完成):
- Todo 6: go vet + go build 全量验证

### Dependency matrix
| Todo | Depends on | Blocks | Can parallelize with |
| --- | --- | --- | --- |
| 1 (F1 webrtc.js) | — | 6 | 2, 3, 4 |
| 2 (F2 拖拽) | — | 6 | 1, 3, 4 |
| 3 (F3 main.go) | — | 6 | 1, 2, 4 |
| 4 (F4 app.js) | — | 6 | 1, 2, 3 |
| 5 (F5-F9 文档) | — | 6 | 1, 2, 3, 4 |
| 6 (验证) | 1, 2, 3, 4, 5 | — | — |

## Todos
<!-- APPEND TASK BATCHES BELOW THIS LINE WITH edit/apply_patch - never rewrite the headers above. -->

- [ ] 1. F1: 修复 webrtc.js window.pc 脱钩 — watchdog 和重连清理恢复
  What to do: 在 `web/webrtc.js` 的 `startWebRTC()` 函数中，创建 RTCPeerConnection 后立即赋值 `window.pc = pc;`。在清理旧 PC 的地方（行19-21）已有 `window.pc = null;`，确认一致。在 `handleWebRTCSignal` 中检查 `if (!pc)` 改为 `if (!pc || !window.pc)` 不需要——只要保证赋值即可。
  Must NOT do: 不改 webrtc.js 的 ontrack/onicecandidate/onconnectionstatechange 逻辑。不引入新的全局变量。
  Parallelization: Wave 1 | Blocked by: — | Blocks: 6
  References (executor has NO interview context):
  - `web/webrtc.js:2` — `var pc = null;` 模块级变量声明
  - `web/webrtc.js:19-21` — 旧 PC 清理：`if (window.pc) { try { window.pc.close(); } catch(e) {} window.pc = null; }` — 这里检查 window.pc 但从未赋值
  - `web/webrtc.js:36` — `try { pc = new RTCPeerConnection({ iceServers: [] }); }` — 赋值给模块级 pc，缺 `window.pc = pc;`
  - `web/app.js:36` — watchdog: `if (!videoReady && window.pc && window.pc.connectionState !== 'connected')` — 依赖 window.pc
  - `web/app.js:85` — 重连清理: `if (window.pc) { window.pc.close(); window.pc = null; }` — 依赖 window.pc
  Acceptance criteria (agent-executable): `grep -n "window.pc = pc" web/webrtc.js` 返回至少1行匹配。`grep -n "window.pc" web/app.js` 所有引用点都能在 webrtc.js 中找到对应的 `window.pc = pc` 赋值。
  QA scenarios: 
  - happy: go build 成功，embed 包含修改后的 webrtc.js
  - failure: 如果遗漏赋值，grep 返回0行 → 编译应失败（但前端是 embed 不编译 JS，所以靠 grep 断言）
  Evidence: .omo/evidence/task-1-code-review-fixes.txt
  Commit: Y | fix(web): 修复 window.pc 脱钩导致 watchdog 和重连清理失效

- [ ] 2. F2: 实现拖拽功能 — 前端发送 + 后端路由 + 确认 control 实现
  What to do: 
  1. `web/touch.js` 的 `onStart` 中，单指 touchstart 时记录起始坐标 `sx, sy` 并发送 `mouse_move`（已有）。在 `onMove` 中，当 `dragging==true` 时改为发送 `mouse_move`（已有）。关键改动：在 `onEnd` 中，如果 `dragging==true`，发送 `mouse_drag` 命令 `{type:'mouse_drag', startX: sx, startY: sy, endX: r.x, endY: r.y}`，然后发送 `mouse_click button:'left' action:'up'` 来释放按键。但实际上更简单的方案是：在 `onStart` 单指 touchstart 时如果是拖拽场景，先发 `mouse_move`（光标到位），然后 `mouse_click left down`；onMove 时持续 `mouse_move`；onEnd 时 `mouse_click left up`。**用这个方案** — 不用 mouse_drag 命令，改用 down/move/up 三步。这样不需要改 handler.go switch。
  
  Wait — 重新审视。touch.js 当前 onMove 逻辑：`if(dragging) wsClient.send({type:'mouse_move',x:r.x,y:r.y});`。缺的是 onStart 时 `mouse_click left down` 和 onEnd 时 `mouse_click left up`。当前 onStart 只发 mouse_move + 长 press 定时器，onEnd 只发 left click。

  **最终方案**（最简单，不改后端）:
  1. `web/touch.js` onStart：当 touchstart 且单指时，在发 mouse_move 后，不立即发 click down。保持 longTimer 逻辑。在 onMove 检测到 dragging=true 时，在第一次进入 dragging 时发 `mouse_click button:'left' action:'down'`。在 onEnd 当 dragging==true 时发 `mouse_click button:'left' action:'up'`，不发 click。
  2. `websocket/handler.go` 不需要改 — mouse_click 已支持 down/up action。
  3. `control/mouse.go` 不需要改 — MouseClick 已支持 down/up。

  Must NOT do: 不新增 mouse_drag WS 命令类型。不改 websocket/handler.go switch。不改 control/mouse.go MouseDrag 方法（保留但不使用）。
  Parallelization: Wave 1 | Blocked by: — | Blocks: 6
  References (executor has NO interview context):
  - `web/touch.js:44-55` — onStart 函数：单指 touchstart 发 mouse_move + longTimer（右键长按）
  - `web/touch.js:56-77` — onMove 函数：dragging=true 时发 mouse_move（但没发 mouse down）
  - `web/touch.js:78-85` — onEnd 函数：dragging=true 时不发 click（当前逻辑正确），但缺 mouse up
  - `web/touch.js:65-66` — dragging 判定：`if(!dragging && (Math.abs(r.x-sx)>0.005||Math.abs(r.y-sy)>0.005)){ dragging=true; clearTimeout(longTimer); }` — 这里进入 dragging 时需要加 `wsClient.send({type:'mouse_click',button:'left',action:'down'});`
  - `websocket/handler.go:307-308` — `case "mouse_click": h.cmdHandler.MouseClick(cmd.Button, cmd.Action)` — 已支持 down/up
  - `control/mouse.go:86-99` — MouseClick switch action: case "down" / case "up" — 已实现
  Acceptance criteria (agent-executable): `grep -n "action.'down'" web/touch.js` 返回至少1行（onMove 进入 dragging 时）。`grep -n "action.'up'" web/touch.js` 返回至少1行（onEnd dragging=true 时）。`go vet ./...` 通过。`go build -o lazyDesk.exe .` 成功。
  QA scenarios:
  - happy: go build 成功，touch.js 包含 mouse_click down/up 发送
  - failure: 如果遗漏 down/up，grep 返回0行
  Evidence: .omo/evidence/task-2-code-review-fixes.txt
  Commit: Y | feat(touch): 实现拖拽 — touch move 时发 mouse down/up 序列

- [ ] 3. F3: 修复 main.go log.Fatalf 绕过清理 — 改为优雅关闭
  What to do: 在 `main.go` 中，将 HTTP goroutine 里的 `log.Fatalf` 改为：记录错误，关闭 capture 和 rtcManager，然后 os.Exit(1)。或者更简单：用 `errCh` 模式 — HTTP goroutine 发送 error 到 channel，主 select 收到后执行清理再退出。
  
  **最终方案**（最小改动）:
  ```go
  // 替换 main.go:59-63
  errCh := make(chan error, 1)
  go func() {
      if err := http.ListenAndServe(addr, mux); err != nil {
          errCh <- err
      }
  }()
  
  select {
  case err := <-errCh:
      log.Printf("Server error: %v", err)
  case <-sigCh:
  }
  
  log.Println("Shutting down...")
  capture.Stop()
  rtcManager.Close()
  ```
  需要把 `sigCh` 的 `signal.Notify` 移到 select 之前（已经在行66-67了，OK）。

  Must NOT do: 不引入 http.Server 的 Shutdown(ctx) 方法（过度工程化）。不改变信号处理逻辑。
  Parallelization: Wave 1 | Blocked by: — | Blocks: 6
  References (executor has NO interview context):
  - `main.go:59-63` — 当前代码: `go func() { if err := http.ListenAndServe(addr, mux); err != nil { log.Fatalf("Server error: %v", err) } }()`
  - `main.go:66-72` — SIGINT/SIGTERM 处理 + capture.Stop() + rtcManager.Close()
  - `main.go:65` — 注释 `// 6. 等待退出信号`
  Acceptance criteria (agent-executable): `go vet ./...` 通过。`go build -o lazyDesk.exe .` 成功。grep 确认 `log.Fatalf` 不再出现在 main.go 的 HTTP goroutine 中: `grep -n "log.Fatalf.*Server error" main.go` 返回0行。`grep -n "errCh" main.go` 返回至少2行（声明+接收）。
  QA scenarios:
  - happy: go build 成功，go vet 通过
  - failure: 如果 log.Fatalf 仍在，grep 返回1行
  Evidence: .omo/evidence/task-3-code-review-fixes.txt
  Commit: Y | fix(main): HTTP 启动失败时优雅关闭 ffmpeg 和 WebRTC

- [ ] 4. F4: 修复 app.js 首次点击播放 — 移除冗余 play 逻辑
  What to do: 在 `web/app.js` 中，移除行107-110 的首次 click play 逻辑。这个逻辑意图是解除静音，但 webrtc.js 的 ontrack 里已经有完整的 play + autoplay 处理（行44-61），包括 autoplay 失败后的 click fallback。app.js 的额外 click listener 会在 video 还没 srcObject 时调 play() → 静默失败，且 `{ once: true }` 消费了 click 事件可能干扰 webrtc.js 的 click fallback。
  
  **最终方案**: 删除 app.js:107-110 的整个 `document.addEventListener('click', ...)` 块。webrtc.js 的 ontrack fallback（行53-59）已经处理了 autoplay 被阻止的场景。

  Must NOT do: 不改 webrtc.js 的 ontrack/autoplay 逻辑。不改变 video 元素的 muted 属性（index.html 里 `muted` 属性保留 — autoplay 需要 muted）。
  Parallelization: Wave 1 | Blocked by: — | Blocks: 6
  References (executor has NO interview context):
  - `web/app.js:107-110` — 当前代码: `document.addEventListener('click', function() { var v = document.getElementById('remoteVideo'); if (v) { v.muted = false; v.play().catch(function(){}); } }, { once: true });`
  - `web/webrtc.js:44-61` — ontrack 里的 play + catch + click fallback（已处理 autoplay 被阻止）
  - `web/webrtc.js:53-59` — click fallback: `document.addEventListener('click', function() { video.play().then(...) })`
  - `web/index.html:15` — `<video id="remoteVideo" autoplay playsinline muted style="display:none;"></video>` — muted 属性
  Acceptance criteria (agent-executable): `grep -n "v.muted = false" web/app.js` 返回0行。`go build -o lazyDesk.exe .` 成功（embed 包含修改后的 app.js）。
  QA scenarios:
  - happy: go build 成功，app.js 不再包含独立 play 逻辑
  - failure: 如果未删除，grep 返回1行
  Evidence: .omo/evidence/task-4-code-review-fixes.txt
  Commit: Y | fix(app): 移除冗余首次点击播放逻辑 — 与 webrtc.js ontrack fallback 冲突

- [ ] 5. F5-F9: 写改进建议文档（不改代码）
  What to do: 创建 `.omo/output/improvement-notes.md`，记录以下6条改进建议，每条包含：位置、问题、建议方案、优先级、是否可逆。
  
  内容:
  1. **F5 [ffmpeg/capture.go:180] sendLoop 不检查 generation** — gen 参数是 dead code。建议：要么在 sendLoop 开头加 `if atomic.LoadInt64(&c.generation) != gen { return }`，要么去掉 gen 参数。优先级: Low。可逆: Yes。
  2. **F6 [websocket/handler.go:133-146] captureStarted 局部变量语义不清** — 多连接各自维护一份，靠 IsRunning() 兜底。建议：将 captureStarted 提升为 Handler 字段（bool + mutex），或直接移除靠 IsRunning()。优先级: Low。可逆: Yes。
  3. **F7 [control/mouse.go:120-131] MouseScroll 多步循环慢** — 50步滚动调50次 mouse_event。建议：用单次 SendInput + 多次 WHEEL_DELTA 合并，或用 WH_MOUSE_LL hook。优先级: Medium。可逆: Yes。
  4. **F8 [config.yaml + config/config.go] config.local.yaml 不生效** — .gitignore 有但代码不读。建议：在 config.Load 里加 `config.local.yaml` 存在则 override。优先级: Low。可逆: Yes。
  5. **F9 [web/index.html:29] connectOverlay() 函数在 inline script** — 与 app.js 的 connect() 函数重复。建议：统一到 app.js，删 inline script。优先级: Low。可逆: Yes。
  6. **F10 [webrtc/manager.go:136-138] OnConnectionStateChange 重复注册** — manager.go 注册了一次，handler.go:226 又注册了一次。Pion 允许多个回调但日志重复。建议：去掉 manager.go 的或 handler.go 的。优先级: Low。可逆: Yes。

  Must NOT do: 不改任何代码。不创建 .go 文件。只写 .omo/output/improvement-notes.md。
  Parallelization: Wave 1 | Blocked by: — | Blocks: 6
  References (executor has NO interview context):
  - `ffmpeg/capture.go:180` — sendLoop(gen int64) gen 未使用
  - `websocket/handler.go:133-146` — captureStarted 局部 bool
  - `control/mouse.go:120-131` — MouseScroll for 循环
  - `config/config.go:64` — Load("config.yaml") 硬编码
  - `web/index.html:91-108` — inline script connectOverlay()
  - `webrtc/manager.go:136-138` — pc.OnConnectionStateChange
  - `websocket/handler.go:226-251` — pc.OnConnectionStateChange (重复)
  Acceptance criteria (agent-executable): `test -f .omo/output/improvement-notes.md` 文件存在。`grep -c "F[5-9]\|F10" .omo/output/improvement-notes.md` 返回 >= 6。
  QA scenarios:
    - happy: 文件存在，包含6条建议
    - failure: 文件不存在或条目不足
  Evidence: .omo/evidence/task-5-code-review-fixes.txt
  Commit: Y | docs: F5-F10 改进建议文档

- [ ] 6. 全量验证 — go vet + go build + 前端 embed 确认
  What to do: 运行 `go vet ./...` 和 `go build -o lazyDesk.exe .`。确认无错误。用 grep 验证所有 F1-F4 修复点：
  - F1: `grep -n "window.pc = pc" web/webrtc.js` >= 1行
  - F2: `grep -n "action.*down" web/touch.js` >= 1行, `grep -n "action.*up" web/touch.js` >= 1行
  - F3: `grep -n "log.Fatalf.*Server" main.go` = 0行, `grep -n "errCh" main.go` >= 2行
  - F4: `grep -n "v.muted = false" web/app.js` = 0行
  - F5-F9: `test -f .omo/output/improvement-notes.md` = true
  
  Must NOT do: 不运行 lazyDesk.exe（需要 Windows 显示+ffmpeg）。不启动浏览器测试。
  Parallelization: Wave 2 | Blocked by: 1, 2, 3, 4, 5 | Blocks: —
  References: 全部上述 todo 的 References
  Acceptance criteria (agent-executable): `go vet ./...` 退出码 0。`go build -o lazyDesk.exe .` 退出码 0。所有 grep 断言通过。
  QA scenarios:
    - happy: 全部通过
    - failure: 任一 grep 或编译失败
  Evidence: .omo/evidence/task-6-code-review-fixes.txt
  Commit: N

## Final verification wave
> Runs in parallel after ALL todos. ALL must APPROVE. Surface results and wait for the user's explicit okay before declaring complete.
- [ ] F1. Plan compliance audit — 确认每个 todo 的 Acceptance criteria 都被验证
- [ ] F2. Code quality review — 确认修改未引入新问题（特别是 touch.js 拖拽逻辑不破坏现有点击/长按/双指滚动）
- [ ] F3. Real manual QA — go build 成功 + 前端 embed 包含所有修改（用 `go run .` 或 `go build` 确认 embed 生效）
- [ ] F4. Scope fidelity — 确认未做安全加固、未拆 HandleWebSocket、未引入测试框架、未碰 NAL 逻辑

## Commit strategy
- 每个 todo 一个 commit（6个 commit）
- commit message 格式: `<type>(<scope>): <description>`
- types: fix, feat, docs
- scopes: web, touch, main, app
- 最后一个验证 todo 不 commit

## Success criteria
- `go vet ./...` 通过
- `go build -o lazyDesk.exe .` 成功
- F1: webrtc.js 包含 `window.pc = pc` 赋值
- F2: touch.js 包含 mouse_click down/up 序列
- F3: main.go 不含 log.Fatalf in HTTP goroutine，含 errCh 模式
- F4: app.js 不含 `v.muted = false` 独立播放逻辑
- F5-F9: .omo/output/improvement-notes.md 存在且包含 >= 6 条建议
- 无回归：NAL 聚合、WebRTC codec 注册、WS 信令、触控映射核心路径未改动
