//go:build e2e

// lazyDesk v2 端到端测试:
// 真实启动服务端 (ffmpeg 采集) + Pion 作为 WebRTC 客户端,
// 验证 信令 / 视频流 / 音频流 / 多客户端 / 断连清理。
//
// 运行: go test -tags e2e ./e2e/ -v
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onkenut/lazyDesk/capture"
	"github.com/onkenut/lazyDesk/config"
	"github.com/onkenut/lazyDesk/control"
	"github.com/onkenut/lazyDesk/server"
	slotwebrtc "github.com/onkenut/lazyDesk/webrtc"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// startServer 启动进程内服务端, 返回 base URL 与清理函数
func startServer(t *testing.T) string {
	t.Helper()
	cfg := &config.Config{}
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 0
	cfg.Ffmpeg.Screen.Codec = "h264_nvenc"
	cfg.Ffmpeg.Screen.Framerate = 60
	cfg.Ffmpeg.Screen.GopSize = 60
	cfg.Ffmpeg.Screen.Fallback = true
	cfg.Ffmpeg.Screen.Capture.Method = "ddagrab"
	cfg.Ffmpeg.Screen.Capture.DrawMouse = true
	cfg.Ffmpeg.Screen.Capture.DupFrames = true // 保证静态画面也有帧
	cfg.Ffmpeg.Screen.NVENC.RateControl = "cbr"
	cfg.Ffmpeg.Screen.Bitrate = "4M"
	cfg.Ffmpeg.Screen.NVENC.Maxrate = "4M"
	cfg.Ffmpeg.Screen.NVENC.Bufsize = "128K"
	cfg.Ffmpeg.Screen.Preset = "p1"
	cfg.Ffmpeg.Screen.Tune = "ll"
	cfg.Ffmpeg.Screen.Profile = "baseline"
	cfg.Ffmpeg.Audio.Device = "立体声混音"
	cfg.Ffmpeg.Audio.Bitrate = "128k"

	cap := capture.New(cfg, nil)
	factory, err := slotwebrtc.NewFactory(cfg)
	if err != nil {
		t.Fatalf("webrtc factory: %v", err)
	}
	ctrl := control.NewHandler(cfg)
	srv := server.NewServer(cfg, cap, factory, ctrl)

	ts := httptest.NewServer(srv.Handler(fstest.MapFS{}))
	t.Cleanup(func() {
		ts.Close()
		cap.Close()
	})
	return ts.URL
}

// client 是模拟浏览器的 WS + WebRTC 客户端
type client struct {
	t          *testing.T
	ws         *websocket.Conn
	pc         *webrtc.PeerConnection
	videoPkts  atomic.Int64
	audioPkts  atomic.Int64
	connected  chan struct{}
	writeMu    sync.Mutex
	done       chan struct{}
	closeOnce  sync.Once
}

// dialClient 建立 WS 连接 + WebRTC 信令, 等待媒体流
func dialClient(t *testing.T, baseURL string) *client {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("pc: %v", err)
	}
	// 接收视频 + 音频
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatalf("video transceiver: %v", err)
	}
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatalf("audio transceiver: %v", err)
	}
	c := dialClientWithPC(t, baseURL, pc)

	// 统计 onTrack (dialClientWithPC 不注册, 供调用方自定义)
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		t.Logf("onTrack: %s (codec %s)", track.Kind(), track.Codec().MimeType)
		go func() {
			buf := make([]byte, 1500)
			for {
				n, _, err := track.Read(buf)
				if err != nil {
					return
				}
				var pkt rtp.Packet
				if err := pkt.Unmarshal(buf[:n]); err != nil {
					continue
				}
				if track.Kind() == webrtc.RTPCodecTypeVideo {
					c.videoPkts.Add(1)
				} else {
					c.audioPkts.Add(1)
				}
			}
		}()
	})
	return c
}

// dialClientWithPC 用给定的 PC 建立 WS 信令连接
func dialClientWithPC(t *testing.T, baseURL string, pc *webrtc.PeerConnection) *client {
	t.Helper()
	wsURL := strings.Replace(baseURL, "http://", "ws://", 1) + "/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}

	c := &client{
		t:         t,
		ws:        ws,
		pc:        pc,
		connected: make(chan struct{}),
		done:      make(chan struct{}),
	}

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		t.Logf("pc state: %s", state)
		if state == webrtc.PeerConnectionStateConnected {
			close(c.connected)
		}
	})

	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand == nil {
			return
		}
		j := cand.ToJSON()
		c.send(map[string]interface{}{
			"type":          "candidate",
			"candidate":     j.Candidate,
			"sdpMid":        j.SDPMid,
			"sdpMLineIndex": j.SDPMLineIndex,
		})
	})

	// 接收服务端消息: answer / candidate
	go func() {
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				select {
				case <-c.done:
				default:
					t.Logf("ws read closed: %v", err)
				}
				return
			}
			var m map[string]interface{}
			if err := json.Unmarshal(msg, &m); err != nil {
				continue
			}
			switch m["type"] {
			case "server_ready":
				offer, err := pc.CreateOffer(nil)
				if err != nil {
					t.Errorf("create offer: %v", err)
					return
				}
				if err := pc.SetLocalDescription(offer); err != nil {
					t.Errorf("set local: %v", err)
					return
				}
				c.send(map[string]interface{}{"type": "offer", "sdp": offer.SDP})
			case "answer":
				sdp, _ := m["sdp"].(string)
				if err := pc.SetRemoteDescription(webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer, SDP: sdp,
				}); err != nil {
					t.Errorf("set remote answer: %v", err)
					return
				}
			case "candidate":
				cd, _ := m["candidate"].(string)
				mid, _ := m["sdpMid"].(string)
				idx := m["sdpMLineIndex"]
				var idxPtr *uint16
				if f, ok := idx.(float64); ok {
					v := uint16(f)
					idxPtr = &v
				}
				ci := webrtc.ICECandidateInit{Candidate: cd, SDPMid: &mid, SDPMLineIndex: idxPtr}
				if err := pc.AddICECandidate(ci); err != nil {
					t.Logf("add ice: %v", err)
				}
			case "error":
				t.Errorf("server error: %v %v", m["code"], m["message"])
			}
		}
	}()

	return c
}

func (c *client) send(v interface{}) {
	data, _ := json.Marshal(v)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
		c.t.Logf("ws send: %v", err)
	}
}

func (c *client) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.pc != nil {
			c.pc.Close()
		}
		c.ws.Close()
	})
}

// TestE2EVideoAndAudio 单客户端: 信令 + 视频流 + 音频流
func TestE2EVideoAndAudio(t *testing.T) {
	base := startServer(t)

	// 探测音频设备是否可用 (不可用时跳过音频断言)
	probe, err := capture.Probe("")
	if err != nil {
		t.Fatalf("ffmpeg probe: %v", err)
	}
	_, audioErr := capture.ListAudioDevices(probe.Path) //nolint:staticcheck
	haveAudio := audioErr == nil

	c := dialClient(t, base)
	defer c.close()

	select {
	case <-c.connected:
	case <-time.After(20 * time.Second):
		t.Fatal("WebRTC 未在 20s 内连接")
	}

	// 等待视频帧到达 (60fps, 3 秒 ≈ 180 包)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if c.videoPkts.Load() > 100 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	vpkts := c.videoPkts.Load()
	if vpkts < 100 {
		t.Fatalf("视频包不足: %d (期望 > 100)", vpkts)
	}
	t.Logf("视频包: %d ✓", vpkts)

	if haveAudio {
		time.Sleep(2 * time.Second)
		apkts := c.audioPkts.Load()
		if apkts < 5 {
			t.Fatalf("音频包不足: %d (期望 > 5)", apkts)
		}
		t.Logf("音频包: %d ✓", apkts)
	} else {
		t.Logf("跳过音频断言 (无采集设备)")
	}
}

// TestE2EMultiClient 双客户端同时收流
func TestE2EMultiClient(t *testing.T) {
	base := startServer(t)

	c1 := dialClient(t, base)
	defer c1.close()
	select {
	case <-c1.connected:
	case <-time.After(20 * time.Second):
		t.Fatal("客户端1 未连接")
	}

	c2 := dialClient(t, base)
	defer c2.close()
	select {
	case <-c2.connected:
	case <-time.After(20 * time.Second):
		t.Fatal("客户端2 未连接")
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if c1.videoPkts.Load() > 50 && c2.videoPkts.Load() > 50 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("客户端1 视频包: %d, 客户端2 视频包: %d", c1.videoPkts.Load(), c2.videoPkts.Load())
	if c1.videoPkts.Load() < 50 || c2.videoPkts.Load() < 50 {
		t.Fatalf("双客户端未同时收到视频流")
	}

	// 断开 c1 → c2 不受影响
	c1.close()
	time.Sleep(2 * time.Second)
	before := c2.videoPkts.Load()
	time.Sleep(2 * time.Second)
	after := c2.videoPkts.Load()
	if after <= before {
		t.Fatalf("客户端1 断开后客户端2 视频停止 (before=%d after=%d)", before, after)
	}
	t.Logf("客户端1 断开后, 客户端2 继续收流 ✓")
}

// TestE2EStatusAPI 状态 API 与断连清理
func TestE2EStatusAPI(t *testing.T) {
	base := startServer(t)
	addr := strings.TrimPrefix(base, "http://")

	c := dialClient(t, base)
	select {
	case <-c.connected:
	case <-time.After(20 * time.Second):
		t.Fatal("WebRTC 未连接")
	}

	// 状态 API: 采集运行 + 客户端计数
	deadline := time.Now().Add(5 * time.Second)
	var st map[string]interface{}
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://%s/api/status", addr))
		if err != nil {
			t.Fatal(err)
		}
		json.NewDecoder(resp.Body).Decode(&st)
		resp.Body.Close()
		if st["clients"].(float64) >= 1 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	video := st["video"].(map[string]interface{})
	if video["running"] != true {
		t.Fatalf("视频采集未运行: %+v", video)
	}
	if st["clients"].(float64) < 1 {
		t.Fatalf("客户端计数错误: %v", st["clients"])
	}
	t.Logf("状态 API ✓ (clients=%v, fps=%.0f)", st["clients"], video["fps"].(float64))

	// 断连 → 采集停止, 客户端归零
	c.close()
	time.Sleep(3 * time.Second)
	resp, err := http.Get(fmt.Sprintf("http://%s/api/status", addr))
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	clients := st["clients"].(float64)
	if clients != 0 {
		t.Fatalf("断连后客户端计数应为 0, 实际 %v", clients)
	}
	t.Logf("断连清理 ✓ (clients=0)")
}
