# 面向 Agent 应用的知识基础设施架构

## 1. 重新定义项目目标

本项目不以“某一类资料管理后台”为最终产品，而是沉淀一套可被多个上层 Agent 应用复用的知识基础设施：

```text
客服 Agent       招聘 Agent       运维 Copilot       内部问答 Agent
    │                │                 │                    │
    └────────────── Application Access Gateway ─────────────┘
                              │
                    Knowledge Platform Core
          ┌───────────────┬───────────────┬───────────────┐
          │               │               │               │
      Control Plane   Ingestion Plane  Retrieval Plane  Eval/Observability
          │               │               │               │
      PostgreSQL       Object Store    Milvus/BM25       Trace/Quality
```

上层 Agent 只关心“在什么应用身份下查询哪些知识”，不直接接触 Milvus Collection、租户过滤表达式、Embedding 版本和底层重试细节。

## 2. 资源层级

```text
Organization / Tenant
  ├── Agent Application
  │     ├── Environment: dev / staging / prod
  │     ├── Application Credential / OIDC Scope
  │     ├── Knowledge Binding
  │     │     ├── Knowledge Base
  │     │     ├── Retrieval Policy
  │     │     └── Allowed Operations
  │     └── Query / Answer / Trace
  └── Knowledge Base
        ├── Sources / Connectors
        ├── Documents / Revisions
        ├── Chunks / ACL Metadata
        ├── Index Build
        └── Published Index Version
```

核心原则是：

- `Agent Application` 是调用方，不是知识库本身；一个应用可以绑定多个知识库。
- `Knowledge Base` 是可复用资源，可以被多个应用绑定，但每个绑定拥有独立检索策略和权限范围。
- `Environment` 让开发、预发布、生产索引和模型配置可以隔离，不能让测试资料污染生产回答。
- `Published Index Version` 是查询面唯一读取的版本，构建中的 Collection 不直接对线上流量可见。

## 3. 四道隔离边界

### 3.1 身份隔离

用户请求通过 OIDC/JWT 或应用 Credential 进入 Gateway。服务端解析出：

```text
subject / tenant_id / application_id / environment / roles / scopes
```

`tenant_id` 和 `application_id` 只能来自可信身份或服务端 Credential 映射，不能信任请求 Body。

### 3.2 控制面隔离

PostgreSQL 保存 Tenant、Application、Membership、Knowledge Binding、Policy 和 Audit。所有资源访问先经过控制面授权，再进入检索或导入服务。

### 3.3 检索面隔离

检索请求在进入 Milvus 前生成不可由客户端修改的 Filter：

```text
tenant_id == verified_tenant
AND application_scope IN verified_scopes
AND environment == verified_environment
AND dataset_id IN authorized_bindings
AND status == active
```

Filter 必须在 ANN 搜索前生效；查询后再做一次结果级 ACL 校验，形成双重门禁。

### 3.4 物理索引隔离

第一阶段允许共享 Collection，但每条数据都必须带 `tenant_id`、`dataset_id`、`environment`、`index_version` 和 ACL Metadata。
高敏感场景可以通过 `tenant` 或 `application` 级 Collection/Database 做物理隔离。物理隔离是部署策略，不应污染上层 API。

## 4. 核心资源模型

建议在现有 `Dataset` 之上增加以下资源，而不是继续扩展 Dataset 字段：

| 资源 | 作用 | 关键字段 |
|---|---|---|
| Application | 一个可独立发布的 Agent 产品 | `app_id`, `tenant_id`, `slug`, `status` |
| Environment | 应用运行环境 | `environment_id`, `app_id`, `name`, `config_version` |
| Knowledge Base | 可复用知识空间 | `dataset_id`, `owner_tenant`, `lifecycle` |
| Knowledge Binding | 应用与知识库的授权关系 | `app_id`, `dataset_id`, `scope`, `priority` |
| Retrieval Policy | 绑定级检索策略 | `top_k`, `filter`, `rerank`, `query_rewrite`, `token_budget` |
| Index Build | 一次可追踪的索引构建 | `build_id`, `source_revision`, `embedding_version`, `status` |
| Published Index | 对查询可见的版本 | `index_version`, `collection`, `alias`, `published_at` |
| Query Trace | 端到端调用记录 | `trace_id`, `app_id`, `policy_version`, `latency`, `decision` |

其中 `Knowledge Binding` 是关键抽象：同一个知识库可以给客服 Agent 只读绑定，也可以给运维 Agent 绑定更高的 Top-K、不同的重排器和不同的文档范围。

## 5. API 演进方向

现有数据集接口继续保留作为管理和兼容入口；面向上层 Agent 的稳定入口逐步增加：

```http
POST /api/v1/apps
POST /api/v1/apps/{app_id}/environments
POST /api/v1/apps/{app_id}/knowledge-bindings
GET  /api/v1/apps/{app_id}/knowledge
POST /api/v1/apps/{app_id}/query
POST /api/v1/apps/{app_id}/answer/stream
GET  /api/v1/apps/{app_id}/traces/{trace_id}
```

调用方只提交业务 Query；服务端根据 `application_id + environment + subject` 解析绑定和 Policy，统一完成：

```text
Authenticate → Authorize Binding → Build Server Filter
→ Route Retrieval → Retrieve/Rerank → Context/Citation
→ Generate/Verify → Trace/Audit
```

未来可以为 LangChain、Spring AI、Go Agent、MCP Server 提供薄适配器，但适配器不复制权限和检索逻辑。

## 6. 存储职责

| 组件 | 负责内容 |
|---|---|
| PostgreSQL | 资源、绑定、策略、版本、任务、审计、评测元数据 |
| Object Storage | 原始文件、解析产物、批量导入中间文件 |
| Milvus | 向量、标量过滤字段、索引和发布 Alias |
| BM25/搜索引擎 | 精确词、代码、编号、结构化字段召回 |
| Event/Queue | 导入、索引构建、发布和重算的异步事件 |
| Trace Store | 查询阶段、模型调用、成本、质量和安全决策 |

数据库和向量库都不是最终真相源：文档生命周期与发布状态以控制面为准，Milvus 只是可重建的查询物化视图。

## 7. 分阶段落地

### P1：应用资源与绑定模型

- 增加 `applications`、`app_environments`、`knowledge_bindings`、`retrieval_policies` 表；
- 为现有 Tenant A/B 创建默认应用和生产环境；
- 现有 Dataset API 保持兼容，新增应用级只读查询入口；
- 回归：跨租户、跨应用、跨环境不能越权。

### P2：检索网关

- 把 `DatasetAPI.search/answer` 下沉到统一 `KnowledgeGateway`；
- 应用绑定解析成服务端 Retrieval Policy；
- 统一 Query Trace、成本、模型和索引版本记录；
- Agent SDK 只依赖 Gateway Contract。

### P3：索引发布与多版本

- Index Build、Checkpoint、验证、发布、回滚；
- 构建 Collection 与线上 Alias 分离；
- Embedding/Chunker/Parser 版本不一致时禁止发布；
- 支持灰度发布和按应用切换索引版本。

### P4：企业运行面

- 多进程 Claim、Outbox、外部队列和 DLQ；
- Connector Plugin、对象存储、增量同步和删除传播；
- 租户级配额、应用级限流、成本预算、SLO 和告警；
- OpenTelemetry、审计导出和合规保留策略。

## 8. 本项目的真实边界

当前已完成的是 P0/P1 之前的可靠性基础：Tenant/Dataset ACL、Milvus 生命周期、异步导入、PostgreSQL Job 持久化、门户人工运维和评测闭环。

当前还不能宣称已经是完整的多应用知识平台，因为 Application、Binding、Environment 和统一 Gateway 尚未落成。后续所有代码和演示都应以本文模型为约束，避免继续把某个客服业务的字段当作平台核心。
