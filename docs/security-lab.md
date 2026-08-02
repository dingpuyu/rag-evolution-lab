# RAG 身份隔离安全演练

这份文档把“身份隔离”当成一个可以攻击、验证和回归的工程能力。演练只针对本地 Compose/PostgreSQL 和 `httptest`，不把故意漏洞部署到公网。

## 1. 威胁模型

攻击者可能已经拿到一个合法的低权限 Token，或者拿到某个 Agent 应用的 Credential，但不能伪造 OIDC 签名。需要防住的不是“没有登录的人”，而是：

| 攻击动作 | 目标 | 正确结果 |
|---|---|---|
| 请求体伪造 `tenant_id` / `role` | 提升检索权限 | 忽略请求体，使用验签 Claims |
| 猜测其他数据集 ID | 枚举资源 | 返回统一 404，不进入 Milvus |
| 未邀请 Subject 携带某租户 Claim | 自助加入租户 | 控制面拒绝，不能自动创建 Membership |
| admin Token 被降级为 viewer | 继续使用旧权限 | DB Membership 与当前 Token 角色不一致时拒绝 |
| 直接连接 Milvus | 绕过 API 过滤器 | 网络层不可达；向量库不能暴露公网 |
| AppCredential 调用其他 App | 跨应用读知识库 | 精确应用路径 + Scope + Binding 三重拒绝 |

## 2. 漏洞 A：自动 Membership 导致自助入租户

### 漏洞原理

旧逻辑在每次请求的 `EnsureIdentity` 中执行：

```sql
INSERT INTO memberships (tenant_id, subject, role, status)
VALUES ($tenant, $subject, $role, 'active')
ON CONFLICT DO NOTHING;
```

因此，一个签名有效但从未被邀请的 Subject，只要 Token 中带有 `tenant_id=tenant_a` 和 `role=admin`，就可能被自动写入控制面并看到 Tenant A 数据集。签名证明“Token 来自 IdP”，不等于证明“这个人已获准访问租户”。

### 修复原则

- 普通请求只读校验 `tenants`、`users`、`memberships`；
- `ProvisionIdentity` 作为显式控制面写操作，只能由邀请、管理员或本地 Demo Bootstrap 调用；
- Membership 的数据库角色必须出现在当前 Token 的角色集合中；
- 租户、用户和 Membership 任意一个不是 active 都 Fail Closed；
- OIDC 模式不连接本地账号和自助注册路径。

## 3. 漏洞 B：角色降级后复用旧 admin

### 漏洞原理

如果数据库里已经存在 `subject=u, role=admin`，旧的 `ON CONFLICT DO NOTHING` 不会更新它。即使 IdP 后续签发的 Token 只有 `viewer`，查询仍然只看数据库里的 admin Membership，造成权限撤销延迟甚至永久失效。

### 修复原则

授权必须满足：

```text
租户 active
∧ 用户 active
∧ Membership active
∧ DB role ∈ 当前 Token roles
```

撤权或角色变更通过显式 `ProvisionIdentity`/管理流程更新数据库，普通请求绝不写权限。

## 4. 漏洞 C：运维与向量化接口暴露

生产 API 只把 `/healthz` 作为公开探针。Milvus 状态、Embedding 信息、Similarity 和 Scale 状态属于内部或管理接口：

- 未认证请求必须返回 401；
- 通过统一身份中间件产生 Request ID 和审计事件；
- Similarity 还必须有租户/应用限流，避免远端 Embedding 被滥用产生费用。

Milvus 自身不是身份边界。即使每一行都有 `allowed_tenants`，直接拿到 Milvus 凭据仍可发送无过滤查询，所以 Compose 已将 Milvus 主机端口限制为 loopback；生产还应放在私有子网或服务网格内。

## 5. 自动化证据

本地 PostgreSQL 回归：

```bash
RAGLAB_TEST_POSTGRES_URL='postgres://raglab:<password>@127.0.0.1:5433/raglab?sslmode=disable' \
  go test ./internal/datasetaccess -run 'TestPostgres(UnprovisionedIdentity|RoleDowngrade)' -count=1 -v
```

HTTP 回归：

```bash
go test ./internal/httpapi -run 'TestEnterprise(OperationalAndEmbeddingRoutesRequireIdentity|SearchRequiresJWTAndIgnoresClientSuppliedIdentity)' -count=1 -v
```

应观察到：

- 未 Provision 的 Subject 不能看到租户目录；
- 角色降级请求被拒绝；
- 伪造请求体中的租户和角色不出现在 Milvus Filter；
- 跨租户资源请求不触发 Milvus Search；
- 未认证的内部运维接口返回 401，健康检查仍返回 200。

## 6. 生产前检查

```bash
RAGLAB_REQUIRE_OIDC=true \
RAGLAB_AUTH_OIDC_ISSUER=https://id.example.com/realms/acme \
RAGLAB_AUTH_JWKS_URL=https://id.example.com/realms/acme/protocol/openid-connect/certs \
RAGLAB_POSTGRES_PASSWORD='<secret>' \
RAGLAB_MINIO_ROOT_USER='<user>' \
RAGLAB_MINIO_ROOT_PASSWORD='<secret>' \
RAGLAB_POSTGRES_URL='postgres://...?...&sslmode=verify-full' \
make production-preflight
```

门禁通过只代表“身份和配置没有明显越界”，不替代 HTTPS、私网、安全组、备份恢复、密钥轮换、日志脱敏和真实 OIDC 集成测试。

## 7. 面试讲法

可以这样解释项目经验：

> 我把多租户 RAG 的安全边界放在 API Gateway 和控制面，而不是放在前端或向量库字段上。请求先完成 OIDC/JWKS 验签，再由 PostgreSQL 校验用户、租户、Membership 和应用 Binding；服务端根据可信 Claims 生成 Milvus Pre-ANN Filter，客户端不能提交 Filter。我们专门构造了“未邀请用户自带 tenant Claim”“角色降级复用旧 admin”“跨租户 ID 枚举”“未认证运维接口”四类漏洞，并用回归测试证明修复后不会进入 Milvus 或生成答案上下文。生产上再通过私网隔离 Milvus、密钥管理、审计脱敏和 OIDC 启动门禁完成纵深防御。

这比只说“数据库有 tenant_id 字段”更能体现真实企业 RAG 的安全经验。
