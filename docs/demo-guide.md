# Five-minute Demo Guide

This guide demonstrates how one controlled RAG change fixes observable failures while preserving security boundaries. All commands run locally without an external model.

## 1. Verify the harness

```bash
go test ./...
go run ./cmd/raglab validate
```

Expected dataset summary:

```text
valid documents=13 golden_cases=20
```

## 2. Compare aggregate metrics

```bash
go run ./cmd/raglab compare \
  --baseline v0-keyword \
  --candidate v2-metadata \
  --split development
```

Key change:

```text
Hit Rate@5:         0.850 -> 0.900
MRR:                0.762 -> 0.900
Document Recall@5:  0.850 -> 0.900
Metadata Violations:   41 -> 0
Unauthorized Results:   0 -> 0
```

The security invariant remains unchanged while stale, wrong-version and cross-product candidates are eliminated.

## 3. Reproduce a stale-version answer

Baseline:

```bash
go run ./cmd/raglab query \
  --pipeline v0-keyword \
  --query "当前稳定版的单点登录入口在哪里？" \
  --tenant tenant_a \
  --role admin \
  --product identity
```

The top answer incorrectly cites AcmeCloud 2.1 and recommends `安全设置 → SSO`.

Metadata candidate:

```bash
go run ./cmd/raglab query \
  --pipeline v2-metadata \
  --query "当前稳定版的单点登录入口在哪里？" \
  --tenant tenant_a \
  --role admin \
  --product identity
```

The deprecated document is removed before scoring. The answer now cites AcmeCloud 2.3 and returns `身份中心 → 企业登录`.

## 4. Distinguish authorization from refusal quality

Baseline:

```bash
go run ./cmd/raglab query \
  --pipeline v0-keyword \
  --query "Tenant A 的 reports-priority-a 队列什么时候可以启用？" \
  --tenant tenant_b \
  --role admin \
  --product operations \
  --version 2.3
```

The ACL correctly blocks Tenant A's private runbook, but unrelated public knowledge remains in Top-K and the extractive generator produces a misleading answer.

Metadata candidate:

```bash
go run ./cmd/raglab query \
  --pipeline v2-metadata \
  --query "Tenant A 的 reports-priority-a 队列什么时候可以启用？" \
  --tenant tenant_b \
  --role admin \
  --product operations \
  --version 2.3
```

The candidate returns zero results and responds with `知识库中没有找到足够证据。` No private fact enters retrieval, context, citation or trace.

## 5. Explain the remaining failure

The generic unanswerable case still fails because active but weakly related knowledge can pass deterministic metadata filters. This is intentional: Metadata Filter solves candidate validity, not semantic answerability. A later experiment must add score calibration or an answerability gate and prove it does not increase false refusals.

## Discussion points

- Why filtering happens before BM25 scoring.
- Why ACL and metadata violations use separate regression metrics.
- Why an explicitly requested deprecated version is allowed.
- Why a higher Hit Rate does not prove refusal quality.
- How the same harness will evaluate Hybrid Retrieval and Reranking.
