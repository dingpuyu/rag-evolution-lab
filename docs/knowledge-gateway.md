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

一个应用可以绑定多个知识库。每个 Binding 独立保存 `top_k`、`candidate_k`、`rerank`、`query_rewrite` 和 `token_budget`，当前版本已经实际使用 `candidate_k` 做候选召回，并将策略和命中数回传给调用方；重排和查询改写留在后续迭代，不在第一版伪装成已实现能力。

## 返回内容

`query` 返回 `app_id`、`environment_id`、每个绑定的策略/命中数和合并后的 `milvus.SearchResult`。`answer` 在相同检索结果上复用现有 grounded-answer 服务，仍执行：

- Prompt Injection Evidence 脱敏；
- Citation Allowlist 校验；
- 拒答原因和安全调整记录；
- 模型、Token、延迟和检索 Filter 元数据。

这样既保持旧 Dataset Answer 的可验证性，也让 Agent harness 可以按应用、环境和绑定观察质量。

## 已验证结果

在本地 PostgreSQL + Milvus + Qwen3-Embedding-4B 环境中完成真实 API 验证：

| 场景 | 结果 |
|---|---|
| Tenant A 管理员查询 `tenant_a-support-agent` | HTTP 200；命中 Tenant A 运维知识库；返回 `knowledge-gateway` trace |
| Tenant B 查询 Tenant A 应用 | HTTP 404；没有进入向量检索 |
| 默认部署启动 | 自动 seed Tenant A/B Support Agent、dev Environment 和租户运维 Binding |
| 多 Binding 合并 | 逐 Binding 召回、`chunk_id` 去重、距离排序、全局 Top-K 截断 |

回归覆盖位于 `internal/knowledgegateway` 和 `internal/httpapi`：包括策略化候选数、公共数据集 scope、撤权后的 fail-closed 以及跨租户统一 404。

## 当前边界与下一步

当前 Gateway 是应用级检索契约的第一版，仍需继续补齐：

1. Query Trace 持久化：记录 binding、策略版本、索引版本、模型和成本；
2. `answer/stream` 应用级 SSE，让门户不再依赖 Dataset 兼容路由；
3. Binding 级 rerank/query rewrite 实现及离线评测；
4. Index Build / Published Alias，让环境绑定到可回滚的索引版本；
5. 应用级限流、配额和 Credential/OIDC scope。

这些能力会继续保持在 Gateway 和控制面中实现，避免散落到客服、招聘或运维等上层 Agent 项目里。
