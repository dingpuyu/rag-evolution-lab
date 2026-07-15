# 数据目录

## 目录规划

```text
datasets/
  corpus/
    acmecloud/
      documents/
      manifest.yaml
  golden/
    development/
    regression/
    blind/
  fixtures/
    unit/
    integration/
  schemas/
    document.schema.json
    golden-case.schema.json
```

## 规则

- 所有语料均为项目自建的合成数据，不复制受版权保护的企业文档。
- 文档必须在 Manifest 中登记。
- Golden Case 必须通过 JSON Schema 校验。
- Blind Set 不在 README、Demo 和单测中直接暴露答案。
- Fixture 只保留最小测试数据，不复制完整 Corpus。
- 修改语料或标注必须更新数据版本。

## 当前状态

已完成 Schema 和示例 Case，尚未生成正式语料与完整 Golden Dataset。
