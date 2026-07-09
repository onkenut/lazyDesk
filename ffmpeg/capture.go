package ffmpeg

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onkenut/lazyDesk/config"
	pmedia "github.com/pion/webrtc/v4/pkg/media"
)

const (
	sendBufSize = 60    // 发送缓冲帧数 ~1秒@60fps，NVENC 输出更快需要更大缓冲
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
	frameNum int

	// RTP timestamp (computed from config framerate)
	rtpStep      uint32
	rtpTimestamp uint32
	frameDur     time.Duration

	// ROOT-3: SPS/PPS 缓存 — 新连接重发参数集
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

	args := c.buildFFmpegArgs(fr)

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
	c.frameNum = 0
	c.rtpTimestamp = 0
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
		// I4: 关闭 stdout pipe，防止资源泄漏
		if c.stdout != nil {
			c.stdout.Close()
		}
		// B2: 只有当前 generation 匹配才清理
		if atomic.LoadInt64(&c.generation) == gen {
			c.mu.Lock()
			if c.cmd != nil {
				c.cmd.Process.Kill()
				c.cmd = nil // ROOT-2: 标记进程已退出，IsRunning() 不再返回过期 true
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

// findStartCode 滑动窗口快速检测 H264 起始码 (0x000001 或 0x00000001)
// 返回起始码位置和长度 (3 或 4)，未找到返回 -1,0
func findStartCode(data []byte, off int) (pos int, codeLen int) {
	var state uint32
	for i := off; i < len(data); i++ {
		state = (state << 8) | uint32(data[i])
		// 检查 4 字节起始码 0x00000001
		if i-off >= 3 && state == 0x00000001 {
			return i - 3, 4
		}
		// 检查 3 字节起始码 0x000001 (仅当高位为 0 时)
		if i-off >= 2 && (state&0x00FFFFFF) == 0x000001 {
			return i - 2, 3
		}
	}
	return -1, 0
}

// process scans for H264 start codes and aggregates NALs into access units
func (c *Capture) process(data []byte) {
	if len(c.nalBuf) > 0 {
		data = append(c.nalBuf, data...)
		c.nalBuf = c.nalBuf[:0]
	}

	for i := 0; i < len(data); {
		spos, clen := findStartCode(data, i)
		if spos < 0 {
			// 未找到起始码，剩余数据进缓冲区
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

// onNalComplete processes a complete NAL unit
// NVENC baseline: one NAL per frame → send directly (no AU aggregation needed).
// AUD (type 9) is dropped — it's a byte-alignment delimiter, not needed for decoding.
// SPS/PPS are cached for ResendParameterSets.
func (c *Capture) onNalComplete(nal []byte, codeLen int) {
	if codeLen >= len(nal) {
		return
	}
	nalType := nal[codeLen] & 0x1F

	// AUD(9): frame boundary marker — drop it, pure overhead
	if nalType == 9 {
		return
	}

	// ROOT-3: 缓存 SPS/PPS
	switch nalType {
	case 7:
		c.cachedSPS = append([]byte(nil), nal...)
	case 8:
		c.cachedPPS = append([]byte(nil), nal...)
	}

	// 将 SPS/PPS/SEI/slice 作为独立 sample 发送
	// NVENC baseline (-bf 0) 每个 frame 是单 NAL，Pion 逐 NAL 分包远优于大块 AU
	isSlice := nalType == 1 || nalType == 5
	if isSlice || nalType == 6 || nalType == 7 || nalType == 8 {
		c.sendNAL(nal, isSlice)
	}
}

// sendNAL sends a single NAL unit as a media sample (non-blocking)
// Pion's H264Payloader expects Annex B format (WITH start codes) —
// it uses h264.NALHeaders() internally to find NAL boundaries.
func (c *Capture) sendNAL(nal []byte, isSlice bool) {
	if !isSlice {
		data := make([]byte, len(nal))
		copy(data, nal)
		sample := pmedia.Sample{
			Data:            data,
			PacketTimestamp: c.rtpTimestamp,
			Duration:        c.frameDur,
		}
		select {
		case c.sendCh <- sample:
		default:
		}
		return
	}

	c.frameNum++
	c.rtpTimestamp += c.rtpStep
	if c.frameNum%300 == 0 {
		log.Printf("video: %d frames sent, latest %d bytes", c.frameNum, len(nal))
	}

	data := make([]byte, len(nal))
	copy(data, nal)
	sample := pmedia.Sample{
		Data:            data,
		PacketTimestamp: c.rtpTimestamp,
		Duration:        c.frameDur,
	}
	select {
	case c.sendCh <- sample:
	default:
		if c.frameNum%30 == 0 {
			log.Printf("video: send buffer full, dropping frame %d", c.frameNum)
		}
	}
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

// IsRunning 返回 ffmpeg 进程是否在运行
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

// ResendParameterSets 将缓存的 SPS/PPS 写入当前 video track (重连场景)
// 保留 Annex B 起始码 — Pion H264Payloader 需要起始码定位 NAL 边界
func (c *Capture) ResendParameterSets() {
	c.mu.Lock()
	track := c.videoTrack
	sps := c.cachedSPS
	pps := c.cachedPPS
	c.mu.Unlock()

	if track == nil || sps == nil || pps == nil {
		return
	}

	// SPS + PPS 合并为一个 sample (Pion 将其作为 STAP-A 发送)
	au := make([]byte, 0, len(sps)+len(pps))
	au = append(au, sps...)
	au = append(au, pps...)

	sample := pmedia.Sample{
		Data:            au,
		PacketTimestamp: c.rtpTimestamp,
		Duration:        c.frameDur,
	}
	if err := track.WriteVideoSample(sample); err != nil {
		log.Printf("ResendParameterSets: %v", err)
	}
}

// buildFFmpegArgs 根据 codec 类型动态构建 ffmpeg 命令行参数
func (c *Capture) buildFFmpegArgs(fr int) []string {
	sc := c.cfg.Ffmpeg.Screen
	var args []string

	if strings.EqualFold(sc.Codec, "h264_nvenc") {
		// ddagrab + NVENC GPU 硬编码管线 (lavfi 路径)
		cap := sc.Capture
		method := cap.Method
		if method == "" {
			method = "ddagrab"
		}
		dupVal := 0
		if cap.DupFrames {
			dupVal = 1
		}
		mouseVal := 0
		if cap.DrawMouse {
			mouseVal = 1
		}
		ddagrabFilter := fmt.Sprintf("ddagrab=output_idx=%d:framerate=%d:dup_frames=%d:draw_mouse=%d",
			cap.OutputIdx, fr, dupVal, mouseVal)

		args = append(args,
			"-f", "lavfi",
			"-i", ddagrabFilter,
			"-c:v", "h264_nvenc",
			"-preset", sc.Preset,
			"-tune", sc.Tune,
		)

		// Profile
		if sc.Profile != "" {
			args = append(args, "-profile:v", sc.Profile)
		}

		// NVENC 专属参数
		nv := sc.NVENC
		if nv.RateControl != "" {
			args = append(args, "-rc", nv.RateControl)
		}
		if sc.Bitrate != "" {
			args = append(args, "-b:v", sc.Bitrate)
		}
		if nv.Maxrate != "" {
			args = append(args, "-maxrate", nv.Maxrate)
		}
		if nv.Bufsize != "" {
			args = append(args, "-bufsize", nv.Bufsize)
		}
		args = append(args,
			"-multipass", fmt.Sprintf("%d", nv.Multipass),
			"-delay", fmt.Sprintf("%d", nv.Delay),
			"-rc-lookahead", fmt.Sprintf("%d", nv.RCLookahead),
			"-b_ref_mode", fmt.Sprintf("%d", nv.BRefMode),
			"-bf", fmt.Sprintf("%d", nv.BFrames),
		)
		if nv.NoScenecut {
			args = append(args, "-no-scenecut", "1")
		}
		if nv.NonrefP {
			args = append(args, "-nonref_p", "1")
		}

		// GOP 大小
		gop := sc.GopSize
		if gop <= 0 {
			gop = 60
		}
		args = append(args, "-g", fmt.Sprintf("%d", gop))

	} else {
		// gdigrab + libx264 CPU 软编码管线 (回退方案)
		args = append(args,
			"-f", "gdigrab",
			"-framerate", fmt.Sprintf("%d", fr),
			"-i", "desktop",
			"-c:v", sc.Codec,
			"-preset", sc.Preset,
			"-tune", sc.Tune,
		)
		if sc.PixFmt != "" {
			args = append(args, "-pix_fmt", sc.PixFmt)
		}
		if sc.Profile != "" {
			args = append(args, "-profile:v", sc.Profile)
		}
		if sc.Bitrate != "" {
			args = append(args, "-b:v", sc.Bitrate)
		}
		if sc.GopSize > 0 {
			args = append(args, "-g", fmt.Sprintf("%d", sc.GopSize))
		}
	}

	args = append(args, "-an", "-f", "h264", "-")
	return args
}
