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
- 网页显示任务汇总、执行阶段、尝试次数、结果和错误。

## 3. API

所有接口都需要 Platform Admin Bearer Token。

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

## 4. 持久化和安全边界

状态默认保存到：

```text
data/ingestion/jobs.json
```

可以通过 `RAGLAB_INGESTION_JOB_STATE` 或 `--ingestion-job-state` 修改。

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

运行：

```bash
go test ./internal/ingestionjob ./internal/httpapi ./internal/milvus
```

## 6. 当前边界与下一步

这一版是单进程、持久化本地状态文件的可靠性基线，尚不冒充分布式队列。

下一阶段需要通过失败实验逐项引入：

1. PostgreSQL Job/Outbox 真相源；
2. NATS、Kafka 或 Redis Streams 队列适配器；
3. Lease、Heartbeat 和 Worker 宕机接管；
4. 指数退避、错误分类和 DLQ；
5. Tenant 级并发、公平调度与背压；
6. Parser、Embedding、Index Writer 独立 Worker Pool；
7. OpenTelemetry Trace、队列延迟、阶段耗时和告警；
8. 大文件放入对象存储，任务消息只传引用；
9. 批量任务、父子任务、部分失败和补偿操作。

只有完成多进程故障注入后，才能宣称任务系统具备生产级分布式可靠性。
