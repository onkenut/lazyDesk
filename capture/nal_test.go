package capture

import (
	"bytes"
	"testing"
)

// makeNAL 构造一个 Annex B NAL (起始码 + 类型字节 + 载荷)
func makeNAL(nalType byte, payload []byte, codeLen int) []byte {
	sc := []byte{0x00, 0x00, 0x01}
	if codeLen == 4 {
		sc = []byte{0x00, 0x00, 0x00, 0x01}
	}
	nal := make([]byte, 0, len(sc)+1+len(payload))
	nal = append(nal, sc...)
	nal = append(nal, nalType)
	nal = append(nal, payload...)
	return nal
}

// buildStream 拼接多个 NAL 为一条字节流
func buildStream(nals ...[]byte) []byte {
	var out []byte
	for _, n := range nals {
		out = append(out, n...)
	}
	return out
}

// parseAll 用随机 chunk 切分喂给解析器, 收集全部 NAL
func parseAll(t *testing.T, data []byte, chunkSizes []int) []NAL {
	t.Helper()
	var got []NAL
	s := &NALStream{}
	off := 0
	for off < len(data) {
		n := chunkSizes[off%len(chunkSizes)]
		if off+n > len(data) {
			n = len(data) - off
		}
		s.Feed(data[off:off+n], func(nal NAL) {
			got = append(got, NAL{
				Data:    append([]byte(nil), nal.Data...),
				Type:    nal.Type,
				CodeLen: nal.CodeLen,
			})
		})
		off += n
	}
	// 流结束: 取回最后一个未切出的 NAL
	if nal, ok := s.Flush(); ok {
		got = append(got, NAL{
			Data:    append([]byte(nil), nal.Data...),
			Type:    nal.Type,
			CodeLen: nal.CodeLen,
		})
	}
	return got
}

func TestNALBasicSplit(t *testing.T) {
	sps := makeNAL(7, []byte{0xAA, 0xBB}, 4)
	pps := makeNAL(8, []byte{0xCC}, 4)
	slice := makeNAL(1, bytes.Repeat([]byte{0x01}, 100), 3)
	sei := makeNAL(6, []byte{0x05}, 3)
	stream := buildStream(sps, pps, slice, sei)

	got := parseAll(t, stream, []int{1024})
	if len(got) != 4 {
		t.Fatalf("expect 4 NALs, got %d", len(got))
	}
	expects := []struct {
		typ     byte
		codeLen int
	}{
		{7, 4}, {8, 4}, {1, 3}, {6, 3},
	}
	for i, e := range expects {
		if got[i].Type != e.typ || got[i].CodeLen != e.codeLen {
			t.Errorf("NAL[%d]: type=%d codeLen=%d, want type=%d codeLen=%d",
				i, got[i].Type, got[i].CodeLen, e.typ, e.codeLen)
		}
	}
	if !bytes.Equal(got[0].Data, sps) {
		t.Errorf("NAL[0] content mismatch: %x", got[0].Data)
	}
	if !bytes.Equal(got[2].Data, slice) {
		t.Errorf("NAL[2] content mismatch")
	}
}

func TestNALChunkBoundaries(t *testing.T) {
	// 用各种奇怪 chunk 大小验证跨边界切分
	chunks := [][]int{
		{1},
		{2},
		{3},
		{4},
		{1, 2, 3, 4, 5},
		{7, 3},
	}
	for _, cs := range chunks {
		sps := makeNAL(7, []byte{1, 2, 3, 4}, 4)
		slice := makeNAL(1, bytes.Repeat([]byte{0x02}, 50), 3)
		stream := buildStream(sps, slice)
		got := parseAll(t, stream, cs)
		if len(got) != 2 {
			t.Fatalf("chunks %v: expect 2 NALs, got %d", cs, len(got))
		}
		if got[0].Type != 7 || got[1].Type != 1 {
			t.Errorf("chunks %v: wrong types %d %d", cs, got[0].Type, got[1].Type)
		}
		if !bytes.Equal(got[0].Data, sps) || !bytes.Equal(got[1].Data, slice) {
			t.Errorf("chunks %v: content mismatch", cs)
		}
	}
}

func TestNALTruncatedStartCode(t *testing.T) {
	// 起始码被截断在 chunk 末尾: "00 00" 结尾, 下一块补 "00 01"
	// part1 = 完整 SPS + SC 前缀 2 字节; part2 = SC 剩余 2 字节 + 第二个 NAL
	sps := makeNAL(7, []byte{0x11, 0x22}, 4)
	slice := makeNAL(1, []byte{0x33}, 4)
	part1 := append(append([]byte{}, sps...), 0x00, 0x00)
	part2 := []byte{0x00, 0x01, 0x01, 0x33}
	stream := append(part1, part2...)

	got := parseAll(t, stream, []int{len(part1), len(part2)})
	if len(got) != 2 {
		t.Fatalf("expect 2 NALs, got %d", len(got))
	}
	if got[0].Type != 7 || got[0].CodeLen != 4 {
		t.Errorf("NAL[0]: type=%d codeLen=%d", got[0].Type, got[0].CodeLen)
	}
	if !bytes.Equal(got[0].Data, sps) {
		t.Errorf("NAL[0] content mismatch: %x", got[0].Data)
	}
	if !bytes.Equal(got[1].Data, slice) {
		t.Errorf("NAL[1] content mismatch: %x", got[1].Data)
	}
}

func TestNALBufferOverflowProtection(t *testing.T) {
	// 超过缓冲上限的无起始码数据 → Reset 触发, 不崩溃
	s := &NALStream{}
	big := bytes.Repeat([]byte{0x00}, maxNALSize+1024)
	emitted := 0
	s.Feed(big, func(NAL) { emitted++ })
	if emitted != 0 {
		t.Fatalf("no NAL should emit, got %d", emitted)
	}
	if s.have || len(s.cur) != 0 {
		t.Fatalf("expected Reset after overflow, have=%v cur=%d", s.have, len(s.cur))
	}
}

func TestNALTypeExtraction(t *testing.T) {
	// 0x65 = type 5 (IDR), 0x41 = type 1, 0x06 = SEI
	cases := []struct {
		header byte
		want   byte
	}{
		{0x65, 5}, {0x41, 1}, {0x06, 6}, {0x67, 7}, {0x68, 8},
		{0x0c, 12}, {0x09, 9}, {0x61, 1}, {0x21, 1},
	}
	for _, c := range cases {
		nal := makeNAL(c.header, []byte{0xFF, 0x00}, 4)
		if typ := nalType(nal, 4); typ != c.want {
			t.Errorf("header 0x%02x: type=%d, want %d", c.header, typ, c.want)
		}
	}
	// 起始码长度大于数据 → 0x1F
	if typ := nalType([]byte{0x00, 0x00}, 4); typ != 0x1F {
		t.Errorf("short NAL: type=%d, want 0x1F", typ)
	}
}
