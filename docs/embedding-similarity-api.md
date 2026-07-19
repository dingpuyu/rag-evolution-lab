# Embedding 文字相似度实验 API

## 1. 这个实验验证什么

Embedding 模型将文本映射为固定维度的浮点向量。相似度接口同时展示：

- 模型/后端名称；
- 向量维度；
- 向量前 N 个数值与完整向量可选输出；
- L2 Norm、最小值和最大值；
- Cosine Similarity；
- Dot Product；
- Euclidean Distance；
- Embedding 调用耗时。

它不直接判断两个句子的“答案是否相同”。Embedding 相似度只能表示模型向量空间中的接近程度，阈值必须在目标数据集上标定。

## 2. 启动离线 Hash Baseline

```bash
go run ./cmd/raglab serve-embedding --backend hash --dimensions 512
```

Hash Embedder 用于单测、接口演示和离线回归，不代表真实神经网络语义模型。

## 3. 启动本地 Qwen3 Embedding

当前本机 Ollama 模型为 `qwen3-embedding:4b-local`：

```bash
RAGLAB_OLLAMA_MODEL=qwen3-embedding:4b-local \
  go run ./cmd/raglab serve-embedding --backend ollama
```

验证 RAG Query/Document 非对称编码时，可以提供 Query Instruction：

```bash
RAGLAB_OLLAMA_MODEL=qwen3-embedding:4b-local \
RAGLAB_QUERY_INSTRUCTION="Given a Chinese enterprise knowledge-base query, retrieve relevant passages that answer the query" \
  go run ./cmd/raglab serve-embedding --backend ollama
```

默认监听 `127.0.0.1:8080`，只暴露到本机。

## 4. 接口

### 健康检查

```http
GET /healthz
```

### 查看后端

```http
GET /api/v1/embeddings/info
```

### 比较文字相似度

```http
POST /api/v1/embeddings/similarity
Content-Type: application/json
```

请求：

```json
{
  "text_a": "企业员工如何配置单点登录？",
  "text_b": "管理员可以在身份中心开启 SSO 企业登录。",
  "mode": "symmetric",
  "preview_dimensions": 12,
  "include_vectors": false
}
```

字段：

| 字段 | 含义 |
|---|---|
| `text_a` | 第一段文本；`query_document` 模式下作为 Query |
| `text_b` | 第二段文本；`query_document` 模式下作为 Document |
| `mode` | `symmetric` 或 `query_document`，默认 `symmetric` |
| `preview_dimensions` | 返回向量预览长度，默认 12，最大 64 |
| `include_vectors` | 是否返回完整向量；Qwen3 为 2560 维，默认不返回以控制响应大小 |

调用示例：

```bash
curl -s http://127.0.0.1:8080/api/v1/embeddings/similarity \
  -H 'Content-Type: application/json' \
  -d '{
    "text_a":"企业员工如何配置单点登录？",
    "text_b":"管理员可以在身份中心开启 SSO 企业登录。",
    "mode":"symmetric",
    "preview_dimensions":8
  }'
```

## 5. 两种模式的区别

### symmetric

```text
vectorA, vectorB = EmbedDocuments([textA, textB])
similarity = cosine(vectorA, vectorB)
```

适合：句子相似度、聚类、去重等对称任务。交换 A/B 后结果应该一致。

### query_document

```text
queryVector    = EmbedQuery(textA)       // 可以增加检索指令
documentVector = EmbedDocuments([textB])
similarity     = cosine(queryVector, documentVector)
```

适合：RAG 检索。Query 和 Document 的角色不同，特别是使用检索指令的模型；交换 A/B 不保证结果相同。

## 6. 三个距离指标

### Cosine Similarity

```text
cos(A, B) = A·B / (||A|| × ||B||)
```

主要比较方向，最常用于文本向量检索。越接近 1 通常越相似，但阈值依赖模型和数据。

### Dot Product

```text
dot(A, B) = Σ AiBi
```

同时受方向和向量长度影响。若模型输出已归一化向量，Dot Product 与 Cosine 接近。

### Euclidean Distance

```text
distance(A, B) = sqrt(Σ(Ai-Bi)²)
```

越小越接近。是否适用取决于模型训练方式和向量库索引配置。

## 7. 安全与工程约束

- 服务默认只监听回环地址。
- 单个请求最大 64 KiB。
- 每段文字最大 8000 字符。
- JSON 拒绝未知字段和多个对象。
- 完整向量默认关闭，避免无意返回超大响应。
- 浏览器 CORS 仅允许 `localhost` 和 `127.0.0.1` 来源。
- HTTP Server 支持 SIGINT/SIGTERM 优雅停止。

## 8. 网页实验台

启动 API 后，在另一个终端运行：

```bash
cd web
npm run dev
```

打开页面中的 `Embedding 实验`，可以修改两段文字、切换编码模式并观察向量预览与三个相似度指标。

## 9. 本机 Qwen3 实测样例

2026-07-19 使用 `qwen3-embedding:4b-local` 比较：

- A：`企业员工如何配置单点登录？`
- B：`管理员可以在身份中心开启 SSO 企业登录。`
- Mode：`symmetric`

结果：

| 项目 | 结果 |
|---|---:|
| Dimensions | 2560 |
| Cosine Similarity | 约 0.757 |
| Vector A L2 Norm | 约 1.0 |
| Vector B L2 Norm | 约 1.0 |
| 模型冷启动请求 | 约 5.4s |
| 模型常驻后的两次请求 | 约 267ms / 236ms |

这组数据只代表当前设备和两段样本文本。它说明接口工作正常，也说明性能测试必须区分 Cold Start 与 Warm Request；不能把单次本地结果当作生产 SLA。
