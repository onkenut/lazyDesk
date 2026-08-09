package capture

import (
	"bytes"
	"testing"
)

// feedAssembler 把 NAL 列表喂给 assembler, 返回全部完整帧
func feedAssembler(t *testing.T, nals []NAL) [][]byte {
	t.Helper()
	a := &VideoAssembler{}
	var frames [][]byte
	for _, nal := range nals {
		if f := a.Feed(nal); f != nil {
			frames = append(frames, append([]byte(nil), f...))
		}
	}
	if f := a.Flush(); f != nil {
		frames = append(frames, append([]byte(nil), f...))
	}
	return frames
}

func TestAssemblerNormalizesStartCode(t *testing.T) {
	// 混合 3/4 字节起始码 → 输出必须全部为 4 字节起始码
	nal3 := NAL{Data: []byte{0x00, 0x00, 0x01, 0x65, 0xAA}, Type: 5, CodeLen: 3}
	nal4 := NAL{Data: []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0xBB}, Type: 1, CodeLen: 4}

	frames := feedAssembler(t, []NAL{nal3, nal4})
	if len(frames) != 2 {
		t.Fatalf("expect 2 frames, got %d", len(frames))
	}
	// 每帧必须以 0x00000001 开头
	for i, f := range frames {
		if !bytes.HasPrefix(f, []byte{0x00, 0x00, 0x00, 0x01}) {
			t.Errorf("frame[%d] 起始码未规范化: %x", i, f[:6])
		}
	}
	// 内容完整: 3 字节 SC + 0x65 + 0xAA = 5 字节 → 规范化后 6 字节
	if !bytes.Equal(frames[0], []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA}) {
		t.Errorf("frame[0] 内容错误: %x", frames[0])
	}
	if !bytes.Equal(frames[1], []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0xBB}) {
		t.Errorf("frame[1] 内容错误: %x", frames[1])
	}
}

func TestAssemblerFrameBoundary(t *testing.T) {
	// SPS+PPS+SEI+IDR → 关键帧; 后续 SEI+slice → P 帧
	sps := NAL{Data: []byte{0x00, 0x00, 0x01, 0x67, 0x01}, Type: 7, CodeLen: 3}
	pps := NAL{Data: []byte{0x00, 0x00, 0x01, 0x68, 0x02}, Type: 8, CodeLen: 3}
	sei := NAL{Data: []byte{0x00, 0x00, 0x01, 0x06, 0x03}, Type: 6, CodeLen: 3}
	idr := NAL{Data: []byte{0x00, 0x00, 0x01, 0x65, 0xAA}, Type: 5, CodeLen: 3}
	slice := NAL{Data: []byte{0x00, 0x00, 0x01, 0x41, 0xBB}, Type: 1, CodeLen: 3}

	frames := feedAssembler(t, []NAL{sps, pps, sei, idr, sei, slice})
	if len(frames) != 2 {
		t.Fatalf("expect 2 frames, got %d", len(frames))
	}
	// 关键帧含 SPS+PPS+SEI+IDR (4 个 NAL)
	if !bytes.Contains(frames[0], []byte{0x67}) || !bytes.Contains(frames[0], []byte{0x68}) ||
		!bytes.Contains(frames[0], []byte{0x65}) {
		t.Errorf("关键帧缺少参数集: %x", frames[0])
	}
	// P 帧含 SEI+slice, 无 SPS
	if bytes.Contains(frames[1], []byte{0x67}) {
		t.Errorf("P 帧不应含 SPS: %x", frames[1])
	}
	if !bytes.Contains(frames[1], []byte{0x41}) {
		t.Errorf("P 帧缺少 slice: %x", frames[1])
	}
}

func TestAssemblerAUDAndFillerDropped(t *testing.T) {
	aud := NAL{Data: []byte{0x00, 0x00, 0x01, 0x09}, Type: 9, CodeLen: 3}
	filler := NAL{Data: []byte{0x00, 0x00, 0x01, 0x0C}, Type: 12, CodeLen: 3}
	sei := NAL{Data: []byte{0x00, 0x00, 0x01, 0x06, 0x05}, Type: 6, CodeLen: 3}
	slice := NAL{Data: []byte{0x00, 0x00, 0x01, 0x41, 0xBB}, Type: 1, CodeLen: 3}

	frames := feedAssembler(t, []NAL{aud, filler, sei, slice})
	if len(frames) != 1 {
		t.Fatalf("expect 1 frame, got %d", len(frames))
	}
	// SEI 必须被丢弃: 参与 RTP 打包会导致 Chromium 无法解码 P 帧
	for _, b := range [][]byte{[]byte{0x09}, []byte{0x0C}, []byte{0x06}} {
		if bytes.Contains(frames[0], b) {
			t.Errorf("不应包含 NAL 0x%02x: %x", b[0], frames[0])
		}
	}
}

func TestAssemblerParameterSets(t *testing.T) {
	a := &VideoAssembler{}
	sps := NAL{Data: []byte{0x00, 0x00, 0x01, 0x67, 0x01}, Type: 7, CodeLen: 3}
	pps := NAL{Data: []byte{0x00, 0x00, 0x00, 0x01, 0x68, 0x02}, Type: 8, CodeLen: 4}
	a.Feed(sps)
	a.Feed(pps)
	ps := a.ParameterSets()
	if ps == nil {
		t.Fatal("ParameterSets 返回 nil")
	}
	// 应为 4 字节起始码 + 0x67... + 4 字节起始码 + 0x68...
	expect := []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x01, 0x00, 0x00, 0x00, 0x01, 0x68, 0x02}
	if !bytes.Equal(ps, expect) {
		t.Errorf("ParameterSets 错误: %x", ps)
	}
}

func TestAssemblerNoBufferOverwrite(t *testing.T) {
	// 回归: 上一帧返回后, 新 slice 不得覆盖 frame 内容
	a := &VideoAssembler{}
	sliceA := NAL{Data: []byte{0x00, 0x00, 0x01, 0x41}, Type: 1, CodeLen: 3}
	sliceB := NAL{Data: []byte{0x00, 0x00, 0x01, 0x41, 0x99, 0x88}, Type: 1, CodeLen: 3}

	fA := a.Feed(sliceA)
	if fA != nil {
		t.Fatal("第一帧不应返回")
	}
	fB := a.Feed(sliceB) // 触发 frame A 返回
	if fB == nil {
		t.Fatal("第二帧应返回")
	}
	if !bytes.Equal(fB, []byte{0x00, 0x00, 0x00, 0x01, 0x41}) {
		t.Errorf("frame A 被后续写入破坏: %x", fB)
	}
	// 继续喂第三帧, 验证 frame B 不损坏
	sliceC := NAL{Data: []byte{0x00, 0x00, 0x01, 0x41, 0x77}, Type: 1, CodeLen: 3}
	fC := a.Feed(sliceC)
	if fC == nil {
		t.Fatal("第三帧应返回")
	}
	if !bytes.Equal(fC, []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x99, 0x88}) {
		t.Errorf("frame B 被后续写入破坏: %x", fC)
	}
}
