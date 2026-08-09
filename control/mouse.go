//go:build windows

package control

import (
	"log"
	"syscall"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procSetCursorPos     = user32.NewProc("SetCursorPos")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procMouseEvent       = user32.NewProc("mouse_event")
	procSendInput        = user32.NewProc("SendInput")
	procMapVirtualKeyW   = user32.NewProc("MapVirtualKeyW")
	procVkKeyScanW       = user32.NewProc("VkKeyScanW")
)

const (
	MOUSEEVENTF_MOVE       = 0x0001
	MOUSEEVENTF_LEFTDOWN   = 0x0002
	MOUSEEVENTF_LEFTUP     = 0x0004
	MOUSEEVENTF_RIGHTDOWN  = 0x0008
	MOUSEEVENTF_RIGHTUP    = 0x0010
	MOUSEEVENTF_MIDDLEDOWN = 0x0020
	MOUSEEVENTF_MIDDLEUP   = 0x0040
	MOUSEEVENTF_WHEEL      = 0x0800

	WHEEL_DELTA = 120

	SM_CXSCREEN = 0
	SM_CYSCREEN = 1

	KEYEVENTF_KEYUP    = 0x0002
	KEYEVENTF_SCANCODE = 0x0008

	INPUT_KEYBOARD = 1
)

// keyInput Windows INPUT 结构体 (键盘)
type keyInput struct {
	Type      uint32
	Ki        keyboardInput
	Padding   [8]byte
}

type keyboardInput struct {
	WVk         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

// MouseMove 移动鼠标到绝对坐标。
// xRatio/yRatio 为 0..1 归一化坐标 (来自触控层), screenW/screenH 为实际采集分辨率
// (来自 ffmpeg 输出, 保证触摸点与画面精确对齐, 不受 DPI 缩放影响)。
func (h *Handler) MouseMove(xRatio, yRatio float64, screenW, screenH int) {
	if screenW <= 0 || screenH <= 0 {
		// 采集未就绪时回退到系统度量
		w, _, _ := procGetSystemMetrics.Call(uintptr(SM_CXSCREEN))
		h, _, _ := procGetSystemMetrics.Call(uintptr(SM_CYSCREEN))
		screenW = int(w)
		screenH = int(h)
	}

	x := int(xRatio * float64(screenW))
	y := int(yRatio * float64(screenH))

	procSetCursorPos.Call(uintptr(x), uintptr(y))
}

// MouseClick 鼠标点击
func (h *Handler) MouseClick(button string, action string) {
	var downFlags, upFlags uintptr

	switch button {
	case "left":
		downFlags = MOUSEEVENTF_LEFTDOWN
		upFlags = MOUSEEVENTF_LEFTUP
	case "right":
		downFlags = MOUSEEVENTF_RIGHTDOWN
		upFlags = MOUSEEVENTF_RIGHTUP
	case "middle":
		downFlags = MOUSEEVENTF_MIDDLEDOWN
		upFlags = MOUSEEVENTF_MIDDLEUP
	default:
		log.Printf("Unknown mouse button: %s", button)
		return
	}

	switch action {
	case "click":
		procMouseEvent.Call(downFlags, 0, 0, 0, 0)
		procMouseEvent.Call(upFlags, 0, 0, 0, 0)
	case "double":
		procMouseEvent.Call(downFlags, 0, 0, 0, 0)
		procMouseEvent.Call(upFlags, 0, 0, 0, 0)
		procMouseEvent.Call(downFlags, 0, 0, 0, 0)
		procMouseEvent.Call(upFlags, 0, 0, 0, 0)
	case "down":
		procMouseEvent.Call(downFlags, 0, 0, 0, 0)
	case "up":
		procMouseEvent.Call(upFlags, 0, 0, 0, 0)
	}
}

// MouseScroll 鼠标滚轮 (deltaY 为滚轮步数，正=上滚，负=下滚)
func (h *Handler) MouseScroll(deltaY int) {
	if deltaY == 0 {
		return
	}

	// 每次 mouse_event 发送一个滚轮刻度 (WHEEL_DELTA = 120)
	// 多次调用以支持滚动多格
	var flags uintptr = MOUSEEVENTF_WHEEL
	var dwData uintptr

	steps := deltaY
	if steps < 0 {
		steps = -steps
	}
	if steps > 100 {
		steps = 100 // B5: 防止大值冻结系统
	}

	for i := 0; i < steps; i++ {
		if deltaY > 0 {
			dwData = WHEEL_DELTA
		} else {
			dwData = uintptr(^uint32(WHEEL_DELTA - 1)) // 补码表示 -120
		}
		procMouseEvent.Call(flags, 0, 0, dwData, 0)
	}
}

// keyNameToVK 将字符串键名转换为 Windows 虚拟键码
func keyNameToVK(key string) uint16 {
	keyMap := map[string]uint16{
		"backspace": 0x08,
		"tab":       0x09,
		"enter":     0x0D,
		"shift":     0x10,
		"ctrl":      0x11,
		"alt":       0x12,
		"escape":    0x1B,
		"space":     0x20,
		"pageup":    0x21,
		"pagedown":  0x22,
		"end":       0x23,
		"home":      0x24,
		"left":      0x25,
		"up":        0x26,
		"right":     0x27,
		"down":      0x28,
		"printscreen": 0x2C,
		"insert":    0x2D,
		"delete":    0x2E,
		"cmd":       0x5B, // 左 Win
		"win":       0x5B,
		"f1":        0x70,
		"f2":        0x71,
		"f3":        0x72,
		"f4":        0x73,
		"f5":        0x74,
		"f6":        0x75,
		"f7":        0x76,
		"f8":        0x77,
		"f9":        0x78,
		"f10":       0x79,
		"f11":       0x7A,
		"f12":       0x7B,
		"numlock":   0x90,
		"scrolllock": 0x91,
	}

	if vk, ok := keyMap[key]; ok {
		return vk
	}

	// 单个字母/数字字符 — 必须大写，因为 Windows VK 码与大写字母一致
	if len(key) == 1 {
		ch := key[0]
		if ch >= 'a' && ch <= 'z' {
			ch -= 32 // 转大写
		}
		return uint16(ch)
	}

	log.Printf("Unknown key: %s", key)
	return 0
}
