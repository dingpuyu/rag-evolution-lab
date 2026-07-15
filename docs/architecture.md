# 系统架构

## 1. 架构目标

系统需要同时支持实验可比性和后续工程化：

- 所有 Pipeline 共享统一输入输出协议。
- Pipeline 版本通过组件组合和配置表达。
- 每个阶段可独立运行、评测和回归。
- 所有中间步骤可追踪。
- 检索与生成解耦，能够分别评测。
- 外部模型、Embedding 和 Reranker 可替换。

## 2. 总体结构

```text
Client / CLI
    │
    ▼
Query API
    │
    ▼
Pipeline Registry ─────────────── Pipeline Config
    │
    ▼
Query Processor
    │
    ├── Intent / Risk Classifier
    ├── Metadata Extractor
    └── Query Rewriter
    │
    ▼
Retrieval Router
    ├── Exact → Metadata BM25
    ├── Semantic → Hybrid Union
    ├── Access → Tenant Gate + Consensus
    └── Risk → Anchor Gate + Consensus
    │
    ▼
Retriever
    ├── Keyword Retriever
    ├── Vector Retriever
    ├── Hybrid Fusion
    └── Retrieval Router
    │
    ▼
Reranker
    │
    ▼
Context Builder
    ├── Deduplication
    ├── Parent Expansion
    ├── Token Budget
    └── Citation Mapping
    │
    ▼
Generator
    │
    ▼
Answer Verifier
    │
    ▼
Answer + Citations + Trace
```

## 3. 统一领域模型

```go
type QueryRequest struct {
    Query      string
    Pipeline   string
    TenantID   string
    UserRole   string
    Product    string
    Version    string
}

type QueryResponse struct {
    Answer       string
    Answerable   bool
    Citations    []Citation
    Retrieval    []RetrievedChunk
    TraceID      string
    Usage        TokenUsage
}

type Pipeline interface {
    Name() string
    Query(ctx context.Context, req QueryRequest) (*QueryResponse, error)
}
```

具体接口在实现阶段通过测试驱动细化，设计文档只约束职责，不提前锁死代码形式。

## 4. Pipeline 版本管理

不为每个版本复制一份完整代码。公共组件放在 `internal/`，版本差异通过配置表达：

```text
configs/pipelines/
  v0-keyword.yaml
  v1-vector.yaml
  v2-structured.yaml
  v3-hybrid.yaml
  v4-routing.yaml
  v5-rerank.yaml
  v6-iterative.yaml
  v7-production.yaml
```

示例：

```yaml
name: v3-hybrid
query_processor:
  metadata_filter: true
retrieval:
  keyword:
    enabled: true
    top_k: 20
  vector:
    enabled: true
    top_k: 20
  fusion:
    type: rrf
    k: 60
rerank:
  enabled: false
context:
  top_k: 6
  token_budget: 4000
```

这样可以：

- 避免版本代码分叉。
- 让实验配置进入 Git。
- 精确复现实验。
- 方便做单变量对比。

## 5. 数据存储

### PostgreSQL

负责：

- 文档和 Chunk Metadata
- pgvector Embedding
- PostgreSQL Full Text Search
- Pipeline 配置版本引用
- Query Trace
- Evaluation Run

### 文件系统

负责：

- 原始语料
- Golden Dataset
- 固定测试 Fixture
- 评测报告

第一版不引入 Elasticsearch、Redis 和消息队列。只有评测证明 PostgreSQL 成为瓶颈后才增加组件。

## 6. Trace 模型

每次查询记录以下阶段：

```text
query_received
query_classified
query_rewritten
metadata_extracted
keyword_retrieved
vector_retrieved
results_fused
results_reranked
context_packed
answer_generated
answer_verified
query_completed
```

每个 Span 至少记录：

- 输入摘要
- 输出摘要
- Pipeline 版本
- 模型和索引版本
- 耗时
- Token
- 错误与降级策略

不得把密钥、完整敏感文档和跨租户内容写入日志。

## 7. 依赖倒置

对外部能力定义小接口：

- `Embedder`
- `Classifier`
- `Generator`
- `Reranker`
- `KeywordIndex`
- `VectorIndex`
- `TraceExporter`

单元测试使用 Fake 实现，集成测试才连接 PostgreSQL，模型 Contract Test 才调用真实或录制的模型响应。

## 8. 失败与降级

| 失败 | 默认策略 |
|---|---|
| Embedding 失败 | 返回明确错误，不缓存空向量 |
| Vector Retriever 超时 | Hybrid 版本降级到 Keyword |
| Keyword Retriever 超时 | Hybrid 版本降级到 Vector |
| Reranker 失败 | 保留 Fusion 排序 |
| Query Rewrite 非法 | 使用原始 Query |
| Generator 非法引用 | 拒绝返回未验证引用 |
| 权限上下文缺失 | Fail Closed |

## 9. 非目标

第一阶段不建设：

- 通用 Agent 框架
- 多 Agent
- 模型训练平台
- 自研向量数据库
- Kubernetes 集群
- 通用低代码工作流

## 10. 预期仓库结构

```text
cmd/
  server/
  raglab/
configs/
  pipelines/
datasets/
  corpus/
  golden/
  fixtures/
docs/
  adr/
eval/
  scripts/
  reports/
internal/
  ingest/
  retrieval/
  rerank/
  context/
  generation/
  security/
  trace/
  evaluation/
tests/
  integration/
```
