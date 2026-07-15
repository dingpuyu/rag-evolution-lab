# Local Embedding Benchmark

## 目标

在不使用云端 API 和外部凭据的情况下，将真实 Embedding 模型接入现有 RAG Pipeline，并与 V0 关键词检索、V1 确定性 Hash Vector 保持相同语料、切块、权限规则和 Golden Dataset。

本轮是 Naive Vector Baseline：文档标题与正文直接拼接，Query 和 Document 不添加任务指令或前缀，不做混合检索、Metadata Filter 与 Rerank。这个约束用于先暴露模型替换本身能否带来收益。

## 环境

- macOS arm64，32 GB RAM
- Ollama 0.30.11
- 13 Documents / 38 Chunks
- Development Split：20 Cases
- Top-K：5

## 结果

| Pipeline / Model | Hit Rate@5 | MRR | Document Recall@5 | Unauthorized Retrievals |
|---|---:|---:|---:|---:|
| V0 Keyword | 0.850 | 0.762 | 0.850 | 0 |
| V1 Semantic Hash | 0.900 | 0.779 | 0.875 | 0 |
| V1 Ollama / mxbai-embed-large | 0.750 | 0.692 | 0.700 | 0 |
| V1 Ollama / nomic-embed-text v1.5 | 0.700 | 0.675 | 0.650 | 0 |

### 分类表现

| Model | Exact Match | Semantic Paraphrase | Table Numeric | Multi-hop | Version Filter |
|---|---:|---:|---:|---:|---:|
| mxbai-embed-large | 1.000 | 0.500 | 1.000 | 1.000 | 0.667 |
| nomic-embed-text v1.5 | 1.000 | 0.250 | 1.000 | 1.000 | 0.667 |

表格中的分类数值为 Hit Rate@5。

## 观察

1. 真实向量模型没有自动超过基线。两个已有模型的主要损失都来自中文语义改写，说明公开 Benchmark 排名不能代替目标语料评测。
2. 精确错误码、表格数值和多跳问题仍有较高召回，但这不代表纯向量检索是最佳策略；这些类型通常更适合在后续阶段与关键词检索融合。
3. `version_003` 在两个模型上都失败，符合 V1 尚未启用 Metadata Filter 的预期。
4. 权限泄漏仍为 0，说明外部 Embedder 的引入没有绕过检索前的 ACL 过滤。
5. `unanswerable_001` 仍会返回相似内容，暴露了缺少拒答阈值与置信度校准的问题。

## 可复现命令

```bash
RAGLAB_OLLAMA_MODEL=mxbai-embed-large:latest \
  go run ./cmd/raglab eval --pipeline v1-ollama --split development

RAGLAB_OLLAMA_MODEL=nomic-embed-text:latest \
  go run ./cmd/raglab eval --pipeline v1-ollama --split development
```

## 下一轮实验

1. 使用更适合中文与跨语言检索的 `qwen3-embedding:0.6b` 建立第三组真实模型基线。
2. 区分 Query Embedding 与 Document Embedding，评测检索任务指令或前缀带来的变化。
3. 保存逐 Case 的排名差异，定位语义改写失败是否来自模型、Chunk 内容或 Query 表达。
4. 增加 Embedding Cache，避免每次 CLI 启动都重建语料向量。
5. 在 V2 启用 Metadata Filter，在 V3 引入 BM25 + Vector Hybrid Retrieval。
