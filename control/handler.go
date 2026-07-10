//go:build windows

package control

import (
	"github.com/onkenut/lazyDesk/config"
)

// Handler 控制指令处理器，封装鼠标/键盘/电源操作
type Handler struct {
	cfg *config.Config
}

// NewHandler 创建控制处理器
func NewHandler(cfg *config.Config) *Handler {
	return &Handler{cfg: cfg}
}
