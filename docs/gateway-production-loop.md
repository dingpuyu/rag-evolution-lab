# Knowledge Gateway 生产闭环实验

这份文档记录一次完整的应用级 RAG 请求：身份认证 → 应用/环境授权 → Binding 策略 → Query Rewrite → Milvus 召回 → Rerank → grounded answer → SSE → Query Trace；同时包含索引版本发布和回滚。

## 启动依赖

```bash
docker compose -f deploy/stack/docker-compose.yml up -d postgres milvus
go run ./cmd/raglab serve-lab \
  -addr 127.0.0.1:18085 \
  -ollama-url http://127.0.0.1:11434 \
  -postgres-url 'postgres://raglab:raglab-local@127.0.0.1:5433/raglab?sslmode=disable' \
  -milvus-url http://127.0.0.1:19530 \
  -generation-provider ollama \
  -generation-model hermes3:8b \
  -generation-base-url http://127.0.0.1:11434
```

本地演示账号：`alice@tenant-a.local / RagLab-Alice-2026!`；平台管理员：`admin@raglab.local / RagLab-Platform-2026!`。生产环境应切换 OIDC/RS256，并关闭本地账号签发。

## 发布一个可查询的索引版本

```bash
TOKEN=$(curl -sS -X POST http://127.0.0.1:18085/api/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"admin@raglab.local","password":"RagLab-Platform-2026!"}' \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')

curl -X POST http://127.0.0.1:18085/api/v1/apps/tenant_a-support-agent/environments/tenant_a-support-agent-dev/indexes/publish \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"version":"v-current","collection":"raglab_lifecycle_v1","alias":"raglab_knowledge_active"}'
```

发布前会执行 Collection 存在性、Embedding 维度、非空行数和索引 `Finished` 检查。失败不会写入 `published` 状态，也不会改变 Alias。

第二个版本发布后可以回滚：

```bash
curl -X POST http://127.0.0.1:18085/api/v1/apps/tenant_a-support-agent/environments/tenant_a-support-agent-dev/indexes/rollback \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"release_id":"tenant_a-support-agent-dev-v-current"}'
```

## 验证 Query Rewrite、Rerank 和 Trace

```bash
USER_TOKEN=$(curl -sS -X POST http://127.0.0.1:18085/api/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"alice@tenant-a.local","password":"RagLab-Alice-2026!"}' \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')

curl -X POST http://127.0.0.1:18085/api/v1/apps/tenant_a-support-agent/query \
  -H "Authorization: Bearer $USER_TOKEN" -H 'content-type: application/json' \
  -d '{"query":"单点登录","top_k":3}'
```

当 Binding 的 `query_rewrite` 与 `rerank` 为 `true` 时，响应中的 Binding trace 应出现：

```json
{
  "rewrite": {"applied": true, "rewriter": "semantic-alias-v1", "query": "单点登录\nsso"},
  "rerank": {"applied": true, "model": "heuristic-evidence-reranker", "candidates": 6},
  "index_version": "v-current"
}
```

响应的 `trace_id` 可用于持久化查询：

```bash
curl http://127.0.0.1:18085/api/v1/apps/tenant_a-support-agent/traces/<trace_id> \
  -H "Authorization: Bearer $USER_TOKEN"
```

## 验证应用级 SSE

```bash
curl -N -X POST http://127.0.0.1:18085/api/v1/apps/tenant_a-support-agent/answer/stream \
  -H "Authorization: Bearer $USER_TOKEN" -H 'content-type: application/json' \
  -d '{"query":"请说明应急队列的处理规则","top_k":2}'
```

事件顺序是固定协议。`gateway_completed` 的 `result` 是完整的 `AnswerResponse`，包含引用、策略 trace 和 `trace_id`；随后读取 Trace 应看到 `status=completed`、模型、生成延迟和 Token 统计。

## 自动化验证

```bash
go test ./internal/knowledgegateway ./internal/httpapi ./internal/milvus
RAGLAB_TEST_POSTGRES_URL='postgres://raglab:raglab-local@127.0.0.1:5433/raglab?sslmode=disable' \
  go test ./internal/datasetaccess
npm --prefix web run lint
```

核心测试覆盖：策略开关和失败降级、Trace retrieved/completed 生命周期、SSE 完成事件、PostgreSQL Trace 租户隔离、索引发布/回滚状态机和 Milvus Collection readiness gate。
