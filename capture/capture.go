package capture

import (
	"fmt"
	"sync"

	"github.com/onkenut/lazyDesk/config"
)

// Capture 聚合视频/音频采集与扇出 Hub, 对外提供引用计数订阅:
// 第一个订阅者启动采集, 最后一个退订时停止。
type Capture struct {
	Hub   *Hub
	Video *VideoCapture
	Audio *AudioCapture

	probeErr error // ffmpeg 探测失败原因 (Subscribe 时上报)

	refMu sync.Mutex
	refs  int
}

// New 创建采集器。onEvent 接收采集事件 (崩溃/降级/音频禁用等), 可传 nil。
func New(cfg *config.Config, onEvent func(Event)) *Capture {
	probe, err := Probe(cfg.Ffmpeg.Path)
	hub := NewHub()
	if err != nil {
		// ffmpeg 不可用: 仍构造对象, Subscribe 时返回明确错误
		return &Capture{
			Hub:      hub,
			probeErr: err,
		}
	}
	sc := cfg.Ffmpeg.Screen
	video := NewVideo(ScreenCfg{
		Codec:     sc.Codec,
		Preset:    sc.Preset,
		Tune:      sc.Tune,
		PixFmt:    sc.PixFmt,
		Profile:   sc.Profile,
		Bitrate:   sc.Bitrate,
		GopSize:   sc.GopSize,
		Framerate: sc.Framerate,
		Capture: CaptureCfg{
			Method:    sc.Capture.Method,
			OutputIdx: sc.Capture.OutputIdx,
			DrawMouse: sc.Capture.DrawMouse,
			DupFrames: sc.Capture.DupFrames,
		},
		NVENC: NVENCConfig{
			RateControl: sc.NVENC.RateControl,
			Maxrate:     sc.NVENC.Maxrate,
			Bufsize:     sc.NVENC.Bufsize,
			BFrames:     sc.NVENC.BFrames,
			Multipass:   sc.NVENC.Multipass,
			Delay:       sc.NVENC.Delay,
			NoScenecut:  sc.NVENC.NoScenecut,
			RCLookahead: sc.NVENC.RCLookahead,
			BRefMode:    sc.NVENC.BRefMode,
			NonrefP:     sc.NVENC.NonrefP,
		},
		Fallback: sc.Fallback,
	}, probe, hub, onEvent)
	audio := NewAudio(AudioConfig{
		Device:  cfg.Ffmpeg.Audio.Device,
		Bitrate: cfg.Ffmpeg.Audio.Bitrate,
	}, probe, hub, onEvent)

	return &Capture{
		Hub:   hub,
		Video: video,
		Audio: audio,
	}
}

// subscriberBuf 每个订阅者的缓冲帧数 (慢客户端在此范围内吸收抖动)
const subscriberBuf = 16

// Subscribe 注册一个订阅并确保采集运行。返回的订阅从 Chan 读取样本。
func (c *Capture) Subscribe() (*Subscriber, error) {
	c.refMu.Lock()
	defer c.refMu.Unlock()

	if c.probeErr != nil {
		return nil, fmt.Errorf("ffmpeg 不可用: %v", c.probeErr)
	}
	sub := c.Hub.Subscribe(subscriberBuf)
	if c.refs == 0 {
		if err := c.Video.Start(); err != nil {
			c.Hub.Unsubscribe(sub)
			return nil, err
		}
		c.Audio.Start() // 音频失败不影响视频
	}
	c.refs++
	return sub, nil
}

// Unsubscribe 注销订阅; 最后一个退订时停止采集
func (c *Capture) Unsubscribe(sub *Subscriber) {
	c.refMu.Lock()
	defer c.refMu.Unlock()
	if c.refs > 0 {
		c.refs--
	}
	if c.refs == 0 {
		c.Video.Stop()
		c.Audio.Stop()
	}
	c.Hub.Unsubscribe(sub)
}

// VideoResolution 返回当前采集分辨率 (未就绪时为 0,0)
func (c *Capture) VideoResolution() (int, int) {
	if c.Video == nil {
		return 0, 0
	}
	return c.Video.Resolution()
}

// SetOnEvent 设置采集事件回调 (server 启动后绑定, 用于转发给客户端)
func (c *Capture) SetOnEvent(cb func(Event)) {
	if c.Video != nil {
		c.Video.onEvent = cb
	}
	if c.Audio != nil {
		c.Audio.onEvent = cb
	}
}

// ParameterSets 返回缓存的 SPS+PPS (供新连接立即解码)
func (c *Capture) ParameterSets() []byte {
	if c.Video == nil {
		return nil
	}
	return c.Video.ParameterSets()
}

// VideoRTPTS 返回当前视频 RTP 时间戳 (参数集重发时保持时间戳连续)
func (c *Capture) VideoRTPTS() uint32 {
	if c.Video == nil {
		return 0
	}
	return c.Video.LastRTPTS()
}

// Close 停止全部采集 (进程退出时调用)
func (c *Capture) Close() {
	if c.Video != nil {
		c.Video.Stop()
	}
	if c.Audio != nil {
		c.Audio.Stop()
	}
}
