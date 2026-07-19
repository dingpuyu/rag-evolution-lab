# ADR-0006：用 Milvus Retriever 替换进程内向量召回

- 状态：Accepted
- 日期：2026-07-20

## 背景

进程内向量召回便于建立确定性基线，但需要启动时加载全部向量，并以 O(N) 逐条计算相似度，无法代表百万级 RAG 的存储、索引、过滤和运维形态。

## 决策

保留项目内部 `Retriever` 接口，新增 Milvus Adapter。生产形态 Pipeline 使用 Qwen3 Query Embedding、Milvus HNSW/COSINE 和 ANN 前 Scalar Filter；内存实现继续作为测试基线和受控对照组。

ACL 使用 nullable `Array<VarChar>` 保存 allowed tenants/roles。只有 Tenant 与 Role 同时命中才允许读取内部数据；缺少任一身份字段时 fail closed。Product、Version 与 Status 同样下推到 Milvus。

## 后果

- RRF、Router、Reranker、Context 和 Harness 不依赖具体向量库；
- Query trace 可以暴露 Collection、索引、metric、ef 和实际过滤表达式；
- 本地运行增加 Milvus、etcd、MinIO 和 schema migration 成本；
- 真实百万规模仍需单独压测，不能由 38 条数据的功能验证推断。
