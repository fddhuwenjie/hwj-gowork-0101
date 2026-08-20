// Package config 负责从环境变量读取服务配置。
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config 是服务运行配置。
type Config struct {
	Port   int    // HTTP 监听端口
	DBPath string // SQLite 数据库文件路径
}

// 环境变量名。
const (
	EnvPort   = "PORT"
	EnvDBPath = "DB_PATH"
)

// Load 从环境变量加载配置。PORT 缺省 8080，DB_PATH 必填校验由调用方决定，
// 此处提供缺省值 data/metalmics.db 便于本地直接启动。
func Load() (Config, error) {
	cfg := Config{Port: 8080, DBPath: "data/metalmics.db"}
	if v := os.Getenv(EnvPort); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil || port <= 0 || port > 65535 {
			return cfg, fmt.Errorf("环境变量 %s 非法: %q", EnvPort, v)
		}
		cfg.Port = port
	}
	if v := os.Getenv(EnvDBPath); v != "" {
		cfg.DBPath = v
	}
	return cfg, nil
}
