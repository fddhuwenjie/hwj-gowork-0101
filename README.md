# 金属来料材质符合性判定服务（metalmics）

管理金属来料批次、材质证明、光谱分析报告、取样样本与符合性结论的 HTTP JSON 服务。
统一业务模型覆盖：供方、炉批号、牌号规则版本、取样计划、留样位置、复验任务、
让步接收、隔离处置、审计记录与可持久化后台任务。

## 核心能力

- 完整业务链：来料登记 → 取样制样 → 光谱分析 → 材质证明核对 → 符合性判定 → 异议复验 → 接收确认
- 三条共享数据且相互制约的流程：
  - **日常作业**：登记、取样、分析、判定、接收
  - **异常处置**：隔离处置、让步接收、紧急放行、材质证明缺失扫描
  - **复核归档**：规则版本激活/废止、异议复验（留样复验、共同决定覆盖）、审计检索、派生报表
- 硬规则（违反返回 422 及规则编码）：
  - 无材质证明（或未核对通过）不得判定（R04）
  - 光谱分析结果必须与牌号范围一致，否则判定 fail（R05）
  - 异议必须保留原样复验（R08）
  - 复验结论覆盖初检结论必须共同决定（R09）
- 工程能力：幂等提交（自然键去重 + `replayed` 标记）、乐观锁版本冲突（409）、
  批量接收部分失败、持久化后台任务（指数退避重试、重启恢复）、审计追踪、
  分页/过滤/稳定排序、统一 JSON 错误、结构化日志、优雅关闭

## 快速开始

要求：Go toolchain go1.26.5（`GOTOOLCHAIN=local`），所有 `go.mod` 语言版本固定为 `go 1.21`。

```bash
export GOTOOLCHAIN=local
go build ./...
PORT=8080 DB_PATH=data/metalmics.db go run ./cmd/server
curl http://localhost:8080/healthz
```

服务完全离线运行，无任何外部网络依赖（SQLite 为纯 Go 驱动 modernc.org/sqlite）。

### 环境变量

| 变量 | 缺省 | 说明 |
| --- | --- | --- |
| `PORT` | `8080` | HTTP 监听端口 |
| `DB_PATH` | `data/metalmics.db` | SQLite 数据库文件路径，进程关闭后可用同一文件重启恢复 |

## 固定评测环境与 Docker

仓库包含 `Dockerfile`、`build_docker.sh`、`benzhi.Dockerfile`、
`build_benzhi_docker.sh`、`BENZHI_README.md` 和 `.go-annotation-env.json`。
两个 Dockerfile 逐字一致，均使用固定的官方 Go `1.26.5` 多架构镜像摘要；
最终镜像保留完整源码、测试、vendor 依赖和 Go 工具链，构建阶段只执行
`go build ./...`，并通过仓库内 `vendor/` 完全离线解析依赖；启动容器时默认运行
`go run ./cmd/server`。

```bash
# 分别构建两个目标平台
BUILDER=desktop-linux ./build_docker.sh hwj-gowork-0101:amd64 linux/amd64
BUILDER=desktop-linux ./build_docker.sh hwj-gowork-0101:arm64 linux/arm64

# 甲方固定评测入口
BUILDER=desktop-linux ./build_benzhi_docker.sh hwj-gowork-0101-benzhi:amd64 linux/amd64

# 启动业务服务
docker run --rm --platform linux/amd64 \
  -p 8080:8080 -v metalmics-data:/data \
  -e DB_PATH=/data/metalmics.db \
  hwj-gowork-0101-benzhi:amd64
curl http://localhost:8080/healthz

# 断网检查工具链和源码，断网时不从宿主机访问服务端口
docker run --rm --platform linux/amd64 --network none \
  hwj-gowork-0101-benzhi:amd64 bash
```

容器内可执行 `go test ./... -count=1`、`go test -race ./... -count=1`、
`go vet ./...` 和 `go build ./...`。业务服务也可以使用 compose 启动：

```bash
docker compose --env-file .env.example up --build
curl http://localhost:8080/healthz
```

评测文件和固定环境字段详见 [BENZHI_README.md](BENZHI_README.md)。

## 测试与质量门禁

```bash
export GOTOOLCHAIN=local
go test ./... -count=1        # 单元/集成测试（真实临时 SQLite 文件）
go test -race ./... -count=1  # 竞态检测（含并发接收用例）
go vet ./...
go build ./...
```

测试覆盖：领域状态机（8 状态两两组合）、服务流程、SQLite Repository、HTTP Handler、
事务回滚、非法状态转换、并发接收竞态、数据库关闭后重开恢复、边界失败路径。

## 接口一览（共 40 个端点，详见 docs/API.md）

- 供方：`POST/GET /api/v1/suppliers`、`GET /api/v1/suppliers/{id}`
- 牌号规则版本：`POST/GET /api/v1/grade-rules`、`POST .../{id}/activate|retire`
- 来料批次主流程：`POST/GET /api/v1/lots`、`GET .../{id}|detail|conclusions`、
  `POST .../sampling-plans|sampling-complete|analyze|judge|accept|reject`、
  `POST /api/v1/lots/batch-accept`
- 光谱分析：`POST/GET /api/v1/samples/{id}/spectrum-reports`
- 材质证明：`POST/GET /api/v1/lots/{id}/certificates`、`POST /api/v1/certificates/{id}/verify`
- 异议复验：`POST /api/v1/lots/{id}/retests`、`GET /api/v1/retests`、
  `POST /api/v1/retests/{id}/approve|reject|conclude`
- 异常处置：`POST /api/v1/lots/{id}/dispositions`、`GET /api/v1/dispositions`、
  `POST /api/v1/dispositions/{id}/approve|reject|execute`
- 派生查询：`GET /api/v1/reports/retest-accepted`（初检不符合但复验仍接收的批次与证明编号）、
  `GET /api/v1/reports/cert-missing-accepted?days=30`（各供方近期证明缺失而先接收的批次数量）
- 审计与后台任务：`GET /api/v1/audit-events`、`POST/GET /api/v1/jobs`、`POST /api/v1/jobs/{id}/retry`
- 健康检查：`GET /healthz`

## 项目结构

```
cmd/server            进程入口（PORT/DB_PATH）
internal/config       环境变量配置
internal/logging      结构化日志
internal/domain       12 个实体、8 状态状态机、12 条跨实体规则
internal/repository   SQLite schema 与 12 个实体的持久化、事务管理
internal/service      三条业务流程、幂等、批量、后台任务、派生查询
internal/httpapi      路由、处理器、统一错误、分页解析、优雅关闭
internal/app          组装、后台任务调度器、信号处理
docker/               容器入口脚本
docs/                 架构与接口文档
```

更多设计细节见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) 与 [docs/API.md](docs/API.md)。
