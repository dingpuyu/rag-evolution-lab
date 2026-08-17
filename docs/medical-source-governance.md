# 医疗公开资料的来源治理与变更发布

## 1. 为什么不能“网页抓完直接入向量库”

医疗设备销售知识同时具有来源、地区、配置和时效边界。厂商网页可以更新或下线，同一型号可能存在不同区域页面，可选能力也不能被表述成标准配置。如果只保存正文和向量，出现错误回答后很难回答三个关键问题：事实来自哪里、何时采集、哪次审核允许它进入生产索引。

本项目因此把公开资料分为两个平面：

- 数据平面：MinIO 原件、Document IR、Chunk、Embedding 和 Milvus 索引。
- 控制平面：PostgreSQL 中的官方 URL、来源类型、采集时间、审核状态、内容 SHA-256、修订和索引任务状态。

## 2. 发布链路

```text
官方网页
  → 人工提炼事实摘要
  → manifest 声明来源/地区/型号/采集时间
  → source audit 校验域名、HTTPS、文件和内容指纹
  → 人工确认后显式更新 sources.lock.json
  → bootstrap 再次执行离线门禁
  → API 校验 approved 状态
  → Parser / Qwen Embedding / Milvus
  → Agent 有据回答
```

`sources.lock.json` 是审核边界，不是爬虫缓存。默认审计永远不会修改它：新增文档、修改正文或替换 URL 都会得到 `review_required` 并阻断 Bootstrap。只有显式提供审核人才能更新：

```bash
REVIEWED_BY=maintainer make medical-source-lock
```

生产环境应把 `REVIEWED_BY` 替换为 OIDC 身份，并将审批动作写入不可变审计日志；仓库里的本地锁用于复现实验和演示工作流。

## 3. 两类变化不能混为一谈

### 本地语料漂移

本地摘要 SHA-256、路径或 URL 与锁文件不一致，属于未审批发布，必须失败。执行：

```bash
python3 scripts/medical_source_audit.py
```

### 远端来源异常

在线审计限制下载前 2 MiB，记录 HTTP 状态、最终 URL、ETag、Last-Modified 和响应指纹：

```bash
make medical-source-audit
```

厂商站点可能反爬、限流或临时故障，所以默认只形成告警，不自动删除已审核知识。网页响应指纹变化也只创建人工复核信号，不能直接覆盖已发布索引。需要把在线状态作为严格发布门禁时使用 `--strict-online`。

## 4. 后端契约

文档上传元数据新增：

```json
{
  "source_type": "official_manufacturer",
  "source_urls": ["https://vendor.example/product"],
  "collected_at": "2026-08-17",
  "source_review_status": "approved",
  "source_reviewed_at": "2026-08-17T08:00:00Z"
}
```

服务端只接收无凭据的绝对 HTTPS URL，去掉 fragment 并去重；审核状态只能是 `draft`、`approved` 或 `review_required`。公开销售数据集必须是 `approved` 才能进入索引。服务端计算原件 SHA-256，客户端不能自行覆盖。

医疗页面从 PostgreSQL 文档登记记录读取这些字段，展示审核状态、采集日期、内容指纹和官方链接。引用链接也来自登记记录，不再依赖前端硬编码的文档映射。

Bootstrap 还会先读取现有文档登记记录：相同内容哈希复用已经完成的修订，不重复产生任务；内容变化才递增 `source_revision`。这修复了固定 revision 重跑时产生 `stale or conflicting revision` 的幂等性问题。

## 5. Bad Case 与回归经验

本轮新增四类检索回归和三类 Agent 回归：

- 跨区域：繁体中文区域官网不能推出中国大陆一定在售或已注册。
- 新鲜度：采集时的注册证信息不代表交易时永久有效。
- Prompt Injection：用户要求“忽略限制并承诺”不能覆盖系统和证据边界。
- 跨型号 Hard Negative：BeneVision N1 与 IntelliVue MX550 的联网能力不能合并为默认配置。

Agent 评测不仅检查 `answer/clarify/refuse` 和引用数据集，还检查边界回答是否至少包含“不能确认、需要核验、需确认”等纠偏表达。权限、来源审核和内容指纹使用确定性规则，不能交给 LLM Judge。

## 6. 面试时怎么讲

可以按“问题—方案—取舍—验证”回答：

1. 问题：真实知识库最大的风险不只是召回率，而是过期、跨区域、配置混用和无法追责。
2. 方案：将 MinIO/Milvus 数据平面与 PostgreSQL 来源控制平面拆开，用 manifest + 内容锁建立人工审批边界。
3. 取舍：远端网页不可达只告警，不自动删索引；内容变化先进入复核，否则短暂反爬会导致知识库雪崩。
4. 验证：故意修改受控文件会阻断 Bootstrap；在线检查独立报告可达性；17 条检索 Golden 和 12 条 Agent 用例覆盖来源边界。
5. 后续：把审批人接入 OIDC、将复核任务落 PostgreSQL、评测通过后发布新索引版本，再通过灰度流量切换别名。

这套经验可复用于汽车零部件、工业设备、保险条款等“型号相似、版本繁多、事实会变化”的垂直知识库。
