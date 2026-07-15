# 测试策略

## 1. 测试金字塔

```text
少量真实模型端到端测试
        ↑
Golden Evaluation / Contract Test
        ↑
PostgreSQL + pgvector 集成测试
        ↑
大量确定性 Go 单元测试
```

## 2. Go 单元测试

使用 Table-driven Tests。每个生产 Bug 和评测失败都应沉淀成 Fixture 或 Regression Case。

重点模块：

- Parser：Markdown、表格、代码块、异常编码
- Chunker：边界、Overlap、稳定 ID、父子关系
- Metadata：版本、生效时间、租户、角色
- Fusion：RRF、稳定排序、去重
- Context：Token Budget、合并、裁剪、引用
- Security：ACL、日志脱敏、注入标记
- Evaluation：Recall、MRR、NDCG、Citation 指标

## 3. Fake 与 Fixture

### Fake Embedder

为固定文本返回固定向量，用于测试排序和边界，不依赖外部 API。

### Fake Generator

根据输入 Fixture 返回：

- 合法结构化结果
- 非法 JSON
- 超时
- 引用不存在的 Chunk
- 包含 Forbidden Fact 的答案

### Golden Fixtures

保留最小可读数据集，单测不加载完整语料。

## 4. 集成测试

使用 Testcontainers 启动 PostgreSQL + pgvector，验证：

- Schema Migration
- 文档入库幂等性
- Vector Search
- Full Text Search
- Metadata Filter
- Hybrid Retrieval
- 文档更新和删除
- 多租户隔离

集成测试默认不访问真实模型。

## 5. Model Contract Test

只验证模型交互协议，不验证模型“聪明程度”：

- Structured Output 是否符合 Schema
- Query Rewrite 是否保留实体、数字和版本
- Metadata Extraction 是否使用允许枚举
- 超时和限流是否正确降级
- 模型拒绝或空结果是否被显式处理

CI 默认使用录制响应；手动或定时任务运行真实模型 Contract Test。

## 6. Golden Evaluation

Golden Evaluation 不是传统单测，不要求每次本地开发都运行全部真实模型调用。

模式：

- `fast`：Development Split + Fake/Cache
- `regression`：Regression Split + 固定模型版本
- `full`：全部 Split + 真实外部服务

## 7. 安全测试

以下属于硬性失败：

- 返回其他租户文档
- 引用无权访问的 Chunk
- 文档内容改变 System 指令
- 日志记录密钥或敏感字段
- 无 Tenant Context 时默认放开访问

权限逻辑使用确定性代码，不能交给 LLM 判断。

## 8. 性能与故障测试

后期覆盖：

- 100 并发查询
- 文档导入与查询同时进行
- Vector Retriever 超时
- Keyword Retriever 超时
- Reranker 失败
- Embedding 服务限流
- PostgreSQL 短暂不可用

每种故障都需要定义：

- 是否重试
- 最大重试次数
- 是否降级
- 用户可见错误
- Trace 内容

## 9. CI 阶段

```text
lint
  → unit-test
  → schema-validation
  → integration-test
  → fast-eval
  → security-regression
```

真实模型 Full Evaluation 不阻塞普通 Pull Request，但发布实验报告前必须运行。

## 10. 测试命名

推荐：

```text
TestChunker_PreservesMarkdownTable
TestMetadataFilter_RejectsDeprecatedVersion
TestRRF_StableWhenRanksTie
TestContextPacker_NeverDropsRequiredFactFirst
TestACL_FailsClosedWithoutTenant
TestCitation_RejectsChunkOutsideContext
```

名称直接表达业务约束，方便面试展示测试为何存在。
