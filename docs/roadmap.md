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

- Header-aware Chunking
- 表格和代码块处理
- Parent / Child Chunk
- Metadata Filter
- Hybrid Retrieval
- RRF
- 去重
- 扩展到 60 条 Query

### 需要证明

- 版本过滤降低旧知识污染。
- Hybrid 同时改善精确词和语义问题。
- 召回提高可能伴随 Top-3 Precision 下降。

## Phase 3：V4 / V5 高级 RAG

预计 4～5 天。

### 任务

- Query Classification
- Query Rewrite
- Retrieval Router
- Multi-query 实验
- Reranker
- Context Packing
- Citation Verification
- 80 条完整 Golden Query

### 需要证明

- 不同 Query 应选择不同策略。
- Rerank 改善精度但增加延迟。
- 更长 Context 不必然带来更好答案。

## Phase 4：V6 / V7 进阶能力

预计 4～5 天，可在主项目后继续。

### 任务

- Query Decomposition
- Iterative Retrieval
- Evidence State
- Loop Guard
- 增量索引
- 多租户 ACL
- Prompt Injection 测试
- 缓存、超时与降级

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
