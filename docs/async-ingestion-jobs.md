# 企业 RAG 异步导入任务

## 1. 解决的问题

原有 `/api/v1/milvus/lifecycle/apply` 是同步接口，能够证明幂等、Revision 顺序、
增量 Upsert、删除和写后验证，但不能表达真实生产环境中的排队、阶段进度、失败恢复
和取消。

本阶段增加独立的 Ingestion Job 控制面：

```text
queued
  ↓
validating
  ↓
chunking
  ↓
embedding
  ↓
indexing
  ↓
verifying
  ↓
completed
```

任一执行阶段可以进入 `failed`，排队或运行任务可以进入 `cancelled`。

## 2. 已实现能力

- 异步 Worker，不在 HTTP 请求生命周期内执行 Embedding 和 Milvus 写入；
- `idempotency_key` 与完整载荷哈希绑定；
- 相同 Key、相同载荷返回原任务，不会重复入队；
- 相同 Key、不同载荷返回 `409 Conflict`；
- 默认最多三次尝试，失败或取消任务可人工重试；
- 运行任务取消时向底层 `context.Context` 传播；
- 原子 JSON 状态文件，权限为 `0600`；
- 服务重启时将中断的 `running` 任务恢复为 `queued`；
- 任务完成后从任务账本清除文档正文，只保留事件引用和结果；
- Platform Admin 才能创建、查看、重试和取消任务；
- 租户管理员只能在自己有权访问的数据集下提交和查看任务，服务端会覆盖文档中的租户、产品和可见性字段；
- 网页显示任务汇总、阶段进度、尝试次数、Worker、心跳、结果和可操作错误；
- PostgreSQL 控制面可作为持久化 Job Repository，并记录 `ingestion_job_events` 事件审计；
- 运行任务会写入 Worker、Lease Expiry 和 Last Heartbeat，服务重启时会把未完成任务恢复为排队状态。

## 3. API

平台级接口需要 Platform Admin Bearer Token；数据集级接口允许该数据集所属租户的 Admin，
Platform Admin 仍然可以跨租户查看和运维。

### 创建任务

```http
POST /api/v1/ingestion/jobs
Content-Type: application/json
Authorization: Bearer <token>
```

```json
{
  "idempotency_key": "source-confluence-page-42-r7",
  "change": {
    "event_id": "confluence-page-42-r7",
    "operation": "upsert",
    "source_revision": 7,
    "document": {
      "document_id": "confluence-page-42",
      "title": "SSO 配置",
      "content": "文档正文",
      "product": "identity",
      "version": "7",
      "status": "active",
      "visibility": "private",
      "allowed_tenants": ["tenant_037"],
      "allowed_roles": ["viewer", "admin"]
    }
  }
}
```

首次创建返回 `202 Accepted`；幂等重放返回 `200 OK` 和
`"duplicate": true`。

### 查询任务

```http
GET /api/v1/ingestion/jobs
GET /api/v1/ingestion/jobs/{job_id}
```

### 重试和取消

```http
POST /api/v1/ingestion/jobs/{job_id}/retry
POST /api/v1/ingestion/jobs/{job_id}/cancel
```

已完成任务不能重试或取消，超过最大尝试次数后不能继续重试。

### 数据集级门户接口

门户使用以下接口，把“导入资料”变成一个可观察、可干预的任务流程：

```http
POST /api/v1/datasets/{dataset_id}/ingestion/jobs
GET  /api/v1/datasets/{dataset_id}/ingestion/jobs
POST /api/v1/datasets/{dataset_id}/ingestion/jobs/{job_id}/retry
POST /api/v1/datasets/{dataset_id}/ingestion/jobs/{job_id}/cancel
```

浏览器会在导入页面轮询任务汇总，展示 queued/running/completed/failed/cancelled、
五个执行阶段、尝试次数和心跳时间。失败任务会直接显示后端错误，例如向量维度与
现有 Collection 不一致；用户可以在同一张卡片中重试或取消，不需要打开数据库或日志。
数据集级接口始终从服务端身份和 PostgreSQL membership 推导数据边界，不能信任浏览器
提交的 `tenant_id`、`product`、`visibility` 或 `allowed_tenants`。

## 4. 持久化和安全边界

状态默认保存到：

```text
data/ingestion/jobs.json
```

可以通过 `RAGLAB_INGESTION_JOB_STATE` 或 `--ingestion-job-state` 修改。

当配置 `RAGLAB_POSTGRES_URL` 时，Job 会自动使用 PostgreSQL 控制面中的：

- `ingestion_jobs`：当前状态、幂等键、载荷哈希、租户/数据集、Worker、Lease 和结果；
- `ingestion_job_events`：提交、开始、阶段进度、重试、取消、完成和失败事件。

这使得 API 重启后任务状态仍可由数据库恢复，并能在门户和审计查询中解释“谁在何时
对哪个数据集执行了什么操作”。

排队和失败任务为了能够执行或重试，会暂时保存输入文档；完成后立即清除正文。
因此当前状态文件仍属于敏感数据，不能提交 Git，也不能写入公开日志。

生产版本应把原始文档放入加密对象存储，Job 只保存不可猜测的 Object Key、
Content Hash 和租户信息，并增加数据保留和过期清理策略。

## 5. 自我验证

单元测试覆盖：

- 幂等重放不产生第二个任务；
- 相同 Key 不同载荷冲突；
- 完成态不保留文档正文；
- 第一次失败后能够重试成功；
- 最大尝试次数和状态机门禁；
- 运行中取消传播到底层 Processor；
- 未启动任务在服务重启后恢复；
- Tenant Admin 无权创建任务；
- Platform Admin 创建和查询任务；
- HTTP 幂等重放语义。
- PostgreSQL 状态恢复、幂等重放和事件落库；
- 租户管理员数据集边界与跨租户 404、Viewer 403；
- Milvus Collection 维度不匹配时任务进入失败态并保留可操作错误。

运行：

```bash
go test ./internal/ingestionjob ./internal/httpapi ./internal/milvus
```

## 6. 当前边界与下一步

这一版已经从本地 JSON 基线升级为“PostgreSQL 持久化控制面 + 单进程执行 Worker”。
Lease、Heartbeat 和事件已经可观测，但当前 HTTP 服务启动的 Worker 仍直接消费内存队列，
尚未完成多个进程之间的数据库原子 Claim，也没有外部消息队列。因此不能把这一版描述成
完整的分布式任务平台；它更适合作为生产化演进的第一步和可回归的人机操作闭环。

下一阶段需要通过失败实验逐项引入：

1. PostgreSQL `FOR UPDATE SKIP LOCKED` 原子 Claim 与多进程 Worker 接管；
2. Outbox Relay 以及 NATS、Kafka 或 Redis Streams 队列适配器；
3. 指数退避、错误分类和 DLQ；
4. Tenant 级并发、公平调度与背压；
5. Parser、Embedding、Index Writer 独立 Worker Pool；
6. OpenTelemetry Trace、队列延迟、阶段耗时和告警；
7. 大文件放入对象存储，任务消息只传引用；
8. 批量任务、父子任务、部分失败和补偿操作。

只有完成多进程故障注入后，才能宣称任务系统具备生产级分布式可靠性。
