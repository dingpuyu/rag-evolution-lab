# 一键部署 RAG Evolution Lab

> 新电脑优先使用 `make deploy-init → deploy-up → deploy-bootstrap → deploy-verify`，它会生成随机凭据并验证完整医疗 Agent。双平台步骤见[可移植双平台部署](portable-deployment.md)。本页后续内容保留用于手工配置和理解各 Provider 选项。

这套部署栈用于在一台开发机或演示服务器上快速启动完整体验：RAG Lab API、客服门户、PostgreSQL 控制面和 Milvus 向量库。它使用独立的 Docker named volume，不会复用仓库里已有的本地实验数据卷。

## 1. 前置条件

- Docker Engine 20.10+。
- Docker Compose V2（`docker compose`）或兼容的 `docker-compose` 命令。
- 至少 8 GB 可用内存；首次拉取镜像和构建网页/API 会需要一些时间。

在仓库根目录执行下面的命令。项目会自动使用 Compose 默认值，即使没有 `.env` 也可以启动；建议复制模板后再按需修改：

```bash
cp .env.example .env
# Makefile automatically loads .env when it exists.
RAGLAB_GENERATION_API_KEY="$DEEPSEEK_API_KEY" make stack-up
RAGLAB_API_PORT=18080 RAGLAB_WEB_PORT=13000 make stack-status
RAGLAB_API_PORT=18080 RAGLAB_WEB_PORT=13000 make stack-smoke
```

启动完成后访问：

- 客服门户：<http://localhost:3000/portal>
- API 健康检查：<http://localhost:8080/healthz>
- Milvus 健康检查：<http://localhost:9091/healthz>

Compose 默认同时启动 Redis，作为多副本可替换的共享限流后端。需要查看 Trace 时，在主栈启动后执行：

```bash
make observability-up
# 将 .env 中的 RAGLAB_OTEL_ENDPOINT 改为：
# RAGLAB_OTEL_ENDPOINT=http://otel-collector:4318
make stack-up
```

Jaeger UI：<http://localhost:16686>。关闭观测 profile 使用 `make observability-down`，不会删除主栈数据卷。

首次启动 Milvus 可能需要 1–3 分钟。可以用 `docker-compose -f deploy/stack/docker-compose.yml logs -f milvus api` 查看启动过程。

`make stack-smoke` 会等待 API 和门户健康检查，然后使用默认管理员验证登录、数据集目录和 Agent Application 控制面。它不写入业务资料，适合部署后快速验收；需要验证索引构建、Credential Scope 和回滚链路时，再执行 `make enterprise-eval` 或 `make enterprise-eval-build`。

## 2. 默认体验档

模板默认使用：

- `RAGLAB_EMBEDDING_BACKEND=hash`：确定性 Hash Embedding，启动时不需要 Ollama，适合验收接口、权限、导入、检索和回归流程。
- `RAGLAB_GENERATION_PROVIDER=extractive`：确定性抽取式回答，启动时不需要远程大模型。

这个档位是“工程烟雾测试”，不代表生产检索质量。登录门户后可以导入资料、建立知识库，再执行检索和回答体验。

默认本地演示管理员：

```text
账号：admin@raglab.local
密码：change-this-admin-password
```

在非本机环境使用前，必须修改 `.env` 中的 `RAGLAB_AUTH_SECRET` 和 `RAGLAB_PLATFORM_ADMIN_PASSWORD`，不要把 `.env` 提交到 Git。

## 3. 接入本地 Ollama 与 DeepSeek

如果要使用真实 Embedding 和生成模型，编辑 `.env`：

```dotenv
RAGLAB_EMBEDDING_BACKEND=ollama
RAGLAB_OLLAMA_URL=http://host.docker.internal:11434
RAGLAB_OLLAMA_MODEL=qwen3-embedding:4b-local
RAGLAB_GENERATION_PROVIDER=deepseek
RAGLAB_GENERATION_API_KEY=替换为你的token
RAGLAB_GENERATION_BASE_URL=https://api.deepseek.com
RAGLAB_GENERATION_MODEL=deepseek-v4-pro
```

先确认宿主机 Ollama 已启动并能看到模型：

```bash
ollama list
curl http://127.0.0.1:11434/api/tags
```

Compose 已为 API 容器配置 `host.docker.internal`；Linux Docker 环境也会通过 `host-gateway` 映射到宿主机。切换模型配置后重建 API：

```bash
make stack-up
```

## 4. 接入 DeepSeek 生成 + Qwen Embedding

生成和向量化是两条独立的 Provider 链路。上层 Agent、RAG Gateway 和门户只依赖统一的生成/Embedding 接口，不需要感知模型厂商；因此可以用 DeepSeek 负责回答，用 TokenHub 或企业内部 OpenAI-compatible 网关提供 Qwen Embedding。

以 TokenHub 的 Qwen3 Embedding 4B 为例，在 `.env` 中配置：

```dotenv
RAGLAB_EMBEDDING_BACKEND=openai-compatible
RAGLAB_EMBEDDING_BASE_URL=https://tokenhub.tencentmaas.com/v1
RAGLAB_EMBEDDING_API_KEY=替换为你的Embedding服务token
RAGLAB_EMBEDDING_MODEL=kinfra-text-embedding-4b
RAGLAB_EMBEDDING_DIMENSIONS=2560
RAGLAB_QUERY_INSTRUCTION=Given a user query, retrieve passages that directly answer it

RAGLAB_GENERATION_PROVIDER=deepseek
RAGLAB_GENERATION_BASE_URL=https://api.deepseek.com
RAGLAB_GENERATION_API_KEY=替换为你的DeepSeek token
RAGLAB_GENERATION_MODEL=deepseek-chat
```

切换 Embedding 模型、维度或版本后，必须使用新的 Milvus Collection/索引版本完成回填，再通过 Alias 发布；不能把旧向量和新向量混写。建议同时设置：

```dotenv
RAGLAB_EMBEDDING_VERSION=qwen3-embedding-4b-tokenhub-v1
```

验证 Embedding 适配器而不启动完整门户：

```bash
RAGLAB_EMBEDDING_BACKEND=openai-compatible \
RAGLAB_EMBEDDING_BASE_URL=https://tokenhub.tencentmaas.com/v1 \
RAGLAB_EMBEDDING_API_KEY="$TOKENHUB_API_KEY" \
RAGLAB_EMBEDDING_MODEL=kinfra-text-embedding-4b \
RAGLAB_EMBEDDING_DIMENSIONS=2560 \
go run ./cmd/raglab serve-embedding --backend openai-compatible --addr 127.0.0.1:18081
```

这样做的关键点是：LLM 的失败重试、回答提示词和 Agent 编排不与 Embedding 供应商耦合；Embedding 维度、模型版本和 Milvus Collection 则由索引生命周期门禁统一管理。

## 5. 与已有本地服务并存

如果本机已有 8080、3000、19530、9091 或 5433 被占用，可以使用另一组端口。网页在构建时需要知道 API 的宿主机地址：

```bash
RAGLAB_API_PORT=18080 \
RAGLAB_WEB_PORT=13000 \
RAGLAB_MILVUS_PORT=29530 \
RAGLAB_MILVUS_HEALTH_PORT=29091 \
RAGLAB_POSTGRES_PORT=15433 \
RAGLAB_REDIS_PORT=16379 \
NEXT_PUBLIC_API_BASE=http://localhost:18080 \
make stack-up
```

此时门户地址为 <http://localhost:13000/portal>，API 地址为 <http://localhost:18080>。这些变量也可以写入 `.env`。

## 5. 停止、升级与清理

```bash
# 停止容器，保留数据卷
make stack-down

# 查看容器状态
make stack-status

# 连同 named volume 一起清理（会删除本地导入数据、账号和 Milvus 数据）
docker-compose -f deploy/stack/docker-compose.yml down -v
```

更新代码后再次执行 `make stack-up` 会重新构建 API 和网页镜像；PostgreSQL、Milvus 和 API 状态保存在 named volume 中。

## 6. 生产化边界

这份 Compose 是“单机可复现部署”和面试演示基线，不等同于高可用生产编排。真实生产环境还需要外置 PostgreSQL/Milvus、密钥管理、TLS、OIDC、备份恢复、镜像签名、资源限制、监控告警和 Kubernetes/云托管策略。项目中的安全、评测和增量索引文档分别记录了这些演进边界。

正式上线前建议先执行安全门禁：

```bash
RAGLAB_REQUIRE_OIDC=true \
RAGLAB_AUTH_OIDC_ISSUER=https://id.example.com/realms/acme \
RAGLAB_AUTH_JWKS_URL=https://id.example.com/realms/acme/protocol/openid-connect/certs \
RAGLAB_POSTGRES_PASSWORD='<secret>' \
RAGLAB_MINIO_ROOT_USER='<user>' \
RAGLAB_MINIO_ROOT_PASSWORD='<secret>' \
RAGLAB_POSTGRES_URL='postgres://...?...&sslmode=verify-full' \
make production-preflight
```

预检失败时不要通过修改脚本绕过。生产 OIDC Token 只负责证明身份，租户 Membership 必须由邀请/管理员流程显式 Provision；服务不会因为第一次请求就自动创建权限。Milvus 的宿主机端口在 Compose 中默认只绑定 loopback，云上仍需通过安全组和私有子网再次限制。
