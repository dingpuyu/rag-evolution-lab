# 评测协议

## 1. 目标

评测必须回答四个问题：

1. 正确证据是否被检索到？
2. 正确证据是否被排到足够靠前？
3. 最终答案是否忠于证据？
4. 质量提升是否值得额外延迟和成本？

## 2. 评测层次

### Level 1：组件级确定性测试

- Parser
- Chunker
- Metadata Filter
- BM25
- RRF
- Deduplication
- Context Packing
- Citation Mapping
- ACL

此层不调用 LLM。

### Level 2：检索评测

输入 Query，输出候选 Chunk，不生成答案。

指标：

- Hit Rate@K
- Recall@K
- Precision@K
- MRR
- NDCG@K
- Duplicate Chunk Rate
- Metadata Filter Accuracy
- Unauthorized Retrieval Count
- Route Distribution
- Per-route Quality / Refusal Regression

### Level 3：生成评测

固定检索结果，只评估 Generator：

- Required Fact Coverage
- Forbidden Fact Rate
- Citation Precision
- Citation Recall
- Faithfulness
- Refusal Accuracy

这样可以区分检索失败和生成失败。

### Level 4：端到端评测

从原始 Query 到最终回答：

- Answer Correctness
- Answerability Accuracy
- Unsupported Claim Rate
- End-to-end Latency
- Token Usage
- Estimated Cost

### Level 5：安全与鲁棒性

- 跨租户泄漏
- 无权限文档召回
- Prompt Injection 成功率
- 服务超时与降级
- 索引更新期间的一致性

企业数据集搜索必须通过独立的端到端 Harness，不能只调用 Retriever：

```text
本地账号登录
  -> PostgreSQL 数据集可见性 / Authorize
  -> 服务端生成可信 AccessScope
  -> Milvus pre-ANN Filter
  -> Top-K 文档、事实和租户归属断言
```

运行：

```bash
make dataset-eval
```

Suite 位于 `datasets/search-harness/enterprise-search-v1.json`。它会幂等写入带稳定
beacon 的公开、Tenant A、Tenant B 三份文档，验证：

- Alice / Bob 只能看到各自租户数据集；
- 私有搜索 Top-1 命中本租户 Golden 文档；
- 公共数据集只返回 `visibility=public`；
- 跨租户数据集访问统一返回 `404 dataset_not_found`，防止资源枚举；
- 返回结果、禁止事实以及服务端最终 Milvus Filter 均无泄漏。

报告保存到 `eval/reports/dataset-search-latest.{json,md}`。JSON 保留每个 Hit 的
rank、Milvus score、tenant 和 visibility；score 用于观测，不作为固定门槛，避免
Embedding 或索引参数微调造成脆弱测试。稳定门禁使用 Document ID、排名、事实与权限属性。

## 3. 指标定义

### Retrieval Recall@K

```text
Top-K 中命中的相关 Chunk 数 / 全部标注相关 Chunk 数
```

### MRR

使用首个相关结果排名的倒数，衡量正确证据是否足够靠前。

### Precision@K

```text
Top-K 中不重复的相关文档数 / K
```

用于衡量上下文中的证据密度。无答案 Case 正确返回空结果时单独记为 1，并必须结合 Answerability Accuracy 解读。

### NDCG@K

按相关文档所在排名计算折损累计增益，再除以理想排序的 DCG。它能发现 MRR 看不到的回归：第一个相关结果仍在首位，但第二、第三个必要证据被排到更后。

### P50 / P95 Latency

评测记录每条 Query 的端到端耗时并报告最近秩分位数。内存小数据的微秒级结果只用于验证评测链路；容量结论必须来自固定环境、预热、重复采样和并发压测。

### Metadata Violations

统计 Top-K 中不满足结构化 Query Context 的 Chunk 数量：

- `product` 不匹配；
- 显式 `version` 不匹配；
- 未指定版本时召回非 `active` 文档。

该指标与 Unauthorized Retrieval Count 分开统计，因为“有权限访问”不代表“知识对当前问题有效”。

### Citation Precision

```text
真正支持答案的引用数 / 全部输出引用数
```

### Citation Recall

```text
已经引用的必需证据数 / 回答所需的全部必需证据数
```

### Unsupported Claim Rate

答案中不能由所选 Context 支持的事实性断言比例。

### Refusal Accuracy

分别统计：

- 无答案问题正确拒答
- 有答案问题错误拒答

不能只报告一个合并数字。

## 4. 判定优先级

优先使用确定性判定：

1. Doc / Chunk ID 匹配
2. Required / Forbidden Fact 规则
3. 数值与单位比较
4. Citation 与 Context 映射
5. LLM-as-a-Judge

LLM Judge 不能作为权限泄漏、注入攻击或引用合法性的唯一判定器。

## 5. LLM-as-a-Judge 使用约束

- Judge Prompt 进入 Git 管理。
- Judge 模型和版本必须记录。
- 抽样与人工标注对比。
- 使用不少于两类问题检查偏差。
- 不允许 Judge 访问 Retriever 未提供的额外知识。
- Judge 输出必须包含评分理由和证据定位。

## 6. 实验控制

单次实验尽量只改变一个变量，例如：

- Chunk Size
- Top-K
- Embedding Model
- Fusion 方法
- Reranker
- Context Token Budget

每次 Evaluation Run 保存：

```yaml
run_id: eval_20260715_001
git_commit: "..."
pipeline: v3-hybrid
pipeline_config_sha: "..."
corpus_version: 0.1.0
golden_version: 0.1.0
embedding_model: "..."
generator_model: "..."
reranker_model: null
started_at: "..."
```

Query Router 评测还必须满足：分类器输入不得包含 Golden Category；Case 结果保存实际 Route，Trace 保存 Route Reason 和最终 Strategy。结构化 Product / Version 可以约束检索，但不能被当作自然语言意图标签。

## 7. 回归门禁

初始门槛在获得 V0/V1 基线后确定。硬性约束先定义为：

- Unit Tests：100% 通过
- Unauthorized Retrieval Count：0
- Cross-tenant Citation Count：0
- Prompt Injection Success Count：0
- Dataset Search Harness：全部通过
- Dataset Visibility Violation：0
- Citation 不得指向未进入 Context 的 Chunk
- Candidate Pipeline 的 Recall@5 不得无解释显著下降
- 无答案问题不得因优化而大幅增加幻觉回答

## 8. 比较报告

每次 `compare` 输出：

- 总体指标对比
- 分 Query 类别对比
- 延迟和成本对比
- 新增成功案例
- 新增失败案例
- Regression 列表
- 每条变化案例的 Trace 链接或文件路径

不能只报告总体平均值，因为总体提升可能掩盖错误码、版本过滤或权限类别的退化。

## 9. 推荐演示案例

至少固定展示四条：

1. 错误码：Vector 失败，Hybrid 改善。
2. 版本冲突：无 Metadata 失败，版本过滤改善。
3. 多跳问题：单次检索失败，迭代检索改善。
4. 无答案或注入：高级系统拒答并保留安全边界。

## 10. 完成标准

某个版本只有同时满足以下条件才算完成：

- 对应单元测试存在且通过。
- 新增失败案例进入 Golden Dataset。
- 改进前后都有可复现报告。
- 解释质量、延迟与成本变化。
- 没有引入安全回归。
