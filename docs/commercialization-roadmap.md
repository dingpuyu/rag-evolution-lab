# 商业化级企业 RAG 演进路线

## 1. 目标定义

“商业化级别”不是功能数量多，而是系统能够在真实组织内持续交付可衡量、可隔离、
可恢复的知识服务。至少需要同时满足：

1. **质量可证明**：有 Golden Dataset、线上反馈、回归评测和发布门禁；
2. **数据可治理**：文档来源、版本、权限、删除和索引状态全程可追踪；
3. **租户可隔离**：身份、权限、存储、缓存、日志和配额不能跨租户泄漏；
4. **服务可运营**：有监控、告警、限流、降级、成本计量和审计；
5. **故障可恢复**：任务可重试、索引可重建、数据可备份、发布可回滚；
6. **规模可验证**：所有容量和延迟结论来自可重复压测，而不是架构图。

本项目不把“部署一个开源 RAG 产品”当作完成商业化。RAGFlow 和 QAnything
用于建立参考基线，核心在线服务、权限边界、评测体系和数据生命周期仍由
`rag-evolution-lab` 自己掌控。

## 2. 三个系统的定位

| 系统 | 在本项目中的定位 | 重点学习 | 不直接照搬 |
|---|---|---|---|
| `rag-evolution-lab` | 商业核心与可讲述主项目 | Go 在线服务、Milvus、ACL、生命周期、评测、可观测性 | 不退化成开源 UI 的薄封装 |
| RAGFlow | 当前成熟产品参考系 | Parser、Chunk 预览、Dataset、Search、Agent、数据源和交互体验 | 不把其内部数据库和权限模型作为本项目真相源 |
| QAnything | 中文检索与解析参考系 | 两阶段检索、BCEmbedding/Rerank、混合检索、解析阶段可视化 | 不复制 AGPL-3.0 源码进入闭源商业核心 |

### 许可证边界

- 学习算法思想、公开接口行为和产品交互没有问题；
- 如果未来闭源商业化，不能直接复制 QAnything 的 AGPL-3.0 实现代码；
- 对外部组件建立独立进程或标准协议边界，并保存依赖版本、许可证和 NOTICE；
- 上线前必须做一次完整的依赖许可证扫描，模型权重许可证单独审查；
- 本文是工程边界，不代替正式法律意见。

## 3. 当前能力盘点

### 已具备

- V0～V5 可配置 RAG Pipeline 和失败案例驱动的演进过程；
- BM25、向量、Hybrid/RRF、Routing、Rerank 和 Context Packing；
- 真实 Qwen3 Embedding 与 Milvus Retriever；
- 10K、100K FLAT/HNSW 对照，包含 Recall、QPS、P50/P95/P99；
- OIDC/RS256、JWKS 轮换、Tenant/Role ACL、401/403 和审计；
- Pre-ANN 权限过滤与越权召回零容忍回归；
- 增量 Upsert、版本乱序拒绝、Tombstone、删除后零残留验证；
- Embedding 模型、版本、维度和 Content Hash 一致性门禁；
- Golden Dataset、确定性测试、评测报告和网页实验室。

### 尚未达到商业化验收

- 多格式解析、OCR、表格与父子 Chunk 还没有生产级流水线；
- 导入任务仍缺少独立 Worker、消息队列、死信队列和任务运营页面；
- 数据源 Connector、定时同步、Webhook 和增量游标尚未形成统一协议；
- API Gateway、API Key、租户配额、Token/Embedding/存储计量尚未闭环；
- 缓存、限流、熔断、超时、Fallback 和背压缺少故障注入证明；
- OpenTelemetry、指标、日志、Trace 和告警尚未接入统一观测面；
- 备份恢复、索引重建、灾难恢复和升级回滚尚未演练；
- 100K 已验证，但 1M 数据与持续增量写入尚未完成；
- 缺少真实业务 Blind Set、人工反馈和线上 A/B/Canary 机制。

## 4. 目标架构

```text
Client / Admin Console
        │
        ▼
API Gateway
  ├── OIDC / API Key
  ├── Tenant Quota / Rate Limit
  ├── Request Size / Content Policy
  └── Trace ID / Idempotency Key
        │
        ├──────────────── Query Plane ────────────────┐
        │                                             │
        ▼                                             ▼
RAG Orchestrator                                Evaluation Service
  ├── Query Router                                ├── Golden / Blind Set
  ├── Hybrid Retriever                            ├── Online Feedback
  ├── Reranker                                    ├── Regression Gate
  ├── Context / Citation Gate                     └── A/B / Canary
  ├── Generator
  └── Safety Verifier
        │
        ▼
Milvus + Metadata Store + Cache

Ingestion API
  ├── Source Connector / Upload
  ├── Outbox / Queue / DLQ
  └── Job State Machine
        │
        ▼
Parser Workers → Chunk Workers → Embedding Workers → Index Writer
        │                                             │
        └──────── Object Storage / Metadata DB ───────┘

统一观测面：OpenTelemetry + Metrics + Logs + Audit + Cost Ledger
```

### 控制面与数据面

商业化实现必须将两者分开：

- **控制面**：Tenant、用户、角色、知识库、数据源、Pipeline 版本、模型配置、
  API Key、配额、账单和发布策略；
- **数据面**：解析、Embedding、索引写入、检索、重排、生成和引用验证；
- 控制面只下发带版本的配置，数据面不能信任客户端传入的 Tenant 和 Role；
- 每个回答必须能反查知识版本、索引版本、模型版本、Pipeline 版本和 Trace。

## 5. 分阶段交付与验收

## Stage C1：参考系统对照实验

目标：把 RAGFlow、QAnything 的优点变成可验证需求，不做主观 UI 比较。

任务：

- RAGFlow、QAnything 和本项目导入同一批 Markdown/PDF；
- 固定 Embedding、Query、Top-K 和 Rerank 开关；
- 记录解析 Chunk、标题层级、表格保存率、引用与阶段耗时；
- 增加 BCE Reranker 或兼容服务作为 V5 的真实重排实验；
- 将产品差异转成 Golden Case 和失败用例。

验收：

- 至少 50 条 Development、30 条 Blind Query；
- 输出三系统统一格式的 Recall@5、MRR、NDCG、Citation Precision 和 P95；
- 所有结论能链接到输入、配置、Trace 和原始结果；
- QAnything 仅黑盒运行或独立参考，不复制其实现代码。

## Stage C2：生产级 Ingestion Plane

目标：任何文档变更都能被追踪、重试、取消和安全删除。

任务：

- 定义 `SourceDocumentEvent`、`IngestionJob`、`ChunkManifest`；
- Outbox → Queue → Worker，按阶段维护状态机；
- Parser、Chunk、Embedding、Index Writer 分离；
- 重试退避、DLQ、幂等键、租户级并发和背压；
- Markdown、PDF、DOCX、XLSX 基线，表格和标题层级进入 Fixture；
- Blue/Green Collection + Alias 原子切换。

验收：

- 进程在任意阶段退出后可恢复，不重复生成有效 Chunk；
- 同一事件重复投递 100 次只产生一次业务变更；
- 删除请求完成后，强一致查询和缓存均无残留；
- 失败任务能定位到阶段、错误分类、重试次数和源文档；
- 10 万文档持续增量期间在线查询无越权、无明显停顿。

## Stage C3：多租户控制面

目标：从“带 ACL 的检索服务”升级成可交付的企业产品。

任务：

- Tenant、Workspace、KnowledgeBase、DataSource、User、Group、Role；
- OIDC Group/Claim 到内部角色的映射与回收；
- 知识库、文档、Chunk 三级权限继承；
- API Key 生命周期、作用域、轮换和吊销；
- 租户配额：文件、Chunk、存储、QPS、Token、Embedding 调用；
- 管理后台展示任务、索引、用量、失败和审计事件。

验收：

- 跨租户访问在 API、缓存、检索、日志和导出五个层面均被阻止；
- 用户离组或权限回收后在规定时间内生效；
- 客户端伪造 Tenant/Role/KnowledgeBase ID 无效；
- 每次管理操作都有 Actor、Target、Before/After 和 Request ID；
- 配额超限返回稳定错误码，不把系统拖入 OOM。

## Stage C4：在线可靠性与成本

目标：模型或依赖故障时服务仍然行为可预测。

任务：

- Gateway 限流、请求大小、超时和幂等；
- Embedding、Milvus、Rerank、LLM 分阶段 Deadline；
- Circuit Breaker、隔离舱、降级与重试预算；
- Semantic Cache 与权限、模型、知识版本共同组成 Cache Key；
- Token、Embedding、Rerank、存储和出口流量计量；
- OpenTelemetry Trace、Prometheus 指标、结构化日志和告警。

验收：

- Milvus 超时能按 Pipeline 策略降级或明确失败；
- Reranker 故障不破坏原始候选顺序和权限；
- LLM 超时不会无限占用 Worker，客户端取消能向下游传播；
- 日志不包含密钥、完整私有文档或跨租户 Chunk；
- 能回答一次请求“慢在哪里、花了多少、用了哪个版本、为何降级”；
- 定义并验证查询、索引新鲜度和可用性 SLO。

## Stage C5：百万级与灾难恢复

目标：证明系统在目标规模下可扩展、可恢复，而不是追求单机漂亮 QPS。

任务：

- 1M Chunk 数据集，包含 Hard Negative、热点租户和过滤基数差异；
- 写入、更新、删除与查询混合负载；
- HNSW 参数、分片、Replica、内存和磁盘容量规划；
- Metadata DB、对象存储、Milvus 的备份与恢复；
- 索引损坏重建、节点退出、网络延迟和模型服务故障演练；
- 灰度发布 Pipeline/Embedding/Index，支持回滚。

验收：

- 报告 Recall@10、业务 Topic Precision、P50/P95/P99、Error Rate；
- ACL Hard Negative 在全量压测中仍为 0；
- 1%～10% 增量更新期间 Freshness Lag 满足目标；
- 从备份恢复后完成 Row Count、Manifest、权限和 Golden Query 对账；
- 明确 RPO、RTO、容量水位和扩容触发条件；
- 同配置重复压测质量稳定，性能波动可解释。

## 6. 商业化发布门禁

任何 Pipeline、模型或索引版本发布前必须满足：

```text
Unit / Contract / Integration
        ↓
Golden Regression
        ↓
Security & ACL Regression
        ↓
Blind Set Evaluation
        ↓
Load / Fault Test
        ↓
Canary Tenant
        ↓
Progressive Rollout
```

必须阻止发布的条件：

- 任意 Unauthorized Retrieval；
- 引用指向最终 Context 之外；
- 删除文档仍可召回；
- Embedding 维度或版本混用；
- Blind Set 关键指标跌破阈值；
- P95、错误率或单请求成本超过预算；
- 缺少可回滚的 Pipeline、模型或索引版本。

## 7. 面试中的项目表述

推荐表述：

> 我没有把 RAGFlow 或 QAnything 当作项目本身，而是把它们当作行业参考基线。
> 我用同一语料、同一查询集和同一 Embedding 做受控对比，把观察到的解析、两阶段
> 检索和可视化能力转成自己的失败用例。核心服务使用 Go，向量检索使用 Milvus，
> 权限在 ANN 前过滤；同时实现 OIDC、增量索引、删除一致性、评测门禁和规模压测。
> 这样既能利用成熟产品校准方向，又能证明我理解商业 RAG 的安全、质量和运维边界。

不能宣称：

- 100K 合成数据等于百万真实文档生产经验；
- 本机 QPS 等于服务器容量；
- 部署开源项目等于完成企业级架构；
- 有登录页面就等于完成多租户隔离；
- 接入 Reranker 就必然提升答案质量。

## 8. 下一步执行顺序

1. 完成 RAGFlow 同语料对照，不再扩展其部署组件；
2. 不急于完整安装 QAnything，先提取两阶段检索和解析可观测性验收项；
3. 为本项目接入真实 Reranker，复现“召回提高但排序仍差”的案例；
4. 建设异步 Ingestion Job 状态机和网页任务看板；
5. 接入 OpenTelemetry 和统一请求成本账本；
6. 扩展 100K Harness 到 1M，并增加混合读写与故障注入；
7. 最后再做 Kubernetes、HA 和商业管理后台，避免过早堆基础设施。
