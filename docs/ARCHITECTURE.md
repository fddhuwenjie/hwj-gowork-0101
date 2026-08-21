# 架构设计

## 分层

| 层 | 目录 | 职责 |
| --- | --- | --- |
| HTTP | `internal/httpapi` | 路由、参数解析、统一 JSON 错误、访问日志、优雅关闭 |
| 服务 | `internal/service` | 业务编排、事务边界、幂等、规则调用、审计写入 |
| 领域 | `internal/domain` | 实体、状态机、跨实体规则，无框架依赖 |
| 持久化 | `internal/repository` | SQLite DDL、12 个实体的 CRUD、派生查询、TxManager |

入口 `cmd/server` 从 `PORT`、`DB_PATH` 环境变量读取配置，`internal/app` 完成组装。

## 持久化实体（12 个）

| 表 | 实体 | 说明 |
| --- | --- | --- |
| suppliers | 供方 | code 唯一（幂等键） |
| grade_rules | 牌号规则版本 | (grade, version_no) 唯一；draft/active/retired |
| material_lots | 来料批次 | lot_no 唯一；主流程状态机载体 |
| mill_certificates | 材质证明 | cert_no 唯一；核对通过方可判定 |
| sampling_plans | 取样计划 | 每批次一份；含留样位置 retain_location |
| samples | 取样样本 | (plan_id, sample_no) 唯一；retained 标记留样 |
| spectrum_reports | 光谱分析报告 | report_no 唯一；提交时按 active 规则固化结论 |
| conformity_conclusions | 符合性结论 | (lot_id, round) 唯一；初检/复验各一条 |
| retest_tasks | 复验任务 | 部分唯一索引：同批次仅一个未关闭任务 |
| dispositions | 处置单 | 让步接收/隔离处置统一模型，部分唯一索引 |
| audit_events | 审计记录 | 只增不改，与业务写入同事务 |
| background_jobs | 后台任务 | 持久化调度，指数退避重试，重启恢复 |

## 主流程状态机（8 个合法状态）

```
registered ──▶ sampled ──▶ analyzed ──▶ judged ──▶ accepted（终态）
    │            │            │           │  ▲
    │            │            │           ├──▶ rejected ──▶ retesting
    │            │            │           ├──▶ retesting ──┘
    ▼            ▼            ▼           ▼  （retesting→judged）
 quarantined ◀───────────────┴───────────┘
 quarantined ──▶ rejected / accepted（经处置单执行）
```

全部转换由 `LotStatus.MustTransitionTo` 校验，非法转换返回 409 `invalid_transition`。

## 三条流程的共享与制约

1. **日常作业**（DailyService）：登记→取样→分析→判定→接收。
2. **异常处置**（ExceptionService）：隔离可在任意非终态打断日常流程（R11 隔离批次
   不得直接接收）；让步接收仅对结论 fail 的批次开放（R10），批准后赋予接收资格（R12）；
   紧急放行（urgent_release）允许无证明批次被接收，并被派生查询二与后台扫描任务跟踪。
3. **复核归档**（ReviewService/ReportService）：异议复验要求已判定（R07）且使用留样（R08），
   复验结论必须与留样复验光谱一致（R05），覆盖初检结论必须共同决定（R09）；
   规则版本激活/废止改变后续判定的依据（R02）；审计与派生报表提供归档视图。

## 跨实体规则（12 条）

R01 供方必须存在；R02 牌号须有 active 规则版本；R03 证明牌号/炉批号与批次一致；
R04 无证明或未核对不得判定；R05 光谱结果必须落在牌号区间；R06 取样数量须与计划一致；
R07 复验须在判定之后；R08 复验必须使用留存原样；R09 覆盖初检须共同决定且不能同人；
R10 让步接收仅对不符合批次；R11 隔离批次不得直接接收；R12 接收须结论 pass 或有已批准让步。

## 事务性多实体用例

- 登记批次：批次 + 审计（供方/规则校验失败整体回滚）
- 提交光谱报告：报告 + 样本状态 + 审计
- 符合性判定：结论 + 批次状态/初检结果 + 审计
- 复验出具结论：结论 + 复验任务 + 批次状态/复验结果 + 审计
- 处置单执行：处置单 + 批次终态 + 审计

所有多实体写入经 `TxManager.InTx` 在单事务完成，任一失败整体回滚（有回滚测试覆盖）。

## 幂等与并发

- 创建类端点以自然键幂等：lot_no / cert_no / plan_no / report_no / (grade,version_no) /
  (plan_id,sample_no) / 同批次未关闭任务与处置单（部分唯一索引）。重复提交返回 200
  且 `replayed=true`。
- 状态流转类端点携带 `version` 乐观锁，冲突返回 409 `version_conflict`。
- SQLite 单连接串行化 + 事务内重读，保证并发下只有一个请求能完成流转（有竞态测试）。

## 后台任务

`cert_missing_scan` 任务落库后由调度器周期领取；可重试（瞬时）失败按 2^n 秒退避重试，
达到 max_attempts 后进入 failed 终态并保留最后一次错误；不可恢复错误（未知任务类型、
参数无法解析等重试也注定失败的情形）首次执行即进入 failed 终态，不再退避重试，避免
任务反复退回 pending 被下一轮重复执行。failed 任务可经 `POST /api/v1/jobs/{id}/retry`
人工重置为 pending；进程重启时将遗留 running 任务重新排队（重启恢复）。

## 派生查询与稳定排序

- `GET /api/v1/reports/retest-accepted`：初检 fail 且复验 pass 且已接收的批次与证明编号，
  按批次 id 升序。
- `GET /api/v1/reports/cert-missing-accepted`：各供方近 N 天无证明而先接收的批次数量，
  按数量降序、供方编码升序。
- 全部列表端点：排序键白名单校验 + 同键按 id 升序兜底，保证稳定顺序。
