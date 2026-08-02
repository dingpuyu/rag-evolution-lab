# Five-minute Demo Guide

This guide demonstrates how controlled RAG changes fix observable failures while preserving security boundaries. The deterministic V4 path runs locally without an external model.

## 1. Verify the harness

```bash
go test ./...
go run ./cmd/raglab validate
go run ./cmd/raglab validate --split v4-challenge
```

Expected dataset summary:

```text
valid documents=23 golden_cases=20
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

## 5. Route by Query risk

```bash
go run ./cmd/raglab eval \
  --pipeline v4-router \
  --split development

go run ./cmd/raglab eval \
  --pipeline v4-router \
  --split v4-challenge
```

The report includes a route distribution. Exact identifiers and numbers use Metadata BM25; semantic paraphrases use Hybrid Union; access-sensitive queries use Tenant Scope Gate plus Consensus; external-status verification uses Anchor Gate plus Consensus.

Both current splits report Hit Rate@5, MRR and Recall@5 of 1.000 with zero metadata or authorization violations. This is 28 synthetic cases, not a production generalization claim.

## 6. Reproduce a pre-retrieval tenant refusal

```bash
go run ./cmd/raglab query \
  --pipeline v4-router \
  --query "租户 A 的报表专属加速队列名称是什么？" \
  --tenant tenant_b \
  --role admin \
  --product operations \
  --version 2.3
```

V4 classifies the Query as access-sensitive and rejects the explicit Tenant A / authenticated Tenant B conflict before retrieval. Similar public operations content cannot turn the request into an answer.

## 7. Explain the remaining limitation

The rule classifier is deliberately small and explainable. The current 28 cases all pass, but the rules have participated in Challenge iteration. A blind split and at least 60 Golden Queries are required before claiming robust routing generalization.

## Discussion points

- Why filtering happens before BM25 scoring.
- Why ACL and metadata violations use separate regression metrics.
- Why an explicitly requested deprecated version is allowed.
- Why a higher Hit Rate does not prove refusal quality.
- How the same harness will evaluate Hybrid Retrieval and Reranking.
- Why structured request context must not be mistaken for Query intent.
- Why tenant conflicts use a deterministic Gate instead of an Embedding threshold.
- How routing reduces local model calls from 20 to 9 on the Development Split.
