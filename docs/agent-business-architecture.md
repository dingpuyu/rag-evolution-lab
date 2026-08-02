# 企业 IT 服务台 Agent 方案

本项目的 Agent 目标不是做一个泛聊天机器人，而是构建一个可以承接企业客服、运维和账号支持场景的受控 Agent。第一条真实业务链路选择“企业 IT 服务台”：用户提出问题后，Agent 识别意图，在授权知识库中检索；需要实时信息时调用只读工具；涉及写操作时只生成待确认草稿，不直接执行。

## 技术定位

```text
Python Agent Service（LangGraph + LangChain，主编排层）
                             │ REST / SSE / App Credential
                             ▼
                    Knowledge Gateway（Go）
                             │
              ┌──────────────┴──────────────┐
              │                             │
       Go Agent Loop（兼容/降级）       Enterprise RAG
                                  Milvus + BM25 + RRF + Rerank
              │                             │
              └──────────────┬──────────────┘
                             ▼
                    DeepSeek API（规划/生成）
```

核心检索和权限链路继续由 Go 实现，但正式 Agent 编排以 Python + LangGraph 为主：LangChain 负责模型、工具和结构化输出适配，LangGraph 负责状态、分支、循环、人工确认和恢复执行。Go Agent Loop 保留为兼容/降级运行时和回归基线。未来如需 Java 接入，使用 Spring AI 调用同一 Gateway，不复制 RAG 逻辑。

## 第一条业务闭环：IT 服务台

| 意图 | Agent 动作 | 安全策略 |
|---|---|---|
| 产品/流程咨询 | `knowledge_answer` | 授权知识库检索，回答必须带引用 |
| 服务状态查询 | `service_status` | 只读工具，返回数据时间和状态来源 |
| 当前用户权限 | `account_access` | 只读取可信身份 Claims，不调用模型判断权限 |
| 创建工单 | `ticket_draft` | 只生成草稿，必须用户确认后才允许写入 |
| 缺少关键信息 | `clarify` | 询问产品、环境、错误码等必要槽位 |

## Agent Loop 契约

每轮最多执行 4 步，状态只能沿着下面的有限状态机前进：

```text
received → planned → tool_called → observed → planned
                         │
                         ├─ needs_confirmation → waiting_confirmation
                         └─ final → completed
```

Planner 输出固定 JSON，不允许输出自由格式指令：

```json
{
  "type": "tool",
  "tool": "knowledge_answer",
  "arguments": {"query": "如何配置企业单点登录"},
  "reason": "需要读取企业知识库"
}
```

工具返回结果必须带 `tool_call_id`、`status` 和可审计摘要。工具超时、未知工具、循环超过上限和写操作未确认都会 fail-closed。

## 模型策略

- 本地真实部署默认使用 DeepSeek OpenAI-compatible API。
- `RAGLAB_GENERATION_API_KEY` 优先，`DEEPSEEK_API_KEY` 作为兼容回退；密钥只从环境变量读取，不写入仓库。
- DeepSeek 负责结构化意图/工具决策和自然语言生成；权限、检索、引用和工具副作用由 Go 服务端控制。
- 无 DeepSeek 时仅允许确定性测试替身，不把它当作生产模型。

## 面试表达

> 我把 Agent 分成决策层、工具层和知识层。决策层用 DeepSeek 输出结构化 Action，工具层通过白名单执行只读查询或生成待确认草稿，知识层由 Go Knowledge Gateway 统一完成租户授权、Milvus 检索、Rerank、引用校验和 Trace。LangChain/Spring AI 只作为上层适配器调用 Gateway，不直接触碰向量库，因此 Agent 的权限和可审计性不会依赖 Prompt。

## 本地体验

启动完整栈后，使用租户 A 管理员调用 Agent：

```bash
TOKEN=$(curl -sS -H 'Content-Type: application/json' \
  -d '{"email":"alice@tenant-a.local","password":"RagLab-Alice-2026!"}' \
  http://127.0.0.1:18080/api/v1/auth/login | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')

curl -sS -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"environment_id":"tenant_a-support-agent-dev","query":"如何申请企业单点登录？"}' \
  http://127.0.0.1:18080/api/v1/apps/tenant_a-support-agent/agent/answer
```

建议依次体验：

1. `你好，你是谁？`：Planner 直接返回人设回答。
2. `当前服务状态怎么样？`：调用 `service_status` 只读工具。
3. `我的权限是什么？`：读取当前可信身份 Claims。
4. `如何申请企业单点登录？`：调用授权 RAG，返回引用。
5. `帮我创建一个登录故障工单`：只生成草稿并等待用户确认。
