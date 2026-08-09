package capture

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// EventType 采集事件类型 (供 server 转发给客户端/状态页)
type EventType string

const (
	EventVideoFallback EventType = "video_fallback" // 自动降级到 CPU 编码
	EventVideoCrashed  EventType = "video_crashed"  // 进程异常退出
	EventVideoRestart  EventType = "video_restart"  // 自动重启
	EventVideoStopped  EventType = "video_stopped"  // 主动停止
	EventAudioDisabled EventType = "audio_disabled" // 音频不可用 (设备缺失等)
	EventAudioCrashed  EventType = "audio_crashed"
	EventAudioRestart  EventType = "audio_restart"
)

// Event 采集事件
type Event struct {
	Type    EventType
	Message string
}

// VideoStatus 视频采集状态 (供 /api/status)
type VideoStatus struct {
	Running   bool    `json:"running"`
	Pipeline  string  `json:"pipeline"`
	HW        bool    `json:"hw"`
	FPS       float64 `json:"fps"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Restarts  int     `json:"restarts"`
	LastError string  `json:"last_error,omitempty"`
}

// VideoCapture 管理 ffmpeg 视频采集进程:
// 采集 → NAL 解析 → 帧聚合 → 扇出到 Hub
type VideoCapture struct {
	cfg      ScreenCfg
	probe    ProbeResult
	hub      *Hub
	onEvent  func(Event)

	mu        sync.Mutex
	cmd       *exec.Cmd
	stdout    io.ReadCloser
	stopCh    chan struct{}
	running   bool
	pipeline  Pipeline
	restarts  int
	lastErr   string
	resW, resH int
	frameNum  int64
	startedAt time.Time

	// NAL 解析 + 帧聚合 (仅 readLoop goroutine 访问, 无锁)
	stream    NALStream
	assembler VideoAssembler

	rtpTS  uint32 // 递增的 RTP 时间戳 (90000Hz)
	rtpStep uint32
	frameDur time.Duration
}

// NewVideo 创建视频采集器。probe 缓存启动探测结果。
func NewVideo(cfg ScreenCfg, probe ProbeResult, hub *Hub, onEvent func(Event)) *VideoCapture {
	fr := cfg.Framerate
	if fr <= 0 {
		fr = 30
	}
	return &VideoCapture{
		cfg:      cfg,
		probe:    probe,
		hub:      hub,
		onEvent:  onEvent,
		rtpStep:  uint32(90000 / fr),
		frameDur: time.Second / time.Duration(fr),
	}
}

// Start 启动采集 (幂等)。若已运行则返回 nil。
func (v *VideoCapture) Start() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.running {
		return nil
	}

	pipeline, err := BuildVideoArgs(v.cfg, v.probe)
	if err != nil {
		v.lastErr = err.Error()
		return err
	}

	cmd := exec.Command(v.probe.Path, pipeline.Args...)
	cmd.Stderr = nil // 单独管道读取

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		v.lastErr = err.Error()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		v.lastErr = err.Error()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		v.lastErr = err.Error()
		return fmt.Errorf("ffmpeg 启动失败: %w", err)
	}

	v.cmd = cmd
	v.stdout = stdout
	v.stopCh = make(chan struct{})
	v.running = true
	v.pipeline = pipeline
	v.startedAt = time.Now()
	v.frameNum = 0
	v.rtpTS = 0
	v.stream.Reset()
	v.assembler.Reset()

	log.Printf("[capture] 视频采集启动: %s (PID %d)", pipeline.Name, cmd.Process.Pid)

	go v.readStderr(stderr)
	go v.readLoop()
	go v.waitExit(cmd)

	return nil
}

// readStderr 解析 ffmpeg stderr: 提取分辨率, 收集错误行
func (v *VideoCapture) readStderr(stderr io.ReadCloser) {
	defer stderr.Close()
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 64*1024), 256*1024)
	var lastErrLines []string
	for scanner.Scan() {
		line := scanner.Text()
		// 解析采集分辨率: "Stream #0:0: Video: h264 (Baseline), ..., 2560x1440 [SAR ...]"
		if m := resRegex.FindStringSubmatch(line); len(m) == 3 {
			if w, err1 := strconv.Atoi(m[1]); err1 == nil {
				if h, err2 := strconv.Atoi(m[2]); err2 == nil {
					v.mu.Lock()
					v.resW, v.resH = w, h
					v.mu.Unlock()
					log.Printf("[capture] 采集分辨率: %dx%d", w, h)
				}
			}
		}
		// 收集错误行 (保留最近 5 条, 供崩溃诊断)
		if isFFmpegError(line) {
			lastErrLines = append(lastErrLines, line)
			if len(lastErrLines) > 5 {
				lastErrLines = lastErrLines[len(lastErrLines)-5:]
			}
		}
	}
	if len(lastErrLines) > 0 {
		v.mu.Lock()
		v.lastErr = strings.Join(lastErrLines, "\n")
		v.mu.Unlock()
	}
}

// isFFmpegError 判断 stderr 行是否为错误 (排除信息/统计行)
func isFFmpegError(line string) bool {
	lower := strings.ToLower(line)
	keywords := []string{"error", "could not", "failed", "not found", "cannot", "no such",
		"invalid", "unsupported", "unknown", "device", "permission denied"}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			// "Errors" 出现在 "No errors" 中时跳过
			if kw == "error" && strings.Contains(lower, "no errors") {
				continue
			}
			return true
		}
	}
	return false
}

// readLoop 读取 H264 字节流 → NAL 切分 → 帧聚合 → 扇出
func (v *VideoCapture) readLoop() {
	defer func() {
		if v.stdout != nil {
			v.stdout.Close()
		}
	}()

	reader := bufio.NewReaderSize(v.stdout, 256*1024)
	buf := make([]byte, 65536)

	for {
		select {
		case <-v.stopCh:
			return
		default:
		}
		n, err := reader.Read(buf)
		if n > 0 {
			v.process(buf[:n])
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("[capture] 视频流读取错误: %v", err)
			}
			return
		}
	}
}

// process 解析数据块并发布完整帧
func (v *VideoCapture) process(data []byte) {
	v.stream.Feed(data, func(nal NAL) {
		if frame := v.assembler.Feed(nal); frame != nil {
			v.publishFrame(frame)
		}
	})
}

// publishFrame 将聚合帧扇出到 Hub
func (v *VideoCapture) publishFrame(frame []byte) {
	// 拷贝: Hub 订阅者异步消费, 不能引用复用缓冲区
	data := make([]byte, len(frame))
	copy(data, frame)

	v.frameNum++
	v.rtpTS += v.rtpStep
	v.hub.Publish(Sample{
		Kind:     KindVideo,
		Data:     data,
		TS:       v.rtpTS,
		Duration: v.frameDur,
	})
	if v.frameNum%300 == 0 {
		log.Printf("[capture] 视频: %d 帧已推送, 最近 %d 字节", v.frameNum, len(frame))
	}
}

// waitExit 监控进程退出, 非主动停止时自动重启 (最多 3 次)
func (v *VideoCapture) waitExit(cmd *exec.Cmd) {
	err := cmd.Wait()
	v.mu.Lock()
	isStopped := !v.running
	if isStopped {
		v.mu.Unlock()
		return
	}
	restarts := v.restarts
	lastErr := v.lastErr
	v.mu.Unlock()

	if lastErr == "" && err != nil {
		lastErr = err.Error()
	}
	log.Printf("[capture] 视频进程退出: %v (lastErr: %s)", err, lastErr)

	if restarts >= 3 {
		v.emit(Event{Type: EventVideoCrashed, Message: "视频采集进程多次崩溃, 已停止: " + lastErr})
		v.mu.Lock()
		v.running = false
		v.lastErr = lastErr
		v.mu.Unlock()
		return
	}

	v.emit(Event{Type: EventVideoCrashed, Message: "视频采集进程退出: " + lastErr})
	time.Sleep(2 * time.Second)

	v.mu.Lock()
	v.restarts++
	v.mu.Unlock()
	v.emit(Event{Type: EventVideoRestart, Message: "正在重启视频采集..."})
	log.Printf("[capture] 重启视频采集 (%d/3)", restarts+1)
	_ = v.Start()
}

// Stop 停止采集 (幂等)
func (v *VideoCapture) Stop() {
	v.mu.Lock()
	if !v.running {
		v.mu.Unlock()
		return
	}
	v.running = false
	close(v.stopCh)
	cmd := v.cmd
	v.cmd = nil
	v.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}
	log.Printf("[capture] 视频采集已停止")
	v.emit(Event{Type: EventVideoStopped, Message: "视频采集已停止"})
}

// emit 触发事件回调 (如已注册)
func (v *VideoCapture) emit(ev Event) {
	if v.onEvent != nil {
		v.onEvent(ev)
	}
}

// ParameterSets 返回缓存的 SPS+PPS AU (含起始码), 供新订阅者立即解码
func (v *VideoCapture) ParameterSets() []byte {
	return v.assembler.ParameterSets()
}

// Resolution 返回采集分辨率 (未解析到则为 0,0)
func (v *VideoCapture) Resolution() (int, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.resW, v.resH
}

// LastRTPTS 返回最近一帧的 RTP 时间戳 (供参数集重发, 保持时间戳连续)
func (v *VideoCapture) LastRTPTS() uint32 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.rtpTS
}

// Status 返回当前状态快照
func (v *VideoCapture) Status() VideoStatus {
	v.mu.Lock()
	defer v.mu.Unlock()
	return VideoStatus{
		Running:   v.running,
		Pipeline:  v.pipeline.Name,
		HW:        v.pipeline.HW,
		FPS:       v.hub.FPS(),
		Width:     v.resW,
		Height:    v.resH,
		Restarts:  v.restarts,
		LastError: v.lastErr,
	}
}
