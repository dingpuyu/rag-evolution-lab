# ADR-0004：Hybrid Retrieval 使用 Reciprocal Rank Fusion

- 状态：Accepted
- 日期：2026-07-16

## 背景

BM25 分数由词频和逆文档频率决定，向量检索分数来自余弦相似度。两种分数不在同一尺度上，直接相加会让结果依赖模型、语料和分数分布，难以跨实验复现。

## 决策

V3 使用 Reciprocal Rank Fusion（RRF）融合 Keyword 与 Vector 排名：

```text
score(document) = Σ 1 / (60 + rank_i(document))
```

- 两路 Retriever 并行执行。
- 每路至少召回 20 个候选，或最终 Top-K 的 4 倍，取较大值。
- 按稳定 Chunk ID 去重。
- ACL 和 Metadata Filter 在各 Retriever 评分前执行。
- 默认采用候选并集；Consensus 实验要求候选同时被两路召回。

## 原因

- 不需要校准异构分数。
- 排名贡献容易解释和单元测试。
- 新增 Retriever 时可以继续累加排名贡献。
- 能明确区分 Candidate Generation 与后续 Rerank。

## 影响

- RRF 只融合已有排序，不能判断单路候选是否足以回答问题。
- 并集策略提升语义覆盖时可能引入无关合法证据。
- 交集策略更适合保守拒答，但可能损失只被单路发现的语义证据。
- Query Routing、Answerability Gate 和 Reranker 需要在后续阶段解决这个 Precision/Recall 权衡。
