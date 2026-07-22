# 100K Milvus规模验证与自我改进

## 1. 验证目标

本轮把10K Harness扩展到100,000个Chunk，重点不是宣称“已经支持百万级生产”，而是验证规模增长后数据生成、可恢复写入、索引就绪、ANN质量、标量过滤、ACL和评测解释是否仍然成立。

测试环境为Apple M1 Pro、32GB统一内存，Milvus Standalone `v2.6.20`运行在本地Colima中。所有向量由固定Seed生成，Benchmark只测Milvus检索阶段，不包含Embedding推理耗时。

## 2. 从10K到100K的工程改进

### 2.1 Hard-v2数据分布

10K Easy-v1中各主题中心相互独立，所有`ef`都得到1.000 Recall，无法暴露ANN取舍。Hard-v2改为每10个主题共享一个语义簇中心，再叠加主题噪声、Chunk噪声和Query噪声，制造跨主题近邻和同主题近似Chunk。

数据规模为：

- 100,000 Chunks、1,000 Topics、100 Tenants；
- 1024维、COSINE、固定Seed `20260723`；
- Active / Deprecated / Draft生命周期；
- Public / Internal可见性；
- Tenant与Role ACL Hard Negative。

### 2.2 可恢复写入

写入器按Batch同时写入FLAT与HNSW Collection，只有两边都成功才原子更新Checkpoint。网络或服务错误使用指数退避重试；重启后校验数据配置、Collection名称与offset再续写，避免把不同版本数据混到同一索引。

完成态Checkpoint也经过真实验证：再次执行`--resume`时从100,000恢复，没有重复Upsert，耗时约24ms。

### 2.3 索引就绪门禁

Row Count达到100,000不代表HNSW已经可用于稳定压测。Runner现在同时等待：

1. FLAT与HNSW Row Count都等于预期值；
2. Index State为`Finished`；
3. `indexedRows`等于预期值；
4. `pendingRows`为0。

只有四项都满足才写入完成态Checkpoint并开始Benchmark。

### 2.4 评测指标升级

FLAT在相同Filter下生成精确Top-K，HNSW与其计算Exact Recall@10。同时新增两个业务指标：

- Topic Hit@10：Top-K是否至少命中一个正确主题Chunk；
- Topic Precision@10：Top-K中正确主题Chunk的比例。

Exact Recall衡量ANN对精确邻居集合的逼近程度；Topic指标用于区分“真正检错主题”和“多个近似等价Chunk发生互换”。两者不能互相替代。

## 3. 运行方式

启动Milvus后完整重建并压测：

```bash
make milvus-up
make scale-100k
```

核心参数为：

```text
profile=hard-v2, chunks=100000, dimensions=1024
topics=1000, tenants=100, batch=200
HNSW M=8, efConstruction=160
queries=300, warmup=50, concurrency=8, topK=10
ef=16,32,64,128
```

中断后可以使用相同参数加`--resume`和原Checkpoint继续。完整命令和默认Checkpoint规则见`go run ./cmd/ragbench seed --help`。

## 4. 实测结果

### 4.1 写入与资源

| 指标 | 结果 |
|---|---:|
| 两个Collection Row Count | 各100,000 |
| 唯一Chunk写入耗时 | 717.34s |
| 唯一数据吞吐 | 139.40 rows/s |
| Upsert重试 | 0 |
| HNSW索引 | M=8 / efConstruction=160 / Finished |
| Milvus容器内存快照 | 约1.13GiB |
| Milvus数据目录快照 | 约5.6GB |
| Unauthorized Retrievals | 0 / 300 |

磁盘数字包含FLAT与HNSW双份实验数据及Milvus内部文件，因此不能直接外推单Collection生产容量。

### 4.2 代表性质量与延迟

| Scenario | ef | Exact Recall@10 | Topic Hit@10 | Topic Precision@10 | P95 ms |
|---|---:|---:|---:|---:|---:|
| active_all | 16 | 0.8603 | 0.9833 | 0.9833 | 13.889 |
| active_all | 64 | 0.8927 | 1.0000 | 1.0000 | 10.216 |
| public_active | 16 | 0.6020 | 0.9733 | 0.9473 | 13.212 |
| public_active | 64 | 0.6490 | 1.0000 | 0.9923 | 19.894 |
| tenant_admin_active | 16 | 0.8380 | 0.9800 | 0.9800 | 19.483 |
| tenant_admin_active | 64 | 0.8760 | 1.0000 | 1.0000 | 18.803 |

完整12组结果见[自动生成报告](../eval/reports/scale-100k-latest.md)。本机短时QPS受缓存、容器调度和后台负载影响，仅用于同环境相对比较，不是容量承诺。

`public_active`的Exact Recall较低，但Topic Hit在`ef=64`达到1.000、Topic Precision达到0.9923。检查数据分布后确认，标量过滤缩小候选集且同主题Chunk高度相近，HNSW经常返回与FLAT不同但语义等价的Chunk。因此需要同时报告精确ANN指标与业务相关性指标。

## 5. 参数选择

本数据集上`ef=64`是较合理的默认点：三类场景的Topic Hit均达到1.000。继续从`ef=64`提高到128时，三类场景的Exact Recall分别只增加0.0013、0.0130和0.0043，业务Topic Hit已经没有提升，边际收益有限。

生产选参不能只看全局平均值，应按Filter选择性、租户规模和目标SLO分桶评测，并预留不同场景使用不同`ef`的能力。

## 6. 自我验证与问题修复记录

| 暴露的问题 | 验证方式 | 改进 |
|---|---|---|
| Easy-v1 Recall全为1.000 | 10K多组`ef`无差异 | 增加Hard-v2相邻主题簇与Query扰动 |
| `ef < topK`由Milvus运行时报错 | tenant过滤场景复现 | CLI运行前Fail Fast并补单测 |
| Flush触发Milvus低频率限制 | 完成态恢复复现 | Flush指数退避；完成Checkpoint跳过重复Flush |
| 长写入中断需从零开始 | Mock故障与真实Checkpoint验证 | 原子Checkpoint、配置校验、断点续写 |
| Row Count完成但索引可能未就绪 | 检查Index State和行进度 | 增加索引Finished / indexedRows / pendingRows门禁 |
| Exact Recall低估等价Chunk | 对比Chunk主题标签 | 增加Topic Hit与Topic Precision |
| 单轮性能数字可能偶然 | 同参数重复300 Query | 12组质量指标逐项一致，QPS/P95保留为环境相关值 |

## 7. 仍未证明的事情

- 当前是单机Standalone，不代表分布式Milvus的扩缩容和故障恢复能力；
- 数据是可控合成向量，不代表真实Embedding分布；
- 未包含Embedding、Rerank和LLM生成的端到端延迟；
- 300 Query适合回归，不足以给出长期稳定吞吐和容量边界；
- 100K成功不等于1M成功，下一步还需增量更新、删除、Alias切换和资源曲线。

这轮成立的结论是：项目已经具备可复现的100K双索引验证闭环，并且能解释ANN精度、业务相关性、Filter、ACL、延迟和恢复能力之间的差异。
