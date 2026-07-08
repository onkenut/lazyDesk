package ffmpeg

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/onkenut/lazyDesk/config"
	"github.com/pion/webrtc/v4/pkg/media"
)

// Capture 管理 ffmpeg 屏幕捕获子进程，输出 raw H264 Annex-B
type Capture struct {
	cfg          *config.Config
	cmd          *exec.Cmd
	stdout       io.ReadCloser
	running      bool
	mu           sync.Mutex
	stopCh       chan struct{}
	videoTrack   VideoWriter

	// H264 解析状态
	nalBuf       []byte // 正在累积的 NAL unit
	startCodeLen int    // 当前 start code 长度 (3 or 4 bytes)
	foundStart   bool
	frameCount   int
	startTime    time.Time
}

// VideoWriter is the interface for writing video samples to WebRTC tracks
type VideoWriter interface {
	WriteVideoSample(sample media.Sample) error
}

// NewCapture 创建新的捕获管理器
func NewCapture(cfg *config.Config) *Capture {
	return &Capture{
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

// SetVideoTrack 设置视频输出目标 (在 WebRTC track 创建后调用)
func (c *Capture) SetVideoTrack(w VideoWriter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.videoTrack = w
}

// Start 启动 ffmpeg 屏幕捕获子进程，输出 raw H264
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

	// Raw H264 Annex-B 输出: 屏幕→H264, 无音频, 输出到 stdout
	args := []string{
		"-f", "gdigrab",
		"-framerate", fmt.Sprintf("%d", c.cfg.Ffmpeg.Screen.Framerate),
		"-i", "desktop",
		"-c:v", c.cfg.Ffmpeg.Screen.Codec,
		"-preset", c.cfg.Ffmpeg.Screen.Preset,
		"-tune", c.cfg.Ffmpeg.Screen.Tune,
		"-pix_fmt", c.cfg.Ffmpeg.Screen.PixFmt,
		"-an",                     // 无音频
		"-f", "h264",              // raw H264 Annex-B
		"-",
	}

	cmd := exec.Command(ffmpegPath, args...)
	cmd.Stderr = log.Writer()

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
	c.startTime = time.Now()
	c.startCodeLen = 4 // 默认 4-byte start code
	c.nalBuf = make([]byte, 0, 128*1024)
	c.frameCount = 0

	log.Printf("ffmpeg capture started (PID: %d)", cmd.Process.Pid)

	go c.readLoop()

	return nil
}

// readLoop 从 ffmpeg stdout 读取 H264 Annex-B，解析 NAL units 并推送到 WebRTC
func (c *Capture) readLoop() {
	defer func() {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
	}()

	reader := bufio.NewReaderSize(c.stdout, 256*1024)

	// 预分配大 buffer 用于读取
	buf := make([]byte, 32768)

	// start code 前缀 (3 or 4 bytes)
	var prefix3 = []byte{0x00, 0x00, 0x01}
	var prefix4 = []byte{0x00, 0x00, 0x00, 0x01}

	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		n, err := reader.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("ffmpeg read error: %v", err)
			}
			return
		}

		// 扫描数据中的 NAL start codes
		c.scanNalUnits(buf[:n], prefix3, prefix4)
	}
}

// scanNalUnits 扫描字节流中的 H264 NAL start codes 并提取 NAL units
func (c *Capture) scanNalUnits(data []byte, prefix3, prefix4 []byte) {
	for i := 0; i < len(data); {
		// 查找下一个 start code
		idx3 := findPattern(data, prefix3, i)
		idx4 := findPattern(data, prefix4, i)

		var startIdx, newCodeLen int
		switch {
		case idx4 >= 0 && (idx3 < 0 || idx4 <= idx3):
			startIdx = idx4
			newCodeLen = 4
		case idx3 >= 0:
			startIdx = idx3
			newCodeLen = 3
		default:
			// 没有找到新的 start code，将剩余数据追加到当前 NAL
			c.nalBuf = append(c.nalBuf, data[i:]...)
			return
		}

		// 在 startIdx 之前的数据属于当前 NAL unit 的尾部
		if c.foundStart {
			c.nalBuf = append(c.nalBuf, data[i:startIdx]...)
			c.emitNalUnit(c.nalBuf)
		}

		// 开始新的 NAL unit
		c.nalBuf = make([]byte, 0, 128*1024)
		c.nalBuf = append(c.nalBuf, data[startIdx:startIdx+newCodeLen]...)
		c.startCodeLen = newCodeLen
		c.foundStart = true

		// 移动到 start code 之后
		i = startIdx + newCodeLen
	}
}

// emitNalUnit 将完整 NAL unit 推送到 WebRTC track
func (c *Capture) emitNalUnit(nalData []byte) {
	c.mu.Lock()
	track := c.videoTrack
	c.mu.Unlock()

	if track == nil {
		return
	}

	// 计算时间戳
	c.frameCount++

	now := time.Now()
	sample := media.Sample{
		Data:      nalData,
		Timestamp: now,
		Duration:  33 * time.Millisecond,
	}

	if err := track.WriteVideoSample(sample); err != nil {
		log.Printf("Failed to write video sample: %v", err)
	}
}

// findPattern 在 data 中从 offset 开始查找 pattern
func findPattern(data, pattern []byte, offset int) int {
	for i := offset; i <= len(data)-len(pattern); i++ {
		match := true
		for j := 0; j < len(pattern); j++ {
			if data[i+j] != pattern[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
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
