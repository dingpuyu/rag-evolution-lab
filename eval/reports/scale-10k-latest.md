# 10K Milvus Scale Benchmark

生成时间：`2026-07-22 16:48:05Z`

## 数据与写入

- 数据：10000 chunks / 100 topics / 100 tenants / 1024 dimensions
- Collection：`raglab_bench_10k_flat_v1`（精确对照）与 `raglab_bench_10k_hnsw_v1`（ANN）
- Batch：100，写入耗时：127.55s，唯一数据吞吐：78.40 rows/s

## HNSW 与 FLAT Recall 对照

| Scenario | ef | Queries | Recall@10 | QPS | P50 ms | P95 ms | P99 ms | Errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| active_all | 32 | 100 | 1.0000 | 903.63 | 7.034 | 18.773 | 24.427 | 0 |
| active_all | 64 | 100 | 1.0000 | 814.47 | 8.857 | 18.084 | 21.807 | 0 |
| active_all | 128 | 100 | 1.0000 | 941.35 | 7.333 | 14.739 | 21.482 | 0 |
| public_active | 32 | 100 | 1.0000 | 761.66 | 8.766 | 20.729 | 23.716 | 0 |
| public_active | 64 | 100 | 1.0000 | 661.72 | 10.778 | 19.156 | 23.150 | 0 |
| public_active | 128 | 100 | 1.0000 | 820.54 | 9.090 | 14.833 | 18.400 | 0 |
| tenant_admin_active | 32 | 100 | 1.0000 | 1312.27 | 5.599 | 9.434 | 10.464 | 0 |
| tenant_admin_active | 64 | 100 | 1.0000 | 1268.50 | 5.977 | 9.322 | 10.202 | 0 |
| tenant_admin_active | 128 | 100 | 1.0000 | 1135.43 | 6.132 | 11.550 | 14.038 | 0 |

## ACL硬门禁

- Queries：100
- Unauthorized Retrievals：**0**

> 本报告在预计算Query Vector上测量Milvus检索阶段，不包含Embedding模型耗时；FLAT只用于Ground Truth，不作为线上索引方案。
