package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config 应用配置根结构
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Ffmpeg  FfmpegConfig  `yaml:"ffmpeg"`
	WebRTC  WebRTCConfig  `yaml:"webrtc"`
	Control ControlConfig `yaml:"control"`
	Power   PowerConfig   `yaml:"power"`
}

// ServerConfig 服务配置
type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

// ScreenConfig 屏幕捕获参数
type ScreenConfig struct {
	Framerate int          `yaml:"framerate"`
	Codec     string       `yaml:"codec"`
	Preset    string       `yaml:"preset"`
	Tune      string       `yaml:"tune"`
	PixFmt    string       `yaml:"pix_fmt"`
	Profile   string       `yaml:"profile"`   // H.264 profile (baseline/high)
	Capture   CaptureCfg   `yaml:"capture"`   // 捕获方式配置
	NVENC     NVENCConfig  `yaml:"nvenc"`     // NVENC 专属参数
	Bitrate   string       `yaml:"bitrate"`   // 视频码率 (如 "15M")
	GopSize   int          `yaml:"gop_size"`  // IDR 帧间隔
}

// CaptureCfg 屏幕捕获方式配置
type CaptureCfg struct {
	Method     string `yaml:"method"`      // "ddagrab" 或 "gdigrab"
	OutputIdx  int    `yaml:"output_idx"`  // 显示器索引 (0=主屏)
	DrawMouse  bool   `yaml:"draw_mouse"`  // 是否渲染光标
	DupFrames  bool   `yaml:"dup_frames"`  // true=填充重复帧(恒定帧率), false=不填充(更低延迟)
}

// NVENCConfig NVIDIA NVENC 硬编码专属参数
type NVENCConfig struct {
	RateControl string `yaml:"rate_control"` // cbr / vbr / constqp
	Maxrate     string `yaml:"maxrate"`      // 峰值码率
	Bufsize     string `yaml:"bufsize"`      // VBV 缓冲区大小
	BFrames     int    `yaml:"b_frames"`     // B 帧数 (0=无B帧)
	Multipass   int    `yaml:"multipass"`    // 两通编码 (0=禁用)
	Delay       int    `yaml:"delay"`        // async_depth (0=零延迟)
	NoScenecut  bool   `yaml:"no_scenecut"`  // 禁用场景切换检测
	RCLookahead int    `yaml:"rc_lookahead"` // 前瞻帧数 (0=禁用)
	BRefMode    int    `yaml:"b_ref_mode"`   // B帧作为参考 (0=禁用)
	NonrefP     bool   `yaml:"nonref_p"`     // 允许非参考P帧
}

// AudioConfig 音频捕获参数
type AudioConfig struct {
	Device  string `yaml:"device"`
	Codec   string `yaml:"codec"`
	Bitrate string `yaml:"bitrate"`
}

// FfmpegConfig ffmpeg 配置
type FfmpegConfig struct {
	Path   string       `yaml:"path"`
	Screen ScreenConfig `yaml:"screen"`
	Audio  AudioConfig  `yaml:"audio"`
}

// WebRTCConfig WebRTC 配置
type WebRTCConfig struct {
	ICEServers []string `yaml:"ice_servers"`
}

// ControlConfig 控制配置
type ControlConfig struct {
	DpiScale    float64 `yaml:"dpi_scale"`
	LongPressMs int     `yaml:"long_press_ms"`
}

// PowerConfig 电源配置
type PowerConfig struct {
	Enabled bool `yaml:"enabled"`
}

// Load 从 YAML 文件加载配置
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		// 设置默认值
		Server: ServerConfig{
			Port: 8080,
			Host: "0.0.0.0",
		},
		Ffmpeg: FfmpegConfig{
			Screen: ScreenConfig{
				Framerate: 60,
				Codec:     "h264_nvenc",
				Preset:    "p1",
				Tune:      "ll",
				PixFmt:    "", // NVENC 自动选择，不指定
				Profile:   "baseline",
				Bitrate:   "15M",
				GopSize:   60,
				Capture: CaptureCfg{
					Method:    "ddagrab",
					OutputIdx: 0,
					DrawMouse: true,
					DupFrames: false,
				},
				NVENC: NVENCConfig{
					RateControl: "cbr",
					Maxrate:     "15M",
					Bufsize:     "250K",
					BFrames:     0,
					Multipass:   0,
					Delay:       0,
					NoScenecut:  true,
					RCLookahead: 0,
					BRefMode:    0,
					NonrefP:     true,
				},
			},
			Audio: AudioConfig{
				Device:  "立体声混音 (Stereo Mix)",
				Codec:   "libopus",
				Bitrate: "128k",
			},
		},
		Control: ControlConfig{
			DpiScale:    1.0,
			LongPressMs: 500,
		},
		Power: PowerConfig{
			Enabled: true,
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
