# 医疗检索精度优化闭环

## 1. 这轮解决的核心问题

公开医疗设备资料先从 9 份扩展到 24 份，再扩展到当前 39 份。第一次扩容让旧检索链路暴露出两个不同问题，第二次扩容则专门验证新增产品族和跨厂商相似型号是否会破坏既有排序：

1. 可回答问题中的同义表达、相似型号和跨产品线问题可能召回错误文档。
2. 不可回答问题也会被向量检索强行匹配到“最相似”的文档，随后生成看似合理但没有证据的答案。

因此本轮主指标不是单纯 Hit@5，而是 `Outcome Accuracy`：可回答问题必须召回正确证据，不可回答问题必须正确拒答。它更接近生产系统的业务结果。

## 2. 数据与来源治理

- 39 份公开事实摘要，覆盖迈瑞、飞利浦和德尔格的监护、AED、除颤监护、麻醉、超声、输注、呼吸机、心电和血液分析产品。
- 资料只引用厂商官网、厂商公开 PDF 和 NMPA UDI 等公开来源，并保存 URL、采集日期、内容 SHA-256 和人工审核记录。
- 新增资料采用短篇转述，不复制整本说明书；配置、区域、注册、价格和临床操作边界均明确写入正文。
- 本地来源审计为 39/39 approved、0 drift。最新在线审计检查 43 个 URL：10 个原始响应指纹稳定，25 个厂商动态网页的响应指纹发生变化，8 个页面超时或不可达。在线变化只进入人工复核队列，不会自动改写已审核摘要或已发布索引，因此不等同于本地语料漂移。

构建与审计：

```bash
make medical-public-build
make medical-source-audit
```

## 3. 197 条分层测试集

| Split | 数量 | 用途 |
| --- | ---: | --- |
| Development | 43 | 调整同义词、阈值和切分参数 |
| Regression | 30 | 相似型号与 Hard Negative 回归 |
| Blind | 40 | 首轮未知表达验证 |
| Holdout | 25 | 第一轮改进后的冻结验证 |
| Final | 25 | 达标验收，首次运行后不修改 |
| Acceptance | 22 | 独立的发布门禁 |
| Safety | 12 | 临床、隐私、绕过和动态信息零容忍门禁 |

Case 覆盖精确型号、产品家族、同义表达、可选配置、相似型号、未知型号、价格/库存、未来注册、患者隐私、临床建议和安全绕过。开发集用于调参；Final、Acceptance 和 Safety 只用于验证，首次失败报告会保留，不能通过改答案抬分。

每次评测前运行独立数据质量门禁，校验 Case ID 与 Split、跨集合完全重复问题、证据文档存在性、`required_facts` 是否真的出现在标注证据中、拒答题是否错误携带证据，以及各 Split/Hard Negative/拒答用例的最低覆盖。首轮门禁真实发现 16 个问题，其中包括比较题只标注单侧文档、中文数字与标签不一致，以及把通用回答措辞误当成证据事实；修复后 39 份资料、197 条 Case 全部通过，且没有跨集合精确问题泄漏。

```bash
make medical-dataset-audit
```

## 4. 优化过程与证据

### 4.1 失败基线

- 扩容后的首轮 Blind Outcome Accuracy 只有 0.56。
- `v0-keyword` 和没有拒答门禁的 `v5-rerank` 在 Acceptance 上都只有 0.591。
- 主要失败不是“完全搜不到”，而是临床决策、实时价格、库存、未来注册和未知型号被相似文档误接住。

### 4.2 分词与 Query Expansion

BM25、确定性向量和启发式 Rerank 共用受控别名扩展，同时保留原词。例如：

- 网络中断 → 断网
- 网络恢复 → 复网
- 缓存 → 本地存储
- 补传 → 续传
- 自动体外除颤器 → AED
- 床旁监护 → 病人监护

这里使用“原词 + 别名”，而不是破坏性改写。这样既保留错误码、型号等精确锚点，也提高自然语言改写的召回。

### 4.3 切分实验

| Chunk / Overlap | Development MRR | Regression MRR |
| --- | ---: | ---: |
| 350 / 60 | 0.961 | 0.950 |
| 500 / 80 | 0.957 | 0.933 |
| 700 / 0 | 0.983 | 0.933 |
| 900 / 120 | 0.957 | 0.933 |

当前公开销售资料篇幅较短、标题语义强。700/0 在 Development 上更高，但 350/60 在更强调相似型号区分的 Regression 上最高，因此继续选用 350/60。该结论不能直接外推到长说明书；长 PDF 仍应按标题、表格、页码和父子块结构单独实验。

### 4.4 Selective RAG 拒答门禁

在 Rerank 后增加确定性 Evidence Gate，拒绝以下问题：

- 临床诊断、治疗、用药、患者参数和报警阈值；
- 患者隐私或身份证信息；
- 绕过安全检查、报警或联锁；
- 实时价格、库存和未来注册状态；
- 私有医院资产数量；
- 低证据分或无法被证据覆盖的强型号标识符。

门禁会写入 Query Trace，并返回明确的 `refusal_reason`。LLM 负责解释证据，不负责放宽安全和数据边界。

### 4.5 远端 Provider A/B：Chunk 重复挤占 Top-K

39 份资料完成在线向量化后，扩容后的 `text-embedding-v4（1024 维）+ qwen3-rerank` 基线虽然 Hit@5 为 1.0，但 Regression MRR 只有 0.830。逐题检查发现，正确文档已经被召回，问题出在 Qwen 对每个 Chunk 独立打分：同一个相似但错误的文档可能有多个 Chunk 连续排在前面，把正确文档压到第 3～5 名。

本轮只改一个变量：在 Rerank 后做稳定的文档级轮转。每个文档的最高分 Chunk 先进入候选集，再放入各文档的第二个 Chunk；不丢弃证据，只改变 Top-K 的文档多样性。A/B 结果如下：

| 方案 | Regression MRR | Regression NDCG@5 | Development / Acceptance / Safety Outcome |
| --- | ---: | ---: | ---: |
| Qwen Chunk 级排序基线 | 0.830 | 0.850 | 1.000 / 1.000 / 1.000 |
| + 文档级稳定轮转 | 0.853 | 0.883 | 1.000 / 1.000 / 1.000 |

这个实验说明“能召回”和“排得好”是两层问题。遇到 Top5 命中但 MRR 偏低时，应先查看同一文档是否重复占位，而不是马上替换 Embedding 模型或继续堆同义词。

### 4.6 在线 Agent Bad Case：文字拒绝不等于决策正确

在线体验中询问“今天 BeneHeart C 的价格和库存是多少”，模型生成的文字明确表示无法提供，但结构化 `decision` 仍是 `answer`。如果只看回答文本，这条会被误认为安全；在自动评测和业务编排里，它却会进入成功分支。

修复后，价格、库存、现货和交期被识别为动态交易数据，在检索前由确定性策略返回 `refuse / dynamic_commercial_data_unavailable`，不调用 RAG 和 LLM。Customer Agent v5 同时新增对应的 Golden Case 和 Prompt Injection 组合 Case。

第一次平台全量运行 89/90，唯一失败来自评测批任务命中应用 429，被错误归类为检索失败。平台没有绕过生产限流，而是增加默认 90 RPM 的评测节流和仅针对 429 的有限指数退避；同一套 90 条用例复跑达到 90/90。由此把“模型质量失败”和“运行环境失败”分开归因。

### 4.7 生产参数审计：“存在配置”不等于“配置已生效”

对线上链路逐段核对时发现，Knowledge Binding 已保存 `token_budget`，但该值没有传入生成前的 Context Builder。这类问题不会在小数据 Demo 中报错，却会在长文档或多知识库场景中放大 Prompt、延迟和费用。

修复后，Go Grounded Answer 和 Python LangGraph Agent 都使用所有已授权 Binding 中最小的正数预算打包最终证据。Agent 在型号、版本、来源和权限证据校验完成后再打包，避免无效证据先占用预算；多余 Chunk 不进入 LLM，最后一个 Chunk 可以被确定性截断。响应新增 `candidate_chunks`、`selected_chunks`、`estimated_tokens`、`token_budget` 和 `truncated`，医疗对话页直接展示这些数据，同时继续使用 Provider 返回的 Prompt Tokens 计费。Citation Allowlist 基于打包后的 Context，因此被截掉的证据不能被模型引用。

修复后重新执行 Customer Agent v5 全量 90 条在线用例，结果仍为 90/90：Hit@5 1.0、MRR 0.929、决策准确率 1.0、权限泄漏 0、WrongModelRate 0，P50 延迟约 362ms。这一步用于证明成本治理没有换来可见的质量回退，而不是只证明新增单测能通过。

同时明确分离两类切分参数：

- 在线 Document IR 当前为 `700/80`，已经过 165 Chunk 和 90/90 Agent 回归；
- 离线短摘要精度实验选出 `350/60`，用于比较检索组合。

两者不能因为“350/60 在一个集合上 MRR 更高”就直接混用。修改在线切分必须生成新的 Collection/Manifest，重跑端到端回归后再通过 Alias 灰度发布。新评测报告会为 Chunk、CandidateTopN、Top-K、Context Budget、Evidence 阈值、RRF 和文档多样化生成不含密钥的 SHA-256 参数指纹，防止不同配置的结果被误当成同一实验。

## 5. 最终结果

冻结数据的真实演进结果：

- 第一份 Final：23/25，Outcome Accuracy 0.92，已经超过 0.80 目标；两条失败是 AED 能量选择和麻醉浓度表达绕过临床门禁。
- 第一份 Acceptance：21/22，Outcome Accuracy 0.955；新增失败是“诊断并给出治疗建议”的表达缺口。
- 修复后 Safety：12/12。
- 24 份语料快照上的 `text-embedding-v4（1024 维）+ qwen3-rerank`：Development、Regression、Acceptance、Safety 的 Outcome Accuracy 均为 1.0；Acceptance MRR 0.932，P95 约 390ms。
- 39 份语料快照上的确定性回归：Development 43/43、Regression 30/30、Acceptance 22/22、Safety 12/12；Regression MRR 为 0.950。扩容首跑曾把正确的产品总览证据误判为失败，根因是 Golden 只允许一个相关文档；复核后把两个均能完整支撑结论的官方摘要登记为等价证据，而没有修改事实答案。
- 39 份语料快照上的本地 `Qwen3-Embedding-4B-Q4_K_M（2560 维）+ Heuristic Rerank`：四个执行集 Outcome Accuracy 均为 1.0；Development MRR 1.000、Regression MRR 0.900、Acceptance MRR 0.977，查询 P95 约 177ms。这个结果证明真实语义向量链路可工作，但不能替代远端 `text-embedding-v4 + qwen3-rerank` 的扩容后验收。
- 39 份语料的远端 Provider 扩容验收已完成：Development、Regression、Acceptance、Safety 的 Outcome Accuracy 均为 1.0；文档级多样化把 Regression MRR 从 0.830 提升到 0.853、NDCG@5 从 0.850 提升到 0.883，且其他集合无回退。
- 在线 Customer Agent v5 全量评测 90/90：73 条检索 Golden Cases 加 17 条 Agent 决策 Case；Hit@5 1.0、MRR 0.929、决策准确率 1.0、权限泄漏 0、WrongModelRate 0。

不应把当前 1.0 宣称成“生产准确率 100%”。更可信的表述是：冻结 Final 首跑达到 92%，独立 Acceptance 首跑达到 95.5%；扩容后真实 Provider 的当前受控集合全部通过，在线平台回归为 90/90。这些数字只证明当前 39 份短摘要和已知问题空间，后续仍要用完整说明书、扫描 PDF、脱敏真实日志和持续采集的未知 Bad Case 验证。

报告：

- [确定性最新报告](../eval/reports/medical-public-retrieval-deterministic-latest.md)
- [39 份语料本地 Qwen 最新报告](../eval/reports/medical-public-retrieval-local-qwen-latest.md)
- [真实 Qwen 最新报告](../eval/reports/medical-public-retrieval-qwen-latest.md)
- [Golden Dataset 质量审计](../eval/reports/medical-dataset-quality-latest.md)
- [第一份冻结 Final 原始报告](../eval/reports/medical-public-retrieval-first-final-2026-09-01.json)
- [第一份冻结 Acceptance 原始报告](../eval/reports/medical-public-retrieval-first-acceptance-2026-09-01.json)

## 6. 真实在线导入验证

全部 39 份销售资料已通过异步摄取链路进入当前服务：MinIO 原件与 Document IR、Qwen Embedding、Milvus 写入验证、PostgreSQL 任务状态全部参与。

实际上传前增加离线 Import Plan：逐份验证来源锁与内容 SHA-256、摄取元数据、目标 Dataset、派生格式文件和同文档修订唯一性。当前计划为 66 个上传版本、4 个 Dataset，其中 `public-medical-device-sales` 恰好包含全部 39 份受审资料，0 错误、0 警告。该门禁不会调用模型，也不会因为 Provider 暂时不可用而退回 Hash Embedding。

```bash
make medical-bootstrap-plan
```

- `public-medical-device-sales`：39 个最新文档版本全部 completed，共 165 个 Chunk；
- `public-medical-device`：23 个最新文档版本全部 completed，共 78 个 Chunk；
- Tenant A/B 私有资料保持独立；
- 医疗端到端 Smoke 39/39，通过登录、权限、文档版本、检索、AED 问答、澄清、临床拒答、动态商业数据拒答和引用验证。

本轮在线导入还发现两类部署问题并完成修复：

1. `.env` 使用宿主端口 18080，但脚本固定访问 8080。现在脚本从 `RAGLAB_API_PORT` 推导地址，Make 目标统一加载环境文件。
2. 等待器会把多天前的失败任务混入当前发布。现在 Bootstrap 输出本次 66 个精确 Job ID，等待器只检查本批任务；历史失败仍保留审计，但不污染新发布。
3. `openpyxl` 在保存 XLSX 时覆盖修改时间，使同一文件每次生成的 SHA 不同并触发重复 Embedding。现在生成器固定 Office Core Properties，连续两次生成的所有文件哈希一致；再次 Bootstrap 可立即 66/66 通过且没有待处理任务。

导入期间同时运行 RAGFlow、Elasticsearch 和两个平台，8GB Colima 中的 Milvus 发生重启，21 个任务因连接中断失败。暂停无关容器后 Milvus 稳定，21 个任务全部通过正式 Retry API 恢复，没有直接篡改数据库状态。这说明生产部署需要资源隔离、并发背压、可靠重试和依赖健康门禁。

## 7. 可复现命令

```bash
# 确定性 CI，不消耗模型额度
make medical-retrieval-eval

# 只检查语料、证据标注、拒答结构、数据泄漏和覆盖
make medical-dataset-audit

# 真实 Qwen Embedding + Rerank，Key 只从环境注入
make medical-retrieval-qwen

# 自动启动回环地址的 llama.cpp，运行后自动关闭；可用
# RAGLAB_LOCAL_QWEN_GGUF 指定 GGUF 路径。
make medical-retrieval-local-qwen

# 在线多租户、检索与 Agent 验收
make medical-bootstrap-plan
make medical-bootstrap
make medical-smoke
```

## 8. 面试讲法

> 我没有只调一个向量库 TopK，而是把公开语料从 9 份逐步扩到 24 份、再扩到 39 份，并建立 197 条分层 Golden Cases，把“能答时检索正确、不能答时拒答”定义为 Outcome Accuracy。首轮 Blind 只有 56%，根因主要是相似文档强匹配不可回答问题。我分别做了来源治理、受控同义词扩展、切分消融、混合检索与 Qwen Rerank，并在生成前增加确定性的 Selective RAG 门禁。冻结 Final 首跑达到 92%，独立 Acceptance 首跑达到 95.5%。扩容后真实 Qwen 的四个发布集合 Outcome 均为 100%；逐题 Trace 又发现同文档 Chunk 挤占 Top-K，我用文档级轮转把 Regression MRR 从 0.830 提升到 0.853。在线 Agent 还暴露出“文字拒绝但 decision=answer”和评测 429 被误判成 RAG 失败的问题，分别通过确定性动态数据边界、批评测节流与有限退避修复，Customer Agent v5 最终 90/90。这套过程体现的不是一次调参，而是数据—评测—诊断—改进—回归—发布的完整闭环。

下一轮优先接入脱敏真实问答日志、完整说明书和扫描 PDF，重点验证长文档父子块、表格、多版本与跨文档组合证据，而不是继续扩大规则词表。
