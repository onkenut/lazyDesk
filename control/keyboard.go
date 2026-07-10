//go:build windows

package control

import (
	"log"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// KeyPress 单键按下/释放/点击 (使用 Windows SendInput API)
func (h *Handler) KeyPress(key string, action string) {
	vk := keyNameToVK(key)
	if vk == 0 {
		return
	}

	scanCode, _, _ := procMapVirtualKeyW.Call(uintptr(vk), 0)

	// "tap" = 按下后立即释放 (用于功能键)
	if action == "tap" {
		h.sendKeyEvent(uint16(scanCode), KEYEVENTF_SCANCODE)
		h.sendKeyEvent(uint16(scanCode), KEYEVENTF_SCANCODE|KEYEVENTF_KEYUP)
		return
	}

	flags := uint32(KEYEVENTF_SCANCODE)
	if action == "up" {
		flags |= KEYEVENTF_KEYUP
	}
	h.sendKeyEvent(uint16(scanCode), flags)
}

// sendKeyEvent 发送单个键盘事件
func (h *Handler) sendKeyEvent(scanCode uint16, flags uint32) {
	inputs := [1]keyInput{}
	inputs[0].Type = INPUT_KEYBOARD
	inputs[0].Ki.WScan = scanCode
	inputs[0].Ki.DwFlags = flags
	size := unsafe.Sizeof(keyInput{})
	procSendInput.Call(1, uintptr(unsafe.Pointer(&inputs[0])), size)
}

// KeyCombo 组合键
func (h *Handler) KeyCombo(keys []string) {
	if len(keys) == 0 {
		return
	}

	type keyInfo struct{ vk, scanCode uint16 }
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
	inputs := make([]keyInput, len(keyInfos))
	for i, ki := range keyInfos {
		inputs[i].Type = INPUT_KEYBOARD
		inputs[i].Ki.WScan = ki.scanCode
		inputs[i].Ki.DwFlags = KEYEVENTF_SCANCODE
	}
	procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), size)

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
	h.KeyCombo([]string{"ctrl", "v"})
}

// setClipboard 将文本写入 Windows 剪贴板，使用 PowerShell。
// 通过 stdin 传递文本，避免命令行参数解析问题 (空格/引号/特殊字符)。
func setClipboard(text string) error {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"$input | Set-Clipboard")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
