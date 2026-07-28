# RAG Desk 企业智能客服门户

`web/app/portal` 是项目面向业务使用者的独立体验入口。工程实验室仍然保留在 `/`，客服门户位于 `/portal`。

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
2. 智能客服：选择可见知识库，通过 `/answer/stream` 实时展示回答、召回数量、耗时、模型和引用。
3. 只看检索：直接观察 Milvus Top-K、距离、版本、可见性和租户字段。
4. 知识库：租户管理员创建租户知识库，并在创建时设置 `viewer` / `admin` 允许角色；平台管理员可创建公开库。
5. 导入资料：租户管理员只能向自己拥有的租户知识库写入资料。提交后进入异步 Job，门户看板实时展示 validating → chunking → embedding → indexing → verifying、Worker 心跳、尝试次数和写后校验；失败任务可在页面重试，运行任务可人工取消。
6. 权限与审计：显示当前 Claims、PostgreSQL membership、知识库策略和平台管理员的请求审计。

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
