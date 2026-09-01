# 隔离 Retrieval Sandbox：从 Chunk 指标到真实检索验证

## 目标

文档质量实验不能止于“某个答案跨度存在于 Chunk”。真正有价值的 Candidate 还必须经过线上同构的 Embedding、索引、Hybrid Search 和 Rerank，并证明证据能够被 Query 找回。

RAG 平台因此提供管理员专用接口：

```http
POST /api/v1/evaluation/retrieval-sandbox/runs
Authorization: Bearer <admin-token>
Content-Type: application/json
```

接口只给独立 `agent-evaluation` 平台使用，不是最终用户检索 API。

## 执行链路

```text
trusted identity + bounded chunks/queries
→ server-generated raglab_eval_<random> collection
→ current Embedder: text-embedding-v4 / 1024d
→ HNSW/COSINE + SPARSE_INVERTED_INDEX/BM25
→ tenant/role pre-ANN filter
→ RRF(k=60) + exact identifier preservation
→ strict qwen3-rerank
→ top-k evidence + pre/post rank trace
→ drop temporary collection
```

这里直接复用线上 `retrieval.Embedder`、Milvus Client、Exact Identifier 规则和 `knowledgegateway.HitReranker` 接口，没有实现一套只为评测好看的替代算法。

## 安全边界

- 仅 `admin/platform_admin`；viewer 实测为 403。
- API 不接受 Collection 字段，物理名称由服务端随机生成，不能覆盖或读取生产索引。
- 每条记录的 Tenant/Role 来自已验证身份，不信任请求体；搜索在 ANN 前应用 ACL Filter。
- 请求上限 2 MiB、80 chunks、20 queries、单 Chunk 7000 字符、Rerank Candidate 最多 50；进程内最多同时运行 2 个 Sandbox，超出的请求等待或随调用方超时取消。
- 实验 Reranker 使用 strict 模式。生产链路可以配置确定性 fallback，但实验不能在 fallback 后仍把 Provider 标为 Qwen。
- 成功与错误路径都执行 Drop；成功响应必须返回 `cleanup_completed=true`、`collection_scope=temporary-isolated`、`production_mutation=false`。
- 响应不返回物理 Collection 名，访问令牌与模型 Key 不落库。

## 真实实验发现

`400/100` 与 `700/80` 在 4 条 Development Case 上都达到 Document Hit@5/MRR `1.0`。如果只看文档 ID，会认为没有改进。

新增的 `retrieval_evidence_span_containment` 要求一个已召回 Chunk 完整包含 Golden 答案单元：Baseline 为 `0`，Candidate 为 `1.0`。因此修复的是“文档命中了但 Agent 拿不到完整证据”，不是通过多放几个相邻 Chunk 掩盖问题。

第一轮还发现 Rerank Candidate 误随 Chunk 总量增长到 33/34。收敛到 Top 20 后，所有质量指标保持不回退；网络延迟只作为软指标观察，不能用单次调用包装成稳定性能结论。

第二轮检查引用定位时发现：XLSX Parser 的 Document IR 已保留 `sheet=兼容矩阵` 和 `cell_range=A1:C1,A3:C3`，但无索引 Artifact DTO 只复制了页码，导致来源信息在进入 Sandbox 前丢失。现在 Block、Chunk、Milvus Record、Search Hit 与持久化 Rank Trace 都传递 Page、Sheet、Cell Range 和 Heading Path。评测平台新增 `retrieval_source_locator_accuracy` 硬门禁；冻结数据集 `v1.3.0` 的真实实验 `docqexp_6208de8661b64bf3a75d6f0efe6f4819` 为 `1.0`，单测注入错误行号后正确进入 HOLD。

## 运维与排障

实验完成后可确认没有残留：

```bash
curl -sS -X POST http://127.0.0.1:19530/v2/vectordb/collections/list \
  -H 'Authorization: Bearer root:Milvus' \
  -H 'Content-Type: application/json' \
  --data '{}'
```

列表中不应存在 `raglab_eval_*`。若进程被强制终止，未来生产化应再加按前缀与 TTL 的启动回收器；当前实现覆盖正常返回、Provider 错误和请求取消，但无法承诺在 `SIGKILL` 时运行 defer。

## 下一步

1. 由评测平台冻结 Candidate，执行一次性 Holdout。
2. 加入 Regression 发布门禁，继续保持评测平台不直接发布生产。
3. 已完成 PDF Page 与 XLSX Sheet/Cell Range/Heading Path 引用正确性硬门禁；下一步扩大到跨页表格和合并单元格。
4. 增加临时 Collection TTL/启动回收，覆盖宿主机崩溃场景。
