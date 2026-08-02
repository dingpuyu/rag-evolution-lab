# AI Provider 胶水层

本项目把生成模型和 Embedding 模型拆成两条可替换的 Provider 链路。Agent、RAG Gateway、检索器和门户只依赖稳定接口，不直接依赖 DeepSeek、Qwen、Ollama 或某个云厂商 SDK。

```text
Agent / RAG Gateway
        │
        ├── generation.Generator
        │     └── OpenAI-compatible Chat Completions
        │           └── DeepSeek / TokenHub / 私有网关
        │
        └── retrieval.Embedder
              ├── OllamaEmbedder
              │     └── 本地 Qwen3-Embedding
              └── OpenAICompatibleEmbedder
                    └── TokenHub / 私有 Qwen Embedding 网关
```

## 推荐组合

当前本地体验使用：

```text
生成：DeepSeek Chat API
向量化：Ollama + qwen3-embedding:4b-local
向量库：Milvus
```

如果需要把 Embedding 迁移到云端或企业内部网关，只切换：

```dotenv
RAGLAB_EMBEDDING_BACKEND=openai-compatible
RAGLAB_EMBEDDING_BASE_URL=https://tokenhub.tencentmaas.com/v1
RAGLAB_EMBEDDING_API_KEY=...
RAGLAB_EMBEDDING_MODEL=kinfra-text-embedding-4b
RAGLAB_EMBEDDING_DIMENSIONS=2560
```

生成模型仍保持独立：

```dotenv
RAGLAB_GENERATION_PROVIDER=deepseek
RAGLAB_GENERATION_BASE_URL=https://api.deepseek.com
RAGLAB_GENERATION_API_KEY=...
RAGLAB_GENERATION_MODEL=deepseek-chat
```

## 为什么要拆开

1. 生成模型和 Embedding 的升级节奏、成本和可用性不同。
2. Embedding 变更会改变向量空间，必须重新建 Collection/索引版本；生成模型变更通常不需要回填资料。
3. 统一接口可以让 Agent 业务、权限、引用、评测和页面保持稳定，便于本地模型、云模型和私有化网关之间切换。
4. Provider 层集中处理认证、超时、批量请求、错误信息、返回数量和向量维度校验，避免业务代码散落厂商细节。

## 迁移门禁

更换 Embedding 模型、维度、量化版本或 Query Instruction 时：

1. 更新 `RAGLAB_EMBEDDING_VERSION`。
2. 构建新的 Milvus Collection/索引版本。
3. 用同一评测集比较 Recall@K、MRR、P95 和成本。
4. 通过 Alias 发布；验证失败时回滚 Alias。

禁止把不同模型或不同维度的向量写入同一个 Collection。`OpenAICompatibleEmbedder` 会在请求层校验批量数量和维度，索引生命周期还会在发布层再次校验模型版本。

## 面试表达

可以这样描述：

> 我把 LLM 和 Embedding 抽象成两个独立的 Provider Port。生成侧通过 OpenAI-compatible Chat Completions 接 DeepSeek，向量侧既支持本地 Ollama Qwen3，也支持 OpenAI-compatible 的云端/私有 Embedding 网关。业务层只使用 `Generator` 和 `Embedder` 接口，Provider 负责鉴权、批量、超时和维度校验；当 Embedding 版本变化时，通过新 Collection 加 Alias 灰度发布，避免向量空间混写。
