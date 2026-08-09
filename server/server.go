// Package server 提供 HTTP 服务: 静态前端 / WebSocket 信令 / 状态 API
package server

import (
	"encoding/json"
	"io/fs"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/onkenut/lazyDesk/capture"
	"github.com/onkenut/lazyDesk/config"
	"github.com/onkenut/lazyDesk/control"
	slotwebrtc "github.com/onkenut/lazyDesk/webrtc"
)

// Version 构建版本号
const Version = "v2.0.0"

// Server 聚合 HTTP 服务
type Server struct {
	cfg     *config.Config
	capture *capture.Capture
	factory *slotwebrtc.Factory
	control *control.Handler
	started time.Time

	mu       sync.Mutex
	sessions map[*Session]struct{}
}

// NewServer 创建服务
func NewServer(cfg *config.Config, cap *capture.Capture, factory *slotwebrtc.Factory, ctrl *control.Handler) *Server {
	return &Server{
		cfg:      cfg,
		capture:  cap,
		factory:  factory,
		control:  ctrl,
		started:  time.Now(),
		sessions: make(map[*Session]struct{}),
	}
}

// Handler 返回 HTTP 路由
func (s *Server) Handler(webFS fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.Handle("/", http.FileServer(http.FS(webFS)))
	return mux
}

// clientCount 当前活跃客户端数
func (s *Server) clientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

// addSession / removeSession 会话注册表
func (s *Server) addSession(sess *Session) {
	s.mu.Lock()
	s.sessions[sess] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) removeSession(sess *Session) {
	s.mu.Lock()
	delete(s.sessions, sess)
	s.mu.Unlock()
}

// BroadcastEvent 把采集事件推送给所有在线客户端
func (s *Server) BroadcastEvent(ev capture.Event) {
	s.mu.Lock()
	sessions := make([]*Session, 0, len(s.sessions))
	for sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()
	for _, sess := range sessions {
		sess.sendJSON(map[string]interface{}{
			"type":    "event",
			"event":   string(ev.Type),
			"message": ev.Message,
		})
	}
}

// lanAddrs 枚举本机局域网 IPv4 地址 (供平板输入/状态页显示)
func lanAddrs() []string {
	var addrs []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return addrs
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrsList, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrsList {
			if ipnet, ok := a.(*net.IPNet); ok {
				ip := ipnet.IP.To4()
				if ip != nil && !ip.IsLoopback() {
					addrs = append(addrs, ip.String())
				}
			}
		}
	}
	return addrs
}

// statusResponse /api/status 返回体
type statusResponse struct {
	Version string             `json:"version"`
	Uptime  int64              `json:"uptime_s"`
	Clients int                `json:"clients"`
	Video   capture.VideoStatus `json:"video"`
	Audio   capture.AudioStatus `json:"audio"`
	Addrs   []string           `json:"addrs"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := statusResponse{
		Version: Version,
		Uptime:  int64(time.Since(s.started).Seconds()),
		Clients: s.clientCount(),
		Video:   s.capture.Video.Status(),
		Audio:   s.capture.Audio.Status(),
		Addrs:   lanAddrs(),
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("[server] /api/status 编码失败: %v", err)
	}
}
