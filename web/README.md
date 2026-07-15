# RAG Evolution Lab Web

交互式成果展示页，用真实实验数据呈现 RAG Pipeline 从 V0 到 V2 的演进。

## 展示内容

- V0 Keyword、V1 Vector、V2 Metadata 的演进路径
- Hit Rate、MRR、Recall 与 Metadata Violations 对比
- 版本污染和权限拒答两个 Before / After Case
- 可替换 Pipeline 架构
- Evaluation Harness 与回归门禁

## 本地运行

需要 Node.js `>=22.13.0`：

```bash
npm install
npm run dev
```

验证：

```bash
npm test
```
