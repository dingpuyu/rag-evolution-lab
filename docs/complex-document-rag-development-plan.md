# 复杂文档知识检索体系开发计划

## 1. 项目目标

把 Rag Evolution Lab 从通用 RAG 实验系统升级为可服务多个知识领域的知识基础设施。系统面对 PDF、Word、Excel、HTML、扫描件等异构资料，以及产品族、型号、版本、地区、语言、生效时间高度重叠的文档时，应当：

- 先确定问题适用的知识域、产品、型号和版本，再执行内容检索；
- 能识别当前文档、历史文档、替代关系和冲突关系；
- 返回带文档版本、页码、章节和来源的可追溯证据；
- 不确定时澄清或拒答，不用相似但不适用的文档补答案；
- 通过线上反馈、人工标注、离线回放和灰度发布持续修复 Bad Case。

项目不以第一版达到“完美准确率”为目标。第一版首先保证问题可观测、错误可分类、修改可评测、索引可回滚。

## 2. 双项目边界

### rag-evolution-lab

负责生产查询链路：

```text
Source → Parse → Normalize → Enrich → Chunk → Index
      → Query Understand → Filter → Retrieve → Rerank
      → Verify → Evidence Contract → Agent
```

主要职责包括文档生命周期、权限、索引、检索策略、引用和 Query Trace。

### rag-quality-loop（后续独立仓库）

负责质量运营闭环：

```text
Production Trace / Feedback
→ Bad Case Inbox
→ Human Triage / Annotation
→ Regression Case
→ Baseline vs Candidate Replay
→ Quality Gate
→ Canary / Rollback
```

评测项目只通过稳定 API 和导出的 Trace Contract 调用主项目，不直接依赖主项目内部 Go 包。

## 3. 目标架构

### 3.1 文档接入层

- Connector：本地上传、对象存储、知识网站、企业网盘和业务系统；
- Parser Plugin：PDF、DOCX、XLSX、HTML、Markdown、OCR；
- 文件类型识别使用内容签名，不只相信扩展名；
- 保存原始文件、解析产物、标准化文档和 Chunk Manifest；
- 每一步记录工具版本、输入 Hash、输出 Hash、耗时和质量告警。

### 3.2 统一文档中间格式（Document IR）

所有 Parser 输出相同的结构：

```text
Document
  ├── Heading
  ├── Paragraph
  ├── List
  ├── Table / Row / Header
  ├── Code / Error Code
  ├── Image / OCR Region
  └── Provenance(page, bounding_box, source_path)
```

Chunk 不直接从原始文件生成，而是从 Document IR 生成。这样 Parser、Chunker 和 Embedding 可以独立升级和回放。

### 3.3 复杂适用范围模型

现有 `product + version` 继续兼容，逐步增加：

- `domain`：知识领域；
- `manufacturer`、`product_family`、`model_codes`；
- `software_version_from/to`、`hardware_revision`；
- `region`、`language`；
- `effective_from/to`；
- `authority_level`、`document_revision`；
- `supersedes`、`applies_to`、`conflicts_with`；
- `device_identifier`、`lot_or_batch`（领域可选字段）。

设备标识、型号和批次是医疗设备资料的重要检索条件。FDA 对 UDI 的公开说明也把固定的设备标识部分与型号/版本联系起来，因此医疗设备测试域会专门验证“相似型号不能串检索”。参考：[FDA UDI Basics](https://www.fda.gov/medical-devices/unique-device-identification-system-udi-system/udi-basics)。

### 3.4 检索链路

```text
Query
→ Domain / Intent / Entity / Version Resolution
→ Access Scope + Applicability Filter
→ Exact Recall + BM25 + Dense Recall
→ RRF Fusion
→ Cross-Encoder Rerank
→ Version / Authority / Conflict Verification
→ Parent Context Packing
→ Answerable / Clarify / Refuse
```

约束：

- Tenant、产品、型号、版本、生效状态在 ANN 前过滤；
- 错误码、UDI、型号、参数名走精确词和关键词通道；
- 未指定版本时只使用当前生效版本；
- 明确询问历史版本时允许查询已废止资料，但回答必须标记版本；
- 同时命中互相冲突的高权威文档时，不允许模型自行合并；
- 无法确认型号或版本时返回澄清问题。

### 3.5 Agent 检索契约

Agent 不直接操作 Milvus。Knowledge Gateway 返回：

```json
{
  "answerable": false,
  "decision": "needs_clarification",
  "reason": "ambiguous_device_model",
  "applied_scope": {
    "domain": "medical-device",
    "model_codes": []
  },
  "candidate_entities": ["VSM-100", "VSM-100 Pro"],
  "evidence": [],
  "trace_id": "..."
}
```

## 4. Bad Case 运营闭环

### 4.1 Bad Case 来源

- 用户点踩或人工报错；
- 正确答案无引用、引用错误版本或引用越权；
- 低置信度、错误拒答、应澄清却直接回答；
- Query Trace 中检索为空、Rerank 大幅改变排名或发生冲突；
- 新文档发布后的高频回归问题。

### 4.2 根因分类

每个 Bad Case 必须落在一个主要根因：

1. `missing_document`
2. `parse_error`
3. `metadata_error`
4. `entity_resolution_error`
5. `recall_error`
6. `rerank_error`
7. `wrong_version`
8. `context_pack_error`
9. `generation_error`
10. `permission_or_freshness_error`

不能把所有失败都归为 Prompt 问题。

### 4.3 修复完成标准

```text
Bad Case 确认
→ 标注相关/无关证据和预期决策
→ 加入 Regression Split
→ 修复 Parser / Metadata / Retrieval / Rerank / Prompt
→ Candidate 回放通过
→ Blind Split 无退化
→ 灰度发布
→ 线上观察后关闭
```

## 5. 分阶段开发计划

### Phase 0：领域化数据基线（当前阶段）

交付物：

- 支持通过 `RAGLAB_DATASET_DOMAIN` 切换独立知识域；
- 建立 `medical-device` 合成语料；
- 覆盖相似型号、相邻版本、表格、服务通告、过期文档和租户资料；
- 建立 Development Golden Cases 和首份 Baseline 报告。

验收：语料和标注通过校验；评测能够独立运行；测试数据不包含真实患者信息或真实临床操作参数。

### Phase 1：Document IR 与多格式解析

优先支持 Markdown、PDF、DOCX、XLSX，再增加 HTML 和 OCR。

验收：

- 同一内容转换为不同文件格式后，关键事实召回一致；
- 表格表头不会与数据行分离；
- 页面、标题路径和源文件坐标可追溯；
- Parser 失败进入隔离队列，不发布半成品索引。

### Phase 2：文档族、型号和版本治理

实现 Product Family、Model、Version Range、Revision 和 Supersedes 关系；增加元数据校验和人工修正入口。

验收：

- 当前版本查询不召回过期版本；
- 指定历史版本可以命中历史文档；
- 相似型号 Hard Negative 不进入最终 Context；
- 型号不明确的问题会触发澄清。

### Phase 3：分层召回与证据验证

实现精确实体召回、BM25、Dense、RRF、Cross-Encoder Rerank，以及版本/权威性验证器。

验收重点：`CorrectVersion@5`、`WrongModelRate`、`HardNegativeErrorRate`、`MRR`、P95 和成本。

### Phase 4：独立评测项目

建立 `rag-quality-loop`：Case Registry、Replay Runner、Baseline/Candidate、Bad Case Inbox、人工标注和 HTML 报告。

数据分为：Development、Regression、Blind、Production Replay。权限泄漏和错误引用使用确定性断言，LLM Judge 只辅助判断语义质量。

### Phase 5：线上反馈与灰度闭环

把 Query Trace、用户反馈和管理员审核接入评测项目；策略、索引、Embedding、Reranker 和 Prompt 均有独立版本；支持 10% 灰度、质量门禁和自动回滚建议。

## 6. 医疗设备测试域边界

首个领域使用完全虚构的 `MediAxis PulseCare` 监护设备系列。数据只验证软件检索能力，不是设备说明书、医疗建议或临床操作指南。

数据设计参考真实行业会出现的文档形态：设备标识、标签、型号、软件版本、兼容性矩阵、现场更正通知和历史资料。FDA 公开资料说明了设备标签与唯一标识的作用，并区分现场更正与移除；测试域只借鉴这种资料组织方式，不复制任何真实设备操作内容。参考：[FDA Device Labeling](https://www.fda.gov/medical-devices/overview-device-regulation/device-labeling)、[FDA What is a Medical Device Recall?](https://www.fda.gov/medical-devices/medical-device-recalls-and-early-alerts/what-medical-device-recall)。

## 7. 本阶段推荐命令

```bash
RAGLAB_DATASET_DOMAIN=medical-device go run ./cmd/raglab validate --split development

RAGLAB_DATASET_DOMAIN=medical-device \
  go run ./cmd/raglab eval --pipeline v0-keyword --split development

RAGLAB_DATASET_DOMAIN=medical-device \
  go run ./cmd/raglab compare \
    --baseline v0-keyword \
    --candidate v5-rerank \
    --split development --json
```

Baseline 不是目标成绩，而是后续每一次 Bad Case 修复的比较起点。
