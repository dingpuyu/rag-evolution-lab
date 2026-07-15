# ADR-0002：使用 PostgreSQL 同时承担全文与向量检索

- 状态：Accepted
- 日期：2026-07-15

## 背景

项目需要关键词、向量、Metadata Filter 和 Hybrid Retrieval，但初始数据只有数百个 Chunk。

## 决策

第一版使用 PostgreSQL Full Text Search + pgvector，不引入 Elasticsearch 或独立向量数据库。

## 原因

- 减少部署和调试成本。
- 能在同一事务和权限模型中管理文档与 Metadata。
- 足以支撑初始实验规模。
- 后续可以通过评测数据判断是否需要拆分。

## 影响

- 需要明确记录 PostgreSQL 分词对中文语料的限制。
- 如果关键词检索质量成为瓶颈，再以实验方式引入 Elasticsearch，而不是预先增加组件。
