# RagLab LangGraph Agent Service

这是 Rag Evolution Lab 的上层 Agent 编排服务，面向企业 IT 服务台场景。

## 分工

- LangGraph：状态图、业务分支、人工确认和可恢复线程。
- LangChain：DeepSeek Chat 模型、结构化 Planner 和回答生成。
- Go Knowledge Gateway：身份验证、租户/应用权限、Milvus 检索、Rerank 和引用。
- 本服务不直接连接 Milvus，也不自行解析或拼接租户过滤条件。

## 本地运行

```bash
cd services/agent-orchestrator
uv sync --extra test
uv run pytest
RAGLAB_API_URL=http://127.0.0.1:18080 \
DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY" \
uv run uvicorn app.main:app --reload --port 8090
```

没有 DeepSeek Key 时会自动使用确定性 Planner/Answerer，方便离线单测；明确的知识、状态、身份和工单意图始终先经过确定性安全路由，防止模型误选工具。

## API

`POST /api/v1/apps/{app_id}/agent/answer`

```json
{
  "environment_id": "tenant_a-support-agent-dev",
  "query": "如何申请企业单点登录？"
}
```

请求必须携带用户的 `Authorization`。服务只转发该凭证给 Go Gateway，由 Go 重新验证，不在 Agent 服务中复制权限逻辑。

工单请求会返回 `needs_confirmation=true` 和 `thread_id`。前端带同一个 `thread_id` 再提交 `confirmation=true`，LangGraph 才会恢复线程；当前版本仍是安全演示，不会连接真实工单写接口。

## Compose

完整栈会暴露：

- Go API：`http://localhost:18080`
- LangGraph Agent：`http://localhost:8090`
- Web：`http://localhost:13000/portal`

`DEEPSEEK_API_KEY` 只通过 Compose 环境变量注入，不写入镜像和仓库。

