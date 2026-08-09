//go:build e2e

// 位流可解码性验证: 真实 ffmpeg 采集 → NAL 解析 → 帧聚合 → 写入文件 → ffprobe 校验。
// 证明 assembler 输出的 H.264 位流是合法的、可被解码器解析的。
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/onkenut/lazyDesk/capture"
)

// TestBitstreamDecodable 验证聚合位流可被 ffprobe 解析
func TestBitstreamDecodable(t *testing.T) {
	probe, err := capture.Probe("")
	if err != nil {
		t.Skipf("无 ffmpeg: %v", err)
	}

	// 生成 3 秒采集流 (NVENC)
	out := filepath.Join(t.TempDir(), "stream.h264")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}

	args := []string{
		"-hide_banner", "-nostats", "-loglevel", "error",
		"-f", "lavfi",
		"-i", "ddagrab=output_idx=0:framerate=30:dup_frames=1:draw_mouse=1",
		"-c:v", "h264_nvenc",
		"-preset", "p1", "-tune", "ll", "-profile:v", "baseline",
		"-rc", "cbr", "-b:v", "4M", "-maxrate", "4M", "-bufsize", "128K",
		"-bf", "0", "-g", "30",
		"-an", "-f", "h264", "pipe:1",
	}
	cmd := exec.Command(probe.Path, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	// NAL 解析 + 帧聚合 → 写入文件
	stream := &capture.NALStream{}
	assembler := &capture.VideoAssembler{}
	buf := make([]byte, 65536)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		n, err := stdout.Read(buf)
		if err != nil {
			t.Fatalf("读取采集流失败: %v", err)
		}
		stream.Feed(buf[:n], func(nal capture.NAL) {
			if frame := assembler.Feed(nal); frame != nil {
				f.Write(frame)
			}
		})
	}
	// 残留帧
	if frame := assembler.Flush(); frame != nil {
		f.Write(frame)
	}
	f.Close()

	// ffprobe 验证
	ffprobe := filepath.Join(filepath.Dir(probe.Path), "ffprobe.exe")
	check := exec.Command(ffprobe, "-v", "error", "-show_streams", "-select_streams", "v",
		"-show_entries", "stream=codec_name,width,height,pix_fmt,profile", "-of", "json", out)
	output, err := check.Output()
	if err != nil {
		t.Fatalf("ffprobe 解析失败 (位流不可解码): %v\n%s", err, output)
	}
	t.Logf("ffprobe 解析成功: %s", string(output))
}
