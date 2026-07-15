# Phase 1 Baseline Report

> Dataset version: 0.1.0  
> Split: development  
> Corpus: 13 synthetic documents / 38 chunks  
> Golden cases: 20  
> Top-K: 5

## 1. Purpose

Phase 1 establishes two intentionally simple baselines:

- `v0-keyword`: in-memory BM25-style keyword retrieval.
- `v1-vector`: deterministic semantic hash vectors with cosine similarity.

The local vector implementation is designed for reproducible tests without an API key. It is not presented as a production embedding model. A real embedding adapter will be evaluated separately in a later experiment.

Both baselines enforce tenant and role ACLs. Product, version, lifecycle and quality metadata are intentionally not used yet, so that V2 can measure their effect.

## 2. Overall results

| Pipeline | Hit Rate@5 | MRR | Document Recall@5 | Unauthorized Retrievals |
|---|---:|---:|---:|---:|
| V0 Keyword | 0.850 | 0.762 | 0.850 | 0 |
| V1 Vector | 0.900 | 0.779 | 0.875 | 0 |
| Delta | +0.050 | +0.017 | +0.025 | 0 |

## 3. Category results

| Category | V0 Hit | V0 MRR | V1 Hit | V1 MRR | Observation |
|---|---:|---:|---:|---:|---|
| exact_match | 1.000 | 1.000 | 1.000 | 0.688 | Keyword keeps exact identifiers near rank 1. |
| semantic_paraphrase | 0.750 | 0.625 | 1.000 | 0.875 | Vector aliases improve paraphrased queries. |
| version_filter | 1.000 | 0.833 | 1.000 | 1.000 | Both still retrieve conflicting versions in some Top-K results. |
| multi_hop | 1.000 | 1.000 | 1.000 | 1.000 | Document recall is high, but Phase 1 does not yet verify evidence synthesis. |
| access_control | 0.500 | 0.500 | 0.500 | 0.500 | ACL blocks the private document, but unrelated public noise prevents clean refusal. |
| unanswerable | 0.000 | 0.000 | 0.000 | 0.000 | Neither baseline has an answerability threshold. |

## 4. Reproduced failures

### Keyword misses a semantic paraphrase

`semantic_004`: “这个产品有哪些收费档位？”

The keyword baseline returns no result because the corpus uses “套餐与计费”. The vector baseline canonicalizes the semantic alias and retrieves the billing document.

### Vector weakens exact identifier ranking

- `exact_003`: `X-RateLimit-Reset` is ranked second.
- `exact_004`: `E3018` is ranked fourth.

The correct documents are still inside Top-5, but MRR drops. This is a direct motivation for Hybrid Retrieval in V3.

### Missing metadata admits stale knowledge

The unanswerable certification query retrieves unrelated documents, including an authorized but deprecated low-quality support ticket. ACL alone is insufficient; lifecycle and quality metadata must be enforced separately.

### ACL is safe but refusal is poor

`access_002` asks Tenant B about a Tenant A private queue. Both pipelines correctly return zero unauthorized chunks, but they retrieve unrelated public documents instead of producing an empty result. Retrieval authorization and answerability are different problems.

## 5. What the metrics do not prove yet

- Document recall does not prove the final answer covers all required facts.
- The extractive generator only exposes the top chunk; it is not a production answer generator.
- Multi-hop cases are scored at retrieval level only.
- Citation precision and faithfulness are not yet evaluated.
- The deterministic vector baseline cannot replace a real embedding benchmark.

## 6. Phase 2 hypotheses

Phase 2 will test these hypotheses:

1. Structure-aware chunking improves section-level precision and citation quality.
2. Version, status and quality filters remove stale documents without reducing relevant recall.
3. Parent-child chunks restore context lost by smaller chunks.
4. A real embedding adapter improves semantic queries but still needs keyword retrieval for exact identifiers.
5. Retrieval score calibration and answerability rules reduce false-positive answers.

## 7. Reproduction

```bash
go test ./...
go run ./cmd/raglab validate
go run ./cmd/raglab ingest
go run ./cmd/raglab compare \
  --baseline v0-keyword \
  --candidate v1-vector \
  --split development
```
