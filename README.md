# RAG Evolution Lab

一个可复现、可评测、可逐步演进的 RAG 工程实验室。

项目围绕同一套企业知识库、固定 Golden Dataset 和统一 Pipeline 接口，保留从关键词检索到高级检索系统的多个版本。每次演进都必须先复现问题，再通过自动化测试和量化指标证明优化有效。

## 项目目标

- 系统探索 RAG 的核心原理、常见问题和工程实践。
- 建立可解释的失败案例，而不是只展示成功 Demo。
- 比较关键词、向量、混合检索、查询变换、重排和迭代检索。
- 同时评估质量、延迟、Token 和成本。
- 为后续 InsightAgent 提供可复用的检索与评测模块。

## 演进路线

| 版本 | 核心能力 | 主要解决的问题 |
|---|---|---|
| V0 | Keyword Baseline | 建立低成本、可解释基线 |
| V1 | Naive Vector RAG | 处理语义改写和同义表达 |
| V2 | Structured Chunking + Metadata | 处理结构破坏、版本冲突和权限过滤 |
| V3 | Hybrid Retrieval | 兼顾精确词与语义召回 |
| V4 | Query Transformation + Routing | 针对不同 Query 选择检索策略 |
| V5 | Rerank + Context Engineering | 提升 Top-K 精度并控制上下文预算 |
| V6 | Iterative Retrieval | 处理多跳问题与证据不足 |
| V7 | Production RAG | 增量索引、安全、可观测性和故障降级 |

## 设计原则

1. 同一数据集、同一评测协议、统一接口。
2. Pipeline 版本通过配置和组件组合表达，避免复制整套实现。
3. 每次优化必须对应明确的失败类型。
4. 检索层和生成层分别评测。
5. 确定性指标优先，LLM-as-a-Judge 只作为补充。
6. 所有效果数字必须由可复现的评测命令生成。
7. 权限泄漏和注入攻击属于零容忍回归项。

## 当前状态

Phase 1 已完成：

- Go CLI 与统一 Pipeline 接口
- 13 篇 AcmeCloud 合成文档
- 20 条 Development Golden Query
- 基础 Markdown Chunker，共生成 38 个 Chunk
- V0 Keyword Baseline
- V1 Deterministic Vector Baseline
- 可选 Ollama 本地 Embedding 后端
- Tenant / Role ACL
- Query Trace
- Hit Rate、MRR 和 Document Recall 评测
- PostgreSQL + pgvector Migration
- 单元测试与 GitHub Actions CI

当前基线结果见 [Phase 1 Baseline Report](eval/reports/phase1-baselines.md)。
本地真实模型的初始对比见 [Local Embedding Benchmark](eval/reports/local-embedding-benchmark.md)。

## 文档导航

- [系统架构](docs/architecture.md)
- [知识库与数据集设计](docs/dataset-design.md)
- [评测协议](docs/evaluation-protocol.md)
- [测试策略](docs/testing-strategy.md)
- [版本路线图](docs/roadmap.md)
- [架构决策记录](docs/adr/README.md)
- [数据目录说明](datasets/README.md)

## 计划技术栈

- 在线服务：Go
- 数据库：PostgreSQL + pgvector
- 全文检索：PostgreSQL Full Text Search
- 单元测试：Go `testing`
- 集成测试：Testcontainers for Go
- 评测分析：Python + Pandas
- 部署：Docker Compose
- 可观测性：结构化 Trace，后续接入 OpenTelemetry / Langfuse

## 本地运行

```bash
go test ./...
go run ./cmd/raglab validate
go run ./cmd/raglab ingest
go run ./cmd/raglab query --pipeline v0-keyword --query "E1027 是什么错误？"
go run ./cmd/raglab eval --pipeline v1-vector --split development
go run ./cmd/raglab compare --baseline v0-keyword --candidate v1-vector
```

### 使用 Ollama 本地 Embedding

默认测试与 CI 仍使用确定性的 Hash Embedder，不依赖本地服务。设置模型环境变量后，CLI 会额外注册 `v1-ollama` Pipeline：

```bash
ollama pull qwen3-embedding:0.6b
RAGLAB_OLLAMA_MODEL=qwen3-embedding:0.6b \
  go run ./cmd/raglab eval --pipeline v1-ollama --split development
```

Ollama 服务不在默认地址时，可以设置 `RAGLAB_OLLAMA_URL`。项目通过 `/api/embed` 批量构建语料向量，并对返回数量、维度和 HTTP 错误做校验。

真实模型的语料向量会按“模型名称 + 完整 Chunk 内容”生成内容哈希并缓存到 `data/cache/embeddings`。修改模型或语料后缓存自动失效，Query 仍实时编码。

对于支持检索指令的模型，可以只在 Query 侧增加 Instruction，Document 保持原文：

```bash
RAGLAB_OLLAMA_MODEL=qwen3-embedding:4b-local \
RAGLAB_QUERY_INSTRUCTION="Given a Chinese enterprise knowledge-base query, retrieve relevant passages that answer the query" \
  go run ./cmd/raglab eval --pipeline v1-ollama --split development
```

建议先运行一次无 Instruction 基线，再运行上面的实验，通过相同 Golden Dataset 比较变化。

也可以使用：

```bash
make test
make validate
make compare
```

## Phase 1 基线

| Pipeline | Hit Rate@5 | MRR | Document Recall@5 | Unauthorized Retrievals |
|---|---:|---:|---:|---:|
| V0 Keyword | 0.850 | 0.762 | 0.850 | 0 |
| V1 Vector | 0.900 | 0.779 | 0.875 | 0 |

这些结果不是目标上限。第一阶段刻意保留了语义改写、精确标识符排名、过期知识、无答案和噪声拒答等失败，用于驱动后续版本。

## License

本项目暂不指定开源许可证。在决定公开仓库前补充。
