# Agent 对话工作台体验

门户里的 `IT 服务台 Agent` 是一个完整的应用级对话入口，不是单次 Demo 表单。它把身份、应用/环境、LangGraph 状态、工具调用和 Go RAG Gateway 串在一条请求链路上。

## 启动

```bash
cp .env.example .env
make stack-up
```

默认端口是 `http://localhost:3000/portal`；如果本机 `.env` 将端口改为 `RAGLAB_WEB_PORT=13000`，则访问 `http://localhost:13000/portal`。Agent 服务默认监听 `8090`，健康检查为 `http://localhost:8090/healthz`。

登录后从左侧进入「IT 服务台 Agent」。平台管理员可以选择应用和环境；每次请求都会把当前登录 Token 传给 Agent，再由 Go Gateway 重新校验应用绑定、租户和数据集权限。

## 建议体验顺序

1. 点击「当前服务状态怎么样？」：验证受控 `service_status` 工具和 Agent 回复。
2. 发送「如何申请企业单点登录？」：验证 `knowledge_search` → Go 授权 RAG → 引用/证据轨迹。
3. 发送「帮我创建一个登录故障工单」：Agent 只生成草稿并暂停，页面会显示「确认提交工单」。
4. 点击确认：LangGraph 使用同一个 `thread_id` 恢复执行；演示环境会明确提示尚未接入真实工单写入，避免伪造外部副作用。
5. 继续发送其他问题，检查消息历史、右侧本轮 Trace，以及「新会话」清空行为。

## 页面与服务契约

```text
POST /api/v1/apps/{app_id}/agent/answer
{
  "environment_id": "{environment_id}",
  "query": "用户问题",
  "thread_id": "工单确认时回传",
  "confirmation": true
}
```

普通问答在浏览器会话中保留消息历史；服务端的 LangGraph checkpoint 主要负责需要恢复的人工确认状态。答案卡片会显示回答来源、工具名、步骤数和引用，右侧 Trace 则展示 planner → tool → observation 的执行链路。

## 当前边界

- 对话 UI 已支持多轮展示、执行中状态、应用/环境切换、引用、Trace、人工确认和安全失败提示。
- 当前消息历史保存在浏览器内存中，刷新页面会重新开始；下一步可将 conversation/thread 元数据持久化到 PostgreSQL，并增加 SSE 增量事件。
- 工单工具目前是安全的草稿/确认演示，接入真实 ITSM 时应替换 `ticket_draft` 的写入适配器，并保留幂等键、审计和权限检查。
