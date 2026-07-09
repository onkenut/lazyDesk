package websocket

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onkenut/lazyDesk/control"
	"github.com/onkenut/lazyDesk/ffmpeg"
	slotwebrtc "github.com/onkenut/lazyDesk/webrtc"
	pionwebrtc "github.com/pion/webrtc/v4"
)

// 控制指令 JSON 结构体
type Command struct {
	Type      string   `json:"type"`
	X         float64  `json:"x,omitempty"`
	Y         float64  `json:"y,omitempty"`
	Button    string   `json:"button,omitempty"`
	Action    string   `json:"action,omitempty"`
	DeltaY    int      `json:"deltaY,omitempty"`
	Key       string   `json:"key,omitempty"`
	Keys      []string `json:"keys,omitempty"`
	Text      string   `json:"text,omitempty"`

	// WebRTC 信令字段
	SDP           string  `json:"sdp,omitempty"`
	Candidate     string  `json:"candidate,omitempty"`
	SdpMid        string  `json:"sdpMid,omitempty"`
	SdpMLineIndex *uint16 `json:"sdpMLineIndex,omitempty"`
}

// Handler WebSocket 连接处理器
type Handler struct {
	rtcManager *slotwebrtc.Manager
	capture    *ffmpeg.Capture
	cmdHandler *control.Handler
	upgrader   websocket.Upgrader
	connCount  int32 // 活跃连接数 (引用计数)
}

// NewHandler 创建 WebSocket 处理器
func NewHandler(rtc *slotwebrtc.Manager, cap *ffmpeg.Capture, cmd *control.Handler) *Handler {
	return &Handler{
		rtcManager: rtc,
		capture:    cap,
		cmdHandler: cmd,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

// HandleWebSocket 处理每个 WebSocket 连接
func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	var connClosed sync.Once
	closeCh := make(chan struct{})
	closeConn := func() {
		connClosed.Do(func() {
			close(closeCh)
			conn.Close()
		})
	}
	defer closeConn()

	log.Printf("WebSocket client connected: %s", r.RemoteAddr)

	// ROOT-1: Pong 处理器 — 客户端自动回复 Pong 时重置读超时
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// N6: per-connection writeMu — 不同连接互不阻塞
	writeMu := &sync.Mutex{}
	sendJSON := func(msg map[string]interface{}) {
		data, err := json.Marshal(msg)
		if err != nil {
			log.Printf("JSON marshal error: %v", err)
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("WebSocket write error: %v", err)
		}
	}
	sendError := func(code, message string) {
		sendJSON(map[string]interface{}{
			"type":    "error",
			"code":    code,
			"message": message,
		})
	}

	// 创建 PeerConnection (每个连接一个)
	pc, err := h.rtcManager.CreatePeerConnection()
	if err != nil {
		log.Printf("Failed to create PeerConnection: %v", err)
		sendError("peer_connection_failed", err.Error())
		return
	}
	defer func() {
		if err := pc.Close(); err != nil {
			log.Printf("PeerConnection close error: %v", err)
		}
	}()

	// 启动 ffmpeg 捕获 (引用计数 — 延迟到 WebRTC ready 后)
	// ARCH-2: 不在 WS 握手时 Start，避免 ffmpeg 帧撑爆 Pion 缓冲
	atomic.AddInt32(&h.connCount, 1)
	captureStarted := false
	startCapture := func() {
		if captureStarted {
			return
		}
		if !h.capture.IsRunning() {
			if err := h.capture.Start(); err != nil {
				log.Printf("Failed to start capture: %v", err)
				sendError("capture_failed", err.Error())
				return
			}
		}
		captureStarted = true
		// ROOT-3: 重发缓存的 SPS/PPS，新连接可以立即解码
		h.capture.ResendParameterSets()
	}
	defer func() {
		if atomic.AddInt32(&h.connCount, -1) == 0 {
			h.capture.Stop()
		}
	}()

	// 发送就绪信号
	sendJSON(map[string]interface{}{"type": "server_ready"})

	// WebRTC Offer 处理
	handleOffer := func(cmd *Command) {
		offer := pionwebrtc.SessionDescription{
			Type: pionwebrtc.SDPTypeOffer,
			SDP:  cmd.SDP,
		}

		if err := pc.SetRemoteDescription(offer); err != nil {
			log.Printf("SetRemoteDescription failed: %v", err)
			sendError("set_remote_failed", err.Error())
			return
		}

		answer, err := pc.CreateAnswer(nil)
		if err != nil {
			log.Printf("CreateAnswer failed: %v", err)
			sendError("create_answer_failed", err.Error())
			return
		}

		if err := pc.SetLocalDescription(answer); err != nil {
			log.Printf("SetLocalDescription failed: %v", err)
			sendError("set_local_failed", err.Error())
			return
		}

		// 发送 Answer 给客户端
		sendJSON(map[string]interface{}{
			"type": "answer",
			"sdp":  answer.SDP,
		})
	}

	// ICE candidate 处理
	handleCandidate := func(cmd *Command) {
		candidate := pionwebrtc.ICECandidateInit{
			Candidate: cmd.Candidate,
		}
		// B6: 必须做栈上副本，Pion 异步读取指针，不能让 cmd 复用后覆盖
		if cmd.SdpMid != "" {
			mid := cmd.SdpMid
			candidate.SDPMid = &mid
		}
		if cmd.SdpMLineIndex != nil {
			idx := *cmd.SdpMLineIndex
			candidate.SDPMLineIndex = &idx
		}
		if err := pc.AddICECandidate(candidate); err != nil {
			log.Printf("AddICECandidate failed: %v", err)
		}
	}

	// ICE candidate → 转发给客户端
	pc.OnICECandidate(func(candidate *pionwebrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		candidateJSON := candidate.ToJSON()
		sendJSON(map[string]interface{}{
			"type":          "candidate",
			"candidate":     candidateJSON.Candidate,
			"sdpMid":        candidateJSON.SDPMid,
			"sdpMLineIndex": candidateJSON.SDPMLineIndex,
		})
	})

	// WebRTC 连接状态
	pc.OnConnectionStateChange(func(state pionwebrtc.PeerConnectionState) {
		log.Printf("WebRTC state: %s", state.String())
		switch state {
		case pionwebrtc.PeerConnectionStateConnected:
			// ARCH-2: WebRTC 就绪后才启动 ffmpeg 捕获
			startCapture()
		case pionwebrtc.PeerConnectionStateFailed,
			pionwebrtc.PeerConnectionStateClosed:
			closeConn()
		case pionwebrtc.PeerConnectionStateDisconnected:
			// ROOT-5: ICE 短暂断开 — 给 ICE 重启机会，不立即关闭
			go func() {
				timer := time.NewTimer(30 * time.Second)
				defer timer.Stop()
				select {
				case <-timer.C:
					// 30s 后检查，如果仍未恢复则关闭
					if pc.ConnectionState() == pionwebrtc.PeerConnectionStateDisconnected {
						closeConn()
					}
				case <-closeCh:
					// 连接已通过其他方式关闭
				}
			}()
		}
	})

	// ROOT-1: 周期性 Ping 心跳 — 防止客户端无操作时 60s 读超时
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				writeMu.Lock()
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					writeMu.Unlock()
					return
				}
				writeMu.Unlock()
			case <-closeCh:
				return
			}
		}
	}()

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

		var cmd Command
		if err := json.Unmarshal(message, &cmd); err != nil {
			log.Printf("Invalid JSON: %v", err)
			continue
		}

		switch cmd.Type {

		// ===== WebRTC 信令 =====
		case "offer":
			handleOffer(&cmd)
		case "candidate":
			handleCandidate(&cmd)

		// ===== 鼠标 =====
		case "mouse_move":
			h.cmdHandler.MouseMove(cmd.X, cmd.Y)
		case "mouse_click":
			h.cmdHandler.MouseClick(cmd.Button, cmd.Action)
		case "mouse_scroll":
			h.cmdHandler.MouseScroll(cmd.DeltaY)

		// ===== 键盘 =====
		case "key_press":
			h.cmdHandler.KeyPress(cmd.Key, cmd.Action)
		case "key_combo":
			h.cmdHandler.KeyCombo(cmd.Keys)
		case "text_input":
			h.cmdHandler.TextInput(cmd.Text)

		// ===== 电源 =====
		case "power":
			h.cmdHandler.PowerAction(cmd.Action)

		default:
			log.Printf("Unknown command: %s", cmd.Type)
		}
	}
}
