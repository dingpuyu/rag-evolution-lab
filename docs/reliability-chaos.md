# 检索可靠性与故障演练

商业 RAG 不能把向量服务的短暂故障直接放大成整条问答链路不可用。本项目在
Hybrid/RRF 层加入了可控的检索预算和降级策略，并明确区分普通查询与安全敏感查询。

## 当前策略

| 场景 | 策略 | 原因 |
|---|---|---|
| 普通 Hybrid（BM25 + Vector） | 2 秒共享检索预算；一侧失败时保留健康源 | 保住可用性，关键词结果仍可回答一部分问题 |
| Access-sensitive / Unanswerable-risk Consensus | 保持 `MinSourceMatches=2`，任一源失败则失败闭环 | 不在证据不足时放宽安全边界或生成误导答案 |
| 所有源都失败 | 返回明确错误 | 不伪造“没有证据”或成功结果 |

普通 Hybrid 的 Trace 会记录 `allow_partial_results=true` 和
`search_timeout_ms=2000`；发生降级时结果的 `stage` 会带上 `-partial`，便于在生产
观测面统计降级次数。Consensus 路由不启用部分结果，属于 fail-closed 策略。

## 回归用例

```bash
make reliability-test
```

用例覆盖：

- 向量源返回错误时，BM25 结果仍可返回；
- 慢源超过 10ms 测试预算时，快速源结果按时返回；
- 默认 RRF 仍保持失败即报错，避免无意改变安全语义；
- Consensus 路由继续要求多源证据，不因可用性改动而放宽权限。

## 规模验证边界

`make scale-100k` 仍用于 Milvus 100K FLAT/HNSW 的索引、Recall、QPS、P50/P95/P99
基准；本次新增的是查询编排层的超时与降级语义，不能替代真实 Milvus 故障演练。
下一步会增加可启动的故障注入代理，验证：Milvus 5xx、连接超时、Embedding 限流、
查询与增量写入并发，以及恢复后是否自动回到完整 Hybrid。
