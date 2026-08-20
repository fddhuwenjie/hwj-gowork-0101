# 本质评测环境说明

## 项目

- 项目编号：`hwj-gowork-0101`
- 项目名称：金属来料材质符合性判定服务
- 项目说明：基于 Go HTTP JSON 与 SQLite 文件持久化，管理来料批次、材质证明、光谱分析、取样复验、异常处置和符合性结论

## 固定环境

- Go toolchain：`go1.26.5`
- go.mod language version：`go 1.21`
- GOTOOLCHAIN：`local`
- 支持平台：`linux/amd64`、`linux/arm64`
- Docker 基础镜像：`golang:1.26.5-bookworm`
- Docker manifest：`golang@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd`

## 构建

`benzhi.Dockerfile` 与项目 `Dockerfile` 逐字一致。镜像保留完整源码、测试、vendor 依赖和 Go 工具链，构建阶段使用 `-mod=vendor` 离线执行 `go build ./...`，不访问模块代理。

```bash
BUILDER=desktop-linux ./build_benzhi_docker.sh hwj-gowork-0101-benzhi:amd64 linux/amd64
BUILDER=desktop-linux ./build_benzhi_docker.sh hwj-gowork-0101-benzhi:arm64 linux/arm64
```

未配置 `desktop-linux` builder 时，可省略 `BUILDER=desktop-linux`，脚本默认使用 `default`。

## 运行服务

容器默认启动 `go run ./cmd/server`，无需手动进入容器。服务监听 `8080`，SQLite 数据库可通过命名卷持久化：

```bash
docker run --rm --platform linux/amd64 \
  -p 8080:8080 \
  -v metalmics-data:/data \
  -e PORT=8080 \
  -e DB_PATH=/data/metalmics.db \
  hwj-gowork-0101-benzhi:amd64

curl http://localhost:8080/healthz
```

服务无外部依赖。若要验证断网运行，可在 `docker run` 中加入 `--network none`；此时宿主机无法通过端口访问服务，应在容器内部或通过日志完成检查。

## 容器内验证

通过 `bash` 覆盖默认启动命令即可进入容器：

```bash
docker run --rm --platform linux/amd64 --network none \
  hwj-gowork-0101-benzhi:amd64 bash
```

进入后可执行：

```bash
go version
go env GOTOOLCHAIN GOPROXY GOSUMDB GOMODCACHE GOCACHE
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
```

本机 Go 版本与固定容器工具链不一致时，以容器结果为准。
