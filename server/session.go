package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onkenut/lazyDesk/capture"
	slotwebrtc "github.com/onkenut/lazyDesk/webrtc"
	pionwebrtc "github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// Command 客户端 → 服务端消息结构
type Command struct {
	Type   string  `json:"type"`
	X      float64 `json:"x,omitempty"`
	Y      float64 `json:"y,omitempty"`
	Button string  `json:"button,omitempty"`
	Action string  `json:"action,omitempty"`
	DeltaY int     `json:"deltaY,omitempty"`
	Key    string  `json:"key,omitempty"`
	Keys   []string `json:"keys,omitempty"`
	Text   string  `json:"text,omitempty"`

	// WebRTC 信令字段
	SDP           string  `json:"sdp,omitempty"`
	Candidate     string  `json:"candidate,omitempty"`
	SdpMid        string  `json:"sdpMid,omitempty"`
	SdpMLineIndex *uint16 `json:"sdpMLineIndex,omitempty"`
}

// Session 一个客户端的 WS 连接 + WebRTC 生命周期
type Session struct {
	srv       *Server
	conn      *websocket.Conn
	remote    string
	writeMu   sync.Mutex
	closeOnce sync.Once
	closeCh   chan struct{}

	peerObj *slotwebrtc.Peer
	sub     *capture.Subscriber
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleWS 处理 WebSocket 连接
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] 升级失败: %v", err)
		return
	}

	sess := &Session{
		srv:     s,
		conn:    conn,
		remote:  r.RemoteAddr,
		closeCh: make(chan struct{}),
	}
	s.addSession(sess)
	defer sess.close()

	log.Printf("[ws] 客户端连接: %s (在线 %d)", sess.remote, s.clientCount())

	// Pong 处理器 — 重置读超时
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// 发送就绪信号
	sess.sendJSON(map[string]interface{}{"type": "server_ready"})

	// 周期性 Ping 心跳 + stats 推送
	go sess.pingLoop()
	go sess.statsLoop()

	// 主消息循环
	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[ws] 连接错误: %v", err)
			}
			break
		}

		var cmd Command
		if err := json.Unmarshal(message, &cmd); err != nil {
			log.Printf("[ws] 无效 JSON: %v", err)
			continue
		}
		sess.dispatch(&cmd)
	}
	log.Printf("[ws] 客户端断开: %s", sess.remote)
}

// dispatch 分发消息
func (s *Session) dispatch(cmd *Command) {
	switch cmd.Type {

	// ===== WebRTC 信令 =====
	case "offer":
		s.handleOffer(cmd)
	case "candidate":
		s.handleCandidate(cmd)

	// ===== 鼠标 =====
	case "mouse_move":
		w, h := s.srv.capture.VideoResolution()
		s.srv.control.MouseMove(cmd.X, cmd.Y, w, h)
	case "mouse_click":
		s.srv.control.MouseClick(cmd.Button, cmd.Action)
	case "mouse_scroll":
		s.srv.control.MouseScroll(cmd.DeltaY)

	// ===== 键盘 =====
	case "key_press":
		s.srv.control.KeyPress(cmd.Key, cmd.Action)
	case "key_combo":
		s.srv.control.KeyCombo(cmd.Keys)
	case "text_input":
		s.srv.control.TextInput(cmd.Text)

	// ===== 电源 =====
	case "power":
		s.srv.control.PowerAction(cmd.Action)

	default:
		log.Printf("[ws] 未知指令: %s", cmd.Type)
	}
}

// handleOffer 处理 WebRTC Offer → 创建 PeerConnection + tracks → Answer
func (s *Session) handleOffer(cmd *Command) {
	// 会话已关闭, 忽略迟到消息
	select {
	case <-s.closeCh:
		return
	default:
	}

	// 重复 offer (刷新/重连): 先清理旧连接
	s.closePeer()

	peer, err := s.srv.factory.NewPeer()
	if err != nil {
		s.sendError("peer_connection_failed", err.Error())
		return
	}
	// 关闭竞态: close() 与读循环并发时, 新 Peer 需在 close 之后被清理
	select {
	case <-s.closeCh:
		peer.Close()
		return
	default:
	}
	s.peerObj = peer

	offer := pionwebrtc.SessionDescription{Type: pionwebrtc.SDPTypeOffer, SDP: cmd.SDP}
	if err := peer.PC.SetRemoteDescription(offer); err != nil {
		s.closePeer()
		s.sendError("set_remote_failed", "解析 Offer 失败: "+err.Error())
		return
	}
	log.Printf("[webrtc] %s: Offer m-lines: %s", s.remote, sdpMLines(cmd.SDP))

	// 按客户端 offer 决定是否加音频 track (客户端 addTransceiver('audio') 才有 m=audio)
	includeAudio := strings.Contains(cmd.SDP, "m=audio")
	if err := peer.AddTracks(includeAudio); err != nil {
		s.closePeer()
		s.sendError("add_track_failed", err.Error())
		return
	}

	// 注册回调 (必须在 SetLocalDescription 之前, 否则丢失 early candidates)
	peer.PC.OnICECandidate(func(c *pionwebrtc.ICECandidate) {
		if c == nil {
			return
		}
		j := c.ToJSON()
		s.sendJSON(map[string]interface{}{
			"type":          "candidate",
			"candidate":     j.Candidate,
			"sdpMid":        j.SDPMid,
			"sdpMLineIndex": j.SDPMLineIndex,
		})
	})
	peer.PC.OnConnectionStateChange(func(state pionwebrtc.PeerConnectionState) {
		log.Printf("[webrtc] %s 状态: %s", s.remote, state.String())
		switch state {
		case pionwebrtc.PeerConnectionStateConnected:
			s.onConnected()
		case pionwebrtc.PeerConnectionStateFailed,
			pionwebrtc.PeerConnectionStateClosed:
			s.close()
		case pionwebrtc.PeerConnectionStateDisconnected:
			// ICE 短暂断开 — 给 ICE 重启机会, 30s 未恢复则关闭
			go func() {
				timer := time.NewTimer(30 * time.Second)
				defer timer.Stop()
				select {
				case <-timer.C:
					if peer.PC.ConnectionState() == pionwebrtc.PeerConnectionStateDisconnected {
						s.close()
					}
				case <-s.closeCh:
				}
			}()
		}
	})

	answer, err := peer.PC.CreateAnswer(nil)
	if err != nil {
		s.closePeer()
		s.sendError("create_answer_failed", err.Error())
		return
	}
	if err := peer.PC.SetLocalDescription(answer); err != nil {
		s.closePeer()
		s.sendError("set_local_failed", err.Error())
		return
	}
	s.sendJSON(map[string]interface{}{
		"type": "answer",
		"sdp":  answer.SDP,
	})
	log.Printf("[webrtc] %s: Answer 已发送 (audio=%v) m-lines: %s", s.remote, includeAudio, sdpMLines(answer.SDP))
}

// sdpMLines 提取 SDP 的 m= 与编解码器行摘要 (调试用)
func sdpMLines(sdp string) string {
	var out []string
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "m=") || strings.HasPrefix(line, "a=rtpmap:") ||
			strings.HasPrefix(line, "a=fmtp:") || strings.HasPrefix(line, "a=group:") {
			out = append(out, line)
		}
	}
	return strings.Join(out, " | ")
}

// handleCandidate 添加远程 ICE candidate
func (s *Session) handleCandidate(cmd *Command) {
	if s.peerObj == nil {
		return // offer 未到, 忽略
	}
	candidate := pionwebrtc.ICECandidateInit{Candidate: cmd.Candidate}
	if cmd.SdpMid != "" {
		mid := cmd.SdpMid
		candidate.SDPMid = &mid
	}
	if cmd.SdpMLineIndex != nil {
		idx := *cmd.SdpMLineIndex
		candidate.SDPMLineIndex = &idx
	}
	if err := s.peerObj.PC.AddICECandidate(candidate); err != nil {
		log.Printf("[webrtc] AddICECandidate 失败: %v", err)
	}
}

// onConnected WebRTC 就绪 → 订阅采集流并开始推流
func (s *Session) onConnected() {
	if s.sub != nil {
		return
	}
	sub, err := s.srv.capture.Subscribe()
	if err != nil {
		s.sendError("capture_failed", err.Error())
		s.close()
		return
	}
	s.sub = sub

	// 立即重发 SPS/PPS, 新客户端无需等待下一个关键帧
	// 时间戳必须与当前流连续, 否则解码器会丢弃
	if ps := s.srv.capture.ParameterSets(); ps != nil {
		peerObj := s.peerObj
		if peerObj != nil && peerObj.Video != nil {
			peerObj.Video.WriteSample(media.Sample{
				Data:            ps,
				PacketTimestamp: s.srv.capture.VideoRTPTS(),
				Duration:        time.Second / 30,
			})
		}
	}
	go s.pump()
}

// pump 从订阅通道读取样本并写入 WebRTC track
// 注意: 启动时固定 sub/peerObj 引用 — closePeer 会置 nil, 避免 goroutine 竞态崩溃
func (s *Session) pump() {
	sub := s.sub
	peerObj := s.peerObj
	if sub == nil || peerObj == nil {
		return
	}
	var videoSamples int64
	for {
		select {
		case <-s.closeCh:
			return
		case sample := <-sub.Chan():
			ms := media.Sample{
				Data:            sample.Data,
				PacketTimestamp: sample.TS,
				Duration:        sample.Duration,
			}
			var track *pionwebrtc.TrackLocalStaticSample
			switch sample.Kind {
			case capture.KindVideo:
				track = peerObj.Video
				videoSamples++
				if videoSamples%300 == 0 {
					log.Printf("[webrtc] %s: 视频样本 %d (最近 %d 字节)", s.remote, videoSamples, len(sample.Data))
				}
			case capture.KindAudio:
				track = peerObj.Audio
			}
			if track != nil {
				if err := track.WriteSample(ms); err != nil {
					log.Printf("[webrtc] %s: WriteSample(%v) 错误: %v", s.remote, sample.Kind, err)
					// PC 关闭后返回错误, 由状态回调触发清理
					select {
					case <-s.closeCh:
						return
					default:
					}
				}
			}
		}
	}
}

// pingLoop 周期性发送 Ping 防止空闲超时
func (s *Session) pingLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.writeMu.Lock()
			s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := s.conn.WriteMessage(websocket.PingMessage, nil)
			s.writeMu.Unlock()
			if err != nil {
				return
			}
		case <-s.closeCh:
			return
		}
	}
}

// statsLoop 每 2 秒推送采集统计给客户端
func (s *Session) statsLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			vs := s.srv.capture.Video.Status()
			as := s.srv.capture.Audio.Status()
			var dropped int64
			if s.sub != nil {
				dropped = s.sub.Dropped()
			}
			s.sendJSON(map[string]interface{}{
				"type":     "stats",
				"fps":      vs.FPS,
				"pipeline": vs.Pipeline,
				"audio":    as.Running,
				"clients":  s.srv.clientCount(),
				"dropped":  dropped,
			})
		case <-s.closeCh:
			return
		}
	}
}

// sendJSON 发送 JSON 消息 (线程安全)
func (s *Session) sendJSON(msg map[string]interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[ws] JSON 编码失败: %v", err)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("[ws] 发送失败: %v", err)
	}
}

// sendError 发送错误消息
func (s *Session) sendError(code, message string) {
	s.sendJSON(map[string]interface{}{
		"type":    "error",
		"code":    code,
		"message": message,
	})
}

// closePeer 关闭 WebRTC 连接 (保留 WS)
func (s *Session) closePeer() {
	if s.sub != nil {
		s.srv.capture.Unsubscribe(s.sub)
		s.sub = nil
	}
	if s.peerObj != nil {
		s.peerObj.Close()
		s.peerObj = nil
	}
}

// close 关闭整个会话 (WS + WebRTC + 订阅)
func (s *Session) close() {
	s.closeOnce.Do(func() {
		close(s.closeCh)
		s.closePeer()
		s.conn.Close()
		s.srv.removeSession(s)
	})
}
