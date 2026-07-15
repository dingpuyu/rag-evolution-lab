# ADR-0003：Pipeline 版本使用配置组合而不是代码复制

- 状态：Accepted
- 日期：2026-07-15

## 背景

项目需要保留 V0～V7 多个可运行版本。如果每个版本复制完整实现，后续 Bug 修复和接口变化会产生大量分叉。

## 决策

共享 Parser、Retriever、Reranker、Context Builder 和 Generator 实现。各版本通过 Git 管理的 YAML 配置选择组件和参数。

## 原因

- 保证实验只改变预期变量。
- 避免重复代码。
- 配置 Hash 可以进入 Evaluation Run。
- 便于回放历史实验。

## 影响

- Pipeline Registry 和配置校验必须在早期实现。
- 不兼容的算法变化需要新组件类型，不能偷偷改变旧配置语义。
