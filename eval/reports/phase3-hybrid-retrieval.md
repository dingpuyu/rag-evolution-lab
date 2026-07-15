# Phase 3 Hybrid Retrieval Experiment

> Dataset version: 0.1.0  
> Split: development  
> Corpus: 13 synthetic documents / 38 chunks  
> Golden cases: 20  
> Top-K: 5  
> Fusion: RRF, rank constant 60, candidate depth max(20, Top-K × 4)

## Hypothesis

BM25 擅长错误码、数值和专有词，Vector 擅长语义改写。使用 RRF 融合两者应在不直接比较异构分数的前提下提高召回；与 Metadata Filter 组合后，应同时消除跨产品和过期知识污染。

## Controlled change

- `v3-hybrid`：BM25 + Vector + RRF，保留 V1 的 ACL 行为，不启用 Metadata。
- `v3-hybrid-metadata`：两路检索在评分前使用与 V2 相同的 Metadata Filter。
- `v3-hybrid-metadata-consensus`：在上一组基础上，只保留两路都召回的 Chunk，用于测量保守证据门控的代价。
- Keyword 和 Vector 并行召回，使用稳定 Chunk ID 去重。
- Qwen3 实验使用本地 `Qwen3-Embedding-4B Q4_K_M`，不添加 Query Instruction。

## Results

| Pipeline | Hit Rate@5 | MRR | Document Recall@5 | Metadata Violations | Unauthorized |
|---|---:|---:|---:|---:|---:|
| V0 Keyword | 0.850 | 0.762 | 0.850 | 41 | 0 |
| V3 Hash Hybrid | 0.900 | 0.783 | 0.875 | 50 | 0 |
| V1 Qwen3 Vector | 0.900 | 0.850 | 0.875 | 37 | 0 |
| V3 Qwen3 Hybrid | 0.900 | 0.842 | **0.900** | 42 | 0 |
| V2 Metadata BM25 | 0.900 | 0.900 | 0.900 | 0 | 0 |
| V3 Qwen3 Hybrid + Metadata | 0.900 | 0.900 | 0.900 | 0 | 0 |
| V3 Qwen3 Hybrid + Metadata + Consensus | 0.900 | 0.900 | 0.900 | 0 | 0 |

总分相同并不表示行为相同：

| Category | V2 Metadata | Hybrid + Metadata | + Consensus |
|---|---:|---:|---:|
| Semantic Paraphrase Hit@5 | 0.750 | **1.000** | 0.750 |
| Access Control Hit@5 | **1.000** | 0.500 | **1.000** |
| Version Filter MRR | 1.000 | 1.000 | 1.000 |
| Metadata Violations | 0 | 0 | 0 |

## Failure analysis

1. Qwen3 Hybrid 将 Document Recall 从 0.875 提升到 0.900，但 MRR 从 0.850 降到 0.842，Metadata 污染从 37 增加到 42。候选更多不等于排序更好。
2. Hybrid + Metadata 让四条语义改写全部命中，同时 `access_002` 退化：ACL 正确排除了 Tenant A 私有文档，但 Vector 仍返回了合法却无关的公共 operations 文档，系统因此没有拒答。
3. Consensus Gate 修复 `access_002`，但丢失一条只被向量路命中的语义证据。简单并集偏向 Recall，简单交集偏向 Precision，二者都不是通用答案。
4. 所有版本 Unauthorized Retrievals 都为 0，说明融合发生在权限过滤之后，没有扩大安全边界。
5. `unanswerable_001` 仍然失败。RRF 分数只表达排名共识，不是跨 Query 可比较的置信度，不能直接充当回答阈值。

## What this proves

- RRF 解决的是异构排序融合，不是相关性校准或拒答。
- 只看总体 Hit Rate 会掩盖类别之间“一个修复、一个退化”的抵消。
- Metadata Filter 必须在每个 Candidate Retriever 内执行，不能在截断 Top-K 后补过滤。
- 下一阶段应把 Query 风险分类、Retrieval Routing 和 Answerability Gate 作为独立变量评测，而不是针对单个 Case 写规则。

## Reproduction

```bash
go test -race ./...
go run ./cmd/raglab compare --baseline v0-keyword --candidate v3-hybrid
go run ./cmd/raglab compare --baseline v2-metadata --candidate v3-hybrid-metadata

RAGLAB_OLLAMA_MODEL=qwen3-embedding:4b-local \
  go run ./cmd/raglab eval --pipeline v3-ollama-hybrid-metadata

RAGLAB_OLLAMA_MODEL=qwen3-embedding:4b-local \
  go run ./cmd/raglab eval --pipeline v3-ollama-hybrid-metadata-consensus
```

## Next experiment

进入 V4 Query Routing：先把 Query 分为 exact、semantic、access-sensitive 和 unanswerable-risk，再分别选择 Keyword、Hybrid Union 或 Hybrid Consensus。路由器必须有确定性单测，并与“所有 Query 固定使用同一策略”的基线比较。
