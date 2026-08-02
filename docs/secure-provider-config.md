# 远程模型配置与密钥安全

本项目的模型 Key 只在服务端使用。浏览器只调用 RAG API/Agent API，不接触 DeepSeek 或 Embedding Key。

## 本地 macOS

`.env` 和 `.env.*` 已加入 `.gitignore`，并且 `.dockerignore` 不会把它们复制进镜像。推荐把阿里云 Embedding Key 放在 launchd 环境中，而不是仓库文件：

```bash
launchctl setenv QWEN_API_KEY '你的阿里云百炼Key'
```

重新打开终端后，用下面的命令确认只看到状态，不打印 Key：

```bash
if [ -n "$QWEN_API_KEY" ]; then echo 'QWEN_API_KEY=set'; else echo 'QWEN_API_KEY=unset'; fi
```

复制远程非敏感配置模板：

```bash
cp deploy/stack/remote-embedding.env.example .env.remote
chmod 600 .env.remote
```

启动时 Compose 会把 `QWEN_API_KEY` 映射成容器内的 `RAGLAB_EMBEDDING_API_KEY`：

```bash
docker-compose --env-file .env --env-file .env.remote \
  -f deploy/stack/docker-compose.yml up -d --build api
```

如果当前终端没有继承 launchd 环境，重新打开终端，或在启动命令前使用一次性环境变量；不要把真实值写入仓库。

## 服务器

生产环境不要把 Key 写入 Dockerfile、镜像、前端 `NEXT_PUBLIC_*` 变量、GitHub Actions 日志或命令行参数。推荐使用云厂商 Secret Manager、部署平台 Secret 或权限为 `600` 的服务器文件，并在容器启动时注入。

DeepSeek 和 Embedding 使用不同的 Key，按服务和环境分别创建、轮换和吊销。日志、Trace、错误响应只记录 Provider 名称和模型名，不记录 Authorization Header、Key 或完整请求体。

## 向量版本安全

远程 Embedding 模型或维度变化时，必须更新 `RAGLAB_EMBEDDING_VERSION`，新建 Milvus Collection 并重新导入，验证通过后再切换 Alias。不能把远程 1024 维向量和当前本地 Qwen3-Embedding-4B 的 2560 维向量写入同一个 Collection。
