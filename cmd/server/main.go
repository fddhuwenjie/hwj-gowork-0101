// Command server 是金属来料材质符合性判定服务的进程入口。
// 配置通过环境变量 PORT 与 DB_PATH 注入。
package main

import (
	"os"

	"metalmics/internal/app"
)

func main() {
	os.Exit(app.Run())
}
