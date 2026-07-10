# PROJECT KNOWLEDGE BASE

**Generated:** 2026-07-10
**Module:** github.com/onkenut/lazyDesk
**Go:** 1.25.0

## OVERVIEW
LAN-based remote desktop: tablet browser controls Windows PC via WebRTC video streaming + WebSocket input commands. Single Go binary embeds frontend. Windows-only at runtime (gdigrab + user32.dll).

## STRUCTURE
```
lazyDesk/
├── main.go            # Composition root: wires modules, embeds web/, serves HTTP
├── config.yaml        # All tunables (port, codec, DPI, power toggle)
├── config/            # YAML loader + struct definitions
├── ffmpeg/capture.go  # ffmpeg subprocess -> H264 NAL aggregation -> sendCh (MOST COMPLEX)
├── webrtc/manager.go  # PeerConnection lifecycle, H264 codec registration, track writer
├── websocket/handler.go # Signaling + command dispatch (LARGEST: 328 LOC)
├── control/           # Win32 input injection (build-tagged windows/!windows)
│   ├── mouse.go       # SetCursorPos, mouse_event, SendInput
│   ├── keyboard.go    # SendInput, clipboard paste for CJK text
│   └── power.go       # sleep/shutdown/lock/monitor_off
└── web/               # Embedded frontend (no build step, raw JS)
    ├── index.html     # Shell: canvas + touch layer + control panel
    ├── app.js         # WS routing, connection state machine, auto-connect
    ├── ws.js          # WebSocket client (reconnect, heartbeat)
    ├── webrtc.js      # Browser WebRTC + canvas render loop
    ├── touch.js       # Touch -> mouse mapping (coords via canvas._renderRect)
    ├── controls.js    # Virtual keyboard, modifier keys, power buttons
    └── style.css      # Full-screen tablet UI
```

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Change capture settings | `config.yaml` -> `ffmpeg.screen` | Framerate, codec, preset, GOP |
| Debug video black screen | `ffmpeg/capture.go` -> `onNalComplete`, `ResendParameterSets` | SPS/PPS caching, IDR interval |
| Add new input command | `websocket/handler.go` switch + `control/` method | Both sides needed |
| Change WebRTC codecs | `webrtc/manager.go` -> `CreatePeerConnection` | Explicit H264 PT registration, NOT RegisterDefaultCodecs |
| Frontend video rendering | `web/webrtc.js` -> `startCanvasRender` draw loop | readyState >= 2 required |
| Touch coordinate mapping | `web/touch.js` -> `videoRatio()` | Reads `canvas._renderRect` set by webrtc.js |
| WebSocket keepalive | `websocket/handler.go` Ping/Pong + `web/ws.js` heartbeat | 25s ping, 90s read timeout |
| ffmpeg lifecycle | `websocket/handler.go` -> `connCount` atomic | Start on WebRTC connected, stop at 0 clients |

## CODE MAP

| Symbol | Type | Location | Refs | Role |
|--------|------|----------|------|------|
| `main` | func | main.go:23 | — | Composition root, 6-step startup |
| `Config.Load` | func | config/config.go:64 | 4 | YAML -> struct with defaults |
| `Capture` | struct | ffmpeg/capture.go:29 | 2 | ffmpeg proc + NAL parser + send loop |
| `VideoWriter` | iface | ffmpeg/capture.go:24 | 1 | Decouples capture from transport |
| `Capture.Start` | method | ffmpeg/capture.go:114 | 1 | Launches ffmpeg, readLoop, sendLoop |
| `Capture.process` | method | ffmpeg/capture.go:264 | 0 | H264 start-code scanner (core logic) |
| `Capture.onNalComplete` | method | ffmpeg/capture.go:306 | 0 | NAL type classification, AU aggregation |
| `Capture.emitAU` | method | ffmpeg/capture.go:340 | 0 | Non-blocking sample enqueue |
| `Capture.ResendParameterSets` | method | ffmpeg/capture.go:81 | 1 | SPS/PPS cache -> new peer |
| `Capture.IsRunning` | method | ffmpeg/capture.go:407 | 1 | Checks cmd != nil + stopCh |
| `Manager` | struct | webrtc/manager.go:13 | 2 | PeerConnection + track lifecycle |
| `Manager.CreatePeerConnection` | method | webrtc/manager.go:29 | 1 | Kills old PC, creates H264+Opus tracks |
| `Manager.WriteVideoSample` | method | webrtc/manager.go:145 | 1 | Implements VideoWriter iface |
| `Handler` | struct | websocket/handler.go:45 | 0 | WS signaling hub, all modules wired here |
| `Handler.HandleWebSocket` | method | websocket/handler.go:70 | 1 (route) | Monolith: signaling + commands + lifecycle |
| `Command` | struct | websocket/handler.go:26 | 0 | JSON wire format for all WS messages |

## CONVENTIONS
- **Chinese comments** throughout (Simplified Mandarin). Code-level docs, log messages, config descriptions.
- **Config-driven** -- `config.yaml` comment says "禁止硬编码". All tunables there with Go defaults in `config.go`.
- **No framework** -- pure `net/http` + `gorilla/websocket`. No middleware, no router, no auth.
- **No vendor/**, no linter config, no Makefile, no CI.
- **Build tags** for platform: `//go:build windows` (real) vs `//go:build !windows` (stub).
- **Manual DI** -- main.go wires concrete types, no DI container.
- **Single-client model** -- Manager closes old PeerConnection before creating new. No concurrent viewers.

## ANTI-PATTERNS (THIS PROJECT)
- **NEVER** use `RegisterDefaultCodecs()` -- it adds VP8/VP9/AV1 ahead of H264, browser picks VP8, server sends H264 -> decode fails. Always register H264 explicitly with matching payload types. (`webrtc/manager.go:43-75`)
- **NEVER** start ffmpeg at WS handshake -- must wait for `PeerConnectionStateConnected`. Early start overflows Pion buffer. (`websocket/handler.go:131` ARCH-2)
- **NEVER** use a `running` bool for goroutine lifecycle -- use the generation counter. Old goroutines can't overwrite state. (`ffmpeg/capture.go:118` B2)
- **NEVER** send `-Value` as positional arg to PowerShell `Set-Clipboard` -- text with spaces/quotes breaks. Use stdin pipe. (`control/keyboard.go` setClipboard)
- **NEVER** block `readLoop` on network I/O -- use the sendCh queue. (`ffmpeg/capture.go:173` ARCH-1)

## UNIQUE STYLES
- **Fix-tracking annotations** in comments: `B1`-`B6` (bugs), `N5`-`N6` (network/concurrency), `I1`-`I6` (input/interaction), `ARCH-1`-`ARCH-2` (architecture decisions), `ROOT-2` (root cause). No `TODO`/`FIXME` -- all issues are resolved and tagged.
- **Canvas rendering** instead of `<video>` display -- video element hidden (`display:none`), frames drawn to canvas via `drawImage`. Touch coords mapped from `canvas._renderRect` (normalized render area set by draw loop).
- **Non-blocking frame drop** -- `emitAU` uses `select/default` to drop frames when `sendCh` is full rather than backpressure. Logs every 30th drop.
- **Generation-based lifecycle** -- `atomic.AddInt64(&c.generation, 1)` on each `Start()`. All goroutines check generation before mutating state.

## COMMANDS
```bash
go build -o lazyDesk.exe .     # Build (produces ~16MB binary with embedded web/)
go vet ./...                   # Lint (passes clean)
go test ./...                   # Tests (none exist -- all packages report [no test files])
go run .                        # Run (requires Windows + ffmpeg in PATH + display)
```

## NOTES
- **Audio track is a stub** -- Opus track created in PeerConnection but ffmpeg uses `-an` (no audio). Config has audio settings but they're unused.
- **No graceful HTTP shutdown** -- `http.ListenAndServe` in goroutine with `log.Fatalf`. Only `capture.Stop()` + `rtcManager.Close()` on SIGINT.
- **HandleWebSocket is monolithic** (328 LOC) -- handles upgrade, signaling, commands, heartbeat, ffmpeg lifecycle in one function.
- **DPI scale** (`config.control.dpi_scale`) must match Windows display scaling or mouse coords drift. Default 1.0 = 100%.
- **`config.local.yaml`** is in `.gitignore` but code doesn't read it -- only `config.yaml`.
- **Frontend has no build step** -- raw JS served via `//go:embed web/*`. No bundler, no minification.
<!-- OMO_INTERNAL_INITIATOR -->