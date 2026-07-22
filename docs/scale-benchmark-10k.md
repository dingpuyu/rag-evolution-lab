# 10K Milvus规模验证

## 1. 目标

这轮验证的目标不是用10,000条数据冒充百万级生产能力，而是先建立可扩展的Scale Harness：确定性数据生成、流式Batch Upsert、FLAT精确对照、HNSW近似检索、标量过滤、ACL安全门禁和延迟分位数报告。后续扩到10万或100万时复用同一套命令与指标。

## 2. 数据设计

- 10,000 Chunks
- 1024维归一化Float Vector
- 100个语义主题
- 100个Tenant
- 10个Product
- Public / Internal可见性
- Admin / Viewer角色
- Active / Deprecated / Draft生命周期
- 固定Seed `20260723`

向量由“主题中心 + 确定性噪声”生成，不调用LLM，也不在磁盘保存巨型JSON。每个主题的最近向量被故意设为Admin私有数据，用作ACL Hard Negative：如果权限过滤失败，它会优先出现在Top-K。

## 3. 双Collection设计

| Collection | Index | 作用 |
|---|---|---|
| `raglab_bench_10k_flat_v1` | FLAT / COSINE | 生成精确Top-K Ground Truth |
| `raglab_bench_10k_hnsw_v1` | HNSW / COSINE | 测试近似召回、延迟与并发 |

Recall@K按HNSW Top-K与相同Filter下FLAT Top-K的集合重合率计算。FLAT只用于实验对照，不是线上索引建议。

## 4. 运行方式

完整重建并运行：

```bash
make scale-10k
```

Collection已经存在时只重跑Benchmark：

```bash
make scale-bench
```

直接使用CLI：

```bash
go run ./cmd/ragbench all \
  --chunks 10000 \
  --dimensions 1024 \
  --topics 100 \
  --tenants 100 \
  --batch-size 100 \
  --queries 100 \
  --top-k 10 \
  --concurrency 8 \
  --ef 32,64,128
```

## 5. 首轮真实结果

环境：Apple M1 Pro 10核、32GB统一内存，Colima分配约16GB，Milvus Standalone `v2.6.20`。

| 指标 | 结果 |
|---|---:|
| 两个Collection Row Count | 各10,000 |
| 写入耗时 | 127.55s |
| 唯一Chunk吞吐 | 78.40 rows/s |
| 向量维度 | 1024 |
| Query | 每组100 |
| 并发 | 8 |
| 请求错误 | 0 |
| Unauthorized Retrievals | 0 |
| Milvus实验数据目录 | 约453MB（包含原有实验数据） |

完整的9组结果见 [自动生成报告](../eval/reports/scale-10k-latest.md)。首轮P95范围约为9.3～20.7ms，Recall@10均为1.000。

## 6. 如何解释结果

Recall为1.000说明当前Harness的计算和对照链路正确，但不说明HNSW在真实业务数据上没有精度损失。当前主题中心区分明显，属于容易数据集；100条短跑产生的QPS也会受缓存、调度和本机后台任务影响，不能作为容量承诺。

这次真正成立的结论是：

1. 10,000条数据能够以固定Seed重复生成并写入两种索引；
2. HNSW结果能够与FLAT精确结果自动比较；
3. ANN前Tenant/Role Filter在刻意构造的私有最近邻上保持0越权；
4. Harness可以自动输出Recall、QPS和延迟分位数。

## 7. 下一轮改进

以下事项已经在[100K Hard-v2验证](scale-benchmark-100k.md)中完成：跨主题近邻、Warm-up、300 Query、Batch重试、Checkpoint、断点续写与索引就绪门禁。保留中的后续工作为：

- 实现增量Upsert、Delete和Collection Alias切换；
- 采集测试期间CPU、内存、磁盘和Segment指标；
- Query扩大到1,000以上并执行更长时间稳态压测；
- 评估真实Embedding数据分布，再决定是否进入1M。
