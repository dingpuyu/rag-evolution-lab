# RAG 核心知识点验证矩阵

> 目的：把面试中的抽象回答绑定到仓库里的代码、测试、数据、命令和实验报告。
>
> 状态含义：`已验证` 表示已有可重复实验；`部分验证` 表示已有骨架或小规模证据；`待验证` 表示不能在面试中宣称已经完成。

## 验证矩阵

| 编号 | 核心问题 | 状态 | 当前项目证据 | 下一步验收 |
|---|---|---|---|---|
| R1 | 生产 RAG 完整链路 | 部分验证 | `docs/architecture.md`；Pipeline Trace 已覆盖路由、检索、Rerank、Context Packing、生成和引用校验 | 接入真实 Generator、Answer Verifier 与 OpenTelemetry |
| R2 | RAG、长上下文、Prompt、微调如何选 | 部分验证 | `docs/roadmap.md` 与 ADR 体现按问题演进；当前使用 RAG 解决动态私有知识 | 增加同一任务的长上下文成本/质量对照实验 |
| R3 | 文档解析、清洗与分块 | 部分验证 | `internal/ingest/chunker.go` 及单测；Header-aware Markdown Chunk | 增加表格、代码块、Parent/Child 和 Chunk 参数消融实验 |
| R4 | Embedding 选型与评估 | 已验证 | Hash Embedder 保证 CI；Ollama + Qwen3-Embedding-4B 真实评测；内容哈希缓存 | 扩展中英文、长文本和不同维度/量化版本的对照集 |
| R5 | 百万级 Chunk 与向量索引 | 待验证 | 有 PostgreSQL + pgvector Migration 和架构设计，但运行时仍为内存索引、38 Chunk | 数据生成器、批量导入、HNSW/IVF 参数实验、容量与 P95 报告 |
| R6 | 混合检索、过滤与融合 | 已验证 | BM25 + Vector + RRF；Metadata、ACL、Union/Consensus 对照及 Phase 2/3 报告 | 迁移到真实 Postgres FTS + pgvector 并验证并发性能 |
| R7 | Query 理解、改写与路由 | 部分验证 | Exact/Semantic/Access/Risk 分类、Router、Tenant/Anchor Gate、Route Trace | Query Rewrite、Multi-query、实体保护和 Blind Split |
| R8 | Rerank 的必要性、选型和延迟 | 已验证首轮基线 | `v5-rerank`、可替换 `Reranker` 接口、确定性 Heuristic、单测和 V4/V5 对比 | 接入 Cross-Encoder/远程 Rerank；按 Route 选择性启用；修复多跳 NDCG 退化 |
| R9 | 可复现 RAG 评估 | 已验证基础 | Golden Dataset；Hit@5、MRR、Recall@5、Precision@5、NDCG@5、Answerability、P50/P95、安全指标 | 扩展到 60/80 Query、独立 Blind Split、置信区间和线上反馈集 |
| R10 | 幻觉、无答案和错误引用 | 部分验证 | Anchor/Consensus Gate；无答案 Case；引用只能指向最终 Context；Citation Violations 门禁 | 真实生成模型、Required/Forbidden Facts、Faithfulness 和 Prompt Injection 生成测试 |
| R11 | 文档增删改和索引一致性 | 待验证 | 只存在路线设计，尚无可运行增量索引 | 版本化索引、幂等 Upsert、Delete、双写/切换和陈旧结果测试 |
| R12 | 扩展性、可观测性与成本 | 部分验证 | Query Trace、Embedding Cache、P50/P95、候选数、Context Token 估算 | OpenTelemetry、压测、超时降级、缓存命中率、Token/金额和资源水位 |

## 本轮新增的可验证知识点

### 1. Candidate Retrieval 与 Final Context 必须分开

`v5-rerank` 最多召回 20 个候选，经 Rerank 后返回 Top-K，再由 Context Packer 按 Chunk 数和 Token Budget 选择最终上下文。这样可以分别回答：

- 召回是否足够广？
- 排序是否把正确证据提前？
- 最终送入模型的证据是否超预算？
- 引用是否真的来自模型看到的上下文？

### 2. Rerank 必须保留无模型基线

`internal/rerank.Heuristic` 是确定性的 CI Baseline，不冒充 Cross-Encoder。生产 Reranker 可以通过同一接口替换。基线的意义是：

- 外部模型不可用时仍能运行回归。
- 能将“增加 Rerank 阶段”的工程开销和“模型效果”分开测量。
- 发现 Rerank 对不同 Query 类型的回归。

### 3. Context Budget 是正确性约束，不只是成本优化

Context Packer 当前实现：

- 稳定顺序去重；
- 最大 Chunk 数；
- 中英文混合 Token 估算；
- 首个超长 Chunk 截断；
- Trace 记录候选数、选中数、Token 和截断状态。

估算 Token 只用于确定性预算，实际计费必须使用模型 Provider 返回的 Usage。

### 4. Citation 必须绑定最终 Context

引用校验不允许 Generator 引用“曾经召回但没有进入上下文”的 Chunk，也会核对 Chunk 与 Document 的对应关系。该规则是确定性安全门禁，不交给 LLM Judge。

### 5. 平均指标不足以评价排序

本轮增加：

- `Precision@5`：Top-5 中相关证据的密度；
- `NDCG@5`：多个相关证据的排序质量；
- `Answerability Accuracy`：有答案/无答案判断；
- `P50/P95`：性能分布；
- `Citation Violations`：引用越界硬门禁。

无答案 Case 的 Precision/NDCG 在正确返回空结果时记为 1，用来表达“没有召回噪声”这一目标；报告中必须同时保留 Answerability，避免和普通相关性指标混淆。

## 本轮实验结论

命令：

```bash
go run ./cmd/raglab compare \
  --baseline v4-router \
  --candidate v5-rerank \
  --split development

go run ./cmd/raglab compare \
  --baseline v4-router \
  --candidate v5-rerank \
  --split v4-challenge
```

Development：

| Pipeline | Hit@5 | MRR | Recall@5 | Precision@5 | NDCG@5 | P95 |
|---|---:|---:|---:|---:|---:|---:|
| V4 Router | 1.000 | 1.000 | 1.000 | 0.310 | 1.000 | 约 0.05–0.10ms |
| V5 Heuristic Rerank | 1.000 | 1.000 | 1.000 | 0.310 | 0.996 | 约 0.17–0.19ms |

V4 Challenge：两者的五项质量指标一致；两轮本机采样均显示 V5 P95 更高。微秒级数据只证明评测链路能够观察额外开销，不用于生产容量判断。

结论：

1. 当前 28 条小型合成数据已经被 V4 路由规则充分拟合，无法证明 Rerank 的正收益。
2. Heuristic Rerank 在 Development 多跳 Case 上调整了次要相关证据的顺序，造成 NDCG 轻微下降。
3. Rerank 增加了可测量延迟，即便内存小数据下绝对值很小。
4. 下一轮不能调规则“追分”，而应新增未参与开发的 Rerank Challenge/Blind Split，并接入真正的 Cross-Encoder 对照。

这是有效的失败实验：证明了“增加高级组件”不等于系统效果必然提升。

## 面试展示顺序

1. 先展示 V0/V1：Keyword 与 Vector 的不同失败类型。
2. 展示 V2：Metadata 将污染从 41 降为 0。
3. 展示 V3：Hybrid 改善语义召回，但伤害保守拒答。
4. 展示 V4：Router 按风险选择策略并减少 Vector 调用。
5. 展示 V5：Rerank 未提升已饱和数据，反而暴露 NDCG 与延迟回归。
6. 最后说明下一实验如何避免评测集过拟合。
