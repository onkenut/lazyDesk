package capture

// VideoAssembler 把 NAL 流聚合为完整的 H.264 帧 (Access Unit)。
//
// 帧的边界由 slice NAL (type 1/5) 界定: 遇到新的 slice 时, 之前的
// AU 即为一帧。SPS/PPS/SEI 作为参数集/帧元数据追加到当前 AU,
// 使每个帧样本自包含 (客户端无需等待参数集即可解码)。
// AUD(9) 与 filler(12) 是纯开销, 直接丢弃。
//
// 所有 NAL 起始码统一规范化为 4 字节 (0x00000001):
// Pion 的 H264Payloader 仅按 3 字节起始码切分, 混合 3/4 字节流会导致
// NAL 边界误判、数据损坏 (浏览器解码 0 帧)。统一 4 字节后切分恒正确。
type VideoAssembler struct {
	au       []byte // 当前 AU (Annex B, 全 4 字节起始码)
	hasSlice bool
	sps      []byte // 最新 SPS (含 4 字节起始码), 供新客户端重发
	pps      []byte // 最新 PPS
}

// appendNAL 把 NAL 追加到 AU, 3 字节起始码规范化为 4 字节
func (a *VideoAssembler) appendNAL(dst []byte, nal NAL) []byte {
	if nal.CodeLen == 3 {
		dst = append(dst, 0x00)
	}
	return append(dst, nal.Data...)
}

// Feed 输入一个 NAL, 返回完整帧 (可能为 nil)。
// 返回的帧引用内部缓冲, 仅在下次 Feed 前有效。
func (a *VideoAssembler) Feed(nal NAL) []byte {
	switch {
	case nal.Type == 1 || nal.Type == 5: // slice / IDR slice — 帧边界
		if a.hasSlice {
			frame := a.au
			// 注意: 不能复用 a.au[:0], 否则会覆盖刚返回的 frame
			a.au = a.appendNAL(nil, nal)
			return frame
		}
		a.au = a.appendNAL(a.au, nal)
		a.hasSlice = true
		return nil

	case nal.Type == 7: // SPS
		a.sps = a.appendNAL(a.sps[:0], nal)
	case nal.Type == 8: // PPS
		a.pps = a.appendNAL(a.pps[:0], nal)
	case nal.Type == 9, nal.Type == 12: // AUD / filler — 丢弃
		return nil
	case nal.Type == 6: // SEI — 纯开销, 丢弃 (避免 SEI 参与 RTP 打包)
		return nil
	}
	// SPS/PPS/SEI 追加到当前 AU (若已有帧待发则成为其后缀, H.264 允许)
	a.au = a.appendNAL(a.au, nal)
	return nil
}

// Flush 返回未发出的残留 AU (流结束时调用), 无则返回 nil
func (a *VideoAssembler) Flush() []byte {
	if a.hasSlice && len(a.au) > 0 {
		frame := a.au
		a.au = nil
		a.hasSlice = false
		return frame
	}
	a.au = nil
	a.hasSlice = false
	return nil
}

// ParameterSets 返回缓存的 SPS+PPS 合并 AU (含 4 字节起始码), 供新订阅者立即解码
func (a *VideoAssembler) ParameterSets() []byte {
	if len(a.sps) == 0 || len(a.pps) == 0 {
		return nil
	}
	au := make([]byte, 0, len(a.sps)+len(a.pps))
	au = append(au, a.sps...)
	au = append(au, a.pps...)
	return au
}

// Reset 清空状态 (流重启时调用)
func (a *VideoAssembler) Reset() {
	a.au = nil
	a.hasSlice = false
	a.sps = nil
	a.pps = nil
}
