# ADR-0005：按 Query 特征和风险选择检索策略

- 状态：Accepted
- 日期：2026-07-16

## 背景

V3 证明固定使用 Hybrid Union 或 Consensus 都存在结构性缺陷：Union 的语义召回更高，但可能在应拒答时返回合法却无关的证据；Consensus 更保守，但会丢失只被向量路发现的语义证据。

## 决策

V4 在检索前使用确定性分类器，只读取 Query 文本和请求上下文，不读取 Golden Category：

| Intent | 可观察特征 | 策略 |
|---|---|---|
| `exact` | 错误码、Header、权限标识、显式版本、数值问题 | Metadata BM25 |
| `semantic` | 无强标识符的自然语言表达 | Metadata Hybrid Union |
| `access_sensitive` | 租户、专属资源、跨租户或高权限操作 | Tenant Scope Gate + Hybrid Consensus |
| `unanswerable_risk` | 外部认证或状态核验 | Anchor Gate + Hybrid Consensus |

分类结果、原因和实际策略写入 Query Trace。评测报告保存每条 Case 的 Route 和总体 Route Distribution。

## 安全约束

- Query 显式引用的 Tenant 与认证上下文冲突时，在检索前 Fail Closed。
- 未知认证、代码或版本必须在证据中保留结构化锚点，语义相似不能代替证明。
- Product、Version、Lifecycle 和 ACL 仍在具体 Retriever 评分前执行。
- 分类器失败时不得绕过已有权限过滤。

## 影响

- Development Split 的 20 次 Query 中，只有 9 次需要 Vector，减少了本地模型调用。
- 规则分类器可解释、可单测，但同义表达覆盖有限，需要 Challenge Split 持续捕获误路由。
- 当前结果来自小型合成语料，不代表生产泛化；扩大 Golden Dataset 和独立 Blind Split 是下一项数据工作。
- 后续可以用小模型分类器替换规则，但必须保留确定性安全 Gate 和离线回退。
