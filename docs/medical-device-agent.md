# 医疗设备销售顾问与知识运维 Agent

## 定位与边界

项目包含两个严格隔离的知识域：客户侧是医疗设备销售公司的公开产品顾问，使用厂商官网和国家药监局公开信息的事实摘要；专业运维侧保留 PulseCare 虚构知识域，用来验证复杂文档、多型号、多版本、错误码、批次、租户隔离和人工 Bad Case 闭环。两侧都不提供患者诊断、治疗、用药、报警阈值或真实设备操作建议。

独立体验页为 `/medical`，复用平台已有的登录、Tenant、Application、Environment、Knowledge Binding 和 Dataset 权限。每个 Tenant 都有两个相互隔离的应用：

- `medical-device-customer-agent`：客户销售顾问，只绑定 `public-medical-device-sales` 公开销售资料；
- `medical-device-agent`：专业运维助手，绑定公共设备资料和本租户私有 Runbook。

这不是前端 Prompt 开关。应用绑定由 Go 控制面持久化，直接调用 API 也不能让客户应用读取私有 Runbook。客户账号只使用稳定的数据面应用契约，不具备应用、环境、绑定和评测控制面的读取权限。

## 运行架构

```text
浏览器 /medical
  ├─ 客户销售顾问 ───────────────→ 厂商官网/NMPA 公开事实摘要
  ├─ 专业运维助手 ───────────────→ 公共资料 + 本租户 Runbook
  ├─ 管理员控制面 ───────────────→ Go API → PostgreSQL
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
  └─ Quality → 静态 Golden + human regression → PostgreSQL → 诊断/单题复测/回归晋升
```

职责边界：

- Go API 是可信控制面和 Knowledge Gateway。客户端不能提交 Tenant、Role、物理 Collection 或 Milvus Filter。
- Python Parser 是无状态文件解析服务；原件与 Document IR 存 MinIO，文档状态与修订存 PostgreSQL。
- Milvus 是检索数据面，新 Schema 包含 `dataset_id`、医疗适用范围、原文定位、稠密向量和 BM25 sparse vector。
- LangGraph 只编排有限状态业务图，不运行无边界自主循环。
- DeepSeek 只根据已验证证据组织答案；现场更正是否适用由确定性工具判定。

## 零基础客户旅程

客户助手把“先学会术语再提问”改为逐步引导：

```text
从零认识病人监护、AED、输注、超声和呼吸机
→ 浏览 BeneVision、BeneHeart、BeneFusion、Resona、IntelliVue、Evita
→ 用白话了解产品定位、官网公开特色和配置边界
→ 报价前核验地区、注册、配置、接口、库存与时效
→ 描述现象或错误码
→ 进行安全、非侵入式外部检查
→ 证据不足时补充信息，必要时升级给专业人员
```

客户专用知识包括公开产品线导航、厂商型号摘要、NMPA UDI 核验流程和公司安全售后分诊指南。回答默认短而分步，术语首次出现会解释；缺少型号的排障请求先澄清，临床问题确定性拒答。生成前还有防御性证据过滤：引用必须来自 `public-medical-device-sales`，原虚构设备库与内部 Runbook 即使因错误配置进入候选集也会被丢弃。

## 公开销售语料治理

`datasets/domains/medical-device-sales` 与虚构工程测试数据完全分离。首批语料包含 9 份资料、46 个已发布 Chunk：迈瑞 BeneVision N1、BeneHeart C 系列、BeneFusion i/u、Resona I9，飞利浦 IntelliVue MX500/MX550，德尔格 Evita V800，跨品牌产品线导航、NMPA UDI 核验和安全售后分诊。

语料不整页复制厂商网页或说明书，而是保存可检索的事实摘要，并在文档中记录官方 URL、采集日期、地区和配置边界。官网公开能力不等于当前库存、报价、注册有效性或标准配置；正式交易必须再次核验。客户回答的引用卡片可直接打开官方来源。

## 文档接入

`POST /api/v1/datasets/{dataset_id}/documents/uploads` 接收 Markdown、HTML、PDF、DOCX 和 XLSX。服务端确定 Dataset 与权限，文件上限为 50 MiB。Parser 当前统一输出 `document-ir-v4`：

```json
{
  "block_type": "table",
  "text": "型号: VSM-100 Pro | 配件: WLM-2 | 最低固件: 3.4",
  "heading_path": ["兼容性矩阵"],
  "provenance": {
    "source_file": "matrix.xlsx",
    "page": 0,
    "sheet": "兼容矩阵",
    "cell_range": "A1:E1,A4:E4"
  }
}
```

页面会显示本次解析预览。页码、标题路径、工作表和单元格范围继续进入 Chunk、Milvus Hit 和最终 Citation，Cleaner 删除项以原因和原 Block 单独保留。DOCX 按 XML 中的真实段落/表格顺序解析；XLSX 形成带表头语义的行级 Block；PDF 文本和表格按页面坐标重排。扫描 PDF 可交给独立 PaddleOCR PP-StructureV3 Worker；未配置 OCR、没有识别结果或低置信度时会阻止发布。刷新页面后，管理员还可以从 MinIO 重新读取 Document IR，并查看原件、解析、切块、Embedding、Milvus、写后验证和可检索七阶段状态。完整设计与 Bad Case 见 [Document IR v4](medical-document-ir-v2.md)，工作台的接口和验收方式见 [真实文档上传与知识状态工作台](document-ingestion-workbench.md)。

质量页还提供跨运行的 Bad Case 工作台。人工修订正确文档、设备上下文或来源定位后，旧验证会自动失效；单题复测通过才能晋升 `human_regression`，下一次完整评测自动执行。真实 XLSX 修复与面试讲法见 [Bad Case 人工修复闭环](medical-bad-case-loop.md)。

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

当一个问题同时出现多个明确型号时，Agent 不再把整句交给一次 Top-K 检索。它先保留全部显式型号，再按型号并行调用同一个受权 Knowledge Gateway，最后按实体交错合并并去重证据。每个子检索仍经过服务端 Dataset/Tenant/Role 过滤；这既避免高相似型号占满候选集，也保证某个型号不会“被另一个型号的资料代表”。Gateway Trace 使用随机 128-bit ID，能够正确区分同一 Agent Run 中的并发子检索。

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

专业运维静态数据集共有 43 条：Development 21 条、Regression 22 条，覆盖同码异义、相似型号、版本冲突、跨格式、澄清、临床拒答、现场通知、租户隔离、Prompt Injection 和过期资料污染。新增的 PDF、DOCX、XLSX 用例不仅检查文档命中，还断言页码、完整标题路径、工作表和单元格范围。销售客户应用另有 17 条独立 Golden Cases，覆盖真实产品线、型号特色、数值规格、配置边界、系统集成、监管核验、售后分诊、跨区域、新鲜度和用户 Prompt Injection。

网页评测一次执行两层测试，并根据当前应用选择不同 Agent 套件：

- RAG：运行当前租户有权限的 Golden Cases，计算 Hit@5、MRR、NDCG、CorrectModel@5、CorrectVersion@5、SourceLocationAccuracy、WrongModelRate 和 Permission Leaks。
- 专业 Agent：10 条核心决策用例，覆盖型号澄清、临床拒答、现场通知适用性和错误码有据回答。
- 客户 Agent：12 条核心决策用例，覆盖从零导览、真实产品线、型号对比、配置边界、缺型号排障、价格库存边界、临床拒答、内部资料探测、跨区域、新鲜度和无证据承诺攻击。

运行、Case、Evidence 和 Event 均持久化到 PostgreSQL；Bearer Token 只保存在当前内存任务中，不落库。失败题可在网页展开实际证据并人工标记为 Bad Case，记录 Root Cause 和备注。

发布硬门禁：跨租户证据、未授权召回和引用越界必须为 0；临床拒答与现场通知确定性判断必须全部通过。真实模型指标由网页/本地验收产生，CI 使用 Mock Embedding/LLM 保证确定性。

### 当前销售应用真实模型基线（2026-08-17）

上一版销售客户应用使用 Qwen `text-embedding-v4`、`qwen3-rerank` 与 DeepSeek 完成 22 条在线用例：22/22 通过。当前 v3 套件已经扩展为 17 条 RAG Golden Cases 和 12 条 Agent 用例；旧满分不作为新套件通过的证据。

2026-08-17 在 Document IR v2 全量重建后的最新销售回归 `medeval_9f778a8a960d4aff9f999aab7ec4f797`：29/29 通过，Hit@5、MRR、NDCG、CorrectModel@5、Agent 决策准确率、临床拒答召回率均为 1.0，权限泄漏 0，发布门禁通过。

本次闭环实际发现并修复两类问题：Agent 容器缺少销售 Golden 路径导致评测 500；客户索要内部 Runbook 时系统正确拒绝提供，但旧评测期望错误。修复后，销售套件只对适用指标设置门禁，不再用专业运维的“软件版本正确率”错误评价无版本语义的销售资料。

v3 回归又发现一个更接近生产的 Bad Case：用户用“忽略资料限制并直接承诺”污染检索 Query 时，系统虽然没有越权承诺，却因短型号未解析而把已有证据判成冲突，首次只得到 28/29。修复方式不是放宽校验，而是把“原始问题”和“检索 Query”分离：确定性移除控制语言、保留型号与规格词，同时补齐 `BeneHeart C` 到完整产品族的别名解析；原始问题仍交给 DeepSeek 用于回答。复测达到 29/29，页面能够引用官方来源纠正错误前提。

### 当前专业运维应用真实模型基线（2026-08-17）

Document IR v2 首次在线回归 `medeval_004f111757f0491f92b2517e861402aa` 达到 49/49，但 MRR 仅 0.7838，因此没有通过既定的 0.80 发布门禁。分析得到两个真实问题：

1. XLSX 结构已经正确，但跨格式副本的适用型号元数据只包含 `VSM-100`，导致 `VSM-100 Pro` 查询在 pre-ANN filter 阶段把正确行过滤掉；修复后元数据变化会形成新索引修订，目标 `3.4` 行回到第 1。
2. DOCX/PDF/HTML 是经过审核的同内容表达，排在原 Markdown 前面时不应被评测当作无关文档；建立等价文档组后，来源专用用例仍继续严格断言页码、标题路径和单元格范围。

第二次真实回归 `medeval_0ad1723d5963455a96c29b69284603c4`：49/49 通过，Hit@5 1.0、MRR 0.8394、NDCG 0.8351、CorrectModel@5 1.0、CorrectVersion@5 1.0、SourceLocationAccuracy 1.0、Agent 决策准确率 1.0、权限泄漏 0，发布门禁通过。整个过程没有下调阈值。

## 本地启动与验收

密钥只由进程环境注入，不写入仓库：

```bash
launchctl setenv QWEN_API_KEY '...'
launchctl setenv DEEPSEEK_API_KEY '...'
make medical-up
make medical-bootstrap
make medical-smoke
```

Embedding 模型、维度或语义版本变化时，必须使用新的物理 Collection，禁止把不同模型的向量写入同一索引。Parser Schema 或影响检索的元数据发生变化时，Bootstrap 会比较 `document_ir_schema_version` 和 `ingestion_metadata_sha256`，自动产生新修订并重新向量化；不再只依赖文件 SHA-256：

```bash
make medical-bootstrap
```

默认真实模型配置：

- Embedding：`text-embedding-v4`，1024 维；
- Rerank：`qwen3-rerank`；
- Generation：`deepseek-chat`。

离线数据集验证：

```bash
make medical-eval-all
```

如果本地 `.env` 配置了 `RAGLAB_WEB_PORT=13000`、`RAGLAB_API_PORT=18080`，页面地址为 `http://localhost:13000/medical`。`make medical-bootstrap` 会同时上传隔离的销售公开语料与专业运维测试语料。

Bootstrap 会先校验 `sources.lock.json`；官方摘要、URL 或内容指纹发生未经审核的变化时直接终止。来源治理、在线健康检查和面试讲法见 [医疗公开资料来源治理](medical-source-governance.md)。

本地客户体验账号为 `customer@tenant-a.local`；该账号角色为 `viewer`，只能进入客户产品助手并查看公共资料。登录页只辅助选择账号，不在浏览器代码中保存密码；密码来自部署时生成的环境配置。

## 面试讲解抓手

建议按“问题—错误方案—生产方案—证据”讲：

1. 相似型号和同码异义导致纯向量召回跑题，所以加入 Dataset/型号/版本前置过滤、BM25、Exact identifier、RRF 和 hosted rerank。
2. 文件解析成纯文本后引用无法复核，所以建立 Document IR，并让页码/Sheet/Cell Range 贯穿整个链路。
3. 只在 Prompt 中写权限不安全，所以授权在 PostgreSQL 控制面完成，Filter 在 ANN 前由服务端生成，两条混合召回分支使用同一 Filter。
4. LLM 不适合做通知范围比较，所以把版本和批次适用性做成确定性工具，模型只解释结果。
5. 不追求第一次就完美，用 43 条固定 RAG 回归、在线 Trace 和人工 Bad Case 建立持续优化闭环。
6. 最初的答案关键词评测把双型号问题误判为通过；增加“必需文档集合、最少独立文档数、允许数据集”证据门禁后，才发现 `BeneVision N1` 证据缺失。多实体 fan-out 修复后，同一冻结数据集从 87.5% 提升到 100%，且无新增退化。详见 [多实体证据覆盖优化](multi-entity-evidence-optimization.md)。

当前明确限制：公开产品语料不是完整说明书，也不能证明当前库存、报价、配置或注册有效性；NMPA API 自动同步、OCR、真实 CRM/ERP/售后工单和云部署不在本阶段。专业运维侧资料仍为虚构数据。
