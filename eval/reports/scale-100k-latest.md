# 100K Milvus Scale Benchmark

生成时间：`2026-07-22 17:36:17Z`

## 数据与写入

- 数据：100000 chunks / 1000 topics / 100 tenants / 1024 dimensions / profile=hard-v2
- Collection：`raglab_bench_100k_flat_v2`（精确对照）与 `raglab_bench_100k_hnsw_v2`（ANN）
- Batch：200，写入耗时：717.34s，唯一数据吞吐：139.40 rows/s，resume_offset=0，retries=0

- Benchmark：queries=300，warmup=50，concurrency=8

## HNSW 与 FLAT Recall 对照

| Scenario | ef | Queries | Exact Recall@10 | Topic Hit@10 | Topic Precision@10 | QPS | P50 ms | P95 ms | P99 ms | Errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| active_all | 16 | 300 | 0.8603 | 0.9833 | 0.9833 | 985.08 | 7.154 | 13.889 | 19.004 | 0 |
| active_all | 32 | 300 | 0.8807 | 0.9900 | 0.9900 | 1118.15 | 6.544 | 10.800 | 12.819 | 0 |
| active_all | 64 | 300 | 0.8927 | 1.0000 | 1.0000 | 1090.12 | 7.077 | 10.216 | 12.199 | 0 |
| active_all | 128 | 300 | 0.8940 | 1.0000 | 1.0000 | 1034.73 | 7.200 | 10.988 | 13.228 | 0 |
| public_active | 16 | 300 | 0.6020 | 0.9733 | 0.9473 | 909.04 | 8.018 | 13.212 | 15.067 | 0 |
| public_active | 32 | 300 | 0.6333 | 0.9900 | 0.9757 | 899.48 | 8.311 | 13.048 | 16.300 | 0 |
| public_active | 64 | 300 | 0.6490 | 1.0000 | 0.9923 | 748.80 | 9.315 | 19.894 | 30.303 | 0 |
| public_active | 128 | 300 | 0.6620 | 1.0000 | 0.9953 | 729.81 | 9.694 | 19.242 | 22.243 | 0 |
| tenant_admin_active | 16 | 300 | 0.8380 | 0.9800 | 0.9800 | 577.25 | 13.133 | 19.483 | 29.223 | 0 |
| tenant_admin_active | 32 | 300 | 0.8630 | 0.9900 | 0.9900 | 588.12 | 13.127 | 18.090 | 20.137 | 0 |
| tenant_admin_active | 64 | 300 | 0.8760 | 1.0000 | 1.0000 | 578.25 | 13.605 | 18.803 | 21.736 | 0 |
| tenant_admin_active | 128 | 300 | 0.8803 | 1.0000 | 1.0000 | 541.62 | 13.921 | 21.930 | 25.604 | 0 |

## ACL硬门禁

- Queries：300
- Unauthorized Retrievals：**0**

> 本报告在预计算Query Vector上测量Milvus检索阶段，不包含Embedding模型耗时；FLAT只用于Ground Truth，不作为线上索引方案。
