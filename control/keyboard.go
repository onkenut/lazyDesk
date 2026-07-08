//go:build windows

package control

import (
	"fmt"
	"log"
	"syscall"
	"unsafe"
)

// KeyPress 单键按下/释放 (使用 Windows SendInput API)
func (h *Handler) KeyPress(key string, action string) {
	vk := keyNameToVK(key)
	if vk == 0 {
		return
	}

	// MapVirtualKey: VK -> scancode
	scanCode, _, _ := procMapVirtualKeyW.Call(uintptr(vk), 0)

	flags := uint32(KEYEVENTF_SCANCODE)
	if action == "up" {
		flags |= KEYEVENTF_KEYUP
	}

	inputs := [1]keyInput{}
	inputs[0].Type = INPUT_KEYBOARD
	inputs[0].Ki.WScan = uint16(scanCode)
	inputs[0].Ki.DwFlags = flags

	size := unsafe.Sizeof(keyInput{})
	procSendInput.Call(1, uintptr(unsafe.Pointer(&inputs[0])), size)
}

// KeyCombo 组合键 (使用 SendInput 依次按下所有键，然后释放)
func (h *Handler) KeyCombo(keys []string) {
	if len(keys) == 0 {
		return
	}

	// 收集所有键的 VK 和 scanCode
	type keyInfo struct {
		vk       uint16
		scanCode uint16
	}
	keyInfos := make([]keyInfo, 0, len(keys))
	for _, key := range keys {
		vk := keyNameToVK(key)
		if vk == 0 {
			return
		}
		scanCode, _, _ := procMapVirtualKeyW.Call(uintptr(vk), 0)
		keyInfos = append(keyInfos, keyInfo{vk: vk, scanCode: uint16(scanCode)})
	}

	size := unsafe.Sizeof(keyInput{})

	// 1. 按下所有键
	inputs := make([]keyInput, len(keyInfos))
	for i, ki := range keyInfos {
		inputs[i].Type = INPUT_KEYBOARD
		inputs[i].Ki.WScan = ki.scanCode
		inputs[i].Ki.DwFlags = KEYEVENTF_SCANCODE
	}
	procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), size)

	// 2. 释放所有键 (逆序)
	for i := len(keyInfos) - 1; i >= 0; i-- {
		inputs[i].Ki.DwFlags = KEYEVENTF_SCANCODE | KEYEVENTF_KEYUP
	}
	procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), size)
}

// TextInput 通过剪贴板粘贴输入文本 (支持中文)
func (h *Handler) TextInput(text string) {
	if err := setClipboard(text); err != nil {
		log.Printf("Failed to set clipboard: %v", err)
		return
	}
	// 模拟 Ctrl+V
	h.KeyCombo([]string{"ctrl", "v"})
}

// setClipboard 将文本写入 Windows 剪贴板
func setClipboard(text string) error {
	// 将 UTF-8 转为 UTF-16
	utf16, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	dataLen := len(utf16) * 2 // 每个 UTF-16 字符占 2 字节

	// 打开剪贴板
	ret, _, _ := procOpenClipboard.Call(0)
	if ret == 0 {
		return syscall.GetLastError()
	}
	defer procCloseClipboard.Call()

	// 清空剪贴板
	procEmptyClipboard.Call()

	// 分配全局内存
	hMem, _, _ := procGlobalAlloc.Call(0x0002, uintptr(dataLen)) // GMEM_MOVEABLE
	if hMem == 0 {
		return fmt.Errorf("GlobalAlloc failed")
	}

	// 锁定内存
	pMem, _, _ := procGlobalLock.Call(hMem)
	if pMem == 0 {
		procGlobalUnlock.Call(hMem)
		return fmt.Errorf("GlobalLock failed")
	}

	// 复制 UTF-16 数据到全局内存
	dst := unsafe.Slice((*byte)(unsafe.Pointer(pMem)), dataLen)
	src := unsafe.Slice((*byte)(unsafe.Pointer(&utf16[0])), dataLen)
	copy(dst, src)

	procGlobalUnlock.Call(hMem)

	// 设置剪贴板数据 (CF_UNICODETEXT = 13)
	ret, _, _ = procSetClipboardData.Call(13, hMem)
	if ret == 0 {
		return syscall.GetLastError()
	}

	return nil
}

// 单字符转 VK
func charToVK(ch byte) uint16 {
	procVkKeyScanW.Call(uintptr(ch))
	// VkKeyScan 返回的低字节是 VK 码
	// 简化处理：对于 ASCII 字母数字直接用 ASCII 值
	return uint16(ch)
}
