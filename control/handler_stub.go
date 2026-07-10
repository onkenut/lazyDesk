//go:build !windows

package control

import (
	"log"

	"github.com/onkenut/lazyDesk/config"
)

// Handler 控制指令处理器 (非 Windows stub — 仅编译占位，运行时不可用)
type Handler struct {
	cfg *config.Config
}

// NewHandler 创建控制处理器
func NewHandler(cfg *config.Config) *Handler {
	return &Handler{cfg: cfg}
}

func (h *Handler) MouseMove(xRatio, yRatio float64) {
	log.Println("control: MouseMove not supported on this platform")
}

func (h *Handler) MouseClick(button, action string) {
	log.Println("control: MouseClick not supported on this platform")
}

func (h *Handler) MouseScroll(deltaY int) {
	log.Println("control: MouseScroll not supported on this platform")
}

func (h *Handler) MouseDrag(startX, startY, endX, endY float64) {
	log.Println("control: MouseDrag not supported on this platform")
}

func (h *Handler) KeyPress(key, action string) {
	log.Println("control: KeyPress not supported on this platform")
}

func (h *Handler) KeyCombo(keys []string) {
	log.Println("control: KeyCombo not supported on this platform")
}

func (h *Handler) TextInput(text string) {
	log.Println("control: TextInput not supported on this platform")
}

func (h *Handler) PowerAction(action string) {
	log.Println("control: PowerAction not supported on this platform")
}
