package ffmpeg

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"

	"github.com/onkenut/lazyDesk/config"
)

// Capture 管理 ffmpeg 屏幕/音频捕获子进程
type Capture struct {
	cfg       *config.Config
	cmd       *exec.Cmd
	stdout    io.ReadCloser
	running   bool
	mu        sync.Mutex
	stopCh    chan struct{}
	videoPipe io.ReadCloser
	audioPipe io.ReadCloser
}

// NewCapture 创建新的捕获管理器
func NewCapture(cfg *config.Config) *Capture {
	return &Capture{
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

// Start 启动 ffmpeg 屏幕捕获子进程
func (c *Capture) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return fmt.Errorf("capture already running")
	}

	ffmpegPath := c.cfg.Ffmpeg.Path
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}

	// 构建 ffmpeg 命令 - 屏幕捕获 (H264) + 音频捕获 (Opus)
	// 使用 -f mpegts 将音视频合并输出到 stdout
	args := []string{
		// 屏幕捕获输入
		"-f", "gdigrab",
		"-framerate", fmt.Sprintf("%d", c.cfg.Ffmpeg.Screen.Framerate),
		"-i", "desktop",
		// 音频捕获输入
		"-f", "dshow",
		"-i", fmt.Sprintf("audio=%s", c.cfg.Ffmpeg.Audio.Device),
		// 视频编码
		"-c:v", c.cfg.Ffmpeg.Screen.Codec,
		"-preset", c.cfg.Ffmpeg.Screen.Preset,
		"-tune", c.cfg.Ffmpeg.Screen.Tune,
		"-pix_fmt", c.cfg.Ffmpeg.Screen.PixFmt,
		// 音频编码
		"-c:a", c.cfg.Ffmpeg.Audio.Codec,
		"-b:a", c.cfg.Ffmpeg.Audio.Bitrate,
		// 输出
		"-f", "mpegts",
		"-",
	}

	cmd := exec.Command(ffmpegPath, args...)
	cmd.Stderr = log.Writer() // ffmpeg 日志输出到 stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	c.stdout = stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	c.cmd = cmd
	c.running = true

	log.Printf("ffmpeg capture started (PID: %d)", cmd.Process.Pid)

	// 启动读取协程
	go c.readLoop()

	return nil
}

// readLoop 持续读取 ffmpeg stdout 输出
func (c *Capture) readLoop() {
	reader := bufio.NewReaderSize(c.stdout, 64*1024) // 64KB buffer
	buf := make([]byte, 4096)

	for {
		select {
		case <-c.stopCh:
			return
		default:
			n, err := reader.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("ffmpeg read error: %v", err)
				}
				return
			}
			// TODO: 将 buf[:n] 推送到 WebRTC tracks
			_ = buf[:n]
		}
	}
}

// Stop 停止 ffmpeg 子进程
func (c *Capture) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return
	}

	close(c.stopCh)

	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		log.Printf("ffmpeg capture stopped")
	}

	c.running = false
}

// IsRunning 检查是否正在运行
func (c *Capture) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// Restart 重启捕获 (异常退出时)
func (c *Capture) Restart() error {
	c.Stop()
	return c.Start()
}
