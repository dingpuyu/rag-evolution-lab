# Phase 5 Rerank 与 Context Engineering 首轮报告

## 实验目的

验证三个假设：

1. 扩大候选池后进行 Rerank，能否改善最终排序；
2. Context Packing 能否在独立预算内选择证据；
3. 引用能否通过确定性规则约束在最终上下文内。

## 控制变量

- Corpus、Chunk、Golden Dataset、Metadata 和 Query Router 不变。
- V4 直接输出 Router Top-5。
- V5 将 Router 候选池扩展到 20，使用确定性 Heuristic Reranker，再输出 Top-5。
- V5 的最终 Context 上限为 6 Chunk / 4000 估算 Token。
- 两个版本均使用相同的 Extractive Generator，避免外部 LLM 噪声。

## 结果

### Development（20 Case）

| Pipeline | Hit@5 | MRR | Recall@5 | Precision@5 | NDCG@5 | Answerability | P95 | 安全/引用违规 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| V4 Router | 1.000 | 1.000 | 1.000 | 0.310 | 1.000 | 1.000 | 约 0.05–0.10ms | 0 |
| V5 Rerank | 1.000 | 1.000 | 1.000 | 0.310 | 0.996 | 1.000 | 约 0.17–0.19ms | 0 |

### V4 Challenge（8 Case）

| Pipeline | Hit@5 | MRR | Recall@5 | Precision@5 | NDCG@5 | Answerability | P95 | 安全/引用违规 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| V4 Router | 1.000 | 1.000 | 1.000 | 0.400 | 1.000 | 1.000 | 一次采样 0.099ms | 0 |
| V5 Rerank | 1.000 | 1.000 | 1.000 | 0.400 | 1.000 | 1.000 | 一次采样 0.180ms | 0 |

微秒级内存实验的延迟会受本机调度影响，当前数字只用于证明评测链路，不代表生产容量。

## 失败分析

- V4 在现有数据上已经达到 Hit/MRR/Recall 满分，存在明显天花板效应。
- Heuristic 对首个相关结果没有破坏，因此 MRR 保持 1.000。
- 多跳问题包含多个相关文档；次要相关证据被调整到更低位置，NDCG 下降。
- V5 扩大候选池并增加后处理，P95 上升符合预期。

## 决策

不将 V5 标记为“效果优化完成”。本轮只完成了 Rerank/Context/Citation 的工程基线和评测能力。

下一轮需要：

1. 建立独立 `v5-challenge` 或 Blind Split；
2. 增加难负例、词面高度相似但语义错误的 Chunk；
3. 接入 Cross-Encoder 或外部 Rerank Provider；
4. 按 Query Route 决定是否启用 Rerank；
5. 比较质量收益、P95、吞吐和模型成本。
