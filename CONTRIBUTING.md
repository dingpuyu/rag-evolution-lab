# Contributing

## 提交原则

- 一个提交只解决一个清晰问题。
- 新增优化前，先加入可复现失败案例。
- 修改检索行为必须附带评测对比。
- 权限、安全和引用回归必须阻塞合并。
- 不提交 API Key、真实企业数据和生成缓存。

## Commit 类型

- `docs:` 设计与说明
- `test:` 单元、集成或回归案例
- `feat:` 新能力
- `fix:` 缺陷修复
- `eval:` 数据集、指标或实验报告
- `refactor:` 不改变行为的结构调整
- `chore:` 工具与依赖

## 开发顺序

```text
failure fixture
  → failing test / baseline evaluation
  → implementation
  → regression evaluation
  → design note or experiment report
```

## Pull Request 检查

- [ ] 单元测试通过
- [ ] Schema 校验通过
- [ ] 新行为有测试覆盖
- [ ] 评测指标无未解释退化
- [ ] 没有跨租户、注入或引用安全回归
- [ ] 文档与配置已同步
