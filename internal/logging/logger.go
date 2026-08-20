// Package logging 提供结构化日志初始化与请求日志辅助。
package logging

import (
	"log/slog"
	"os"
)

// New 创建输出到 stdout 的 JSON 结构化日志器。
func New() *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(handler)
}
