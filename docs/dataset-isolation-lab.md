# 多租户数据集隔离实验

这一阶段验证的不是“前端把入口藏起来”，而是一个已认证用户能否在知道其他数据集 ID 的情况下绕过授权并取回向量数据。

## 三层边界

1. **认证**：本地实验用持久化账号换取 HS256 JWT；生产配置 OIDC Discovery/JWKS 并关闭本地注册、登录和演示 Token 接口。
2. **资源授权**：`GET /api/v1/datasets`只返回当前 Claims 可见的数据集；搜索前由服务端 Catalog 校验数据集归属和角色。
3. **行级隔离**：合法请求进入 Milvus 前，服务端把数据集映射为不可由客户端覆盖的 Product 和 Access Scope，再生成 Pre-ANN Scalar Filter。

```text
Bearer JWT
  -> signature / issuer / audience / expiry
  -> trusted subject / tenant_id / roles
  -> dataset catalog authorization
  -> product + tenant_only/public_only
  -> Milvus ANN pre-filter
  -> results + request audit
```

## 本地注册与生产账号体系

`POST /api/v1/auth/register`用于本地学习体验：

- 用户只能提交 Email、Password 和组织显示名；
- 服务端为每次自助注册生成新的隔离 Tenant；
- 用户不能加入已有 Tenant，不能选择 Role；
- 账号文件权限为`0600`，只保存随机 Salt 和 PBKDF2-HMAC-SHA256 派生值；
- 注册、登录响应不缓存；
- 登录失败统一返回`invalid email or password`。

这是本地可靠性基线，不是完整 IAM。生产环境必须使用企业 IdP 处理邮箱验证、组织邀请、MFA、风控、密码找回、禁用账号和 Session 撤销。配置`RAGLAB_AUTH_OIDC_ISSUER`或`RAGLAB_AUTH_JWKS_URL`后，本地账号路由不会注册。

## 可复现实验账号

默认只监听本地地址时会创建三个演示账号：

| 用户 | 密码 | 可信 Claims |
|---|---|---|
| `admin@raglab.local` | `RagLab-Platform-2026!` | `platform / platform_admin` |
| `alice@tenant-a.local` | `RagLab-Alice-2026!` | `tenant_a / admin` |
| `bob@tenant-b.local` | `RagLab-Bob-2026!` | `tenant_b / admin` |

平台管理员可以查看审计、知识生命周期和异步 Ingestion 管理接口；普通租户管理员不能获得这些权限。
管理员密码可通过`RAGLAB_PLATFORM_ADMIN_PASSWORD`覆盖。演示密码不得用于公网环境。
账号文件由`RAGLAB_AUTH_ACCOUNTS`控制，默认写入`data/auth/accounts.json`并被 Git 忽略。

## 数据集授权语义

| 数据集 | Scope | 访问规则 |
|---|---|---|
| `public-identity` | `public_only` | 所有已认证用户，只返回 Public Row |
| `public-reports` | `public_only` | 所有已认证用户，只返回 Public Row |
| `tenant-a-operations` | `tenant_only` | `tenant_a`且拥有`admin` |
| `tenant-b-operations` | `tenant_only` | `tenant_b`且拥有`admin` |

Public 数据集使用`public_only`，不会因为调用者恰好属于某个 Tenant 而混入同 Product 的私有数据。Tenant 数据集使用`tenant_only`，不会混入公开文档。

## 越权验证

Alice 登录后：

```bash
curl -s http://127.0.0.1:8080/api/v1/datasets \
  -H "Authorization: Bearer $ALICE_TOKEN"
```

目录中应出现`tenant-a-operations`，不得出现`tenant-b-operations`。

即使 Alice 猜到 Tenant B 的资源 ID：

```bash
curl -i -X POST \
  http://127.0.0.1:8080/api/v1/datasets/tenant-b-operations/search \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query":"专属应急队列是什么？","top_k":5}'
```

响应为`404 dataset_not_found`。不存在与无权限返回相同结果以减少资源枚举；自动化测试同时断言 Milvus Search Handler 没有被调用。

合法的 Tenant A 请求生成类似过滤器：

```text
(array_contains(allowed_tenants, "tenant_a")
 and array_contains(allowed_roles, "admin"))
and product == "tenant-operations"
and status == "active"
```

ACL 在 ANN 相似度计算前执行，而不是先召回再由应用层删除。

## 自动化门禁

- 注册账号不能声明已有 Tenant 或提权 Role；
- 两个相同组织显示名的自助注册仍生成不同 Tenant；
- 账号持久化后可以重启登录，文件权限保持`0600`；
- Tenant A 目录不包含 Tenant B 数据集；
- 跨租户资源请求不进入 Milvus；
- Tenant Dataset 缺少可信 Tenant/Role 时生成 Fail-Closed Filter；
- Public Dataset 不包含 Tenant ACL 分支；
- OIDC 模式不暴露本地账号和演示 Token 路由。

## 下一步生产化

- 把静态 Catalog 迁移到 PostgreSQL，增加 Dataset、Membership、Role Binding 和 Invitation 表；
- 资源授权采用 Policy Engine 或集中式 Authorization Service；
- 为权限变更增加版本号和短 TTL Cache，确保撤权快速生效；
- 将审计日志写入不可篡改的集中日志系统；
- 增加对象存储、Chunk、缓存和生成答案的全链路 Tenant Key；
- 增加批量 Hard Negative：枚举不同 Tenant、Role、Dataset、Document 与 Cache Key 的组合。
