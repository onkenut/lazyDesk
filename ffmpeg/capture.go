package ffmpeg

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/onkenut/lazyDesk/config"
	pmedia "github.com/pion/webrtc/v4/pkg/media"
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
	running    bool
	mu         sync.Mutex
	stopCh     chan struct{}
	videoTrack VideoWriter

	// H264 NAL aggregation state
	nalBuf    []byte   // current NAL being accumulated
	foundNal  bool
	codeLen   int // start code length (3 or 4)

	// Access unit aggregation
	au        [][]byte // NALs in current access unit
	hasSlice  bool
	frameNum  int
	startTime time.Time
}

func NewCapture(cfg *config.Config) *Capture {
	return &Capture{
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

func (c *Capture) SetVideoTrack(w VideoWriter) {
	c.mu.Lock(); defer c.mu.Unlock()
	c.videoTrack = w
}

func (c *Capture) Start() error {
	c.mu.Lock(); defer c.mu.Unlock()
	if c.running { return fmt.Errorf("capture already running") }

	ff := c.cfg.Ffmpeg.Path
	if ff == "" { ff = "ffmpeg" }

	args := []string{
		"-f", "gdigrab",
		"-framerate", fmt.Sprintf("%d", c.cfg.Ffmpeg.Screen.Framerate),
		"-i", "desktop",
		"-c:v", c.cfg.Ffmpeg.Screen.Codec,
		"-preset", c.cfg.Ffmpeg.Screen.Preset,
		"-tune", c.cfg.Ffmpeg.Screen.Tune,
		"-pix_fmt", c.cfg.Ffmpeg.Screen.PixFmt,
		"-an",
		"-f", "h264",
		"-",
	}

	cmd := exec.Command(ff, args...)
	cmd.Stderr = log.Writer()
	stdout, err := cmd.StdoutPipe()
	if err != nil { return fmt.Errorf("pipe: %w", err) }
	c.stdout = stdout

	if err := cmd.Start(); err != nil { return fmt.Errorf("start: %w", err) }

	c.cmd = cmd
	c.running = true
	c.startTime = time.Now()
	c.codeLen = 4
	c.nalBuf = make([]byte, 0, 128*1024)
	c.au = nil
	c.hasSlice = false
	c.frameNum = 0

	log.Printf("ffmpeg started (PID: %d)", cmd.Process.Pid)
	go c.readLoop()
	return nil
}

var (
	pattern3 = []byte{0x00, 0x00, 0x01}
	pattern4 = []byte{0x00, 0x00, 0x00, 0x01}
)

func (c *Capture) readLoop() {
	defer func() { c.mu.Lock(); c.running = false; c.mu.Unlock() }()

	reader := bufio.NewReaderSize(c.stdout, 256*1024)
	buf := make([]byte, 32768)

	for {
		select {
		case <-c.stopCh: return
		default:
		}
		n, err := reader.Read(buf)
		if err != nil {
			if err != io.EOF { log.Printf("ffmpeg read: %v", err) }
			return
		}
		c.process(buf[:n])
	}
}

// process scans for H264 start codes and aggregates NALs into access units
func (c *Capture) process(data []byte) {
	for i := 0; i < len(data); {
		i3 := index(data, pattern3, i)
		i4 := index(data, pattern4, i)

		var spos, clen int
		switch {
		case i4 >= 0 && (i3 < 0 || i4 <= i3): spos, clen = i4, 4
		case i3 >= 0: spos, clen = i3, 3
		default:
			// no more start codes, rest belongs to current NAL
			c.nalBuf = append(c.nalBuf, data[i:]...)
			return
		}

		// data[i:spos] is the tail of the current NAL
		if c.foundNal {
			c.nalBuf = append(c.nalBuf, data[i:spos]...)
			c.onNalComplete(c.nalBuf, c.codeLen)
		}

		// start new NAL
		c.nalBuf = append(c.nalBuf[:0], data[spos:spos+clen]...)
		c.codeLen = clen
		c.foundNal = true

		i = spos + clen
	}
}

// onNalComplete processes a complete NAL unit, aggregating into access units
func (c *Capture) onNalComplete(nal []byte, codeLen int) {
	if codeLen >= len(nal) { return }
	nalType := nal[codeLen] & 0x1F

	// Is this a VCL NAL (slice data)?
	isSlice := nalType == 1 || nalType == 5

	// New access unit when: we see a slice NAL and already have slice data
	if isSlice && c.hasSlice {
		c.emitAU()
		c.au = nil
		c.hasSlice = false
	}

	c.au = append(c.au, append([]byte(nil), nal...))
	if isSlice { c.hasSlice = true }
}

// emitAU sends a complete access unit to WebRTC
func (c *Capture) emitAU() {
	if len(c.au) == 0 { return }

	c.mu.Lock()
	track := c.videoTrack
	c.mu.Unlock()
	if track == nil { return }

	// Concatenate all NALs into one buffer
	var total int
	for _, n := range c.au { total += len(n) }
	data := make([]byte, 0, total)
	for _, n := range c.au { data = append(data, n...) }

	c.frameNum++
	sample := pmedia.Sample{
		Data:      data,
		Timestamp: time.Now(),
		Duration:  33 * time.Millisecond,
	}

	if err := track.WriteVideoSample(sample); err != nil {
		log.Printf("WriteSample error: %v", err)
	}
}

func index(data, pat []byte, off int) int {
	end := len(data) - len(pat)
	for i := off; i <= end; i++ {
		if bytes.Equal(data[i:i+len(pat)], pat) { return i }
	}
	return -1
}

func (c *Capture) Stop() {
	c.mu.Lock(); defer c.mu.Unlock()
	if !c.running { return }
	close(c.stopCh)
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		log.Printf("ffmpeg stopped")
	}
	c.running = false
}

func (c *Capture) IsRunning() bool {
	c.mu.Lock(); defer c.mu.Unlock()
	return c.running
}
