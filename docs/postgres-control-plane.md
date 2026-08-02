# PostgreSQL 多租户控制面

这一阶段把数据集授权从进程内静态 Catalog 迁移到 PostgreSQL。Milvus 继续承担向量数据面，PostgreSQL 成为 Tenant、User、Membership、Dataset、Application、Knowledge Binding 和管理审计的控制面事实源。

## 架构边界

```text
OIDC / Local JWT
  -> verified subject / tenant / roles
  -> PostgreSQL Membership + Dataset authorization
  -> server-assigned dataset product / access scope
  -> Milvus Pre-ANN ACL
```

数据库授权和 Milvus 行级过滤不是二选一：

- PostgreSQL 决定调用者是否能访问这个 Dataset Resource；
- Milvus Filter 决定 ANN 搜索可以参与计算的向量行；
- 禁止访问的 Dataset 在调用 Milvus 前返回统一 404；
- 允许访问的 Dataset 仍必须带 Tenant、Role、Product 和 Status 过滤。

## 数据模型

### Tenant

- `id`：稳定租户边界；
- `status`：`active`或`suspended`；
- 租户不能由请求体覆盖，来源是受信身份或 Platform Admin 管理动作。

### User

- 主键是外部 IdP 的稳定 Subject；
- 不用 Email 作为授权主键；
- 本地注册同样生成稳定 Subject，生产环境由 OIDC `sub`提供。

### Membership

- 复合主键：`tenant_id + subject`；
- Role：`viewer`、`admin`、`platform_admin`；
- Status：`active`、`revoked`；
- Token Claims 只证明身份，不自动建立 Membership；邀请、管理员或本地 Demo Bootstrap 必须显式调用 ProvisionIdentity；
- 普通请求只读校验 Tenant、User、Membership 的 active 状态，并要求数据库 Role 出现在当前 Token roles 中；
- 已撤销或已降级的 Membership 不会被后续请求自动恢复，避免权限降级后复用旧 admin。

### Dataset

- 强制归属于一个 Tenant；
- Public Dataset 归属于 Platform Tenant；
- Tenant Admin 创建的数据集由服务端强制写入当前 Tenant；
- Product 是数据集到 Milvus Metadata 的映射，不要求全局唯一；
- Dataset Role 单独存入`dataset_roles`，便于后续扩展 Group 和自定义角色。

### Application / Environment / Knowledge Binding

- `applications` 表示一个独立发布的 Agent 产品，不等同于 Dataset；
- `app_environments` 隔离 dev、staging、prod 的配置和索引发布；
- `knowledge_bindings` 表示应用在某个环境中可使用哪些 Dataset，并保存应用专属 Retrieval Policy；
- Tenant Admin 只能管理本租户 Application，Platform Admin 可以跨租户运维；
- Binding 创建时服务端再次检查 Dataset 授权，客户端不能通过 `tenant_id` 绕过边界。

### Control Plane Audit

管理变更保存：

- Actor Subject
- Tenant
- Action
- Resource Type / ID
- Before / After JSON
- Timestamp

首版已经对 Dataset Create 写入审计，后续成员邀请、撤权、归档和配额变更复用同一结构。

## 本地部署

```bash
make postgres-up
make postgres-status
make milvus-up
make serve-lab
```

默认连接：

```text
postgres://raglab:raglab-local@127.0.0.1:5433/raglab?sslmode=disable
```

可以使用`RAGLAB_POSTGRES_URL`替换。数据写入`data/postgres`，不会提交到 Git。

服务启动时：

1. 连接 PostgreSQL；
2. 获取 Advisory Transaction Lock；
3. 幂等创建表、约束和索引；
4. 初始化 Platform、Tenant A、Tenant B；
5. 初始化两个 Public Dataset 和两个 Tenant Dataset；
6. 连接失败则拒绝启动，不静默退化为内存授权。

本地 HS256 Demo 的三个账号由启动流程显式 Provision 到控制面；这只是本地引导。OIDC 生产模式不会因为请求第一次到达就自动写入用户或 Membership，必须通过邀请/管理员流程完成绑定。

明确传入空`--postgres-url`时才使用只读内存 Catalog，适合单元测试和最小演示。

## API

### 控制面状态

```http
GET /api/v1/control-plane/status
Authorization: Bearer <token>
```

返回 Backend、连接状态以及 Tenant、User、Membership、Dataset 数量。

### 当前 Tenant 成员

```http
GET /api/v1/memberships
Authorization: Bearer <tenant-admin-token>
```

Viewer 不能枚举成员。

### 创建数据集

```http
POST /api/v1/datasets
Authorization: Bearer <tenant-admin-token>
Content-Type: application/json

{
  "name": "客户成功知识库",
  "slug": "customer-success",
  "description": "Tenant A 自建数据集",
  "visibility": "tenant"
}
```

客户端不能提交 Owner Tenant、Product、Allowed Roles、Created By 或 Status。服务端生成：

```text
id      = tenant_a-customer-success
product = dataset-tenant_a-customer-success
owner   = tenant_a
roles   = [admin]
status  = active
```

### 创建 Agent Application 与知识绑定

```http
POST /api/v1/apps
Authorization: Bearer <tenant-admin-token>
Content-Type: application/json

{"name":"客服 Agent","slug":"support-agent","description":"面向客服的知识问答应用"}
```

服务端会自动创建 `dev` Environment。随后绑定知识库：

```http
POST /api/v1/apps/tenant_a-support-agent/bindings
Authorization: Bearer <tenant-admin-token>
Content-Type: application/json

{
  "environment_id":"tenant_a-support-agent-dev",
  "dataset_id":"tenant-a-operations",
  "purpose":"customer support",
  "policy":{"top_k":6,"candidate_k":24,"rerank":true,"token_budget":4000}
}
```

同一 Dataset 可以被多个 Application 绑定；绑定策略存储在控制面，不会修改其他应用的策略。

### 通过 Knowledge Gateway 查询

上层 Agent 不需要知道 Dataset 的 `product`、租户或 Milvus Filter：

```http
POST /api/v1/apps/tenant_a-support-agent/query
Authorization: Bearer <token>
Content-Type: application/json

{"environment_id":"tenant_a-support-agent-dev","query":"如何处理单点登录故障？","top_k":5}
```

服务端会按 Application + Environment 读取所有 active Binding，并对每个 Dataset 再做一次当前身份授权。多个知识库的召回结果会按距离合并、按 `chunk_id` 去重并截断到全局 `top_k`。跨租户访问、已撤权 Dataset 或没有绑定的环境均不会静默降级。

回答入口为 `POST /api/v1/apps/{app_id}/answer`，返回与旧 Dataset Answer 相同的引用校验结构，并额外附带 `app_id`、`environment_id` 和每个 Binding 的策略/命中数，便于 Agent harness 做观测与回归。

## 已验证问题

### Product 不能错误地全局唯一

Tenant A 与 Tenant B 的运维数据集可以映射到相同 Product，再通过 Tenant ACL 分开。第一轮真实迁移曾给 Product 添加全局唯一约束，Seed 阶段立即产生冲突。当前 Migration 会移除该错误约束。

### PostgreSQL Array 的驱动扫描差异

`array_agg(text)`通过`database/sql + pgx stdlib`返回的底层类型并不适合直接扫描到`[]string`。当前查询改用有序`string_agg`并由 Repository 解析，避免依赖驱动隐式转换。

这两个问题已经进入真实集成测试，而不只是写在文档中。

## 集成测试

设置：

```bash
RAGLAB_TEST_POSTGRES_URL='postgres://raglab:raglab-local@127.0.0.1:5433/raglab?sslmode=disable' \
  go test -v ./internal/datasetaccess
```

验证：

- Migration 和 Seed 可重复执行；
- Tenant Admin 创建自己的 Dataset；
- Owner 可以授权；
- 另一个 Tenant 被拒绝；
- Membership 设为`revoked`后立即失去访问；
- 后续请求不会自动恢复被撤销的 Membership；
- Dataset Create 产生一条控制面审计。
- Application、Environment、Binding 的租户隔离和默认策略落库。

## 下一步

- Membership 邀请、接受、Role 变更和撤权 API；
- Dataset Archive 与删除前引用检查；
- Document、DataSource、IngestionJob 外键化；
- Outbox 与 Worker Lease；
- 权限版本和短 TTL Cache；
- PostgreSQL 备份恢复与 Migration 回滚演练。
