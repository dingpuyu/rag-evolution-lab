# Milvus Retriever：从实验数据库到 RAG 主链路

## 1. 为什么这次升级比单独的 Milvus Demo 更重要

之前的网页已经能调用 Qwen3 Embedding 并查询 Milvus，但正式 RAG Pipeline 仍使用进程内向量数组逐条计算 Cosine。那只能证明“会使用向量数据库”，不能证明“能把向量数据库接入可评测的生产链路”。

本次保持 `retrieval.Retriever` 接口不变，新增 Milvus Adapter，把向量召回替换为外部持久化 ANN 检索。Router、Hybrid RRF、Reranker、Context Packing、Citation Verification、Trace 和 Golden Dataset Harness 全部复用。

```text
Query + server-side auth context
        │
        ├── BM25 Retriever ───────────────┐
        │                                 ├── RRF → Router → Rerank → Context → Citation
        └── Qwen3 Query Embedding         │
                └── Milvus HNSW + ACL ────┘
```

可运行的演进版本：

| Pipeline | 作用 |
|---|---|
| `v1-milvus` | 纯 Milvus HNSW 向量召回 |
| `v3-milvus-hybrid` | BM25 与 Milvus 结果经 RRF 融合 |
| `v3-milvus-hybrid-metadata` | Product、Version、Status 与 ACL 在召回前过滤 |
| `v3-milvus-hybrid-metadata-consensus` | 只保留多路共同命中的证据 |
| `v4-milvus-router` | 根据 Query 意图和风险选择检索策略 |
| `v5-milvus-rerank` | 候选扩召后重排、预算化上下文与引用验证 |

## 2. 查询时真实发生了什么

1. Qwen3 只编码 Query，不再在应用启动时加载并编码全部语料；
2. 从服务端请求上下文取得 Tenant 和 Role；
3. 生成 fail-closed Milvus Predicate；
4. Milvus 先得到标量过滤 bitset，再在候选范围执行 HNSW ANN；
5. REST Hit 映射回统一 `domain.RetrievedChunk`；
6. 原有 RRF、Router、Rerank 和 Harness 继续工作。

权限表达式示例：

```text
(visibility == "public"
 or (array_contains(allowed_tenants, "tenant_a")
     and array_contains(allowed_roles, "admin")))
and product == "identity"
and status == "active"
```

Tenant 或 Role 任意一个缺失时不会拼接内部权限分支，只允许 `visibility == "public"`。显式查询历史版本时使用 `version == ...`，否则默认只读取 `status == "active"`。

## 3. 可观测性

Retriever 实现 `TraceAttributesProvider`，每次查询的 retrieval event 会记录：

- `vector_backend=milvus`
- Collection、Embedder
- `index_type=HNSW`、`metric_type=COSINE`
- `search_ef=64`
- `filter_stage=pre_ann`
- 实际 Scalar Filter

因此可以从单次请求 trace 说明“选了哪条路、用了哪个索引、权限在哪里执行”，而不是只看最终答案。

## 4. 实测回归结果

本机使用 Milvus `v2.6.20`、Qwen3-Embedding-4B Q4_K_M、38 个 2560d Chunk 完成真实运行：

| Pipeline / Split | Cases | Hit@5 | MRR | Recall@5 | Unauthorized | Metadata Violations |
|---|---:|---:|---:|---:|---:|---:|
| `v5-milvus-rerank` / development | 20 | 1.000 | 1.000 | 1.000 | 0 | 0 |
| `v5-milvus-rerank` / v4-challenge | 8 | 1.000 | 1.000 | 1.000 | 0 | 0 |

`make compare-milvus` 对比同模型的进程内 Retriever 与 Milvus Retriever，质量指标 delta 全部为 `0.000`，development P95 差异为 `+1.413ms`。小数据下这个数字不能外推到百万规模，它只证明替换没有破坏当前行为契约。

## 5. 真实调试记录

这次并非一次写对：

- 公共文档的 Tenant/Role 集合为空，Milvus REST 不接受当前形式的空 `Array<VarChar>`；最终把 ACL 数组设为 nullable，并在公共行省略字段。
- Milvus v2 REST 返回 Array 时使用 typed FieldData envelope，而不是普通 `[]string`；Client 增加兼容解码，同时保留单测使用的直接数组格式。

这两个问题说明生产接入不能只验证建库成功，还要覆盖真实 Upsert、真实 Search、ACL 正反例和协议反序列化。

## 6. 运行方式

```bash
make milvus-up
make milvus-seed
make query-milvus
make eval-milvus
make compare-milvus
```

自定义查询：

```bash
QUERY='如何配置企业 SSO？' TENANT=tenant_a ROLE=admin make query-milvus
```

直接使用 CLI 时，通过 `RAGLAB_VECTOR_BACKEND=milvus` 只注册 Milvus 版本，避免启动时构建内存 Qwen3 索引；使用 `both` 可同时注册两种后端做受控对比。

## 7. 百万级还需要什么

当前完成的是“生产形态的接口、权限、可观测和回归闭环”，不是百万级性能证明。下一阶段必须生成百万级且分布真实的数据，测试 Recall@K、P95/P99、QPS、内存、磁盘、索引构建和增量更新，并补齐 Bulk Import、Alias 原子切换、Embedding 版本、删除一致性、Compaction、分区倾斜和故障降级。
