# 可移植双平台部署

目标是在一台安装了 Git、Docker 和 Python 3 的新电脑上，部署两个彼此独立的项目：

- `rag-evolution-lab`：医疗设备知识库与业务 Agent。
- `agent-evaluation`：只读连接业务 Agent 的评测与 Prompt 实验平台。

部署脚本会真实生成 JWT、PostgreSQL、MinIO 和四个本地账号的随机凭据，并写入权限为 `0600` 的私有文件。Qwen 与 DeepSeek Key 永远不写入仓库、`.env`、评测记录或凭据文件，只从启动进程环境注入。

## 1. 克隆仓库

```bash
git clone https://github.com/dingpuyu/rag-evolution-lab.git
git clone https://github.com/dingpuyu/agent-evaluation.git
```

两个仓库建议放在同一级目录，评测平台会自动读取 RAG 部署生成的端口和 Tenant A 测试账号。

## 2. 准备模型密钥

macOS/Linux 当前终端：

```bash
export QWEN_API_KEY='你的阿里云百炼 Key'
export DEEPSEEK_API_KEY='你的 DeepSeek Key'
```

密钥只存在于当前终端及其启动的容器环境。不要把上面两行追加到仓库文件。

macOS 已使用 `launchctl setenv` 配置时，启动脚本也会自动读取。Linux 服务器建议由 systemd、Docker Secret 或云密钥管理服务注入。

## 3. 部署 RAG 与医疗 Agent

```bash
cd rag-evolution-lab
make deploy-init
make deploy-up
make deploy-bootstrap
make deploy-verify
```

生成文件：

- `.env`：随机基础设施密钥、随机账号密码、端口和模型非敏感配置。
- `.deploy/credentials.txt`：可登录的本地账号和页面地址。

两者都被 `.gitignore` 排除且权限为 `0600`。`deploy-init` 默认拒绝覆盖，防止在持久化账号已创建后意外轮换密码。需要重建时，应先备份或删除旧 Compose Volume，再显式使用脚本的 `--force`。

默认远端模型组合：

- Qwen `text-embedding-v4`，1024 维。
- `qwen3-rerank`，严格模式。
- DeepSeek `deepseek-chat`。
- Milvus 2.6、PostgreSQL 16、Redis、MinIO、Document Parser。

`deploy-bootstrap` 使用仓库内公开来源摘要与完全虚构的运维资料完成真实解析、Embedding 和 Milvus 写入，并等待所有异步索引任务成功，不依赖宿主机安装 `uv`。多格式派生文件属于开发增强项，不阻塞新机器首次部署。

如只验证离线工程链路：

```bash
PROFILE=offline make deploy-init
make deploy-up
make deploy-bootstrap
make deploy-verify
```

离线档使用确定性 Hash Embedding、启发式 Rerank 和抽取式回答，不代表线上模型质量。

## 4. 部署评测平台

```bash
cd ../agent-evaluation
make deploy-init
make deploy-up
make deploy-verify
```

评测初始化会读取 `../rag-evolution-lab/.env`，生成自己的 `.env` 与 `.deploy/credentials.txt`，自动对齐 RAG API、Agent 端口和 Tenant A 随机密码。评测平台仍只从进程环境读取 DeepSeek Key。

## 5. 验收入口

- 医疗设备 Agent：<http://localhost:3000/medical>
- 知识库门户：<http://localhost:3000/portal>
- RAG API：<http://localhost:8080/healthz>
- Agent API：<http://localhost:8090/healthz>
- Agent Evaluation：<http://localhost:18200>
- Evaluation Studio：<http://localhost:18200/studio>

端口可在初始化时修改，例如：

```bash
python3 scripts/deploy_init.py --api-port 18080 --agent-port 18090 --web-port 13000
```

评测仓库再次执行 `make deploy-init` 时会读取这些实际端口，不需要手动同步。

## 6. 验收内容

`rag-evolution-lab/make deploy-verify` 会验证：

- 服务健康、真实随机管理员登录。
- 数据集和 Agent Application 已创建。
- Tenant A/B 数据集隔离。
- 客户账号只能读取公开销售资料。
- 医疗问答、澄清、临床拒答、现场更正通知和引用。

`agent-evaluation/make deploy-verify` 会验证：

- 在干净目标上先执行一次真实医疗 RAG 基线，并仅从真实失败中固化首条 Bad Case；全通过时不伪造问题。
- 评测平台使用 RAG 身份登录。
- Target Contract、Bad Case 和冻结数据集可读取。
- Development、盲化 Holdout、Regression 三层实验。
- 项目梳理 Agent 和阶段 Prompt 对照。
- Candidate 不写入目标生产配置。

## 7. 边界

这是可复现的单机生产形态部署，不是假装成高可用生产集群。公网部署仍必须增加 HTTPS、反向代理、OIDC、备份、私有网络、镜像仓库、监控告警和托管数据库。随机本地账号适合学习、验收和面试演示，不替代企业身份系统。
