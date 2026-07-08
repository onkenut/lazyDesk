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
	Framerate int    `yaml:"framerate"`
	Codec     string `yaml:"codec"`
	Preset    string `yaml:"preset"`
	Tune      string `yaml:"tune"`
	PixFmt    string `yaml:"pix_fmt"`
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
				Framerate: 30,
				Codec:     "libx264",
				Preset:    "ultrafast",
				Tune:      "zerolatency",
				PixFmt:    "yuv420p",
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
