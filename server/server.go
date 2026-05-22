package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"agr/config"
	"agr/proxy"
	"agr/router"
)

// Server HTTP 服务器
type Server struct {
	httpServer *http.Server
	cfg        *config.Config
}

// New 创建服务器实例
func New(cfg *config.Config) *Server {
	r := router.New(cfg)
	p := proxy.New(cfg, r)

	mux := http.NewServeMux()

	// 一期核心端点
	mux.HandleFunc("/v1/messages", p.HandleMessages)
	mux.HandleFunc("/v1/messages/count_tokens", p.HandleMessagesCountTokens)
	mux.HandleFunc("/v1/responses", p.HandleResponses)

	// 二期 Ollama 端点，一期返回 501
	mux.HandleFunc("/api/chat", p.HandleNotImplemented)
	mux.HandleFunc("/api/generate", p.HandleNotImplemented)
	mux.HandleFunc("/api/tags", p.HandleNotImplemented)

	// 健康检查
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	return &Server{
		httpServer: &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
			Handler: mux,
		},
		cfg: cfg,
	}
}

// Start 启动 HTTP 服务
func (s *Server) Start() error {
	slog.Info("agr 网关启动", "port", s.cfg.Server.Port)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP 服务启动失败: %w", err)
	}
	return nil
}

// Shutdown 优雅停机
func (s *Server) Shutdown() error {
	slog.Info("开始优雅停机...")
	// 给正在处理的流式请求最多 30 秒完成
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("优雅停机失败: %w", err)
	}
	slog.Info("服务已停止")
	return nil
}
