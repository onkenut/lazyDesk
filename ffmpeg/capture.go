package ffmpeg

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onkenut/lazyDesk/config"
	pmedia "github.com/pion/webrtc/v4/pkg/media"
)

const (
	sendBufSize = 30    // 发送缓冲帧数 ~1秒缓冲，Pion WriteSample 偶尔阻塞不丢帧
	maxNALSize  = 16 * 1024 * 1024 // 16MB 单 NAL 上限
)

// VideoWriter is the interface for writing video samples to WebRTC tracks
type VideoWriter interface {
	WriteVideoSample(sample pmedia.Sample) error
}

// Capture manages ffmpeg screen capture + H264 NAL aggregation
type Capture struct {
	cfg        *config.Config
	cmd        *exec.Cmd
	stdout     io.ReadCloser
	mu         sync.Mutex
	stopCh     chan struct{}
	videoTrack VideoWriter
	sendCh     chan pmedia.Sample // 发送队列 — 解耦读取与网络 I/O

	// 生命周期保护
	generation int64 // 每次 Start 递增，防止旧 goroutine 覆盖状态

	// H264 NAL aggregation state
	nalBuf   []byte
	foundNal bool
	codeLen  int

	// Access unit aggregation
	au       [][]byte
	hasSlice bool
	frameNum int

	// RTP timestamp (computed from config framerate)
	rtpStep      uint32
	rtpTimestamp uint32
	frameDur     time.Duration

	// SPS/PPS 缓存 — 新 PeerConnection 连接时重发，避免浏览器无解码参数黑屏
	cachedSPS []byte
	cachedPPS []byte
}

func NewCapture(cfg *config.Config) *Capture {
	fr := cfg.Ffmpeg.Screen.Framerate
	if fr <= 0 {
		fr = 30
	}
	return &Capture{
		cfg:      cfg,
		rtpStep:  uint32(90000 / fr),
		frameDur: time.Second / time.Duration(fr),
	}
}

func (c *Capture) SetVideoTrack(w VideoWriter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.videoTrack = w
}

// ResendParameterSets 将缓存的 SPS/PPS 作为独立 sample 发送给新 track，
// 确保新连接的浏览器能正确解码后续 P 帧。在 WebRTC 连接建立后调用。
func (c *Capture) ResendParameterSets() {
	c.mu.Lock()
	sps := c.cachedSPS
	pps := c.cachedPPS
	track := c.videoTrack
	c.mu.Unlock()

	if track == nil || (sps == nil && pps == nil) {
		return
	}

	// 合并 SPS + PPS 为一个 sample 发送
	var data []byte
	if sps != nil {
		data = append(data, sps...)
	}
	if pps != nil {
		data = append(data, pps...)
	}

	sample := pmedia.Sample{
		Data:     data,
		Duration: 0, // 参数集无持续时间
	}

	select {
	case c.sendCh <- sample:
		log.Println("video: resent cached SPS/PPS to new peer")
	default:
		log.Println("video: send buffer full, cannot resend SPS/PPS")
	}
}

func (c *Capture) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// B2 修复: 用 generation 而非 running bool，旧 goroutine 无法覆盖
	gen := atomic.AddInt64(&c.generation, 1)

	ff := c.cfg.Ffmpeg.Path
	if ff == "" {
		ff = "ffmpeg"
	}
	fr := c.cfg.Ffmpeg.Screen.Framerate
	if fr <= 0 {
		fr = 30
	}

	args := []string{
		"-f", "gdigrab",
		"-framerate", fmt.Sprintf("%d", fr),
		"-i", "desktop",
		"-c:v", c.cfg.Ffmpeg.Screen.Codec,
		"-preset", c.cfg.Ffmpeg.Screen.Preset,
		"-tune", c.cfg.Ffmpeg.Screen.Tune,
		"-pix_fmt", c.cfg.Ffmpeg.Screen.PixFmt,
		"-g", fmt.Sprintf("%d", fr*2), // GOP: 每 2 秒一个 IDR 关键帧，加速重连恢复
		"-an",
		"-f", "h264",
		"-",
	}

	cmd := exec.Command(ff, args...)
	cmd.Stderr = log.Writer()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	c.stdout = stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	c.stopCh = make(chan struct{})
	c.sendCh = make(chan pmedia.Sample, sendBufSize)
	c.cmd = cmd
	c.codeLen = 4
	c.nalBuf = make([]byte, 0, 128*1024)
	c.au = nil
	c.hasSlice = false
	c.frameNum = 0
	c.rtpTimestamp = 0
	c.cachedSPS = nil // 清除旧缓存
	c.cachedPPS = nil
	// B1: 动态计算 RTP 步进和帧间隔
	c.rtpStep = uint32(90000 / fr)
	c.frameDur = time.Second / time.Duration(fr)

	log.Printf("ffmpeg started (PID: %d, gen=%d, %dfps)", cmd.Process.Pid, gen, fr)

	// ARCH-1: 独立发送 goroutine — 解耦读取与网络 I/O
	go c.sendLoop(gen)
	go c.readLoop(gen)
	return nil
}

// sendLoop 从队列取帧写入 WebRTC track (独立 goroutine)
func (c *Capture) sendLoop(gen int64) {
	var slowCount int
	for {
		select {
		case <-c.stopCh:
			return
		case sample := <-c.sendCh:
			c.mu.Lock()
			track := c.videoTrack
			c.mu.Unlock()
			if track == nil {
				continue
			}
			t0 := time.Now()
			if err := track.WriteVideoSample(sample); err != nil {
				log.Printf("WriteSample error: %v", err)
			}
			elapsed := time.Since(t0)
			if elapsed > 100*time.Millisecond {
				slowCount++
				log.Printf("WriteSample slow: %v (count=%d, sendCh=%d/%d)",
					elapsed, slowCount, len(c.sendCh), sendBufSize)
			}
		}
	}
}

func (c *Capture) readLoop(gen int64) {
	defer func() {
		c.flushAU(gen)
		// I4: 关闭 stdout pipe，防止资源泄漏
		if c.stdout != nil {
			c.stdout.Close()
		}
		// B2: 只有当前 generation 匹配才清理
		if atomic.LoadInt64(&c.generation) == gen {
			c.mu.Lock()
			if c.cmd != nil {
				c.cmd.Process.Kill()
				c.cmd = nil // FIX: 标记进程已退出，IsRunning() 不再返回过期 true
			}
			c.mu.Unlock()
		}
	}()

	reader := bufio.NewReaderSize(c.stdout, 256*1024)
	buf := make([]byte, 32768)

	for {
		select {
		case <-c.stopCh:
			return
		default:
		}
		n, err := reader.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("ffmpeg read: %v", err)
			}
			// ROOT-2: readLoop 退出 → flushAU 由 defer 触发
			return
		}
		c.process(buf[:n])
	}
}

// flushAU emits any remaining partial access unit (last frame on shutdown)
func (c *Capture) flushAU(gen int64) {
	if atomic.LoadInt64(&c.generation) != gen {
		return
	}
	if c.hasSlice {
		c.emitAU()
		c.au = nil
		c.hasSlice = false
	}
}

var (
	pattern3 = []byte{0x00, 0x00, 0x01}
	pattern4 = []byte{0x00, 0x00, 0x00, 0x01}
)

// process scans for H264 start codes and aggregates NALs into access units
func (c *Capture) process(data []byte) {
	if len(c.nalBuf) > 0 {
		data = append(c.nalBuf, data...)
		c.nalBuf = c.nalBuf[:0]
	}

	for i := 0; i < len(data); {
		i3 := index(data, pattern3, i)
		i4 := index(data, pattern4, i)

		var spos, clen int
		switch {
		case i4 >= 0 && (i3 < 0 || i4 <= i3):
			spos, clen = i4, 4
		case i3 >= 0:
			spos, clen = i3, 3
		default:
			// B4: NAL 缓冲区上限保护
			if len(data[i:])+len(c.nalBuf) > maxNALSize {
				log.Printf("NAL buffer overflow (%d bytes), resetting", len(c.nalBuf))
				c.nalBuf = c.nalBuf[:0]
				c.foundNal = false
				return
			}
			c.nalBuf = append(c.nalBuf, data[i:]...)
			return
		}

		if c.foundNal {
			c.nalBuf = append(c.nalBuf, data[i:spos]...)
			c.onNalComplete(c.nalBuf, c.codeLen)
		}

		c.nalBuf = append(c.nalBuf[:0], data[spos:spos+clen]...)
		c.codeLen = clen
		c.foundNal = true

		i = spos + clen
	}
}

// onNalComplete processes a complete NAL unit, aggregating into access units
func (c *Capture) onNalComplete(nal []byte, codeLen int) {
	if codeLen >= len(nal) {
		return
	}
	nalType := nal[codeLen] & 0x1F

	// 缓存 SPS/PPS — 新 PeerConnection 连接时重发，避免浏览器黑屏
	switch nalType {
	case 7: // SPS
		c.cachedSPS = append([]byte(nil), nal...)
	case 8: // PPS
		c.cachedPPS = append([]byte(nil), nal...)
	}

	isSlice := nalType == 1 || nalType == 5

	// Access unit boundaries: SPS(7) always; new slice after existing slice.
	// AUD(9) is NOT a boundary — it belongs to the following frame as prefix.
	isBoundary := nalType == 7 || (isSlice && c.hasSlice)

	if isBoundary && c.hasSlice {
		// 只 emit 包含 slice 的 AU，过滤纯 SPS/PPS/AUD 垃圾帧
		c.emitAU()
		c.au = nil
		c.hasSlice = false
	}

	c.au = append(c.au, append([]byte(nil), nal...))
	if isSlice {
		c.hasSlice = true
	}
}

// emitAU sends a complete access unit to the send channel (non-blocking)
func (c *Capture) emitAU() {
	if len(c.au) == 0 {
		return
	}

	var total int
	for _, n := range c.au {
		total += len(n)
	}
	data := make([]byte, 0, total)
	for _, n := range c.au {
		data = append(data, n...)
	}

	c.frameNum++
	c.rtpTimestamp += c.rtpStep
	if c.frameNum%300 == 0 {
		log.Printf("video: %d frames sent, latest %d bytes", c.frameNum, total)
	}

	sample := pmedia.Sample{
		Data:            data,
		PacketTimestamp: c.rtpTimestamp,
		Duration:        c.frameDur,
	}

	// ARCH-1: 非阻塞投递到发送队列，不阻塞 readLoop
	select {
	case c.sendCh <- sample:
		// 帧已入队，发送 goroutine 会处理
	default:
		// 发送队列满 → 丢帧（不阻塞读取管道）
		if c.frameNum%30 == 0 {
			log.Printf("video: send buffer full, dropping frame %d", c.frameNum)
		}
	}
}

func index(data, pat []byte, off int) int {
	end := len(data) - len(pat)
	for i := off; i <= end; i++ {
		if bytes.Equal(data[i:i+len(pat)], pat) {
			return i
		}
	}
	return -1
}

func (c *Capture) Stop() {
	c.mu.Lock()
	select {
	case <-c.stopCh:
		// already stopped
		c.mu.Unlock()
		return
	default:
	}
	close(c.stopCh)
	cmd := c.cmd
	c.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		log.Printf("ffmpeg stopped")
	}
}

func (c *Capture) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.stopCh:
		return false
	default:
		return c.cmd != nil
	}
}
