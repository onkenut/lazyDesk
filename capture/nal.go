package capture

// H.264 Annex B 字节流解析器。
// 从 ffmpeg 的原始 h264 输出中切分 NAL 单元，支持起始码被任意 chunk 边界截断。

// maxNALSize 单个 NAL 的缓冲上限 (防御异常流, 正常单帧远小于此值)
const maxNALSize = 32 * 1024 * 1024

// NAL 是一个完整的 Annex B NAL 单元 (Data 含起始码)。
// 注意: Data 只在 emit 回调期间有效, 需要保留时须拷贝。
type NAL struct {
	Data    []byte // 含起始码的完整 NAL
	Type    byte   // NAL 类型 (低 5 位)
	CodeLen int    // 起始码长度 (3 或 4)
}

// NALStream 增量解析 H.264 Annex B 字节流。
// cur 累积"起始码 + 已到达的载荷"，遇到下一个起始码时切出一个完整 NAL。
type NALStream struct {
	cur     []byte // 当前 NAL (起始码 + 部分/全部载荷), 可能被流末尾截断
	codeLen int    // 当前 NAL 的起始码长度
	have    bool   // 是否已进入 NAL 内容
}

// findStartCode 滑动窗口查找起始码, 返回位置和长度 (3 或 4)。
// 4 字节起始码 (0x00000001) 优先于 3 字节 (0x000001)。
func findStartCode(data []byte, off int) (pos int, codeLen int) {
	for i := off; i < len(data)-2; i++ {
		if data[i] != 0 || data[i+1] != 0 {
			continue
		}
		if data[i+2] == 1 {
			return i, 3
		}
		if i+3 < len(data) && data[i+2] == 0 && data[i+3] == 1 {
			return i, 4
		}
	}
	return -1, 0
}

// Reset 清空解析状态 (流异常/重启时调用)
func (s *NALStream) Reset() {
	s.cur = nil
	s.have = false
	s.codeLen = 0
}

// Feed 喂入一段字节流, 每切出一个完整 NAL 就调用一次 emit。
func (s *NALStream) Feed(data []byte, emit func(NAL)) {
	// 上一轮残留的 NAL 拼接在本轮数据前 (以起始码开头)
	if len(s.cur) > 0 {
		data = append(s.cur, data...)
		s.cur = nil
	}

	// lastStart 记录当前 NAL 的起始码位置
	lastStart := -1
	for i := 0; i < len(data); {
		pos, clen := findStartCode(data, i)
		if pos < 0 {
			// 剩余字节进累积缓冲 (可能是 NAL 载荷或截断的起始码)
			if len(data)-i > 0 {
				if len(s.cur)+len(data)-i > maxNALSize {
					s.Reset()
					return
				}
				s.cur = append(s.cur, data[i:]...)
			}
			return
		}
		if s.have && lastStart >= 0 {
			// data[lastStart:pos] 是一个完整 NAL (起始码 + 载荷)
			nal := data[lastStart:pos]
			emit(NAL{
				Data:    nal,
				Type:    nalType(nal, s.codeLen),
				CodeLen: s.codeLen,
			})
		}
		// 开启新 NAL: 拷贝起始码到独立缓冲 (不引用调用方数据)
		s.cur = append(s.cur[:0], data[pos:pos+clen]...)
		s.codeLen = clen
		s.have = true
		lastStart = pos
		i = pos + clen
	}
}

// Flush 取回流末尾未切出的最后一个 NAL (流结束时调用)。
// 若流在 NAL 中途被截断, 仍返回当前累积内容 (解码器可容忍)。
func (s *NALStream) Flush() (NAL, bool) {
	if s.have && len(s.cur) > 0 {
		nal := s.cur
		s.cur = nil
		s.have = false
		return NAL{Data: nal, Type: nalType(nal, s.codeLen), CodeLen: s.codeLen}, true
	}
	s.cur = nil
	s.have = false
	return NAL{}, false
}

// nalType 提取 NAL 类型 (低 5 位)。数据不足时返回 0x1F (保留)。
func nalType(nal []byte, codeLen int) byte {
	if codeLen >= len(nal) {
		return 0x1F
	}
	return nal[codeLen] & 0x1F
}
