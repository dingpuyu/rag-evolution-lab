# 企业 RAG 回归记录（2026-07-27）

这份记录用于复现当前版本的关键能力：业务门户与离线评测使用独立的
Milvus Lifecycle Collection，评测结果不会被门户导入的资料污染。

## 运行拓扑

| Profile | API | Lifecycle Collection | Alias | 用途 |
|---|---|---|---|---|
| portal | `127.0.0.1:8080` | `raglab_lifecycle_v1` | `raglab_knowledge_active` | 登录、资料导入、客服问答 |
| evaluation | `127.0.0.1:8081` | `raglab_lifecycle_eval_v1` | `raglab_knowledge_eval` | 固定数据集检索与答案评测 |

两个服务可以连接同一个 Milvus 实例，但写入集合、别名、生命周期状态文件和
异步导入任务状态文件均不同。评测集合只由 harness 幂等播种，不会读取门户的
`portal-*` 或 `regression-smoke` 资料。

## 回归命令

```bash
# 业务 API 回归（需要 portal 服务运行在 8080）
make regression-smoke

# 隔离评测服务（另一个终端保持运行）
make serve-lab-eval
make dataset-eval-isolated
make answer-eval-blind-isolated
```

## 本次结果

### 业务 API 回归

`make regression-smoke`：通过。

- Alice / Bob / Platform Admin 登录成功
- 租户数据集可见性正确：Alice 看到 5 个，Bob 看到 3 个
- 跨租户数据集搜索与资料写入均返回 `404`，避免资源枚举
- 服务端检索 Filter 含租户与角色约束，客户端不能放宽
- 资料导入完成 Embedding、Milvus Upsert 和读回校验，`verified=true`
- SSE 顺序为 `started → retrieved → completed → done`
- 平台管理员审计接口 `200`，租户管理员访问 `403`

### 隔离检索评测

报告：[dataset-search-latest.md](../eval/reports/dataset-search-latest.md)

- 8 / 8 用例通过
- Hit@K `1.000`，MRR `1.000`
- P50 `149.8 ms`，P95 `287.0 ms`
- 越权召回、禁止事实、Filter 违规、API 契约违规均为 `0`
- 结果只包含 harness 文档，未出现门户导入文档

### 隔离答案评测

报告：[grounded-answer-blind-isolated-latest.md](../eval/reports/grounded-answer-blind-isolated-latest.md)

- 8 / 8 用例通过
- Answerability `1.000`，Required Fact Coverage `1.000`
- 禁止事实、引用违规、越权召回、契约违规均为 `0`
- P50 `2040.7 ms`，P95 `3727.8 ms`
- 使用 OpenAI-compatible DeepSeek：`deepseek-v4-pro`
- 统计 Token `1829 / 941`；未配置费率，因此成本仅作 Token 记录

## 结论与下一步

当前版本已经形成“业务链路 + 离线检索评测 + 生成答案评测”的最小闭环，且
业务数据和评测数据完成物理集合隔离。下一步优先级：

1. 将回归命令接入 CI，固定 Go、Python、前端和真实 API 四层门禁。
2. 为评测报告增加基线对比，持续跟踪 Hit@K、MRR、Fact Coverage、P95 和 Token 成本。
3. 在 100K Milvus 基准上补充并发、慢查询、索引重建和故障恢复演练。
4. 接入真实 OIDC/JWKS 与密钥轮换，替换本地 HS256 demo 账号作为生产认证路径。
