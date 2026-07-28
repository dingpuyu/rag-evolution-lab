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

## 应用 Credential / OIDC Scope

管理员可创建一次性展示 secret 的应用凭证：

```http
POST /api/v1/apps/{app_id}/credentials
{"name":"support-prod","scopes":["rag:query","rag:answer"]}
```

调用时使用 `Authorization: AppCredential <secret>`。凭证绑定单一应用，路由层拒绝跨应用访问；Gateway 要求 `rag:query` 或 `rag:answer` scope。OIDC 令牌支持 `scope`、`scp` 和 `app_id`/`client_id`/`azp`，并统一映射到同一 Identity 模型。

## 限流、配额与灰度发布

Gateway 默认按 `tenant/app/subject` 做单实例滑动窗口限流，返回 `429 + Retry-After`。可通过 `RAGLAB_RATE_LIMIT_RPM`、`RAGLAB_RATE_LIMIT_BURST`、`RAGLAB_TOKEN_QUOTA_PER_MINUTE` 调整。生产多副本时，将 `internal/ratelimit.Limiter` 替换为 Redis 实现即可保留调用边界。

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
