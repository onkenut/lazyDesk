package webrtc

import (
	"log"
	"sync"

	"github.com/onkenut/lazyDesk/config"
	"github.com/pion/webrtc/v4"
)

// Manager 管理 WebRTC PeerConnection 的生命周期
type Manager struct {
	cfg         *config.Config
	peerConn    *webrtc.PeerConnection
	videoTrack  *webrtc.TrackLocalStaticSample
	audioTrack  *webrtc.TrackLocalStaticSample
	mu          sync.Mutex
}

// NewManager 创建新的 WebRTC 管理器
func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		cfg: cfg,
	}
}

// CreatePeerConnection 创建 PeerConnection 并将 tracks 添加进去
func (m *Manager) CreatePeerConnection() (*webrtc.PeerConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 配置 ICE 服务器 (局域网不需要)
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{},
	}

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}

	// 创建视频 track
	videoTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video", "lazyDesk-screen",
	)
	if err != nil {
		pc.Close()
		return nil, err
	}
	if _, err := pc.AddTrack(videoTrack); err != nil {
		pc.Close()
		return nil, err
	}
	m.videoTrack = videoTrack

	// 创建音频 track
	audioTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "lazyDesk-audio",
	)
	if err != nil {
		pc.Close()
		return nil, err
	}
	if _, err := pc.AddTrack(audioTrack); err != nil {
		pc.Close()
		return nil, err
	}
	m.audioTrack = audioTrack

	// 监听连接状态变化
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("WebRTC connection state: %s", state.String())
	})

	m.peerConn = pc
	return pc, nil
}

// GetPeerConnection 返回当前的 PeerConnection
func (m *Manager) GetPeerConnection() *webrtc.PeerConnection {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.peerConn
}

// GetVideoTrack 返回视频 track (用于写入 H264 数据)
func (m *Manager) GetVideoTrack() *webrtc.TrackLocalStaticSample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.videoTrack
}

// GetAudioTrack 返回音频 track (用于写入 Opus 数据)
func (m *Manager) GetAudioTrack() *webrtc.TrackLocalStaticSample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.audioTrack
}

// Close 关闭连接
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.peerConn != nil {
		m.peerConn.Close()
	}
}
