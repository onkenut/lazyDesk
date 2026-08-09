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

	"github.com/onkenut/lazyDesk/capture"
	"github.com/onkenut/lazyDesk/config"
	"github.com/onkenut/lazyDesk/control"
	"github.com/onkenut/lazyDesk/server"
	"github.com/onkenut/lazyDesk/webrtc"
)

//go:embed web
var webFS embed.FS

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("lazyDesk %s 启动中...", server.Version)

	// 1. 加载配置
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 2. 初始化各模块
	cap := capture.New(cfg, nil)
	rtcFactory, err := webrtc.NewFactory(cfg)
	if err != nil {
		log.Fatalf("初始化 WebRTC 失败: %v", err)
	}
	cmdHandler := control.NewHandler(cfg)
	srv := server.NewServer(cfg, cap, rtcFactory, cmdHandler)

	// 采集事件 → 日志 + 广播给客户端
	cap.SetOnEvent(func(ev capture.Event) {
		log.Printf("[capture] 事件 %s: %s", ev.Type, ev.Message)
		srv.BroadcastEvent(ev)
	})

	// 3. 注册路由
	webSubFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("加载前端资源失败: %v", err)
	}
	handler := srv.Handler(webSubFS)

	// 4. 启动 HTTP 服务
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("lazyDesk 服务已启动: http://%s", addr)
	log.Printf("平板浏览器打开 http://<本机IP>:%d", cfg.Server.Port)

	go func() {
		if err := http.ListenAndServe(addr, handler); err != nil {
			log.Fatalf("HTTP 服务错误: %v", err)
		}
	}()

	// 5. 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("正在退出...")
	cap.Close()
}
