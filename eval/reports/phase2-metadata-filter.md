# Phase 2 Metadata Filter Experiment

> Dataset version: 0.1.0  
> Split: development  
> Corpus: 13 synthetic documents / 38 chunks  
> Golden cases: 20  
> Top-K: 5

## Hypothesis

Tenant and role ACLs prevent private-data leakage, but they do not prevent irrelevant products, stale versions or deprecated documents from entering Top-K. Applying deterministic metadata constraints before scoring should remove this pollution without changing BM25 itself.

## Controlled change

`v0-keyword` and `v2-metadata` use the same corpus, Chunk IDs, tokenizer, BM25 parameters, ACL logic and evaluation protocol. The candidate adds only these pre-scoring constraints:

- If `product` is supplied, keep only matching products.
- If `version` is supplied, keep that version, including an explicitly requested deprecated version.
- If no version is supplied, keep only `active` knowledge.
- ACL remains fail-closed and is evaluated independently.

## Results

| Pipeline | Hit Rate@5 | MRR | Document Recall@5 | Metadata Violations | Unauthorized Retrievals |
|---|---:|---:|---:|---:|---:|
| V0 Keyword | 0.850 | 0.762 | 0.850 | 41 | 0 |
| V2 Metadata | 0.900 | 0.900 | 0.900 | 0 | 0 |
| Delta | +0.050 | +0.138 | +0.050 | -41 | 0 |

### Selected categories

| Category | V0 Hit | V0 MRR | V2 Hit | V2 MRR | Observation |
|---|---:|---:|---:|---:|---|
| version_filter | 1.000 | 0.833 | 1.000 | 1.000 | Relevant version moves to rank 1 and conflicting versions are removed. |
| access_control | 0.500 | 0.500 | 1.000 | 1.000 | ACL was already safe; product/version filters remove unrelated public noise and enable clean refusal. |
| semantic_paraphrase | 0.750 | 0.625 | 0.750 | 0.750 | Filtering improves rank but cannot create missing semantic recall. |
| unanswerable | 0.000 | 0.000 | 0.000 | 0.000 | Metadata is not an answerability threshold; unrelated active knowledge may still be returned. |

## What this proves

1. Retrieval authorization and knowledge validity are separate controls. Zero unauthorized results did not imply zero stale or cross-product results.
2. Filtering before scoring improves ranking by shrinking the candidate set rather than tuning BM25 weights.
3. Explicit historical queries must remain possible. A blanket `status=active` rule would incorrectly reject the `version_002` case.
4. The new `metadata_violations` harness metric catches pollution that Hit Rate and Recall alone hide.

## Limitations and next experiment

- Product and version currently come from structured request context. Extracting them from natural-language Query belongs to the later Query Transformation stage.
- The experiment does not yet filter by effective time or quality score.
- Header-aware chunks already preserve heading paths, but table/code atomicity and parent-child expansion still need dedicated failure cases.
- `unanswerable_001` remains a deliberate failure and motivates score calibration or an answerability gate.
- V3 will combine metadata filtering with keyword and vector candidates using Reciprocal Rank Fusion.

## Reproduction

```bash
go test ./...
go run ./cmd/raglab compare \
  --baseline v0-keyword \
  --candidate v2-metadata \
  --split development
```
