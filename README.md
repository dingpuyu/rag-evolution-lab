# RAG Evolution Lab

一个可复现、可评测、可逐步演进的 RAG 工程实验室。

项目围绕同一套企业知识库、固定 Golden Dataset 和统一 Pipeline 接口，保留从关键词检索到高级检索系统的多个版本。每次演进都必须先复现问题，再通过自动化测试和量化指标证明优化有效。

## 项目目标

- 系统掌握 RAG 面试中的高频知识点。
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

当前处于设计阶段，已经完成：

- 场景与知识库结构设计
- Golden Dataset Schema
- 评测协议
- 系统架构
- 测试策略
- 演进里程碑

尚未开始业务实现。

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

## 预期命令

以下是目标接口，尚未实现：

```bash
raglab ingest --corpus datasets/corpus
raglab eval --pipeline v1-vector --split development
raglab compare --baseline v1-vector --candidate v3-hybrid
raglab inspect --query-id query_001
```

## License

本项目暂不指定开源许可证。在决定公开仓库前补充。
