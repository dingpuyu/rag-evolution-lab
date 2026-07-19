# Milvus 本地向量数据库实验

这个实验把现有的 Qwen3 Embedding 从内存向量检索推进到真实向量数据库：38 个知识 Chunk 经本地模型编码后写入 Milvus Standalone，通过 HNSW 索引、COSINE 距离和标量过滤执行检索，并在网页上显示召回结果与分阶段耗时。

## 1. 架构与版本

本地部署固定使用 Milvus `v2.6.20`，避免 `latest` 漂移。Standalone 由三个容器组成：

- Milvus：REST/gRPC `19530`，健康检查 `9091`
- etcd `v3.5.25`：保存元数据
- MinIO：保存 segment、index 等对象数据

数据保存在 `deploy/milvus/volumes/`，不会提交到 Git。

当前 Collection Schema：

| 字段 | 类型 | 用途 |
|---|---|---|
| `chunk_id` | VarChar / Primary Key | 稳定 Chunk ID，支持幂等 Upsert |
| `document_id`、`title`、`content` | VarChar | 搜索结果展示与溯源 |
| `tenant_id`、`product`、`version`、`status`、`visibility` | VarChar | Pre-filter 标量过滤 |
| `embedding` | FloatVector(2560) | Qwen3-Embedding-4B 向量 |

向量索引显式设置为 `HNSW(M=16, efConstruction=200)` 和 `COSINE`；查询时使用 `ef=64`。这些参数是实验基线，不是未经压测即可照搬的生产参数。

## 2. 启动和检查

本机需要 Docker/Colima 至少分配 4 CPU 和 8GB 内存。本项目所在设备为 Apple Silicon、32GB 内存，当前 Colima 分配 4 CPU / 16GB。

```bash
make milvus-up
make milvus-status
```

若环境只有新版 Docker Compose 插件，可以使用：

```bash
make milvus-up DOCKER_COMPOSE="docker compose"
```

健康检查也可以直接访问：

```bash
curl http://127.0.0.1:9091/healthz
```

停止容器但保留数据：

```bash
make milvus-down
```

## 3. 写入真实测试数据

先保证 Ollama 中存在 `qwen3-embedding:4b-local`，然后执行：

```bash
make milvus-seed
```

命令执行的完整过程是：

1. 加载 13 篇 AcmeCloud 文档并切成 38 个稳定 Chunk；
2. 批量调用 Ollama `/api/embed` 得到 2560 维 Document Embedding；
3. 重建 `raglab_chunks_qwen3` Collection；
4. 创建 HNSW/COSINE 索引；
5. 通过主键 Upsert 写入 Chunk、向量和业务元数据；
6. 主动 Flush streaming data，使演示状态页立即看到 sealed segment 与稳定行数。

`milvus-seed` 会重建演示 Collection，适合可复现实验。生产增量索引不能采用全量 Drop/Recreate，应实现版本化 Collection、批量 Upsert/Delete、Alias 原子切换和数据一致性校验。

## 4. 启动 API 和网页

分别打开两个终端：

```bash
make serve-lab
```

```bash
make web-dev
```

浏览网页后进入 `Milvus Lab`：

- 刷新状态：查看 Collection、行数、维度、索引和 Load State；
- 输入自然语言问题：由 Qwen3 实时生成 Query Embedding；
- 选择 Tenant、Product 和 Top-K：构造 Milvus 标量 Predicate；
- 查看每个 Hit 的 COSINE 分数、Chunk 内容、业务元数据和耗时。

也可以直接调用接口：

```bash
curl http://127.0.0.1:8080/api/v1/milvus/status

curl -X POST http://127.0.0.1:8080/api/v1/milvus/search \
  -H 'Content-Type: application/json' \
  -d '{
    "query":"当前版本如何配置企业单点登录？",
    "tenant_id":"tenant_a",
    "product":"identity",
    "status":"active",
    "top_k":5
  }'
```

## 5. 这个实验验证了什么

### 本机 Smoke Test 结果

在 Apple M1 Pro / Colima 4 CPU、16GB / Qwen3-Embedding-4B Q4_K_M 环境完成真实验证：

| 项目 | 结果 |
|---|---|
| Collection | `raglab_chunks_qwen3` |
| 数据 | 13 documents / 38 chunks / 38 rows |
| 向量 | 2560d FloatVector |
| 索引 | HNSW / COSINE / `LoadStateLoaded` |
| Query | `E1027 错误应该如何重试？` |
| Filter | public + api-gateway + active |
| Top-1 | `api-error-codes-v2.3#c002`，Cosine `0.78886` |
| 单次耗时 | Embedding `247.995ms` / Milvus Search `18.723ms` / Total `266.721ms` |

这些数字只用于证明真实链路已执行，不作为并发性能结论；首次请求、模型常驻状态和数据规模都会显著影响耗时。

### 向量数据库不只是保存数组

Milvus 把向量字段、业务标量字段、ANN 索引、加载状态和一致性级别组合成可查询系统。网页同时显示语义分数与元数据，便于解释“相似但不可用”的结果为什么必须被过滤。

### HNSW 的三个关键参数

- `M`：每个节点保留的近邻连接规模；增大通常提高召回，也增加内存和建索引成本。
- `efConstruction`：建索引时的候选搜索宽度；增大通常提高索引质量，但构建更慢。
- `ef`：查询时的候选搜索宽度；增大通常提高 Recall，也增加延迟。

生产调参应在目标规模和目标数据分布上绘制 Recall@K、P95/P99 延迟、内存和构建时间的 Pareto 曲线。

### Pre-filter 与权限边界

当前过滤表达式把公开数据与目标 Tenant 数据合并，并默认只检索 `active` 文档。生产系统不能把前端传入的 Tenant 当作可信身份；Tenant、Role 和可见范围必须来自服务端认证上下文，且权限过滤要在向量评分前执行并进入回归测试。

### 从 38 条到百万级

当前数据只验证接口和行为，不宣称性能规模。百万级演进需要补充：

- Bulk Import/批量 Upsert，而不是单请求写入全部数据；
- 分区或 Partition Key 的实际数据倾斜评估；
- Collection Alias 和双写/回填方案；
- 增量更新、删除、Embedding 版本迁移与一致性对账；
- Segment/Compaction、索引构建、Load/Release 生命周期；
- 在百万级合成+真实分布数据上测试吞吐、Recall、P95/P99、资源和成本。

## 6. 自动化验证

```bash
go test ./...
go test -race ./...
npm --prefix web test
```

单测使用 `httptest.Server` 验证 REST 路径、鉴权头、HNSW Schema、搜索 Filter、结果反序列化和错误传播，不依赖 Milvus。真实本地服务则通过 Seed、Status 和 Search 三步 Smoke Test 验证。
