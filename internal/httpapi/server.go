package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Server 包装 http.Server，提供带超时的优雅关闭。
type Server struct {
	srv *http.Server
}

// NewServer 构造 HTTP 服务。
func NewServer(addr string, handler http.Handler) *Server {
	return &Server{
		srv: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
	}
}

// ListenAndServe 启动监听，返回错误供主协程处理。
func (s *Server) ListenAndServe() error {
	if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP 服务异常退出: %w", err)
	}
	return nil
}

// Shutdown 在给定超时内优雅关闭：先停止接收新连接，再等待在途请求完成。
func (s *Server) Shutdown(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.srv.Shutdown(ctx)
}
