# Enterprise Runtime Harness 报告

- 结果：**PASS**
- 应用：`tenant_a-support-agent`
- 环境：`tenant_a-support-agent-dev`
- 用例：8（通过 8 / 失败 0）

| 用例 | 结果 | HTTP | 延迟 | 说明 |
|---|---|---:|---:|---|
| `gateway_query_and_trace_id` | PASS | 200 (expect 200) | 259.9 ms |  |
| `query_trace_persisted` | PASS | 200 (expect 200) | 4.5 ms |  |
| `application_credential_created` | PASS | 201 (expect 201) | 6.9 ms |  |
| `credential_query_allowed` | PASS | 200 (expect 200) | 210.0 ms |  |
| `credential_answer_scope_denied` | PASS | 422 (expect 422) | 1.9 ms | knowledge_gateway_failed |
| `credential_cross_app_denied` | PASS | 403 (expect 403) | 1.9 ms | credential_scope_violation |
| `application_credential_revoked` | PASS | 200 (expect 200) | 5.3 ms |  |
| `revoked_credential_denied` | PASS | 401 (expect 401) | 0.8 ms | authentication_required |

## 证明点

- Index Build 以 idempotency key 提交，并等待 durable Manifest（row count、维度、schema hash、manifest hash）。
- Gateway 查询返回 trace_id，并通过应用边界读取持久化 Query Trace。
- App Credential 只授予 `rag:query`，验证查询允许、回答拒绝、跨应用拒绝和撤销后拒绝。
