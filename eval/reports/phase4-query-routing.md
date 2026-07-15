# Phase 4 Query Routing Experiment

> Corpus: 13 synthetic documents / 38 chunks  
> Development: 20 cases  
> V4 Challenge: 8 new cases  
> Embedding: Qwen3-Embedding-4B Q4_K_M, 2560 dimensions  
> Top-K: 5

## Hypothesis

固定 Hybrid Union 与固定 Consensus 分别偏向 Recall 和 Precision。使用 Query 的可观察特征选择 Keyword、Hybrid Union 或 Consensus，并在高风险场景增加确定性 Evidence Gate，可以同时保留语义召回和安全拒答。

## Routing policy

| Intent | Strategy | Development routes | Challenge routes |
|---|---|---:|---:|
| Exact | Metadata BM25 | 11 | 2 |
| Semantic | Metadata Hybrid Union | 4 | 3 |
| Access-sensitive | Tenant Scope Gate + Consensus | 4 | 2 |
| Unanswerable-risk | Anchor Gate + Consensus | 1 | 1 |

分类器不读取 Golden Category。结构化 Product / Version 只用于 Metadata Filter，不作为意图标签；首轮实现曾错误地把请求中的 Version 当作 Query 意图，导致 15/20 条被路由到 Exact，该问题已由回归测试固定。

## Development results

| Pipeline | Hit Rate@5 | MRR | Document Recall@5 | Metadata Violations | Unauthorized |
|---|---:|---:|---:|---:|---:|
| V3 Qwen3 Hybrid + Metadata | 0.900 | 0.900 | 0.900 | 0 | 0 |
| V3 Qwen3 + Metadata + Consensus | 0.900 | 0.900 | 0.900 | 0 | 0 |
| **V4 Qwen3 Query Router** | **1.000** | **1.000** | **1.000** | **0** | **0** |

V4 同时修复 V3 Union 的 `access_002` 错误回答、Consensus 的语义召回退化，以及 `unanswerable_001` 的未知认证误答。

## Challenge results

新增 8 条不复用原 Query 的挑战集，覆盖租户表达改写、结构化权限、数值干扰、语义改写、故障排查和未知认证。

| Run | Hit Rate@5 | MRR | Recall@5 | Failure |
|---|---:|---:|---:|---|
| Initial router | 0.875 | 0.875 | 0.875 | `租户 A 的专属…` 被误路由到 Semantic |
| Synonym-aware classifier only | 0.875 | 0.875 | 0.875 | 已路由到 Access，但公共 operations 噪声仍阻止拒答 |
| **+ Tenant Scope Gate** | **1.000** | **1.000** | **1.000** | none in current split |

这次修复没有调整 RRF 分数：Query 中明确引用 Tenant A，而认证上下文属于 Tenant B 时，系统在检索前拒绝，避免把授权冲突错误地交给相似度模型。

## Efficiency observation

- V3 固定 Qwen3 Hybrid：20/20 条 Query 调用 Vector。
- V4 Development：9/20 条 Query 进入包含 Vector 的策略，其余 11 条使用 Metadata BM25。
- 本机缓存命中后的完整 CLI 运行，本轮观察到 V4 约 3.52 秒，固定 Hybrid 约 4.97 秒。该数字包含 CLI 启动，只用于说明路由减少模型调用，不作为稳定延迟 Benchmark。

## What this proves

1. Routing 的价值不仅是质量，也包括减少不必要的模型调用。
2. Intent Classification、Authorization Gate 和 Retrieval Strategy 是三个不同职责，不能混成一个 Prompt。
3. Trace 必须记录 Route、Reason 和 Strategy，否则无法解释错误来自分类还是检索。
4. 安全冲突应使用确定性 Gate；Embedding 相似度不能决定租户边界或认证事实。
5. 1.000 只表示当前 28 条合成 Case 全部通过。它不是泛化结论，下一步必须扩充到至少 60 条并建立不参与规则迭代的 Blind Split。

## Reproduction

```bash
go run ./cmd/raglab validate --split development
go run ./cmd/raglab validate --split v4-challenge

go run ./cmd/raglab eval --pipeline v4-router --split development
go run ./cmd/raglab eval --pipeline v4-router --split v4-challenge

RAGLAB_OLLAMA_MODEL=qwen3-embedding:4b-local \
  go run ./cmd/raglab eval --pipeline v4-ollama-router --split development

RAGLAB_OLLAMA_MODEL=qwen3-embedding:4b-local \
  go run ./cmd/raglab eval --pipeline v4-ollama-router --split v4-challenge
```

## Next experiment

V5 将在每条 Route 内加入 Reranker 和 Context Packing，而不是再次改变路由规则。重点测量 Top-3 Precision、上下文 Token、延迟和 Citation 覆盖。
