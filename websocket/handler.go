package websocket

import (
	"encoding/json"
	"log"
	"net/http"

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
	SDP       string `json:"sdp,omitempty"`
	Candidate string `json:"candidate,omitempty"`
}

// Handler WebSocket 连接处理器
type Handler struct {
	rtcManager *slotwebrtc.Manager
	capture    *ffmpeg.Capture
	cmdHandler *control.Handler
	upgrader   websocket.Upgrader
	conn       *websocket.Conn // 当前连接的引用
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

// HandleWebSocket 处理 WebSocket 连接 (信令 + 控制指令)
func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	h.conn = conn
	log.Printf("WebSocket client connected: %s", r.RemoteAddr)

	// 创建 PeerConnection
	pc, err := h.rtcManager.CreatePeerConnection()
	if err != nil {
		log.Printf("Failed to create PeerConnection: %v", err)
		return
	}

	// 启动 ffmpeg 捕获 (如尚未启动)
	if !h.capture.IsRunning() {
		if err := h.capture.Start(); err != nil {
			log.Printf("Failed to start capture: %v", err)
			return
		}
	}

	// 监听 ICE 候选
	pc.OnICECandidate(func(candidate *pionwebrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		candidateJSON := candidate.ToJSON()
		msg := map[string]interface{}{
			"type":          "candidate",
			"candidate":     candidateJSON.Candidate,
			"sdpMid":        candidateJSON.SDPMid,
			"sdpMLineIndex": candidateJSON.SDPMLineIndex,
		}
		data, _ := json.Marshal(msg)
		conn.WriteMessage(websocket.TextMessage, data)
	})

	// 主消息循环
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			break
		}

		var cmd Command
		if err := json.Unmarshal(message, &cmd); err != nil {
			log.Printf("Invalid JSON command: %v", err)
			continue
		}

		h.handleCommand(&cmd, pc)
	}
}

// handleCommand 根据指令类型路由到对应处理器
func (h *Handler) handleCommand(cmd *Command, pc *pionwebrtc.PeerConnection) {
	switch cmd.Type {
	// ===== WebRTC 信令 =====
	case "offer":
		h.handleOffer(cmd, pc)
	case "candidate":
		h.handleCandidate(cmd, pc)

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
		log.Printf("Unknown command type: %s", cmd.Type)
	}
}

// handleOffer 处理 WebRTC Offer SDP
func (h *Handler) handleOffer(cmd *Command, pc *pionwebrtc.PeerConnection) {
	offer := pionwebrtc.SessionDescription{
		Type: pionwebrtc.SDPTypeOffer,
		SDP:  cmd.SDP,
	}

	if err := pc.SetRemoteDescription(offer); err != nil {
		log.Printf("Failed to set remote description: %v", err)
		return
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		log.Printf("Failed to create answer: %v", err)
		return
	}

	if err := pc.SetLocalDescription(answer); err != nil {
		log.Printf("Failed to set local description: %v", err)
		return
	}

	// 发送 Answer 给客户端
	msg := map[string]interface{}{
		"type": "answer",
		"sdp":  answer.SDP,
	}
	data, _ := json.Marshal(msg)
	if h.conn != nil {
		h.conn.WriteMessage(websocket.TextMessage, data)
	}
}

// handleCandidate 处理 ICE 候选
func (h *Handler) handleCandidate(cmd *Command, pc *pionwebrtc.PeerConnection) {
	candidate := pionwebrtc.ICECandidateInit{
		Candidate: cmd.Candidate,
	}
	if err := pc.AddICECandidate(candidate); err != nil {
		log.Printf("Failed to add ICE candidate: %v", err)
	}
}
