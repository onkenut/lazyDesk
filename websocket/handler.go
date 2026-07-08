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
	defer conn.Close()

	log.Printf("WebSocket client connected: %s", r.RemoteAddr)

	// 创建 PeerConnection (每个连接一个)
	pc, err := h.rtcManager.CreatePeerConnection()
	if err != nil {
		log.Printf("Failed to create PeerConnection: %v", err)
		h.sendError(conn, "peer_connection_failed", err.Error())
		return
	}
	defer func() {
		if err := pc.Close(); err != nil {
			log.Printf("PeerConnection close error: %v", err)
		}
	}()

	// 启动 ffmpeg 捕获
	if !h.capture.IsRunning() {
		if err := h.capture.Start(); err != nil {
			log.Printf("Failed to start capture: %v", err)
			h.sendError(conn, "capture_failed", err.Error())
			return
		}
		defer h.capture.Stop()
	}

	// 发送就绪信号
	h.sendJSON(conn, map[string]interface{}{"type": "server_ready"})

	// ICE candidate → 转发给客户端
	pc.OnICECandidate(func(candidate *pionwebrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		candidateJSON := candidate.ToJSON()
		h.sendJSON(conn, map[string]interface{}{
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
		case pionwebrtc.PeerConnectionStateFailed,
			pionwebrtc.PeerConnectionStateDisconnected,
			pionwebrtc.PeerConnectionStateClosed:
			conn.Close()
		}
	})

	// 主消息循环
	for {
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

		h.handleCommand(&cmd, pc, conn)
	}
}

// handleCommand 路由控制指令
func (h *Handler) handleCommand(cmd *Command, pc *pionwebrtc.PeerConnection, conn *websocket.Conn) {
	switch cmd.Type {

	// ===== WebRTC 信令 =====
	case "offer":
		h.handleOffer(cmd, pc, conn)
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
		log.Printf("Unknown command: %s", cmd.Type)
	}
}

// handleOffer 处理 WebRTC Offer → 创建 Answer
func (h *Handler) handleOffer(cmd *Command, pc *pionwebrtc.PeerConnection, conn *websocket.Conn) {
	offer := pionwebrtc.SessionDescription{
		Type: pionwebrtc.SDPTypeOffer,
		SDP:  cmd.SDP,
	}

	if err := pc.SetRemoteDescription(offer); err != nil {
		log.Printf("SetRemoteDescription failed: %v", err)
		h.sendError(conn, "set_remote_failed", err.Error())
		return
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		log.Printf("CreateAnswer failed: %v", err)
		h.sendError(conn, "create_answer_failed", err.Error())
		return
	}

	if err := pc.SetLocalDescription(answer); err != nil {
		log.Printf("SetLocalDescription failed: %v", err)
		h.sendError(conn, "set_local_failed", err.Error())
		return
	}

	// 发送 Answer 给客户端
	h.sendJSON(conn, map[string]interface{}{
		"type": "answer",
		"sdp":  answer.SDP,
	})
}

// handleCandidate 处理 ICE 候选
func (h *Handler) handleCandidate(cmd *Command, pc *pionwebrtc.PeerConnection) {
	candidate := pionwebrtc.ICECandidateInit{
		Candidate: cmd.Candidate,
	}
	if cmd.SdpMid != "" {
		candidate.SDPMid = &cmd.SdpMid
	}
	if cmd.SdpMLineIndex != nil {
		candidate.SDPMLineIndex = cmd.SdpMLineIndex
	}
	if err := pc.AddICECandidate(candidate); err != nil {
		log.Printf("AddICECandidate failed: %v", err)
	}
}

// sendJSON 发送 JSON 消息
func (h *Handler) sendJSON(conn *websocket.Conn, msg map[string]interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("JSON marshal error: %v", err)
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("WebSocket write error: %v", err)
	}
}

// sendError 发送错误消息
func (h *Handler) sendError(conn *websocket.Conn, code, message string) {
	h.sendJSON(conn, map[string]interface{}{
		"type":    "error",
		"code":    code,
		"message": message,
	})
}
