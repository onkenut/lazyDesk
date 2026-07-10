---
slug: code-review-fixes
status: plan-complete
intent: clear
pending-action: none — plan written, awaiting user to /start-work
approach: 4 bug fixes (F1-F4) + improvement notes doc (F5-F10), no security/architecture changes
---

# Draft: code-review-fixes

## Components (topology ledger)
| id | outcome | status | evidence |
| --- | --- | --- | --- |
| C1 | F1: webrtc.js window.pc 脱钩修复 | active | web/webrtc.js:2,36; web/app.js:36,85 |
| C2 | F2: 拖拽功能实现 (touch.js down/move/up) | active | web/touch.js:44-85; websocket/handler.go:307; control/mouse.go:86-99 |
| C3 | F3: main.go 优雅关闭 (errCh 替换 log.Fatalf) | active | main.go:59-72 |
| C4 | F4: app.js 移除冗余首次播放 | active | web/app.js:107-110; web/webrtc.js:44-61 |
| C5 | F5-F10: 改进建议文档 | active | 多处, 见 plan |
| C6 | 全量验证 (go vet + build + grep) | active | 依赖 C1-C5 |

## Open assumptions (announced defaults)
| assumption | default | rationale | reversible? |
| --- | --- | --- | --- |
| 拖拽方案 | 用 down/move/up 三步而非 mouse_drag 命令 | 复用已有 MouseClick down/up，不改 handler.go switch | Yes |
| F5-F9 仅文档 | 不实施代码改动 | 用户选"全修+重构建议"，建议是文档不是代码 | Yes |
| 不引入测试 | 无测试框架 | 项目无测试，不在此次范围引入 | Yes |

## Findings (cited - path:lines)
- F1 [CRITICAL]: web/webrtc.js:2 `var pc` 模块级 vs web/app.js:36,85 `window.pc` — 脱钩
- F2 [CRITICAL]: web/touch.js:56-85 onMove dragging=true 缺 mouse down, onEnd 缺 mouse up
- F3 [HIGH]: main.go:60 `log.Fatalf` in goroutine — 绕过 capture.Stop()
- F4 [HIGH]: web/app.js:107-110 冗余 play 逻辑 — 与 webrtc.js:53-59 click fallback 冲突
- F5 [LOW]: ffmpeg/capture.go:180 sendLoop gen 参数未使用
- F6 [LOW]: websocket/handler.go:133 captureStarted 局部变量语义不清
- F7 [MEDIUM]: control/mouse.go:120-131 MouseScroll 多步循环慢
- F8 [LOW]: config/config.go:64 不读 config.local.yaml
- F9 [LOW]: web/index.html:91-108 inline script 重复 connect 逻辑
- F10 [LOW]: webrtc/manager.go:136 + websocket/handler.go:226 OnConnectionStateChange 重复注册

## Decisions (with rationale)
- 拖拽用 down/move/up 三步（不改后端）> 新增 mouse_drag 命令 — 最小改动，复用已有
- F5-F9 只写文档 > 实施修复 — 用户要"重构建议"，不是"重构实施"
- errCh 模式 > http.Server.Shutdown(ctx) — 不过度工程化

## Scope IN
- F1: webrtc.js 加 `window.pc = pc` 赋值
- F2: touch.js 加 mouse_click down/up 到拖拽路径
- F3: main.go errCh 替换 log.Fatalf
- F4: app.js 删冗余 click play listener
- F5-F10: .omo/output/improvement-notes.md 文档

## Scope OUT (Must NOT have)
- 安全加固（auth/TLS/CORS）
- HandleWebSocket 拆分
- 测试框架引入
- NAL 聚合/WebRTC codec 逻辑修改
- F5-F10 代码实施

## Open questions
- 无 — 用户已选"全修+重构建议"

## Approval gate
status: plan-complete
Plan written to .omo/plans/code-review-fixes.md. 6 todos, 2 waves. Awaiting user /start-work.
