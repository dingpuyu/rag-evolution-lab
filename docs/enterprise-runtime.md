# 企业级运行面

这一层把 RAG 的“能检索”提升为可运营的知识基础设施。应用只通过 `app_id + environment_id` 访问知识绑定，物理 Milvus collection、凭证和发布策略都由控制面管理。

## 异步索引构建与 Manifest

```http
POST /api/v1/apps/{app_id}/environments/{environment_id}/index-builds
Idempotency-Key: <body.idempotency_key>
```

请求会立即返回 `202`，worker 从 PostgreSQL `index_builds` 队列恢复 queued/running 任务，最多重试三次。构建完成后生成包含行数、向量维度、SchemaHash、Embedding 版本和 ManifestHash 的不可变证据；只有通过 `ValidateCollection` 的 collection 才能进入发布流程。

## OpenTelemetry 与成本

设置 `RAGLAB_OTEL_ENDPOINT=localhost:4318` 后，服务通过 OTLP/HTTP 导出 W3C trace-context。Gateway span 包含租户、应用、索引版本、改写、重排、候选数和命中数；回答完成时把输入/输出 token 与价格计算写入 `query_traces`，并可用 `RAGLAB_COST_INPUT_USD_PER_1M`、`RAGLAB_COST_OUTPUT_USD_PER_1M` 配置价格。

仓库提供可选的 Collector + Jaeger 本地观测 profile：先启动主栈，再执行 `make observability-up`，把 API 的 `RAGLAB_OTEL_ENDPOINT` 设置为 `http://otel-collector:4318`，即可在 <http://localhost:16686> 查看 trace。Collector 负责批处理和内存保护，Jaeger 只保存本地演示数据；生产环境应替换为团队的 OTLP 后端和保留策略。

## 应用 Credential / OIDC Scope

管理员可创建一次性展示 secret 的应用凭证：

```http
POST /api/v1/apps/{app_id}/credentials
{"name":"support-prod","scopes":["rag:query","rag:answer"]}
```

调用时使用 `Authorization: AppCredential <secret>`。凭证绑定单一应用，路由层拒绝跨应用访问；Gateway 要求 `rag:query` 或 `rag:answer` scope。OIDC 令牌支持 `scope`、`scp` 和 `app_id`/`client_id`/`azp`，并统一映射到同一 Identity 模型。

## 限流、配额与灰度发布

Gateway 默认按 `tenant/app/subject` 做单实例固定窗口限流，返回 `429 + Retry-After`。可通过 `RAGLAB_RATE_LIMIT_RPM`、`RAGLAB_RATE_LIMIT_BURST`、`RAGLAB_TOKEN_QUOTA_PER_MINUTE` 调整。`RAGLAB_RATE_LIMIT_BACKEND=memory` 使用进程内实现；Compose 默认使用 `redis`，通过原子 Lua 脚本同时更新请求数、Token 配额和并发预留，多个 API 副本共享同一窗口。Redis 不可用时当前策略 fail-open 并把 `Remaining` 标为未知，避免限流依赖故障拖垮回答链路；对强一致准入的生产场景应增加 Redis 健康门禁或网关级保护。连接由 `RAGLAB_REDIS_URL`、`RAGLAB_REDIS_PREFIX` 配置。

索引发布支持 `channel=stable|canary` 与 `rollout_percent=0..100`。稳定版本始终 100%，灰度版本按 `sha256(subject|app|environment) % 100` 做稳定分桶；回滚只影响同一 channel，不会打断另一条发布轨道。

## 本地验证

```bash
go test ./...
go run ./cmd/raglab serve-lab
curl -X POST http://127.0.0.1:8080/api/v1/apps/<app>/environments/<env>/index-builds \
  -H 'Authorization: Bearer <admin-token>' -H 'Content-Type: application/json' \
  -d '{"idempotency_key":"demo-1","version":"v3","collection":"raglab_chunks_qwen3"}'
```

建议先用物理 collection 的真实 Embedding 维度跑构建验证，再调用 indexes publish；不要把客户端传入的 collection 直接用于查询。

## 一键回归 Harness

`enterprise-eval` 只通过公开 HTTP API 验证运行面，因此可以对本地进程、Docker Compose 或预发布环境执行。默认是只读数据面回归，同时会创建并撤销一个临时的 `rag:query` Credential，验证应用边界和 Scope 门禁：

```bash
make enterprise-eval
# 或显式指定部署地址和管理员
go run ./cmd/raglab enterprise-eval \
  --api http://127.0.0.1:8080 \
  --email alice@tenant-a.local \
  --password 'RagLab-Alice-2026!'
```

报告会写入 `eval/reports/enterprise-runtime-latest.{json,md}`，包含 Gateway `trace_id`、持久化 Trace、Credential 查询允许、`rag:answer` 缺失拒绝、跨应用拒绝和撤销后拒绝。需要验证异步 Index Build/Manifest 以及发布 supersede/rollback 时，显式执行：

```bash
make enterprise-eval-build
# 等价于：go run ./cmd/raglab enterprise-eval --build --publish \
#   --collection raglab_lifecycle_v1
```

`--build` 和 `--publish` 会写入控制面，默认关闭，避免共享环境的回归任务不断制造版本。构建用幂等 key `runtime-harness-<version>`，等待状态到 `completed`，并要求 `row_count`、`dimensions`、`schema_hash`、`manifest_hash` 均存在；发布模式会先发布 stable、再发布下一 stable 使旧版本进入 superseded，最后回滚旧版本。这样可以把索引生命周期作为可重复的验收证据，而不是只检查 HTTP 200。

限流是可选的主动探针，避免普通回归为了撞出 `429` 耗尽共享环境配额。启动一个低 burst 的隔离进程后执行：

```bash
go run ./cmd/raglab serve-lab --rate-limit-burst 1 --rate-limit-rpm 1
go run ./cmd/raglab enterprise-eval --rate-limit-requests 2
```

报告会标记首次收到 `429 + Retry-After` 的请求位置；生产多副本默认使用 Compose 的 Redis 共享实现，单机实验可显式切回 `RAGLAB_RATE_LIMIT_BACKEND=memory`。
