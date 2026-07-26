# Dataset Search Harness 报告

- 结果：**PASS**
- Suite：`enterprise-dataset-search` (`1.0.0`)
- API：`http://127.0.0.1:8081`
- Milvus：`raglab_knowledge_eval`，Embedding：`ollama/qwen3-embedding:4b-local`，维度：`2560`
- 用例：8（通过 8 / 失败 0）

## 核心指标

| Hit@K | MRR | P50 | P95 | 越权召回 | 禁止事实 | Filter 违规 | API 契约违规 |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1.000 | 1.000 | 149.8 ms | 287.0 ms | 0 | 0 | 0 | 0 |

## 用例明细

| 用例 | 类型 | 结果 | HTTP | 首个相关排名 | 延迟 | 召回文档（Milvus score） |
|---|---|---|---:|---:|---:|---|
| `alice_dataset_visibility` | dataset_visibility | PASS | 200 | 0 | 14.5 ms | `` |
| `bob_dataset_visibility` | dataset_visibility | PASS | 200 | 0 | 5.8 ms | `` |
| `alice_private_relevance` | dataset_search | PASS | 200 | 1 | 287.0 ms | `search-golden-tenant-a-ops=0.7897` |
| `bob_private_relevance` | dataset_search | PASS | 200 | 1 | 254.0 ms | `search-golden-tenant-b-ops=0.7940` |
| `alice_public_relevance` | dataset_search | PASS | 200 | 1 | 269.0 ms | `search-golden-public-sso=0.7580, answer-golden-injection=0.4373` |
| `bob_public_relevance` | dataset_search | PASS | 200 | 1 | 149.8 ms | `search-golden-public-sso=0.7581, answer-golden-injection=0.4373` |
| `alice_cross_tenant_non_enumeration` | dataset_search | PASS | 404 | 0 | 7.4 ms | `` |
| `bob_cross_tenant_non_enumeration` | dataset_search | PASS | 404 | 0 | 5.0 ms | `` |

## 判定说明

- 相关性使用文档 ID 计算 Hit@K 与 MRR；Milvus 相似度只记录，不设脆弱的固定阈值。
- 权限门禁同时检查数据集可见性、跨租户 HTTP 404、结果 tenant/visibility、禁止文档与禁止事实。
- Filter 检查的是服务端最终生成表达式，客户端不能提交或放宽 `AccessScope`。
