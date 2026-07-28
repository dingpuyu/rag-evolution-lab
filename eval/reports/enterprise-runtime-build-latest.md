# Enterprise Runtime Harness 报告

- 结果：**PASS**
- 应用：`tenant_a-support-agent`
- 环境：`tenant_a-support-agent-dev`
- 用例：12（通过 12 / 失败 0）

| 用例 | 结果 | HTTP | 延迟 | 说明 |
|---|---|---:|---:|---|
| `index_build_completed` | PASS | 200 (expect 200) | 8.6 ms | status=completed stage=completed attempts=1 rows=12 dimensions=2560 manifest=707645c1a312444eb1f5552c6f39200bb0416cde28f3480d7ee8cefa28b0d726 |
| `stable_release_published` | PASS | 201 (expect 201) | 178.5 ms | release=tenant_a-support-agent-dev-harness-20260728-180036 state=published channel=stable rollout=100% |
| `stable_release_superseded` | PASS | 201 (expect 201) | 0.0 ms |  |
| `stable_release_rollback` | PASS | 200 (expect 200) | 159.0 ms |  |
| `gateway_query_and_trace_id` | PASS | 200 (expect 200) | 227.2 ms |  |
| `query_trace_persisted` | PASS | 200 (expect 200) | 6.1 ms |  |
| `application_credential_created` | PASS | 201 (expect 201) | 6.1 ms |  |
| `credential_query_allowed` | PASS | 200 (expect 200) | 202.8 ms |  |
| `credential_answer_scope_denied` | PASS | 422 (expect 422) | 2.3 ms | knowledge_gateway_failed |
| `credential_cross_app_denied` | PASS | 403 (expect 403) | 1.9 ms | credential_scope_violation |
| `application_credential_revoked` | PASS | 200 (expect 200) | 5.2 ms |  |
| `revoked_credential_denied` | PASS | 401 (expect 401) | 1.1 ms | authentication_required |

## 证明点

- Index Build 以 idempotency key 提交，并等待 durable Manifest（row count、维度、schema hash、manifest hash）。
- Gateway 查询返回 trace_id，并通过应用边界读取持久化 Query Trace。
- App Credential 只授予 `rag:query`，验证查询允许、回答拒绝、跨应用拒绝和撤销后拒绝。
- 发布控制面验证 stable supersede + rollback，最终回滚版本 `harness-20260728-180036`。
