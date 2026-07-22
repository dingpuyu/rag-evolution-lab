# 版本路线图

## Phase 0：设计基线

### 交付物

- 仓库与 Git 基线
- 架构文档
- 数据集规范
- Golden Schema
- 评测协议
- 测试策略

### 完成条件

- 所有文档互相引用一致。
- Schema 可以校验示例 Golden Case。
- 后续编码任务可以从文档直接拆分。

## Phase 1：V0 / V1 基线（已完成）

预计 3～4 天。

### 任务

- Go Module 和 CLI 骨架
- PostgreSQL Migration
- 文档导入
- 基础 Paragraph Chunker
- V0 Keyword
- V1 Vector
- Trace 骨架
- 20 条 Development Query
- 检索指标

### 需要证明

- Keyword 对错误码更稳定。
- Vector 对语义改写更有优势。
- Chunk Size 会影响 Recall 和 Context 成本。

### 实际结果

- V0 Keyword：Hit Rate@5 0.850，MRR 0.762。
- V1 Vector：Hit Rate@5 0.900，MRR 0.779。
- Keyword 对错误码、Header 等精确标识符排名更稳定。
- Vector 对语义改写更好，但精确标识符 MRR 明显下降。
- 两个版本均未解决无答案判定和噪声拒答。
- ACL 回归中 Unauthorized Retrievals 为 0。

## Phase 2：V2 / V3 检索优化

预计 4～5 天。

### 任务

- [x] Metadata Filter
- [x] Metadata Violations 评测指标
- [x] Header-aware Chunking
- 表格和代码块处理
- Parent / Child Chunk
- [x] Hybrid Retrieval
- [x] RRF
- [x] 去重
- 扩展到 60 条 Query

### 需要证明

- 版本过滤降低旧知识污染。
- Hybrid 同时改善精确词和语义问题。
- 召回提高可能伴随 Top-3 Precision 下降。

### Hybrid 实际结果

- Qwen3 Hybrid 的 Document Recall 从 0.875 提升到 0.900，但 MRR 从 0.850 降到 0.842。
- Hybrid + Metadata 保持 0 次 Metadata Violations，并将语义改写 Hit@5 从 0.750 提升到 1.000。
- 同一版本的 access-control Hit@5 从 1.000 降到 0.500，证明候选并集会损伤保守拒答。
- Consensus Gate 修复拒答，但语义改写回落到 0.750；该权衡将由 V4 Query Routing 继续处理。

## Phase 3：V4 / V5 高级 RAG

预计 4～5 天。

### 任务

- [x] Query Classification
- Query Rewrite
- [x] Retrieval Router
- Multi-query 实验
- [x] Reranker 工程接口与确定性基线
- [x] Context Packing 基线
- [x] Citation Verification 硬门禁
- 80 条完整 Golden Query

### 需要证明

- 不同 Query 应选择不同策略。
- Rerank 改善精度但增加延迟。
- 更长 Context 不必然带来更好答案。

### V4 实际结果

- 确定性分类器将 Query 路由为 exact、semantic、access-sensitive 和 unanswerable-risk。
- Development 20 条与 V4 Challenge 8 条均达到 Hit@5 / MRR / Recall@5 1.000，Metadata 与 Unauthorized Violations 为 0。
- 路由将 Development 上的 Vector Query 调用从 20 次降到 9 次。
- Challenge 首轮暴露租户同义表达误路由，并推动 Tenant Scope Gate 在检索前 Fail Closed。
- 当前 28 条均为小型合成数据，Blind Split 和 60 条规模扩充仍未完成。

### V5 首轮实际结果

- 已注册 `v5-rerank`，将 Router 候选池扩展至 20，再执行确定性 Rerank。
- 已将 Retrieval 与最终 Context 分离，支持 Chunk 数和估算 Token 双预算。
- 引用只能指向最终 Context，评测增加 Citation Violations 硬门禁。
- 增加 Precision@5、NDCG@5、Answerability Accuracy、P50/P95。
- Development 的 Hit/MRR/Recall/Precision 不变，NDCG 从 1.000 降至 0.996，P95 增加。
- 因现有数据存在天花板效应，V5 尚不能宣称质量优化完成；详见 Phase 5 报告。

## Phase 4：V6 / V7 进阶能力

预计 4～5 天，可在主项目后继续。

### 任务

- [x] 10K 确定性规模数据生成器
- [x] FLAT Ground Truth 与 HNSW Recall 对照
- [x] 10K Batch Upsert 与行数对账
- [x] Filter / ACL 并发 Benchmark 基线

- Query Decomposition
- Iterative Retrieval
- Evidence State
- Loop Guard
- 增量索引
- 多租户 ACL
- Prompt Injection 测试
- 缓存、超时与降级

### 10K规模基线结果

- 两个Collection各写入10,000行、1024维向量，唯一数据吞吐78.40 rows/s。
- 100条Query、并发8、三类过滤场景、`ef=32/64/128`均无请求错误。
- 合成主题簇上的HNSW Recall@10为1.000，ACL Unauthorized Retrievals为0。
- 当前数据分布较容易且每组仅100条Query，不能据此宣称真实业务Recall或稳定峰值QPS；下一步加入跨主题Hard Negative并扩大压测样本。

### 需要证明

- 多跳问题需要多轮证据收集。
- 迭代检索必须有停止条件。
- 质量优化不能破坏权限和安全边界。

## 首轮范围建议

首轮以 Phase 0～3 为正式目标，即完成 V0～V5。V6/V7 保留接口和数据案例，但不阻塞第一版实验闭环。

## 每阶段 Git 约定

推荐使用小提交：

```text
docs: define golden dataset schema
test: add exact-code retrieval failures
feat: implement keyword baseline
test: capture vector retrieval regression for E1027
feat: add reciprocal rank fusion
eval: compare vector and hybrid retrieval
```

每个版本打 Tag：

```text
v0.1.0-keyword
v0.2.0-vector
v0.3.0-structured
v0.4.0-hybrid
v0.5.0-routing
v0.6.0-rerank
```
