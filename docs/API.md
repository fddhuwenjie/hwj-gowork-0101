# HTTP JSON 接口说明

基础地址：`http://localhost:8080`。所有写操作可通过请求头 `X-Actor` 指定操作人（缺省 `system`）。

## 通用约定

### 统一错误

```json
{"error":{"code":"rule_violation","message":"无材质证明，不得判定","rule":"R04_cert_required_for_judgment"}}
```

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | validation | 参数非法 |
| 404 | not_found | 资源不存在 |
| 409 | version_conflict / invalid_transition / duplicate | 版本冲突 / 非法状态转换 / 唯一键冲突 |
| 422 | rule_violation | 跨实体业务规则违反（带 rule 编码） |
| 500 | internal | 服务内部错误 |

### 分页与排序

列表端点支持 `page`（默认 1）、`page_size`（默认 20，上限 100）、`sort`、`order(asc|desc)`；
排序字段经白名单校验，响应为 `{"items":[],"total":0,"page":1,"page_size":20}`。

### 幂等

创建类端点重复提交返回 `200` 且 `{"replayed":true}`，不会重复落库。

### 乐观锁

状态流转类端点在请求体携带 `version`（批次/任务/处置单/证明当前版本），冲突返回 409。

## 端点明细

### 健康检查

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/healthz` | 进程与数据库健康检查 |

### 供方

| 方法 | 路径 | 请求体/参数 | 说明 |
| --- | --- | --- | --- |
| POST | `/api/v1/suppliers` | `{code,name,contact}` | 登记供方（code 幂等） |
| GET | `/api/v1/suppliers` | `code_prefix,name_like` + 分页 | 供方列表 |
| GET | `/api/v1/suppliers/{id}` | — | 供方详情 |

### 牌号规则版本

| 方法 | 路径 | 请求体/参数 | 说明 |
| --- | --- | --- | --- |
| POST | `/api/v1/grade-rules` | `{grade,version_no,elements:[{element,min,max}],remark}` | 创建 draft 版本（grade+version_no 幂等） |
| GET | `/api/v1/grade-rules` | `grade,status` + 分页 | 规则列表 |
| POST | `/api/v1/grade-rules/{id}/activate` | `{version}` | 激活（同事务废止旧 active 版本） |
| POST | `/api/v1/grade-rules/{id}/retire` | `{version}` | 废止 |

### 来料批次与主流程

| 方法 | 路径 | 请求体/参数 | 说明 |
| --- | --- | --- | --- |
| POST | `/api/v1/lots` | `{lot_no,supplier_id,heat_no,grade,quantity,received_at?}` | 来料登记（lot_no 幂等；R01/R02） |
| GET | `/api/v1/lots` | `status,supplier_id,grade,lot_no_prefix,received_after,received_before` + 分页 | 批次列表 |
| GET | `/api/v1/lots/{id}` | — | 批次详情 |
| GET | `/api/v1/lots/{id}/detail` | — | 批次全链路聚合（计划/样本/证明/报告/结论） |
| GET | `/api/v1/lots/{id}/conclusions` | — | 批次结论列表 |
| POST | `/api/v1/lots/{id}/sampling-plans` | `{plan_no,required_count,retain_location}` | 制定取样计划（含留样位置） |
| POST | `/api/v1/sampling-plans/{id}/samples` | `{samples:[{sample_no,retained}]}` | 批量登记样本（原子；单样本幂等跳过） |
| POST | `/api/v1/lots/{id}/sampling-complete` | `{version}` | 取样完成（R06） |
| POST | `/api/v1/lots/{id}/analyze` | `{version}` | 完成光谱分析 |
| POST | `/api/v1/lots/{id}/judge` | `{version,decided_by,reason}` | 符合性判定（R04/R05） |
| POST | `/api/v1/lots/{id}/accept` | `{version}` | 接收确认（R11/R12） |
| POST | `/api/v1/lots/{id}/reject` | `{version,reason}` | 拒收确认 |
| POST | `/api/v1/lots/batch-accept` | `{lot_ids:[...]}` | 批量接收（逐项独立事务，部分失败） |

### 光谱分析

| 方法 | 路径 | 请求体/参数 | 说明 |
| --- | --- | --- | --- |
| POST | `/api/v1/samples/{id}/spectrum-reports` | `{report_no,readings:[{element,value}],analyzer}` | 提交报告（report_no 幂等；结论按 active 规则固化） |
| GET | `/api/v1/samples/{id}/spectrum-reports` | — | 样本报告列表（初检+复验） |

### 材质证明

| 方法 | 路径 | 请求体/参数 | 说明 |
| --- | --- | --- | --- |
| POST | `/api/v1/lots/{id}/certificates` | `{cert_no,grade,heat_no,elements?,issued_at}` | 登记证明（cert_no 幂等） |
| GET | `/api/v1/lots/{id}/certificates` | — | 批次证明列表 |
| POST | `/api/v1/certificates/{id}/verify` | `{version}` | 证明核对（R03） |

### 异议复验

| 方法 | 路径 | 请求体/参数 | 说明 |
| --- | --- | --- | --- |
| POST | `/api/v1/lots/{id}/retests` | `{sample_id,reason}` | 申请复验（R07/R08；同批次单任务幂等） |
| GET | `/api/v1/retests` | `lot_id,status` + 分页 | 复验任务列表 |
| POST | `/api/v1/retests/{id}/approve` | `{version}` | 批准（不能同申请人） |
| POST | `/api/v1/retests/{id}/reject` | `{version}` | 驳回（批次回退 judged） |
| POST | `/api/v1/retests/{id}/conclude` | `{version,result,decided_by,co_decided_by?,reason}` | 出具复验结论（覆盖初检须共同决定 R09） |

### 异常处置（让步接收 / 隔离处置）

| 方法 | 路径 | 请求体/参数 | 说明 |
| --- | --- | --- | --- |
| POST | `/api/v1/lots/{id}/dispositions` | `{type:quarantine\|concession,reason}` | 提出处置单（隔离即时生效；让步仅对 fail 批次 R10） |
| GET | `/api/v1/dispositions` | `lot_id,type,status` + 分页 | 处置单列表 |
| POST | `/api/v1/dispositions/{id}/approve` | `{version}` | 批准（不能同提出人） |
| POST | `/api/v1/dispositions/{id}/reject` | `{version}` | 驳回 |
| POST | `/api/v1/dispositions/{id}/execute` | `{version,resolution}` | 执行：`scrap`/`return_to_supplier`→rejected；`concession_accept`→accepted（需已批准让步单）；`urgent_release`→紧急放行 accepted |

### 派生查询

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/reports/retest-accepted` | 初检不符合但复验仍接收的批次与材质证明编号（分页，按批次 id 升序） |
| GET | `/api/v1/reports/cert-missing-accepted?days=30` | 各供方近期证明缺失而先接收的批次数量（数量降序、编码升序） |

### 审计与后台任务

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/audit-events` | 审计检索：`entity,entity_id,actor,since` + 分页 |
| POST | `/api/v1/jobs` | 入队任务 `{type:"cert_missing_scan",payload:{days:30},max_attempts?}` |
| GET | `/api/v1/jobs` | 任务列表：`status,type` + 分页 |
| POST | `/api/v1/jobs/{id}/retry` | 人工重试 failed 任务 |

## 示例：主流程

```bash
B=http://localhost:8080
curl -X POST $B/api/v1/suppliers -d '{"code":"S001","name":"华中特钢"}'
curl -X POST $B/api/v1/grade-rules -d '{"grade":"304","version_no":1,"elements":[{"element":"C","min":0,"max":0.08}]}'
curl -X POST $B/api/v1/grade-rules/1/activate -d '{}'
curl -X POST $B/api/v1/lots -d '{"lot_no":"L1","supplier_id":1,"heat_no":"H1","grade":"304","quantity":10}'
curl -X POST $B/api/v1/lots/1/sampling-plans -d '{"plan_no":"P1","required_count":1,"retain_location":"A-01"}'
curl -X POST $B/api/v1/sampling-plans/1/samples -d '{"samples":[{"sample_no":"S-1","retained":true}]}'
curl -X POST $B/api/v1/lots/1/sampling-complete -d '{}'
curl -X POST $B/api/v1/samples/1/spectrum-reports -d '{"report_no":"R1","analyzer":"lab","readings":[{"element":"C","value":0.05}]}'
curl -X POST $B/api/v1/lots/1/analyze -d '{}'
curl -X POST $B/api/v1/lots/1/certificates -d '{"cert_no":"C1","grade":"304","heat_no":"H1","issued_at":"2026-08-01T00:00:00Z"}'
curl -X POST $B/api/v1/certificates/1/verify -d '{}'
curl -X POST $B/api/v1/lots/1/judge -d '{"decided_by":"qa","reason":"初检"}'
curl -X POST $B/api/v1/lots/1/accept -d '{}'
```
