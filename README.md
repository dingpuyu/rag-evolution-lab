# RAG Evolution Lab

一个可复现、可评测、可逐步演进的 RAG 工程实验室。

项目围绕同一套企业知识库、固定 Golden Dataset 和统一 Pipeline 接口，保留从关键词检索到高级检索系统的多个版本。每次演进都必须先复现问题，再通过自动化测试和量化指标证明优化有效。

## 快速部署

需要在一台机器上直接体验完整的 API、客服门户、PostgreSQL 控制面和 Milvus 向量库时：

```bash
cp .env.example .env
make stack-up
```

然后打开 <http://localhost:3000/portal>。默认档位使用确定性 Hash Embedding 和抽取式回答，不依赖外部模型；接入本地 Ollama 与 DeepSeek 的配置、端口冲突处理、数据卷和清理方式见 [一键部署指南](docs/quick-deploy.md)。

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

Phase 2 Metadata 实验已完成：

- `v2-metadata` 确定性 Product / Version / Lifecycle Filter
- `metadata_violations` 评测指标
- 显式旧版本查询与默认 Active-only 规则
- V0 与 V2 单变量对比报告

Phase 3 已完成首轮 Hybrid Retrieval 实验：

- BM25 与 Vector 并行 Candidate Retrieval
- Reciprocal Rank Fusion（RRF k=60）
- 候选池扩展、稳定 Chunk ID 去重
- Hybrid + Metadata 组合
- Union / Consensus 两种证据策略对照
- Qwen3-Embedding-4B 本地真实模型评测

Phase 4 Query Routing 已完成：

- Exact / Semantic / Access-sensitive / Unanswerable-risk 分类
- Metadata BM25 / Hybrid Union / Hybrid Consensus 动态路由
- Tenant Scope Gate 与结构化 Anchor Gate
- Route、Reason、Strategy Trace
- 8 条独立 V4 Challenge Query
- Development 与 Challenge 共 28 条 Case 全部通过

Phase 5 已完成首轮工程基线实验：

- 可替换 `Reranker` 接口与确定性 Heuristic Baseline
- Candidate Retrieval 与 Final Context 分离
- Token Budget Context Packing
- Citation 必须绑定最终 Context 的确定性门禁
- Precision@5、NDCG@5、Answerability、P50/P95 指标
- 首轮实验发现多跳 NDCG 从 1.000 轻微降至 0.996，未将 V5 标记为效果优化完成

10K Milvus Scale Harness 已完成首轮验证：

- 确定性生成 10,000 个 1024 维 Chunk，不落巨型向量 JSON
- FLAT 精确对照 Collection 与 HNSW 实验 Collection
- Batch Upsert、双 Collection Row Count 对账
- `ef=32/64/128`、三种过滤场景、并发检索
- Recall@10、QPS、P50/P95/P99、Error Rate
- Tenant/Role ACL Hard Negative，Unauthorized Retrievals 为 0
- 首轮报告见 [10K Scale Benchmark](eval/reports/scale-10k-latest.md)

100K Milvus Scale Harness 已完成 Hard-v2 验证：

- 100,000 个 1024 维 Chunk，1,000 个相邻语义主题，FLAT/HNSW 各 100,000 行
- 原子 Checkpoint、指数退避重试、断点续写与完成态幂等恢复
- Row Count + Index Finished + Indexed/Pending Rows 索引就绪门禁
- Warm-up、300 Query、`ef=16/32/64/128`、三种 Filter 场景
- Exact Recall@10 与 Topic Hit/Precision 双层质量解释
- 同参数重复压测的 12 组质量指标逐项一致，ACL 越权为 0
- 完整分析见 [100K Scale Benchmark](docs/scale-benchmark-100k.md)

企业身份与权限闭环已完成：

- 本地HS256演示模式与生产OIDC/RS256模式共用统一Verifier边界
- OIDC Discovery、JWKS缓存、`kid`选钥、未知Key刷新与密钥轮换
- 固定算法、Issuer、Audience、Expiration、Not Before和Issued At校验
- 服务端从可信Claims读取Subject、Tenant和Roles
- 客户端伪造Tenant/Role无效，未认证搜索返回401
- Viewer调用Admin场景返回403且不会访问Milvus
- Milvus Pre-ANN ACL与结构化Request ID审计
- 本地网页可切换预定义Persona；OIDC模式不注册本地Token签发接口
- 配置、信任边界和安全验证见 [企业RAG身份与审计](docs/enterprise-rag-security.md)
- 登录体验、数据集授权和跨租户验证见 [多租户数据集隔离实验](docs/dataset-isolation-lab.md)

面向业务体验的独立客服门户已完成：

- `/portal` 提供登录、知识库选择、Milvus 检索预览、流式回答与引用展示
- 租户管理员可创建带 `viewer` / `admin` 策略的知识库，并向自有租户库导入资料
- 权限与审计页面展示服务端 Claims、PostgreSQL membership、数据集策略和请求决策
- 详细启动方式与验收步骤见 [RAG Desk 企业智能客服门户](docs/customer-portal.md)

评测与业务数据已经支持隔离运行：

- 门户默认使用 `raglab_lifecycle_v1 / raglab_knowledge_active`
- 评测服务使用 `raglab_lifecycle_eval_v1 / raglab_knowledge_eval`
- `make serve-lab-eval` 启动隔离评测 API，`make dataset-eval-isolated` 和 `make answer-eval-blind-isolated` 不会读取门户导入的业务资料
- `make regression-smoke` 固化登录、租户隔离、检索 Filter、资料导入、SSE 和审计回归
- 详细协议见 [评测协议](docs/evaluation-protocol.md)
- 真实登录 → PostgreSQL 授权 → Milvus Filter → Top-K 结果的自动回归见
  [Dataset Search Harness 报告](eval/reports/dataset-search-latest.md)
- 隔离环境的 DeepSeek 答案评测见
  [Grounded Answer 隔离报告](eval/reports/grounded-answer-blind-isolated-latest.md)
- 确定性 Pipeline 的质量门禁可运行 `make eval-gate`；它会检查候选版本相对基线的
  Hit@K、MRR、NDCG 和安全指标，适合接入 CI
- 检索可靠性策略与故障演练见
  [检索可靠性与故障演练](docs/reliability-chaos.md)

PostgreSQL 多租户控制面首版已完成：

- Tenant、User、Membership、Dataset、Dataset Role与控制面审计表
- Advisory Lock保护的幂等自动迁移和默认演示数据
- Tenant Admin创建数据集时由服务端强制Owner、Product、Role和Status
- Membership撤权后立即失去Dataset访问，后续请求不会自动恢复
- PostgreSQL资源授权与Milvus Pre-ANN ACL组成两道隔离边界
- 真实数据库集成测试覆盖创建、跨租户拒绝、撤权和审计
- 设计与实测问题见 [PostgreSQL多租户控制面](docs/postgres-control-plane.md)

Grounded Answering 首版已完成：

- 独立 Dataset Answer API，复用 PostgreSQL 授权与 Milvus pre-ANN Filter
- Ollama `qwen3.5:9b`真实结构化生成和 JSON Schema 输出
- 服务端 Citation Allowlist 和 Context 引用重建
- 稳定拒答原因、无证据不调用模型、Prompt/Output Token 与生成耗时
- Prompt Injection Evidence 移除、响应脱敏和危险请求生成前拒绝
- 6 条真实回答 Harness 全部通过，禁止事实、引用违规和越权召回均为 0
- 增加 `answer/stream` SSE Token Streaming 与网页 Answer Lab，可观察 TTFT、生成、安全调整和最终引用
- 增加 OpenAI-compatible Generator，可通过环境变量切换 DeepSeek、OpenAI 或企业兼容网关
- Answer Harness 已支持 Provider/Model、Token、成本配置、SSE TTFT/Token Rate 和 Blind Split
- 当前 DeepSeek `deepseek-v4-pro` 原始集 6/6、Blind 集 8/8 通过，引用/越权/禁止事实均为 0
- 对模型空拒答增加确定性安全文案兜底，并将修正记录为 `refusal_answer_filled`
- 实现与失败实验见 [Grounded Answering](docs/grounded-answering.md)

增量知识生命周期首版已完成：

- `event_id`幂等、Pending/Completed持久化与安全重放
- `source_revision`乱序拒绝和删除Tombstone
- 新Chunk先Upsert、陈旧Chunk后Delete
- 删除后Strong Query验证零残留
- Content Hash、Embedding Model/Version和向量维度一致性门禁
- 独立物理Collection与Active Alias，避免影响现有实验数据
- 网页可执行写入、更新、Alias检索、删除和再次检索
- 算法、实测结果与生产Outbox边界见 [增量索引与删除一致性](docs/incremental-index-lifecycle.md)

当前基线结果见 [Phase 1 Baseline Report](eval/reports/phase1-baselines.md)。
本地真实模型的初始对比见 [Local Embedding Benchmark](eval/reports/local-embedding-benchmark.md)。
Metadata Filter 实验见 [Phase 2 Metadata Filter Report](eval/reports/phase2-metadata-filter.md)。
Hybrid Retrieval 的分类权衡见 [Phase 3 Hybrid Retrieval Report](eval/reports/phase3-hybrid-retrieval.md)。
Query Routing 与 Challenge Split 结果见 [Phase 4 Query Routing Report](eval/reports/phase4-query-routing.md)。
Rerank、Context 与引用门禁的首轮失败实验见 [Phase 5 Report](eval/reports/phase5-rerank-context.md)。
面试知识点与项目证据的对应关系见 [RAG 核心知识点验证矩阵](docs/interview-validation-matrix.md)。

## 文档导航

- [系统架构](docs/architecture.md)
- [知识库与数据集设计](docs/dataset-design.md)
- [评测协议](docs/evaluation-protocol.md)
- [五分钟 Demo Guide](docs/demo-guide.md)
- [Embedding 文字相似度实验 API](docs/embedding-similarity-api.md)
- [Milvus 本地向量数据库实验](docs/milvus-local-lab.md)
- [10K Milvus 规模验证设计与结果](docs/scale-benchmark-10k.md)
- [100K Milvus 规模验证与自我改进](docs/scale-benchmark-100k.md)
- [Milvus 100K 网页交互实验室](docs/milvus-100k-web-lab.md)
- [企业RAG身份、权限与审计闭环](docs/enterprise-rag-security.md)
- [多租户数据集隔离实验](docs/dataset-isolation-lab.md)
- [PostgreSQL多租户控制面](docs/postgres-control-plane.md)
- [企业RAG增量索引与删除一致性](docs/incremental-index-lifecycle.md)
- [企业RAG异步导入任务状态机](docs/async-ingestion-jobs.md)
- [Grounded Answering与回答质量门禁](docs/grounded-answering.md)
- [商业化级企业RAG演进路线](docs/commercialization-roadmap.md)
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
go run ./cmd/raglab compare --baseline v0-keyword --candidate v2-metadata
go run ./cmd/raglab compare --baseline v2-metadata --candidate v3-hybrid-metadata
go run ./cmd/raglab eval --pipeline v4-router --split development
go run ./cmd/raglab eval --pipeline v4-router --split v4-challenge
go run ./cmd/raglab compare --baseline v4-router --candidate v5-rerank --split development
go run ./cmd/raglab serve-embedding --backend hash
make milvus-up
make postgres-up
make milvus-seed
make serve-lab
make web-dev
make scale-100k
make answer-eval
make answer-eval-blind
make answer-eval-stream
make answer-eval-blind-stream
```

回答评测默认只统计 Token；不要把供应商价格写死在代码中。需要成本估算时，在运行命令前显式配置当前账单费率：

```bash
RAGLAB_PROMPT_COST_PER_1M_USD='input-rate' \
RAGLAB_COMPLETION_COST_PER_1M_USD='output-rate' \
make answer-eval-stream
```

### 使用 Ollama 本地 Embedding

默认测试与 CI 仍使用确定性的 Hash Embedder，不依赖本地服务。设置模型环境变量后，CLI 会额外注册 `v1-ollama` Pipeline：

```bash
# Modelfile 中写入：FROM /absolute/path/Qwen3-Embedding-4B-Q4_K_M.gguf
ollama create qwen3-embedding:4b-local -f Modelfile

RAGLAB_OLLAMA_MODEL=qwen3-embedding:4b-local \
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

当前 Qwen3 4B Q4_K_M 实测为 Hit Rate@5 `0.900`、MRR `0.850`、Document Recall@5 `0.875`。两种 Query Instruction 改变了部分候选排序，但没有改变总体质量指标，说明 Instruction 也必须经过目标数据集评测。

### Embedding 文字相似度接口

本机已经创建 `qwen3-embedding:4b-local` 时，可以启动真实模型实验接口：

```bash
RAGLAB_OLLAMA_MODEL=qwen3-embedding:4b-local \
  go run ./cmd/raglab serve-embedding --backend ollama
```

接口地址为 `POST /api/v1/embeddings/similarity`，返回向量维度、向量预览、L2 Norm、Cosine、Dot Product、Euclidean Distance 和耗时。完整说明与网页使用方式见 [Embedding API 文档](docs/embedding-similarity-api.md)。

### Milvus 向量数据库实验

项目提供固定版本的 Milvus Standalone 本地部署、Qwen3 真实向量写入、HNSW/COSINE 检索、Tenant/Role/Product/Status 标量过滤，并把原有内存 Vector Retriever 替换为完整 RAG Pipeline 中的 Milvus Retriever：

```bash
make milvus-up
make milvus-status
make milvus-seed
make serve-lab
make web-dev
```

```bash
make query-milvus
make eval-milvus

# 企业数据集搜索：相关性 + 跨租户零泄漏
make dataset-eval
make compare-milvus
```

网页的 `Milvus Lab` 会显示 Collection 行数、向量维度、索引类型、Load State、过滤表达式、Top-K Chunk 和 Embedding/Search 分阶段耗时。`v1/v3/v4/v5-milvus` 复用同一套 Routing、Rerank、Context Packing、Citation 和 Evaluation Harness。架构、Schema、面试知识点和百万级演进方向见 [Milvus Retriever 升级](docs/milvus-retriever-upgrade.md) 与 [Milvus 本地实验](docs/milvus-local-lab.md)。

同一页面还提供 `100K Lab`：直接连接100K FLAT/HNSW双Collection，可切换Topic、Filter、Top-K和`ef`，现场比较Exact Recall、Topic Precision、Ground Truth与查询延迟。启动与讲解方式见 [Milvus 100K 网页实验室](docs/milvus-100k-web-lab.md)。

也可以使用：

```bash
make test
make validate
make compare
make compare-metadata
make compare-routing
make compare-rerank
```

## Phase 1 基线

| Pipeline | Hit Rate@5 | MRR | Document Recall@5 | Unauthorized Retrievals |
|---|---:|---:|---:|---:|
| V0 Keyword | 0.850 | 0.762 | 0.850 | 0 |
| V1 Vector | 0.900 | 0.779 | 0.875 | 0 |

## Phase 2 Metadata Filter

| Pipeline | Hit Rate@5 | MRR | Document Recall@5 | Metadata Violations |
|---|---:|---:|---:|---:|
| V0 Keyword | 0.850 | 0.762 | 0.850 | 41 |
| V2 Metadata | 0.900 | 0.900 | 0.900 | 0 |

这个实验保持 BM25、语料、切块和评测集不变，只增加检索前的 Product、Version 和 Lifecycle 约束。它说明 ACL 安全不等于知识有效：V0 虽然没有跨租户泄漏，Top-K 中仍有 41 次 Metadata 污染。

## Phase 4 Query Routing

| Split | Cases | Hit Rate@5 | MRR | Document Recall@5 | Metadata / Unauthorized |
|---|---:|---:|---:|---:|---:|
| Development | 20 | 1.000 | 1.000 | 1.000 | 0 / 0 |
| V4 Challenge | 8 | 1.000 | 1.000 | 1.000 | 0 / 0 |

Development 中 11 条 Exact Query 直接使用 Metadata BM25，只有 9 条进入包含 Vector 的策略。当前满分只代表 28 条合成 Case 全部通过，不代表生产泛化；下一步会扩展到 60 条并增加不参与规则迭代的 Blind Split。

## License

本项目暂未指定开源许可证。
