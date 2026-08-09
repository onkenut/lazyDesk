//go:build e2e

// RTP 载荷验证: Pion 客户端接收服务端 RTP → H264Packet 重组 → ffprobe 校验。
// 证明"经过 RTP 打包后"的位流仍可解码 (浏览器解码器能识别的格式)。
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/onkenut/lazyDesk/capture"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

// TestERTPDepacketized 验证 RTP 载荷重组后可被 ffprobe 解析
func TestERTPDepacketized(t *testing.T) {
	probe, err := capture.Probe("")
	if err != nil {
		t.Skipf("无 ffmpeg: %v", err)
	}
	base := startServer(t)

	// 自定义 PC: 只收视频, 处理器做 depacketize
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "rtp_reassembled.h264")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var frames int
	var maxNal, totalBytes int
	nalTypes := map[byte]int{}
	dep := &codecs.H264Packet{}

	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeVideo {
			return
		}
		t.Logf("video track 就绪: %s", track.Codec().MimeType)
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
				nalu, err := dep.Unmarshal(pkt.Payload)
				if err != nil || len(nalu) == 0 {
					continue
				}
				mu.Lock()
				f.Write(nalu)
				frames++
				totalBytes += len(nalu)
				if len(nalu) > maxNal {
					maxNal = len(nalu)
				}
				// 统计 NAL 类型 (Annex B 起始码后, 统计块内所有 NAL)
				for i := 0; i+3 < len(nalu); i++ {
					if nalu[i] == 0 && nalu[i+1] == 0 && nalu[i+2] == 1 {
						nalTypes[nalu[i+3]&0x1F]++
						i += 2 // 跳过起始码
					}
				}
				mu.Unlock()
			}
		}()
	})

	c := dialClientWithPC(t, base, pc)
	defer c.close()

	select {
	case <-c.connected:
	case <-time.After(20 * time.Second):
		t.Fatal("WebRTC 未连接")
	}

	// 收集 4 秒
	time.Sleep(4 * time.Second)
	mu.Lock()
	f.Close()
	n := frames
	types := make(map[byte]int, len(nalTypes))
	for k, v := range nalTypes {
		types[k] = v
	}
	maxN := maxNal
	total := totalBytes
	mu.Unlock()
	t.Logf("重组 NAL: %d 个, 共 %d 字节, 最大 %d 字节", n, total, maxN)
	t.Logf("NAL 类型分布 (7=SPS 8=PPS 6=SEI 5=IDR 1=slice): %v", types)
	if n < 100 {
		t.Fatalf("重组 NAL 太少: %d", n)
	}
	if types[7] == 0 || types[8] == 0 {
		t.Fatalf("缺少 SPS/PPS: %v", types)
	}
	if types[5] == 0 {
		t.Fatalf("缺少 IDR 关键帧: %v", types)
	}
	if maxN < 10000 {
		t.Logf("警告: 最大 NAL 偏小 (%d 字节), 分片重组可能异常", maxN)
	}

	ffprobe := filepath.Join(filepath.Dir(probe.Path), "ffprobe.exe")
	check := exec.Command(ffprobe, "-v", "error", "-show_streams", "-select_streams", "v",
		"-show_entries", "stream=codec_name,width,height", "-of", "json", out)
	output, err := check.Output()
	if err != nil {
		t.Fatalf("ffprobe 解析 RTP 重组流失败 (RTP 载荷损坏): %v\n%s", err, output)
	}
	t.Logf("RTP 重组流可解析 ✓: %s", string(output))

	// 实际解码验证 (ffprobe 只解析 SPS, 不解码帧)
	ffmpeg := probe.Path
	decode := exec.Command(ffmpeg, "-hide_banner", "-v", "error", "-i", out, "-f", "null", "-")
	if decOut, err := decode.CombinedOutput(); err != nil {
		t.Fatalf("RTP 重组流实际解码失败: %v\n%s", err, decOut)
	} else {
		t.Logf("RTP 重组流实际解码 ✓ (%d 字节)", total)
	}
}
