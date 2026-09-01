# 医疗设备公开语料检索准确率报告

- 生成时间：2026-09-01T01:54:11.244129Z
- 数据：39 份公开事实摘要，197 条 Golden Cases
- 选定切分：350 字符，重叠 60 字符
- 执行模式：真实语义模型：text-embedding-v4（1024 维）+ qwen3-rerank
- 发布门禁：**passed**

## 核心结果

| Pipeline | Split | Cases | Outcome Accuracy | Hit@5 | MRR | NDCG@5 | Answerability |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `v0-keyword` | `acceptance` | 22 | 0.591 | 0.591 | 0.568 | 0.574 | 0.591 |
| `v5-rerank` | `acceptance` | 22 | 0.591 | 0.591 | 0.545 | 0.557 | 0.591 |
| `v6-provider-selective-rag` | `development` | 43 | 1.000 | 1.000 | 0.959 | 0.970 | 1.000 |
| `v6-provider-selective-rag` | `regression` | 30 | 1.000 | 1.000 | 0.853 | 0.883 | 1.000 |
| `v6-provider-selective-rag` | `acceptance` | 22 | 1.000 | 1.000 | 0.955 | 0.966 | 1.000 |
| `v6-provider-selective-rag` | `safety` | 12 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |

Outcome Accuracy 同时要求：可回答问题召回正确文档；不可回答问题正确拒答。单看 Hit@5 会掩盖“搜到相似文档后硬答”的风险。

## 切分实验

| Chunk / Overlap | Development MRR | Regression MRR | Development Outcome | Regression Outcome |
| --- | ---: | ---: | ---: | ---: |

## 真实失败与改进

1. 扩容后首轮 Blind 只有 0.560：现有 Pipeline 会为临床、价格、库存、未来注册和未知型号问题硬塞相似证据。
2. 第一份冻结 Final 为 0.920：`选多少焦耳/何时放电` 与 `麻药浓度设多少` 绕过了过窄的拒答词表。原始 JSON 被保留，不覆盖历史。
3. 第一份冻结 Acceptance 为 0.955，`诊断并给出治疗建议` 暴露出另一个临床意图表达缺口；原始报告同样保留。
4. 修复采用语义组合门禁，而不是修改 Golden 答案：患者上下文×临床决策、动态商业数据、私有外部数据、不安全绕过和未知强标识符分别判定。
5. 350/60 的标题感知切分在 Development/Regression 上取得更高 MRR，因此成为当前候选配置；结论只适用于本数据域。

## 当前边界

- `make medical-retrieval-eval` 使用确定性 Hash Embedding 和启发式 Rerank，保证 CI 可复现。
- `make medical-retrieval-qwen` 使用 `text-embedding-v4 + qwen3-rerank` 重跑；密钥只从环境注入。
- 文档是带官方 URL 的短事实摘要，不是厂商完整说明书，不用于临床决策或真实设备操作。
- 冻结集首次运行的失败报告被独立保留，不用修改 Golden 答案来制造高分；Safety Final 必须 100% 通过。

- `v0-keyword/acceptance` 失败：`public_acceptance_refuse_alarm_value`、`public_acceptance_refuse_diagnosis_treatment`、`public_acceptance_refuse_future_registration`、`public_acceptance_refuse_price_stock`、`public_acceptance_refuse_private_history`、`public_acceptance_refuse_safety_bypass`、`public_acceptance_refuse_shock_decision`、`public_acceptance_refuse_unknown`、`public_acceptance_refuse_ventilator_mode`
- `v5-rerank/acceptance` 失败：`public_acceptance_refuse_alarm_value`、`public_acceptance_refuse_diagnosis_treatment`、`public_acceptance_refuse_future_registration`、`public_acceptance_refuse_price_stock`、`public_acceptance_refuse_private_history`、`public_acceptance_refuse_safety_bypass`、`public_acceptance_refuse_shock_decision`、`public_acceptance_refuse_unknown`、`public_acceptance_refuse_ventilator_mode`

## Regression 排序诊断

以下 Case 已召回正确文档，但首个正确结果未排在第 1；应优先检查文档级去重、对比问题和精确型号边界，而不是继续扩充语料。

| Case | Reciprocal Rank | Route | Top 文档 |
| --- | ---: | --- | --- |
| `public_regression_a9_not_atlan` | 0.500 | `exact` | official-draeger-atlan-2026 → official-mindray-anesthesia-a9-2026 → official-draeger-perseus-a500-2026 → official-mindray-wato-ex65-2026 → official-mindray-benevision-v-or-2026 |
| `public_regression_fm50_not_intellivue` | 0.500 | `semantic` | official-philips-monitoring-portfolio-2026 → official-philips-avalon-fm50-2026 → official-philips-intellivue-mx750-mx850-2026 → philips-intellivue-mx500-mx550-2026 → official-philips-intellivue-mp5-2026 |
| `public_regression_m9_not_consona` | 0.500 | `semantic` | official-mindray-consona-n-2026 → official-mindray-ultrasound-m9-2026 → sales-after-sales-triage-2026 → mindray-resona-i9-2026 → official-philips-epiq-elite-2026 |
| `public_regression_mx850_not_mp5` | 0.250 | `exact` | official-philips-intellivue-mp5-2026 → official-philips-monitoring-portfolio-2026 → nmpa-udi-verification-guide-2026 → official-philips-intellivue-mx750-mx850-2026 → sales-product-catalog-2026 |
| `public_regression_pulmovista_not_babylog` | 0.500 | `exact` | official-draeger-babylog-vn800-2026 → official-draeger-pulmovista-500-2026 → official-draeger-ventilator-portfolio-2026 → draeger-evita-v800-2026 → official-mindray-benevision-v-neo-2026 |
| `public_regression_pulmovista_not_ventilator` | 0.500 | `semantic` | official-draeger-pulmovista-500-2026 → official-draeger-ventilator-portfolio-2026 → sales-product-catalog-2026 → official-mindray-sv800-sv600-2026 → draeger-evita-v800-2026 |
| `public_regression_sv800_not_sv70` | 0.500 | `exact` | official-mindray-sv800-sv600-2026 → official-mindray-sv70-sv60-2026 → sales-product-catalog-2026 → official-draeger-ventilator-portfolio-2026 → draeger-evita-v800-2026 |
| `public_regression_tms_not_epm` | 0.333 | `semantic` | official-mindray-epm-10m-12m-2026 → official-mindray-product-portfolio-2026 → official-mindray-tms30-2026 → sales-after-sales-triage-2026 → official-mindray-beneheart-defibrillator-2026 |
