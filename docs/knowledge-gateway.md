# Knowledge Gateway：面向 Agent 的统一知识入口

Knowledge Gateway 是 Application/Environment/Knowledge Binding 控制面和 Milvus 检索面的连接层。它的目标不是再包装一个 Dataset API，而是把上层 Agent 依赖的契约稳定下来：调用方只需要知道“哪个应用、哪个环境、用户问什么”，不需要知道租户字段、Milvus Collection、Product 或 Filter 表达式。

## 请求契约

```http
POST /api/v1/apps/{app_id}/query
Authorization: Bearer <token>
Content-Type: application/json

{
  "environment_id": "tenant_a-support-agent-dev",
  "query": "服务故障时如何处理？",
  "top_k": 5
}
```

`environment_id` 省略时使用 `{app_id}-dev`。服务端不会接受请求体里的 `tenant_id`、`role`、`product` 或 Milvus Filter；这些字段由已验证的身份、PostgreSQL 资源和 Dataset 元数据生成。

## 一次请求的真实链路

```text
JWT / OIDC identity
        │
        ▼
Application + Environment authorization
        │
        ▼
Active Knowledge Bindings + Retrieval Policy
        │
        ├── Dataset ACL re-check (fail closed)
        ├── server-built Product/Tenant/Role Filter
        └── per-binding candidate retrieval
                    │
                    ▼
         dedupe by chunk_id → distance sort → global top_k
                    │
                    ▼
         generation / citation allowlist / answer contract
```

一个应用可以绑定多个知识库。每个 Binding 独立保存 `top_k`、`candidate_k`、`rerank`、`query_rewrite`、`allow_fallback` 和 `token_budget`。Gateway 会对每个 Binding 独立改写、召回、重排、截断，再做跨知识库去重和全局 Top-K 合并。策略关闭时不会偷偷调用对应组件；策略组件失败时只有 `allow_fallback=true` 才会降级到原查询/未重排结果。

当前内置两种可替换策略：

- `semantic-alias-v1`：保留原问题并追加确定性的业务同义词（例如“单点登录”追加 `sso`），避免改写丢失原始语义；
- `heuristic-evidence-reranker`：对候选的标题、内容、查询词重叠度和 Milvus 距离做确定性重排，便于离线评测和回归。接口位于 `internal/knowledgegateway/strategy.go`，后续可以替换为 Cross-Encoder 或外部 Rerank API。

## 返回内容

`query` 返回 `app_id`、`environment_id`、`trace_id`、改写后的查询、每个绑定的策略/索引版本/命中数和合并后的 `milvus.SearchResult`。`answer` 在相同检索结果上复用现有 grounded-answer 服务，仍执行：

- Prompt Injection Evidence 脱敏；
- Citation Allowlist 校验；
- 拒答原因和安全调整记录；
- 模型、Token、延迟和检索 Filter 元数据。

这样既保持旧 Dataset Answer 的可验证性，也让 Agent harness 可以按应用、环境、绑定和发布版本观察质量。

## 应用级 SSE 与 Query Trace

```http
POST /api/v1/apps/{app_id}/answer/stream
Authorization: Bearer <token>
Content-Type: application/json

{"environment_id":"tenant_a-support-agent-dev","query":"如何处理故障？","top_k":5}
```

SSE 事件顺序固定为 `started → retrieved → generation_started → token* → generation_completed → completed → gateway_completed`。每个事件都带 `app_id` 和解析后的 `environment_id`；最后的 `gateway_completed` 包含完整 `AnswerResponse`，客户端可以用其中的 `trace_id` 查询持久化记录。

```http
GET /api/v1/apps/{app_id}/traces/{trace_id}
Authorization: Bearer <token>
```

Trace 在 PostgreSQL `query_traces` 表中先写入 `retrieved` 状态，生成成功后更新为 `completed`，生成失败更新为 `failed`。记录包括：租户和主体、原始/改写查询、Binding 策略、索引版本与 Collection、Embedding/Generator/Prompt 版本、召回/重排/生成延迟、Token、Answerable、拒答原因、引用上下文元数据。读取会再次校验租户；跨租户统一返回 404，不暴露资源是否存在。

## 索引版本发布与回滚

索引控制面要求管理员身份，构建中的物理 Collection 不能直接被应用读取：

```http
GET  /api/v1/apps/{app_id}/environments/{environment_id}/indexes
POST /api/v1/apps/{app_id}/environments/{environment_id}/indexes/publish
POST /api/v1/apps/{app_id}/environments/{environment_id}/indexes/rollback
```

发布请求示例：

```json
{"version":"v-current","collection":"raglab_lifecycle_v1","alias":"raglab_knowledge_active"}
```

服务端先检查 Collection 存在、Embedding 维度与当前模型一致、行数大于 0、Embedding 索引状态为 `Finished`，再切换 Milvus Alias，并在 PostgreSQL `index_releases` 中将旧版本标记为 `superseded`。Gateway 查询优先解析当前 `published` 版本并使用其物理 Collection；回滚会重新执行同样的就绪校验，再原子地切换控制面状态和 Alias。

## 已验证结果

在本地 PostgreSQL + Milvus + Qwen3-Embedding-4B 环境中完成真实 API 验证：

| 场景 | 结果 |
|---|---|
| Tenant A 管理员查询 `tenant_a-support-agent` | HTTP 200；命中 Tenant A 运维知识库；返回 `knowledge-gateway` trace |
| Tenant B 查询 Tenant A 应用 | HTTP 404；没有进入向量检索 |
| Query Trace 持久化与读取 | PostgreSQL round-trip 通过；跨租户读取 404 |
| 应用级 SSE | 真实 Ollama 生成；事件顺序完整，最终 trace 状态为 `completed` |
| Query Rewrite / Rerank | `单点登录` → `单点登录\nsso`；Binding trace 标记 `semantic-alias-v1` 和 `heuristic-evidence-reranker` |
| 索引发布与回滚 | `v-current → v-next → rollback(v-current)` 通过；Milvus Alias 与控制面状态一致 |
| 默认部署启动 | 自动 seed Tenant A/B Support Agent、dev Environment 和租户运维 Binding |
| 多 Binding 合并 | 逐 Binding 召回、`chunk_id` 去重、距离排序、全局 Top-K 截断 |

回归覆盖位于 `internal/knowledgegateway` 和 `internal/httpapi`：包括策略化候选数、公共数据集 scope、撤权后的 fail-closed 以及跨租户统一 404。

## 当前边界与下一步

当前 Gateway 已具备可演示的应用级生产闭环，下一阶段聚焦规模化和可运营性：

1. 将 Index Build、解析产物和发布审批做成异步 Job，并增加 checksum/manifest 校验；
2. 把 Trace 接入 OpenTelemetry/成本明细和检索质量评测，形成线上问题到 Golden Query 的闭环；
3. 增加应用 Credential、OIDC scope、限流/配额和多副本 SSE 背压；
4. 在百万级数据上验证分片、冷热索引、Alias 灰度和租户级容量治理。

这些能力会继续保持在 Gateway 和控制面中实现，避免散落到客服、招聘或运维等上层 Agent 项目里。
