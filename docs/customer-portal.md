# RAG Desk 企业 Agent 工作台

`web/app/portal` 是项目面向业务使用者的唯一对话入口。工程实验室仍然保留在 `/`，企业 Agent 工作台位于 `/portal`。

## 启动

先启动 RAG API：

```bash
go run ./cmd/raglab serve-lab
```

再启动前端：

```bash
cd web
npm run dev
```

打开 <http://localhost:3000/portal>。如果 API 不在 `127.0.0.1:8080`，设置 `NEXT_PUBLIC_API_BASE`。

## 能力闭环

1. 登录/注册：本地 HS256 演示账号，生产模式沿用后端 OIDC/RS256 Verifier。
2. 企业 Agent 工作台：统一处理知识问答、服务状态、权限查询和工单草稿；LangGraph 负责状态，Go Gateway 负责授权 RAG。
3. Agent Trace：直接观察规划步骤、工具调用、Milvus Top-K、引用、版本、可见性和租户边界。
4. 知识库：租户管理员创建租户知识库，并在创建时设置 `viewer` / `admin` 允许角色；平台管理员可创建公开库。
5. 导入资料：租户管理员只能向自己拥有的租户知识库写入资料。提交后进入异步 Job，门户看板实时展示 validating → chunking → embedding → indexing → verifying、Worker 心跳、尝试次数和写后校验；失败任务可在页面重试，运行任务可人工取消。
6. 权限与审计：显示当前 Claims、PostgreSQL membership、知识库策略和平台管理员的请求审计。

当前演示语料包含 24 篇 AcmeCloud 文档、80 个 Chunk，覆盖身份、报表、API、存储、计费、运维、安全和集成八个公开知识域；Tenant A/B 现在各自有独立的运维手册并通过租户 ACL 隔离。

平台管理员登录后进入“知识库”页，可以在“向量库入库目录”看到当前生命周期 Collection 的实际库存：文档标题、文档 ID、产品/版本、可见性、每篇文档的 Chunk 数，以及 Collection、Embedding 模型、向量维度和总行数。目录接口只返回元数据，不返回正文，并且服务端只允许 `platform_admin` 访问：

```text
GET /api/v1/milvus/catalog
```

点击某个知识空间后，门户会调用数据集级目录接口，只返回当前空间且经过租户/角色过滤的资料：

```text
GET /api/v1/datasets/{dataset_id}/documents
```

平台管理员查看租户空间时也会沿用该空间的 owner tenant 和允许角色过滤，不会因为管理员身份把其他空间的内容混进来。

当前生产配置使用 DashScope OpenAI-compatible `text-embedding-v4`、1024 维向量，生命周期 Collection 为 `raglab_lifecycle_qwen_v4_1024`，别名为 `raglab_knowledge_qwen_v4_1024`。旧的 2560 维 Collection 保留用于回滚/对比，不会混入当前目录。

## 演示账号

| 身份 | 邮箱 | 密码 |
|---|---|---|
| 平台管理员 | `admin@raglab.local` | `RagLab-Platform-2026!` |
| Tenant A 管理员 | `alice@tenant-a.local` | `RagLab-Alice-2026!` |
| Tenant B 管理员 | `bob@tenant-b.local` | `RagLab-Bob-2026!` |

用 Tenant A 导入的资料会带上 `tenant_a` ACL；Tenant B 的 `/datasets` 不会返回 Tenant A 数据集，直接访问 Tenant A 资源也会得到统一的 404，避免资源枚举。公开数据集仍对两个租户可见。

## 后端接口

门户使用数据集级异步导入接口：

```text
POST /api/v1/datasets/{dataset_id}/ingestion/jobs
GET  /api/v1/datasets/{dataset_id}/ingestion/jobs
POST /api/v1/datasets/{dataset_id}/ingestion/jobs/{job_id}/retry
POST /api/v1/datasets/{dataset_id}/ingestion/jobs/{job_id}/cancel
```

请求字段位于 `change.document`：`document_id`、`title`、`content`、`version`、`source_revision`、`event_id`，并通过 `idempotency_key` 防止重复提交。服务端根据数据集策略构造 `product`、`visibility`、`allowed_tenants` 和 `allowed_roles`，客户端不能自行提交 ACL。PostgreSQL 启用时，Job 状态和事件写入 `ingestion_jobs` / `ingestion_job_events`，服务重启后仍可恢复任务看板。
