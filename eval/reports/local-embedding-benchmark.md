# Local Embedding Benchmark

## 目标

在不使用云端 API 和外部凭据的情况下，将真实 Embedding 模型接入现有 RAG Pipeline，并与 V0 关键词检索、V1 确定性 Hash Vector 保持相同语料、切块、权限规则和 Golden Dataset。

本轮是 Naive Vector Baseline：文档标题与正文直接拼接，不做混合检索、Metadata Filter 与 Rerank。除无指令基线外，Qwen3 额外进行两组仅作用于 Query 的检索指令实验，用于验证 Instruction 是否真的改善目标语料，而不是假定它一定有效。

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
| V1 Ollama / Qwen3-Embedding-4B Q4_K_M | **0.900** | **0.850** | **0.875** | 0 |
| Qwen3 4B + Web Search Instruction | 0.900 | 0.850 | 0.875 | 0 |
| Qwen3 4B + Chinese KB Instruction | 0.900 | 0.850 | 0.875 | 0 |

### 分类表现

| Model | Exact Match | Semantic Paraphrase | Table Numeric | Multi-hop | Version Filter |
|---|---:|---:|---:|---:|---:|
| mxbai-embed-large | 1.000 | 0.500 | 1.000 | 1.000 | 0.667 |
| nomic-embed-text v1.5 | 1.000 | 0.250 | 1.000 | 1.000 | 0.667 |
| Qwen3-Embedding-4B Q4_K_M | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |

表格中的分类数值为 Hit Rate@5。

## 观察

1. Qwen3 4B 在中文语义改写分类达到 1.000，弥补了较早两个本地模型最明显的短板；其 MRR 0.850 也高于确定性 Hash Vector 的 0.779。
2. 更强的模型仍没有解决系统层问题：`access_002` 缺少授权且相关的证据，`unanswerable_001` 缺少拒答机制，这两条仍失败。
3. 两种 Query Instruction 都改变了候选排序（分别影响 7 和 8 个 Case），但总体指标完全不变；Metadata Violations 还从 37 增到 40。Instruction 因此应作为受评测的配置，而不是默认优化项。
4. 精确错误码、表格数值和多跳问题保持较高召回，但这不代表纯向量检索是最佳策略；后续仍需要用 Hybrid Retrieval 验证互补收益。
5. 权限泄漏保持为 0，说明外部 Embedder 的引入没有绕过检索前的 ACL 过滤；与此同时，Metadata 污染仍然存在，说明 ACL 安全不等于知识有效。

## 可复现命令

```bash
RAGLAB_OLLAMA_MODEL=mxbai-embed-large:latest \
  go run ./cmd/raglab eval --pipeline v1-ollama --split development

RAGLAB_OLLAMA_MODEL=nomic-embed-text:latest \
  go run ./cmd/raglab eval --pipeline v1-ollama --split development

RAGLAB_OLLAMA_MODEL=qwen3-embedding:4b-local \
  go run ./cmd/raglab eval --pipeline v1-ollama --split development

RAGLAB_OLLAMA_MODEL=qwen3-embedding:4b-local \
RAGLAB_QUERY_INSTRUCTION="Given a Chinese enterprise knowledge-base query, retrieve relevant passages that answer the query" \
  go run ./cmd/raglab eval --pipeline v1-ollama --split development
```

## 下一轮实验

1. 在 V3 引入 BM25 + Vector Candidate Fusion，并用 RRF 消除不同分数空间的尺度差异。
2. 保存 Hybrid 的逐 Case 排名变化，重点观察精确标识符、语义改写与多跳问题是否互补。
3. 为无答案 Query 增加相似度阈值实验和 Answerability Gate，避免“总能召回”被误当作“应该回答”。
4. 将 Metadata Filter 与 Qwen3 Vector 组合，验证 37 次知识有效性污染能否归零且不损伤语义召回。

## 工程跟进

初始实验后已经补充以下能力：

- Query 与 Document 使用独立编码入口，检索 Instruction 只添加到 Query。
- Document Embedding 使用模型名和完整语料内容生成 SHA-256 缓存键。
- 模型或 Chunk 内容变化时缓存自动失效，非法或损坏缓存自动重建。
- Query 保持实时编码，不把线上 Query 写入磁盘。

使用 `mxbai-embed-large` 做本机集成验证时，首次完整运行约 3.40 秒，缓存命中后约 1.37 秒，两次评测指标完全一致。该时间包含 Go CLI 启动与 20 次 Query 编码，只用于确认缓存路径有效，不作为正式性能 Benchmark。

本次 Qwen3 模型为 4.0B 参数、Q4_K_M 量化、约 2.5 GB，Embedding 维度为 2560。首次运行（含语料编码）约 12.40 秒，语料缓存命中后的 Instruction 实验约 4.34 秒。模型文件 SHA-256 为 `2b0cf8f17b4c723c27303015383c27ec4bf2d8314bb677d05e920dd70bb0f16b`。
