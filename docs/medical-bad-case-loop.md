# 医疗 RAG Bad Case 人工修复闭环

## 1. 为什么不能只看一次离线准确率

复杂产品知识库会持续出现新型号、新版本、新格式和高度相似的字段。发布前的 Golden Set 只能覆盖已知问题，生产系统必须把失败样本转化为长期资产，而不是临时改 Prompt 后把问题关闭。

本项目把流程收敛为：

```text
完整评测 / 人工发现
  → 收录 Bad Case 快照
  → 标注根因、设备上下文和正确证据
  → 使用当前生产检索链路单题复测
  → 复测通过后人工晋升
  → 自动加入下一次完整发布门禁
```

## 2. 数据契约

`medical_bad_cases` 保存问题本身和当前人工结论：

- 租户、应用和环境，避免评测资产跨租户泄漏。
- 来源 Evaluation Run、Case 和 Query Trace。
- RAG/Agent 层、期望决策和实际决策。
- 期望/实际文档、页码/工作表/单元格等来源定位。
- 型号、软件版本、批次和区域上下文。
- 根因分类、修复说明和状态。
- 最近一次验证结果、验证次数和晋升时间。

`medical_bad_case_attempts` 追加保存每次复测，不能用最新结果覆盖历史。它记录检索结果、Trace、延迟、Hit@5/MRR/NDCG 和操作人，可用于回答“哪一次修改让结果变好”。

Bad Case 只保存来源 Run ID，不使用级联删除外键：Evaluation Run 属于可清理的运行记录，已经确认的回归资产不能随着运行历史清理而消失。

状态机：

```text
open → diagnosed → verified → regression
          ↑            |
          └── 复测失败 ┘
```

修改正确文档、来源定位或设备上下文会清空旧验证结论并回到 `diagnosed`，防止用旧的通过记录给新标注背书。只有 RAG 层且最近一次复测通过的案例可以晋升；Agent 层案例暂时保留人工诊断，不伪装成已经支持动态回归。

## 3. 单题复测为什么不是“点一下通过”

复测重新调用当前应用环境绑定的 Knowledge Gateway，仍然经过：

```text
服务端应用/租户过滤
→ Query Rewrite
→ Exact + BM25 + Dense
→ RRF
→ Qwen Rerank
→ 型号/版本/来源定位校验
```

验证结果由 `evaluate_retrieval_case` 计算，不接收前端上传的 `passed=true`。网页只能修改人类期望，不能修改实际检索结果。

晋升后的案例由数据库转换成标准 Golden Case 合约，`split=human_regression`。下一次 Evaluation Run 创建时就计入 `total_cases`，运行时与仓库内静态 Golden Cases 一起参与 Hit@5、MRR、引用定位准确率和发布门禁。

## 4. 真实验证记录

本轮使用历史 XLSX 问题完成端到端验证：

- Query：`在 XLSX 跨格式测试副本中，VSM-100 Pro 搭配 WLM-2 的最低固件版本是什么？`
- 历史根因：同一工作表包含标准版和 Pro 行，但文件级型号元数据遗漏 `VSM-100 Pro`。
- 人工期望文档：`vsm100-compatibility-fw2.6-xlsx`。
- 期望定位：`兼容矩阵!A1:E1,A4:E4`。
- 单题复测：目标文档第 1，MRR=1.0，来源定位通过，耗时约 789ms。
- 晋升后的完整在线评测：50/50 通过，Hit@5=1.0，MRR=0.8441，引用定位准确率=1.0，发布门禁通过。
- 新增动态用例 `rag:human_badcase_a64df05f7d9a45a2b2c92b303d8fd1c2` 在完整评测中实际执行并通过。

这证明闭环不是前端演示：数据进入 PostgreSQL、复测调用真实 Qwen Embedding/Milvus 检索链路、Trace 被持久化，晋升结果真正改变下一次评测总数。

联调时还真实遇到一次 PostgreSQL `No space left on device`：检索本身成功，但 Query Trace 无法落库，因此 Gateway 按失败处理。清理可再生的 Docker 悬空镜像后恢复。这里没有选择忽略 Trace 写入错误，因为缺失审计记录的回答不应伪装成生产成功；部署时应对数据库磁盘、Trace 表增长和保留周期设置告警。

## 5. 管理接口

```text
POST  /api/v1/evaluations/runs/{run_id}/cases/{case_id}/bad-case
GET   /api/v1/evaluations/medical-device/bad-cases
PATCH /api/v1/evaluations/medical-device/bad-cases/{bad_case_id}
POST  /api/v1/evaluations/medical-device/bad-cases/{bad_case_id}/verify
POST  /api/v1/evaluations/medical-device/bad-cases/{bad_case_id}/promote
GET   /api/v1/evaluations/medical-device/bad-cases/{bad_case_id}/attempts
```

所有接口要求管理员身份，并按 Tenant 和 App 过滤。运行令牌只存在于当前请求和后台评测任务，不进入 Bad Case 表。

## 6. 面试讲法

可以按“问题—设计—门禁—数据”四步回答：

> 复杂 RAG 不可能上线前覆盖所有相似型号和版本组合，所以我没有把优化做成一次性调参。我把评测失败快照成租户隔离的 Bad Case，由人工标注根因、设备上下文和正确证据，再复用真实 Knowledge Gateway 做低成本单题复测。只有复测通过的检索题才能晋升为 human regression，并自动加入下一次发布门禁。修改标注会使旧验证失效，每次尝试都保留 Trace 和指标。实际用 XLSX 型号范围问题验证后，新增用例参与了 50 题完整回归，50/50 通过，MRR 0.8441，且引用定位准确率保持 100%。

这比回答“用了 RAGAS 或调了 TopK”更能体现生产经验，因为它说明了问题如何被发现、谁确认真值、优化如何验证、失败如何防止再次出现。

## 7. 当前边界与下一步

- 当前动态晋升先覆盖 RAG 检索层；Agent 决策案例需要补齐期望原因码、回答边界和工具结果后再开放自动晋升。
- 当前从 Evaluation Case 收录；下一阶段把对话页的负反馈和 Query Trace 接入同一队列。
- 静态 Git Golden Set 仍是 CI 的确定性基线；数据库中的人工回归集是环境级增量。定期审核后可导出为代码评审过的 JSON，避免环境资产永远只存在数据库。
- OCR、复杂跨页表格和图表语义仍是下一阶段 Document IR 的重点。
