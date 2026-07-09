package webrtc

import (
	"log"
	"sync"

	"github.com/onkenut/lazyDesk/config"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// Manager 管理 WebRTC PeerConnection 的生命周期
type Manager struct {
	cfg        *config.Config
	peerConn   *webrtc.PeerConnection
	videoTrack *webrtc.TrackLocalStaticSample
	audioTrack *webrtc.TrackLocalStaticSample
	mu         sync.Mutex
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

	// N5: 先关闭旧 PeerConnection，防止引用泄漏
	// ROOT-4: 不在此处置空 m.videoTrack，避免 WriteVideoSample 读到 nil 静默丢帧。
	if m.peerConn != nil {
		m.peerConn.Close()
		m.peerConn = nil
	}

	// 仅注册 H264 (所有标准 profile) + Opus
	// 精确控制 payload type，避免 RegisterDefaultCodecs 的 VP8/VP9 干扰
	mediaEngine := &webrtc.MediaEngine{}

	h264Profiles := []struct {
		pt     webrtc.PayloadType
		fmtp   string
	}{
		{103, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f"},
		{107, "level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42001f"},
		{109, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"},
		{114, "level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42e01f"},
		{115, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=4d001f"},
		{39, "level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=4d001f"},
		{40, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=64001f"},
	}
	for _, p := range h264Profiles {
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH264,
				ClockRate:    90000,
				SDPFmtpLine:  p.fmtp,
				RTCPFeedback: []webrtc.RTCPFeedback{
					{Type: "goog-remb"},
					{Type: "ccm", Parameter: "fir"},
					{Type: "nack"},
					{Type: "nack", Parameter: "pli"},
				},
			},
			PayloadType: p.pt,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	}

	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: 48000,
			Channels:  2,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine))

	// ICE 配置: 从 config.yaml 读取
	iceServers := []webrtc.ICEServer{}
	for _, url := range m.cfg.WebRTC.ICEServers {
		iceServers = append(iceServers, webrtc.ICEServer{URLs: []string{url}})
	}
	config := webrtc.Configuration{
		ICEServers: iceServers,
	}

	pc, err := api.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}

	// 创建视频 track (H264)
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

	// 创建音频 track (Opus) — 暂未使用, 预留
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

	// 监听连接状态
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("WebRTC connection state: %s", state.String())
	})

	m.peerConn = pc
	return pc, nil
}

// WriteVideoSample 将视频 sample 写入 track (实现 ffmpeg.VideoWriter 接口)
func (m *Manager) WriteVideoSample(sample media.Sample) error {
	m.mu.Lock()
	track := m.videoTrack
	m.mu.Unlock()

	if track == nil {
		return nil
	}
	return track.WriteSample(sample)
}

// GetPeerConnection 返回当前的 PeerConnection
func (m *Manager) GetPeerConnection() *webrtc.PeerConnection {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.peerConn
}

// Close 关闭连接
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.peerConn != nil {
		m.peerConn.Close()
	}
}
