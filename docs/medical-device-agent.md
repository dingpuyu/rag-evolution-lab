# 医疗设备知识问答 Agent

## 定位与边界

PulseCare 是一套完全虚构的医疗设备运维知识域，用来验证复杂文档、多型号、多版本、错误码、批次、租户隔离和人工 Bad Case 闭环。它面向设备运维知识检索，不提供患者诊断、治疗、用药、报警阈值或真实设备操作建议。

独立体验页为 `/medical`，复用平台已有的登录、Tenant、Application、Environment、Knowledge Binding 和 Dataset 权限。Tenant A 与 Tenant B 都拥有一个同名 `medical-device-agent`，只绑定公共设备资料和本租户私有 Runbook。

## 运行架构

```text
浏览器 /medical
  ├─ 登录与控制面 ───────────────→ Go API → PostgreSQL
  ├─ 多格式上传 → MinIO 原件 → Python Parser → Document IR
  │                                      └→ 异步 Index Job
  │                                          → Qwen text-embedding-v4 (1024)
  │                                          → Milvus versioned collection
  ├─ Agent SSE → Python + LangGraph
  │                 ├→ 规则化范围/型号/版本解析
  │                 ├→ 确定性现场通知适用性工具
  │                 ├→ Go Knowledge Gateway
  │                 │    ├→ Dataset/Tenant/Role pre-ANN filter
  │                 │    ├→ Exact identifier + BM25 + Dense + RRF
  │                 │    └→ qwen3-rerank
  │                 └→ DeepSeek grounded answer
  └─ Quality → 40 RAG cases + 10 Agent cases → PostgreSQL → Bad Case review
```

职责边界：

- Go API 是可信控制面和 Knowledge Gateway。客户端不能提交 Tenant、Role、物理 Collection 或 Milvus Filter。
- Python Parser 是无状态文件解析服务；原件与 Document IR 存 MinIO，文档状态与修订存 PostgreSQL。
- Milvus 是检索数据面，新 Schema 包含 `dataset_id`、医疗适用范围、原文定位、稠密向量和 BM25 sparse vector。
- LangGraph 只编排有限状态业务图，不运行无边界自主循环。
- DeepSeek 只根据已验证证据组织答案；现场更正是否适用由确定性工具判定。

## 文档接入

`POST /api/v1/datasets/{dataset_id}/documents/uploads` 接收 Markdown、HTML、PDF、DOCX 和 XLSX。服务端确定 Dataset 与权限，文件上限为 50 MiB。Parser 统一输出 `document-ir-v1`：

```json
{
  "block_type": "table",
  "text": "配件 | 型号 | 最低版本",
  "heading_path": ["兼容性矩阵"],
  "provenance": {
    "source_file": "matrix.xlsx",
    "page": 0,
    "sheet": "兼容矩阵",
    "cell_range": "A1:D8"
  }
}
```

页面会显示本次解析预览。页码、标题路径、工作表和单元格范围继续进入 Chunk、Milvus Hit 和最终 Citation。扫描 PDF 会标记 `ocr_required` 并阻止发布；OCR 不在本阶段范围。

索引任务具备幂等键、队列、Worker Lease、Heartbeat、失败原因、最多三次尝试、人工重试、取消和写后 Strong Query 验证。原件解析目前在上传请求内完成，Embedding 与索引异步执行；进一步处理超大文件时，可把 Parser 阶段也下沉到同一 Job 状态机。

医疗元数据包括：

- Dataset、Domain、Manufacturer、Product Family；
- Model Codes、软件版本起止、Hardware Revision；
- Region、Language、生效/失效日期；
- Authority Level、Document Revision、Supersedes；
- Device Identifiers、Affected Lots；
- Source File、Page、Sheet、Cell Range、Heading Path。

## 检索与安全

主检索链路是：

```text
原始问题
→ 安全 Query Rewrite（原始标识符原样保留）
→ Application/Environment 解析绑定
→ PostgreSQL Dataset 授权
→ Milvus Dataset/Tenant/Role/型号/版本 pre-ANN filter
→ Dense + BM25 两路召回
→ Milvus RRF(k=60)
→ 字面型号/错误码/批次确定性提升
→ qwen3-rerank
→ 型号、版本、状态和适用范围验证
→ 有据回答 / 澄清 / 拒答
```

Milvus Collection 使用 BM25 Function 自动从 `content` 生成 `SparseFloatVector`，并分别建立 HNSW/COSINE 与 SPARSE_INVERTED_INDEX/BM25。Hybrid Search 的两条子查询都携带同一个服务端 ACL Filter，避免“向量分支安全、全文分支泄漏”的常见错误。

`dataset_id` 是必备字段。新 Schema 发布到新物理 Collection，readiness gate 检查字段、1024 维 Embedding、行数和索引 Finished 后再切换 Alias/环境发布指针；不原地修改旧索引。

## Agent 决策图

```text
classify_scope
→ resolve_device_context
→ medical_persona / medical_refuse / medical_clarify / medical_search
→ verify_evidence
→ generate_grounded_answer
→ finalize
```

- 问候：固定运维人设，不访问 RAG。
- 型号存在歧义：返回候选型号并澄清。
- 临床诊断、治疗、患者参数：确定性拒答。
- 运维问题：无证据、证据与型号/版本冲突或 Gateway 不可用时不调用模型记忆兜底。
- `FC-2026-04`：必须具备完整型号、三段软件版本和批次；工具返回 `applies`、`does_not_apply` 或 `needs_information`，LLM 无权改判。
- 每次运行受最大步数、模型超时和应用配额限制。

SSE 事件包括 `step`、`retrieval`、`token`、`citation`、`decision`、`done`、`error`。最终响应包含 `decision`、`reason_code`、`resolved_context`、`candidate_entities`、`trace_id` 和增强 Citation。

## 评测闭环

静态数据集共有 40 条：Development 21 条、Regression 19 条，覆盖同码异义、相似型号、版本冲突、跨格式、澄清、临床拒答、现场通知、租户隔离、Prompt Injection 和过期资料污染。

网页评测一次执行两层测试：

- RAG：运行当前租户有权限的 Golden Cases，计算 Hit@5、MRR、NDCG、CorrectModel@5、CorrectVersion@5、WrongModelRate 和 Permission Leaks。
- Agent：10 条核心决策用例，计算决策准确率、临床拒答召回率、通知适用性准确率和延迟。

运行、Case、Evidence 和 Event 均持久化到 PostgreSQL；Bearer Token 只保存在当前内存任务中，不落库。失败题可在网页展开实际证据并人工标记为 Bad Case，记录 Root Cause 和备注。

发布硬门禁：跨租户证据、未授权召回和引用越界必须为 0；临床拒答与现场通知确定性判断必须全部通过。真实模型指标由网页/本地验收产生，CI 使用 Mock Embedding/LLM 保证确定性。

## 本地启动与验收

密钥只由进程环境注入，不写入仓库：

```bash
launchctl setenv QWEN_API_KEY '...'
launchctl setenv DEEPSEEK_API_KEY '...'
make medical-up
make medical-bootstrap
make medical-smoke
```

Embedding 模型、维度或语义版本变化时，必须使用新的物理 Collection，并递增源修订以强制重新解析和向量化，禁止把不同模型的向量写入同一索引：

```bash
MEDICAL_SOURCE_REVISION=2 make medical-bootstrap
```

默认真实模型配置：

- Embedding：`text-embedding-v4`，1024 维；
- Rerank：`qwen3-rerank`；
- Generation：`deepseek-chat`。

离线数据集验证：

```bash
make medical-eval-all
```

如果本地 `.env` 配置了 `RAGLAB_WEB_PORT=13000`、`RAGLAB_API_PORT=18080`，页面地址为 `http://localhost:13000/medical`。`make medical-bootstrap` 会上传 17 份 Markdown 原始资料及 PDF、DOCX、XLSX、HTML 派生文件。

## 面试讲解抓手

建议按“问题—错误方案—生产方案—证据”讲：

1. 相似型号和同码异义导致纯向量召回跑题，所以加入 Dataset/型号/版本前置过滤、BM25、Exact identifier、RRF 和 hosted rerank。
2. 文件解析成纯文本后引用无法复核，所以建立 Document IR，并让页码/Sheet/Cell Range 贯穿整个链路。
3. 只在 Prompt 中写权限不安全，所以授权在 PostgreSQL 控制面完成，Filter 在 ANN 前由服务端生成，两条混合召回分支使用同一 Filter。
4. LLM 不适合做通知范围比较，所以把版本和批次适用性做成确定性工具，模型只解释结果。
5. 不追求第一次就完美，用 40 条固定回归、在线 Trace 和人工 Bad Case 建立持续优化闭环。

当前明确限制：OCR、真实医院系统、真实工单写入和云部署不在本阶段；所有资料完全虚构。
