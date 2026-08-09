package capture

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtp"
)

// AudioConfig 音频采集配置
type AudioConfig struct {
	Device  string // 设备关键词 (模糊匹配, 如 "立体声混音")
	Bitrate string // 如 "128k"
}

// AudioStatus 音频采集状态 (供 /api/status)
type AudioStatus struct {
	Running   bool   `json:"running"`
	Disabled  bool   `json:"disabled"` // 设备不可用等, 自动禁用
	Device    string `json:"device"`
	LastError string `json:"last_error,omitempty"`
	Restarts  int    `json:"restarts"`
}

const (
	opusPT        = 111 // WebRTC 标准 Opus payload type
	opusSampleDur = 20 * time.Millisecond
	audioLocal    = "127.0.0.1" // ffmpeg RTP 输出到本机 UDP, 避免防火墙干扰
)

// AudioCapture 管理 ffmpeg 音频采集进程:
// dshow 设备 → libopus → RTP/UDP → 解析 → 扇出到 Hub
type AudioCapture struct {
	cfg     AudioConfig
	probe   ProbeResult
	hub     *Hub
	onEvent func(Event)

	mu       sync.Mutex
	cmd      *exec.Cmd
	conn     *net.UDPConn
	stopCh   chan struct{}
	running  bool
	disabled bool
	device   string
	restarts int
	lastErr  string
	packets  int64
}

// NewAudio 创建音频采集器。probe 用于枚举 dshow 设备。
func NewAudio(cfg AudioConfig, probe ProbeResult, hub *Hub, onEvent func(Event)) *AudioCapture {
	return &AudioCapture{
		cfg:     cfg,
		probe:   probe,
		hub:     hub,
		onEvent: onEvent,
	}
}

// deviceRegex 解析 ffmpeg -list_devices 输出: "设备名" (audio)
var deviceRegex = regexp.MustCompile(`"([^"]+)"\s+\(audio\)`)

// ListAudioDevices 枚举系统音频采集设备
func ListAudioDevices(ffmpegPath string) ([]string, error) {
	cmd := exec.Command(ffmpegPath, "-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var devices []string
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		if m := deviceRegex.FindStringSubmatch(scanner.Text()); len(m) == 2 {
			devices = append(devices, m[1])
		}
	}
	cmd.Wait()
	return devices, nil
}

// matchDevice 按关键词匹配音频设备。
// 只比较括号前的主名: "立体声混音 (Stereo Mix)" 可匹配 "立体声混音 (Realtek HD Audio)"
func matchDevice(name, keyword string) bool {
	lowerName := strings.ToLower(name)
	kw := strings.ToLower(strings.TrimSpace(keyword))
	if kw == "" {
		return true
	}
	if i := strings.IndexByte(kw, '('); i > 0 {
		kw = strings.TrimSpace(kw[:i])
	}
	base := lowerName
	if i := strings.IndexByte(base, '('); i > 0 {
		base = strings.TrimSpace(base[:i])
	}
	return strings.Contains(base, kw) || strings.Contains(lowerName, kw)
}

// resolveDevice 按关键词匹配音频设备。关键词为空时取第一个可用设备。
func (a *AudioCapture) resolveDevice() (string, error) {
	devices, err := ListAudioDevices(a.probe.Path)
	if err != nil {
		return "", fmt.Errorf("枚举音频设备失败: %w", err)
	}
	for _, d := range devices {
		if matchDevice(d, a.cfg.Device) {
			return d, nil
		}
	}
	if len(devices) > 0 {
		return "", fmt.Errorf("未找到匹配音频设备 %q, 可用: %s",
			a.cfg.Device, strings.Join(devices, " / "))
	}
	return "", fmt.Errorf("未找到任何音频采集设备 (请检查立体声混音是否启用)")
}

// Start 启动音频采集 (幂等)。设备不可用时自动禁用并通知, 不阻塞视频。
func (a *AudioCapture) Start() error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil
	}
	if a.disabled {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	// 解析设备
	device, err := a.resolveDevice()
	if err != nil {
		a.mu.Lock()
		a.disabled = true
		a.lastErr = err.Error()
		a.mu.Unlock()
		a.emit(Event{Type: EventAudioDisabled, Message: "音频不可用: " + err.Error()})
		log.Printf("[capture] %v", err)
		return nil // 音频失败不影响视频
	}

	// 本地 UDP 端口
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(audioLocal), Port: 0})
	if err != nil {
		a.mu.Lock()
		a.disabled = true
		a.lastErr = err.Error()
		a.mu.Unlock()
		a.emit(Event{Type: EventAudioDisabled, Message: "音频端口分配失败: " + err.Error()})
		return nil
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port

	bitrate := a.cfg.Bitrate
	if bitrate == "" {
		bitrate = "128k"
	}
	rtpURL := fmt.Sprintf("rtp://%s:%d", audioLocal, port)
	args := []string{
		"-hide_banner", "-nostats", "-loglevel", "warning",
		"-f", "dshow", "-i", "audio=" + device,
		"-c:a", "libopus",
		"-b:a", bitrate,
		"-ar", "48000", "-ac", "2",
		"-payload_type", "111",
		"-f", "rtp", rtpURL,
	}
	cmd := exec.Command(a.probe.Path, args...)
	cmd.Stderr = nil // 继承? 不, 直接丢弃, 错误通过退出码/日志诊断

	if err := cmd.Start(); err != nil {
		conn.Close()
		a.mu.Lock()
		a.disabled = true
		a.lastErr = err.Error()
		a.mu.Unlock()
		a.emit(Event{Type: EventAudioDisabled, Message: "音频进程启动失败: " + err.Error()})
		return nil
	}

	a.mu.Lock()
	a.cmd = cmd
	a.conn = conn
	a.stopCh = make(chan struct{})
	a.running = true
	a.device = device
	a.packets = 0
	a.mu.Unlock()

	log.Printf("[capture] 音频采集启动: %q → opus RTP (端口 %d, PID %d)", device, port, cmd.Process.Pid)

	go a.readLoop()
	go a.waitExit(cmd)
	return nil
}

// readLoop 从 UDP 读取 RTP 包 → Opus 载荷扇出到 Hub
func (a *AudioCapture) readLoop() {
	buf := make([]byte, 4096)
	for {
		select {
		case <-a.stopCh:
			return
		default:
		}
		n, _, err := a.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var pkt rtp.Packet
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			continue // 非 RTP 包, 忽略
		}
		if pkt.PayloadType != opusPT || len(pkt.Payload) == 0 {
			continue
		}
		// 拷贝载荷: Hub 订阅者异步消费
		data := make([]byte, len(pkt.Payload))
		copy(data, pkt.Payload)
		a.hub.Publish(Sample{
			Kind:     KindAudio,
			Data:     data,
			TS:       pkt.Timestamp,
			Duration: opusSampleDur,
		})
		a.mu.Lock()
		a.packets++
		a.mu.Unlock()
	}
}

// waitExit 监控音频进程退出, 非主动停止时自动重启 (最多 3 次)
func (a *AudioCapture) waitExit(cmd *exec.Cmd) {
	err := cmd.Wait()
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return
	}
	restarts := a.restarts
	lastErr := a.lastErr
	a.mu.Unlock()

	if err != nil {
		lastErr = "音频进程退出: " + err.Error()
	}
	log.Printf("[capture] 音频进程退出: %v", err)

	if restarts >= 3 {
		a.mu.Lock()
		a.running = false
		a.disabled = true
		a.lastErr = lastErr
		a.mu.Unlock()
		a.emit(Event{Type: EventAudioCrashed, Message: "音频进程多次崩溃, 已禁用: " + lastErr})
		return
	}

	a.emit(Event{Type: EventAudioCrashed, Message: lastErr})
	time.Sleep(2 * time.Second)
	a.mu.Lock()
	a.restarts++
	a.mu.Unlock()
	a.emit(Event{Type: EventAudioRestart, Message: "正在重启音频采集..."})
	_ = a.Start()
}

// Stop 停止音频采集 (幂等)
func (a *AudioCapture) Stop() {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return
	}
	a.running = false
	close(a.stopCh)
	cmd := a.cmd
	conn := a.conn
	a.cmd = nil
	a.conn = nil
	a.mu.Unlock()

	if conn != nil {
		conn.Close()
	}
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}
	log.Printf("[capture] 音频采集已停止")
}

// Status 返回当前状态快照
func (a *AudioCapture) Status() AudioStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AudioStatus{
		Running:   a.running,
		Disabled:  a.disabled,
		Device:    a.device,
		LastError: a.lastErr,
		Restarts:  a.restarts,
	}
}

// emit 触发事件回调 (如已注册)
func (a *AudioCapture) emit(ev Event) {
	if a.onEvent != nil {
		a.onEvent(ev)
	}
}
