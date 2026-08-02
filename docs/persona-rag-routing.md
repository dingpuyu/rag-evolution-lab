# 通用人设与 RAG 分流

门户回答现在采用“先分流、再回答”的策略：

```text
用户问题
  ├─ 寒暄 / 能力介绍 / 通用概念 → RAG Desk 人设回答
  └─ 企业、产品、账号、权限、配置、流程等专有问题 → 检索授权知识库
                                             ├─ 有可靠证据 → 带引用回答
                                             └─ 没有可靠证据 → 拒答，不使用模型常识猜测
```

分流器位于 `internal/generation/intent.go`，是保守的确定性第一阶段路由，不负责鉴权。真正的租户隔离仍由服务端数据集授权和 Milvus Filter 执行。

通用回答使用 `persona-answer-v1` Prompt Version，并在响应中标记：

```json
{"answer_source":"persona","citations":[]}
```

RAG 回答使用 `grounded-answer-v1`，标记为 `answer_source=rag`，引用只能来自服务端实际召回的 chunk。对于未识别的问题，默认留在 RAG 路径，避免把企业事实误当作通用问题回答。

## 本地验证

```bash
TOKEN=$(curl -sS -H 'Content-Type: application/json' \
  -d '{"email":"admin@raglab.local","password":"change-this-admin-password"}' \
  http://127.0.0.1:18080/api/v1/auth/login | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')

curl -sS -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"query":"你好，你是谁？"}' \
  http://127.0.0.1:18080/api/v1/datasets/public-identity/answer
```

专有资料仍需先导入知识库；没有资料时返回 `insufficient_evidence` 是预期行为。

