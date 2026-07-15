# Architecture Decision Records

本目录记录重要且难以从代码直接理解的技术决策。

## ADR 列表

- [ADR-0001：使用 Go 作为在线服务主语言](0001-go-main-language.md)
- [ADR-0002：使用 PostgreSQL 同时承担全文与向量检索](0002-postgres-first.md)
- [ADR-0003：Pipeline 版本使用配置组合而不是代码复制](0003-configured-pipelines.md)
- [ADR-0004：Hybrid Retrieval 使用 Reciprocal Rank Fusion](0004-use-rrf-for-hybrid-fusion.md)

## 状态

- Proposed
- Accepted
- Superseded
- Rejected

后续如果更换向量库、引入 Elasticsearch、使用外部 Reranker 或接入 Langfuse，应新增 ADR，而不是直接修改历史决策。
