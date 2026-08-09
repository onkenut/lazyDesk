// Package webrtc 为每个客户端创建独立的 PeerConnection。
package webrtc

import (
	"fmt"

	"github.com/onkenut/lazyDesk/config"
	"github.com/pion/webrtc/v4"
)

// Peer 是一个客户端的完整 WebRTC 连接 (视频 + 可选音频 track)。
type Peer struct {
	PC    *webrtc.PeerConnection
	Video *webrtc.TrackLocalStaticSample
	Audio *webrtc.TrackLocalStaticSample
}

// Factory 创建 Peer 的工厂 (持有共享的 MediaEngine/SettingEngine)。
type Factory struct {
	api *webrtc.API
	cfg *config.Config
}

// NewFactory 构建 WebRTC API:
// - 仅注册 H264 + Opus (精确控制 payload type, 排除 VP8/VP9 干扰)
// - 仅收集 UDP4 candidates (过滤平板无法到达的 IPv6/Teredo 地址)
func NewFactory(cfg *config.Config) (*Factory, error) {
	mediaEngine := &webrtc.MediaEngine{}

	// H264 全部标准 profile (baseline 42001f → high 64001f), 覆盖 iOS/Android 浏览器
	h264Profiles := []struct {
		pt   webrtc.PayloadType
		fmtp string
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
				MimeType:    webrtc.MimeTypeH264,
				ClockRate:   90000,
				SDPFmtpLine: p.fmtp,
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

	settingEngine := webrtc.SettingEngine{}
	// 仅 UDP4: 本工具面向局域网, IPv6/Teredo candidates 只会拖慢 ICE
	settingEngine.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})

	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithSettingEngine(settingEngine),
	)
	return &Factory{api: api, cfg: cfg}, nil
}

// NewPeer 创建新的 PeerConnection (不含 track, 由 AddTracks 按客户端 offer 添加)
func (f *Factory) NewPeer() (*Peer, error) {
	iceServers := []webrtc.ICEServer{}
	for _, url := range f.cfg.WebRTC.ICEServers {
		iceServers = append(iceServers, webrtc.ICEServer{URLs: []string{url}})
	}
	pc, err := f.api.NewPeerConnection(webrtc.Configuration{
		ICEServers: iceServers,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 PeerConnection 失败: %w", err)
	}
	return &Peer{PC: pc}, nil
}

// AddTracks 添加视频 track (必选) 和音频 track (按客户端 offer 决定)。
// 必须在 CreateAnswer 之前调用。
func (p *Peer) AddTracks(includeAudio bool) error {
	video, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video", "lazyDesk-screen",
	)
	if err != nil {
		return fmt.Errorf("创建视频 track 失败: %w", err)
	}
	if _, err := p.PC.AddTrack(video); err != nil {
		return fmt.Errorf("添加视频 track 失败: %w", err)
	}
	p.Video = video

	if includeAudio {
		audio, err := webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
			"audio", "lazyDesk-audio",
		)
		if err != nil {
			return fmt.Errorf("创建音频 track 失败: %w", err)
		}
		if _, err := p.PC.AddTrack(audio); err != nil {
			return fmt.Errorf("添加音频 track 失败: %w", err)
		}
		p.Audio = audio
	}
	return nil
}

// Close 关闭连接 (幂等)
func (p *Peer) Close() error {
	return p.PC.Close()
}
