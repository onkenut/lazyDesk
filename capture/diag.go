package capture

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ProbeResult ffmpeg 能力探测结果
type ProbeResult struct {
	Path       string // ffmpeg 可执行文件路径
	Version    string
	HasNVENC   bool // h264_nvenc 编码器
	HasX264    bool // libx264 编码器
	HasDDAGrab bool // ddagrab 过滤器 (Win10+ DXGI 零拷贝)
}

// Probe 探测 ffmpeg 可执行文件及其编码能力。
// ffmpegPath 为空时使用系统 PATH。
func Probe(ffmpegPath string) (ProbeResult, error) {
	path := ffmpegPath
	if path == "" {
		var err error
		path, err = exec.LookPath("ffmpeg")
		if err != nil {
			return ProbeResult{}, err
		}
	}

	res := ProbeResult{Path: path}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if out, err := exec.CommandContext(ctx, path, "-version").Output(); err == nil {
		first := strings.SplitN(string(out), "\n", 2)[0]
		res.Version = strings.TrimSpace(first)
	}

	// 编码器列表
	if out, err := exec.CommandContext(ctx, path, "-hide_banner", "-encoders").Output(); err == nil {
		text := string(out)
		res.HasNVENC = strings.Contains(text, "h264_nvenc")
		res.HasX264 = strings.Contains(text, "libx264")
	}

	// 过滤器列表
	if out, err := exec.CommandContext(ctx, path, "-hide_banner", "-filters").Output(); err == nil {
		res.HasDDAGrab = strings.Contains(string(out), "ddagrab")
	}

	return res, nil
}

// Pipeline 选定的采集管线
type Pipeline struct {
	Name string // 显示名, 如 "ddagrab + NVENC (GPU)"
	HW   bool   // 是否硬件编码
	Args []string
}

// BuildVideoArgs 根据配置与探测结果构建 ffmpeg 视频采集参数。
// 遵循降级链: ddagrab+NVENC → gdigrab+libx264 → 明确错误。
func BuildVideoArgs(cfg ScreenCfg, probe ProbeResult) (Pipeline, error) {
	codec := strings.ToLower(cfg.Codec)
	if codec == "" {
		codec = "h264_nvenc"
	}

	switch codec {
	case "h264_nvenc":
		if probe.HasNVENC && probe.HasDDAGrab {
			return Pipeline{
				Name: "ddagrab + NVENC (GPU)",
				HW:   true,
				Args: buildNVENCArgs(cfg),
			}, nil
		}
		if cfg.Fallback && probe.HasX264 {
			return Pipeline{
				Name: "gdigrab + libx264 (CPU, 自动降级)",
				HW:   false,
				Args: buildX264Args(cfg),
			}, nil
		}
		if !cfg.Fallback {
			return Pipeline{}, newProbeError("h264_nvenc 或 ddagrab 不可用 (已关闭自动降级)")
		}
		return Pipeline{}, newProbeError("ffmpeg 缺少 h264_nvenc/ddagrab 支持, 且 libx264 也不可用")

	case "libx264", "x264":
		if !probe.HasX264 {
			return Pipeline{}, newProbeError("ffmpeg 缺少 libx264 编码器")
		}
		return Pipeline{
			Name: "gdigrab + libx264 (CPU)",
			HW:   false,
			Args: buildX264Args(cfg),
		}, nil

	default:
		return Pipeline{}, newProbeError("未知编码器: " + cfg.Codec)
	}
}

// ScreenCfg 是配置项的精简视图 (避免 capture 包依赖 config 包)
type ScreenCfg struct {
	Codec     string
	Preset    string
	Tune      string
	PixFmt    string
	Profile   string
	Bitrate   string
	GopSize   int
	Framerate int
	Capture   CaptureCfg
	NVENC     NVENCConfig
	Fallback  bool
}

// CaptureCfg 采集方式配置
type CaptureCfg struct {
	Method     string
	OutputIdx  int
	DrawMouse  bool
	DupFrames  bool
}

// NVENCConfig NVENC 专属参数
type NVENCConfig struct {
	RateControl string
	Maxrate     string
	Bufsize     string
	BFrames     int
	Multipass   int
	Delay       int
	NoScenecut  bool
	RCLookahead int
	BRefMode    int
	NonrefP     bool
}

type probeError struct{ msg string }

func (e *probeError) Error() string { return e.msg }
func newProbeError(msg string) error {
	return &probeError{msg: "ffmpeg 能力不足: " + msg}
}

// buildNVENCArgs ddagrab + h264_nvenc 管线 (低延迟 GPU 硬编码)
func buildNVENCArgs(cfg ScreenCfg) []string {
	fr := cfg.Framerate
	if fr <= 0 {
		fr = 60
	}
	cap := cfg.Capture
	if cap.Method == "" {
		cap.Method = "ddagrab"
	}
	dupVal := 0
	if cap.DupFrames {
		dupVal = 1
	}
	mouseVal := 0
	if cap.DrawMouse {
		mouseVal = 1
	}

	args := []string{
		"-hide_banner", "-nostats", "-loglevel", "info",
		"-f", "lavfi",
		"-i", sprintf("ddagrab=output_idx=%d:framerate=%d:dup_frames=%d:draw_mouse=%d",
			cap.OutputIdx, fr, dupVal, mouseVal),
		"-c:v", "h264_nvenc",
	}
	if cfg.Preset != "" {
		args = append(args, "-preset", cfg.Preset)
	}
	if cfg.Tune != "" {
		args = append(args, "-tune", cfg.Tune)
	}
	if cfg.Profile != "" {
		args = append(args, "-profile:v", cfg.Profile)
	}
	nv := cfg.NVENC
	if nv.RateControl != "" {
		args = append(args, "-rc", nv.RateControl)
	}
	if cfg.Bitrate != "" {
		args = append(args, "-b:v", cfg.Bitrate)
	}
	if nv.Maxrate != "" {
		args = append(args, "-maxrate", nv.Maxrate)
	}
	if nv.Bufsize != "" {
		args = append(args, "-bufsize", nv.Bufsize)
	}
	args = append(args,
		"-multipass", itoa(nv.Multipass),
		"-delay", itoa(nv.Delay),
		"-rc-lookahead", itoa(nv.RCLookahead),
		"-b_ref_mode", itoa(nv.BRefMode),
		"-bf", itoa(nv.BFrames),
	)
	if nv.NoScenecut {
		args = append(args, "-no-scenecut", "1")
	}
	if nv.NonrefP {
		args = append(args, "-nonref_p", "1")
	}
	gop := cfg.GopSize
	if gop <= 0 {
		gop = 60
	}
	args = append(args, "-g", itoa(gop))
	args = append(args, "-an", "-f", "h264", "pipe:1")
	return args
}

// buildX264Args gdigrab + libx264 管线 (CPU 软编码回退)
func buildX264Args(cfg ScreenCfg) []string {
	fr := cfg.Framerate
	if fr <= 0 {
		fr = 30
	}
	args := []string{
		"-hide_banner", "-nostats", "-loglevel", "info",
		"-f", "gdigrab",
		"-framerate", itoa(fr),
		"-i", "desktop",
		"-c:v", "libx264",
	}
	if cfg.Preset == "" || cfg.Preset == "p1" {
		args = append(args, "-preset", "ultrafast")
	} else {
		args = append(args, "-preset", cfg.Preset)
	}
	if cfg.Tune == "" || cfg.Tune == "ll" {
		args = append(args, "-tune", "zerolatency")
	} else {
		args = append(args, "-tune", cfg.Tune)
	}
	pix := cfg.PixFmt
	if pix == "" {
		pix = "yuv420p"
	}
	args = append(args, "-pix_fmt", pix)
	if cfg.Profile != "" {
		args = append(args, "-profile:v", cfg.Profile)
	}
	if cfg.Bitrate != "" {
		args = append(args, "-b:v", cfg.Bitrate)
	}
	if cfg.GopSize > 0 {
		args = append(args, "-g", itoa(cfg.GopSize))
	}
	args = append(args, "-an", "-f", "h264", "pipe:1")
	return args
}

// resRegex 解析 ffmpeg 输出的采集分辨率 (Stream #0:0: Video: ... 2560x1440 [SAR...)
var resRegex = regexp.MustCompile(`,\s*(\d{2,5})x(\d{2,5})\s*\[SAR`)

func sprintf(format string, a ...interface{}) string {
	return fmt.Sprintf(format, a...)
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
