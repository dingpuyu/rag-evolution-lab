# RAG Evolution Lab Web

交互式成果展示页，用真实实验数据呈现 RAG Pipeline 演进，并提供可连接本地 Go API 的 Embedding 文字相似度实验台。

## 展示内容

- V0 Keyword、V1 Vector、V2 Metadata 的演进路径
- Hit Rate、MRR、Recall 与 Metadata Violations 对比
- 版本污染和权限拒答两个 Before / After Case
- 可替换 Pipeline 架构
- Evaluation Harness 与回归门禁
- 两段文字的 Embedding 向量、Cosine、Dot Product 与 Euclidean Distance
- 数据集授权后的 Grounded Answer SSE 事件时间线、拒答原因、生成指标与服务端 Citation

## 本地运行

需要 Node.js `>=22.13.0`：

```bash
npm install
npm run dev
```

Embedding 实验需要在仓库根目录另开终端启动 API：

```bash
RAGLAB_OLLAMA_MODEL=qwen3-embedding:4b-local \
  go run ./cmd/raglab serve-embedding --backend ollama
```

Answer Lab 需要启动完整企业实验服务：

```bash
go run ./cmd/raglab serve-lab
```

打开数据隔离区，登录或签发演示身份，选择数据集后点击“流式生成 Grounded Answer”。

验证：

```bash
npm test
```
