# Milvus 100K 网页实验室

## 目标

网页的 `100K Lab` 不是静态展示压测数字，而是直接查询本地 `raglab_bench_100k_flat_v2` 与 `raglab_bench_100k_hnsw_v2`。同一个确定性Query先访问FLAT生成Ground Truth，再访问HNSW，让使用者现场调整`ef`、Top-K和Filter并观察结果。

## 启动

```bash
make milvus-up
go run ./cmd/raglab serve-lab
```

另开终端启动网页：

```bash
cd web
npm run dev
```

打开页面后进入 `100K Lab`，先点击“检查100K索引”，确认：

- FLAT与HNSW各100,000行；
- HNSW状态为`Finished`；
- `indexedRows=100000`；
- `pendingRows=0`；
- 建库参数为`M=8`、`efConstruction=160`。

如果100K Collection不存在，需要先运行`make scale-100k`重建数据。

执行检索前需要在`Trusted Identity Boundary`区域选择服务端预定义Persona并签发本地JWT。未携带Token的检索返回401；Viewer访问Admin场景返回403。Tenant和Role来自验签后的Claims，不信任网页参数。

该Persona签发入口只存在于默认本地模式。配置`RAGLAB_AUTH_OIDC_ISSUER`后，API切换为企业OIDC/RS256验证并关闭`/api/v1/auth/dev-token`；此时应由企业登录前端或API客户端携带Access Token，详细配置见[企业RAG身份与审计](enterprise-rag-security.md)。

## 可交互参数

| 参数 | 作用 |
|---|---|
| Topic | 从1,000个确定性语义主题中选择查询 |
| Scenario | 切换Active、Public、Tenant+Role三类标量过滤 |
| Top-K | 控制返回候选数量 |
| ef | 控制HNSW查询时遍历的候选范围，不需要重建索引 |
| Persona | 切换Public Viewer、Tenant Viewer/Admin和Platform Admin权限 |

页面会显示：

- Query Vector前8维与L2 Norm；
- 实际发送给Milvus的Filter表达式；
- HNSW结果及Cosine分数；
- 每条结果是否也属于FLAT精确Top-K；
- Exact Recall@K、Topic Hit@K和Topic Precision@K；
- FLAT、HNSW和总耗时；
- 可展开的FLAT Ground Truth列表。

## API

索引状态：

```http
GET /api/v1/milvus/scale/status
```

双索引对照搜索：

```http
POST /api/v1/milvus/scale/search
Content-Type: application/json

{
  "topic": 137,
  "scenario": "public_active",
  "top_k": 10,
  "ef": 64
}
```

`serve-lab`默认连接`raglab_bench_100k_*_v2`。需要切换Collection版本时可使用`--scale-prefix`和`--scale-version`。

## 演示讲解建议

1. 先用`ef=16`运行，说明HNSW通过少量图遍历换取速度；
2. 再切换到`ef=64/128`，观察Exact Recall与耗时变化；
3. 展开FLAT Ground Truth，指出HNSW可能返回不同Chunk；
4. 对比Topic Precision，解释“精确邻居集合不同”不一定等于“业务检索错误”；
5. 切换Public和Tenant Admin场景，展示Filter在ANN前执行以及ACL Fail Closed；
6. 用Viewer调用Admin场景展示403，再用Tenant Admin成功检索；
7. 使用Platform Admin读取带Request ID的审计事件；
8. 最后说明FLAT只用于离线评测，线上使用HNSW并通过Harness选参。

这套演示适合回答面试中的HNSW参数、ANN评测、标量过滤、多租户ACL和检索质量解释问题。
