package capture

import (
	"sync"
	"sync/atomic"
	"time"
)

// Kind 样本类型
type Kind int

const (
	KindVideo Kind = iota
	KindAudio
)

// Sample 一帧视频或一段音频, 写入 WebRTC track 的载体。
// Data 是 H.264 Annex B AU 或裸 Opus 包; TS 是 RTP 时间戳 (视频 90000Hz, 音频 48000Hz)。
type Sample struct {
	Kind     Kind
	Data     []byte
	TS       uint32
	Duration time.Duration
}

// Subscriber 是一次扇出订阅, 从 Chan 读取样本。
type Subscriber struct {
	ch      chan Sample
	dropped atomic.Int64 // 因消费慢被丢弃的帧数
	done    chan struct{}
}

// Chan 返回样本通道
func (s *Subscriber) Chan() <-chan Sample { return s.ch }

// Dropped 返回该订阅被丢弃的帧数
func (s *Subscriber) Dropped() int64 { return s.dropped.Load() }

// Hub 把采集样本扇出到所有订阅者。
// 慢订阅者只丢自己的帧, 不影响其他客户端 (多客户端核心)。
type Hub struct {
	mu   sync.Mutex
	subs map[*Subscriber]struct{}
	// 视频帧率统计
	fpsMu   sync.Mutex
	fpsTicks []time.Time
}

// NewHub 创建扇出 Hub
func NewHub() *Hub {
	return &Hub{subs: make(map[*Subscriber]struct{})}
}

// Subscribe 注册订阅, buf 为缓冲帧数 (慢客户端在此范围内吸收抖动)
func (h *Hub) Subscribe(buf int) *Subscriber {
	s := &Subscriber{
		ch:   make(chan Sample, buf),
		done: make(chan struct{}),
	}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

// Unsubscribe 注销订阅并关闭通道
func (h *Hub) Unsubscribe(s *Subscriber) {
	h.mu.Lock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.done)
	}
	h.mu.Unlock()
}

// Publish 向所有订阅者非阻塞扇出一个样本
func (h *Hub) Publish(sample Sample) {
	if sample.Kind == KindVideo {
		h.tickFPS()
	}
	h.mu.Lock()
	for sub := range h.subs {
		select {
		case sub.ch <- sample:
		default:
			// 订阅者消费慢 → 丢弃该帧 (只影响这一个客户端)
			sub.dropped.Add(1)
		}
	}
	h.mu.Unlock()
}

// Count 返回当前订阅数 (在线客户端数)
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// tickFPS 记录一帧视频样本
func (h *Hub) tickFPS() {
	now := time.Now()
	h.fpsMu.Lock()
	h.fpsTicks = append(h.fpsTicks, now)
	// 只保留最近 2 秒
	cutoff := now.Add(-2 * time.Second)
	i := 0
	for i < len(h.fpsTicks) && h.fpsTicks[i].Before(cutoff) {
		i++
	}
	h.fpsTicks = h.fpsTicks[i:]
	h.fpsMu.Unlock()
}

// FPS 返回最近 2 秒的帧率
func (h *Hub) FPS() float64 {
	h.fpsMu.Lock()
	defer h.fpsMu.Unlock()
	if len(h.fpsTicks) < 2 {
		return 0
	}
	span := h.fpsTicks[len(h.fpsTicks)-1].Sub(h.fpsTicks[0]).Seconds()
	if span <= 0 {
		return 0
	}
	return float64(len(h.fpsTicks)-1) / span
}
