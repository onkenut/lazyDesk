package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/onkenut/lazyDesk/config"
	"github.com/onkenut/lazyDesk/control"
	"github.com/onkenut/lazyDesk/ffmpeg"
	"github.com/onkenut/lazyDesk/webrtc"
	"github.com/onkenut/lazyDesk/websocket"
)

//go:embed web/*
var webFS embed.FS

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// 1. 加载配置
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 2. 初始化各模块
	capture := ffmpeg.NewCapture(cfg)
	rtcManager := webrtc.NewManager(cfg)
	cmdHandler := control.NewHandler(cfg)

	// 将 WebRTC Manager 注册为 capture 的视频输出
	capture.SetVideoTrack(rtcManager)

	// 3. 创建 WebSocket 信令处理器
	wsHandler := websocket.NewHandler(rtcManager, capture, cmdHandler)

	// 4. 注册路由
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsHandler.HandleWebSocket)

	// 托管前端静态文件 (从 embed.FS)
	webSubFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("Failed to create web sub-fs: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(webSubFS)))

	// 5. 启动 HTTP 服务
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("lazyDesk server starting on http://%s", addr)
	log.Printf("Open http://localhost:%d on your tablet browser", cfg.Server.Port)

	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// 6. 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	capture.Stop()
	rtcManager.Close()
}
