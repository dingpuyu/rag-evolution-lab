.PHONY: fmt test validate validate-v4 ingest eval compare compare-metadata compare-routing compare-rerank eval-gate dataset-eval dataset-eval-isolated answer-eval answer-eval-blind answer-eval-stream answer-eval-blind-stream answer-eval-blind-isolated serve-embedding milvus-up milvus-down milvus-status milvus-seed query-milvus eval-milvus compare-milvus postgres-up postgres-down postgres-status scale-10k scale-100k scale-bench serve-lab serve-lab-eval regression-smoke web-dev

DOCKER_COMPOSE ?= docker-compose

fmt:
	gofmt -w cmd internal

test:
	go test ./...

validate:
	go run ./cmd/raglab validate

validate-v4:
	go run ./cmd/raglab validate --split v4-challenge

ingest:
	go run ./cmd/raglab ingest

eval:
	go run ./cmd/raglab eval --pipeline v0-keyword --split development

compare:
	go run ./cmd/raglab compare --baseline v0-keyword --candidate v1-vector --split development

compare-metadata:
	go run ./cmd/raglab compare --baseline v0-keyword --candidate v2-metadata --split development

compare-routing:
	go run ./cmd/raglab compare --baseline v3-hybrid-metadata-consensus --candidate v4-router --split development

compare-rerank:
	go run ./cmd/raglab compare --baseline v4-router --candidate v5-rerank --split development

eval-gate:
	go run ./cmd/raglab compare --baseline $${BASELINE_PIPELINE:-v0-keyword} --candidate $${CANDIDATE_PIPELINE:-v1-vector} --split $${SPLIT:-development} --fail-on-regression --min-hit-rate $${MIN_HIT_RATE:-0.89} --min-mrr $${MIN_MRR:-0.76} --min-ndcg $${MIN_NDCG:-0.78}

dataset-eval:
	go run ./cmd/raglab dataset-eval

dataset-eval-isolated:
	RAGLAB_API_URL=$${RAGLAB_EVAL_API_URL:-http://127.0.0.1:8081} go run ./cmd/raglab dataset-eval

answer-eval:
	go run ./cmd/raglab answer-eval

answer-eval-blind:
	go run ./cmd/raglab answer-eval --suite datasets/answer-harness/grounded-answer-blind-v1.json --json-report eval/reports/grounded-answer-blind-latest.json --markdown-report eval/reports/grounded-answer-blind-latest.md

answer-eval-stream:
	go run ./cmd/raglab answer-eval --stream --json-report eval/reports/grounded-answer-stream-latest.json --markdown-report eval/reports/grounded-answer-stream-latest.md

answer-eval-blind-stream:
	go run ./cmd/raglab answer-eval --stream --suite datasets/answer-harness/grounded-answer-blind-v1.json --json-report eval/reports/grounded-answer-blind-stream-latest.json --markdown-report eval/reports/grounded-answer-blind-stream-latest.md

answer-eval-blind-isolated:
	RAGLAB_API_URL=$${RAGLAB_EVAL_API_URL:-http://127.0.0.1:8081} go run ./cmd/raglab answer-eval --suite datasets/answer-harness/grounded-answer-blind-v1.json --json-report eval/reports/grounded-answer-blind-isolated-latest.json --markdown-report eval/reports/grounded-answer-blind-isolated-latest.md

serve-embedding:
	go run ./cmd/raglab serve-embedding --backend auto

milvus-up:
	$(DOCKER_COMPOSE) -f deploy/milvus/docker-compose.yml up -d

milvus-down:
	$(DOCKER_COMPOSE) -f deploy/milvus/docker-compose.yml down

milvus-status:
	$(DOCKER_COMPOSE) -f deploy/milvus/docker-compose.yml ps
	@curl --fail --silent http://127.0.0.1:9091/healthz
	@echo

milvus-seed:
	go run ./cmd/raglab milvus-seed --model $${RAGLAB_OLLAMA_MODEL:-qwen3-embedding:4b-local}

postgres-up:
	$(DOCKER_COMPOSE) -f deploy/postgres/docker-compose.yml up -d

postgres-down:
	$(DOCKER_COMPOSE) -f deploy/postgres/docker-compose.yml down

postgres-status:
	$(DOCKER_COMPOSE) -f deploy/postgres/docker-compose.yml ps
	@docker exec raglab-postgres pg_isready -U raglab -d raglab

query-milvus:
	RAGLAB_VECTOR_BACKEND=milvus go run ./cmd/raglab query --pipeline v5-milvus-rerank --query "$${QUERY:-当前版本如何配置企业单点登录？}" --tenant "$${TENANT:-tenant_a}" --role "$${ROLE:-admin}"

eval-milvus:
	RAGLAB_VECTOR_BACKEND=milvus go run ./cmd/raglab eval --pipeline v5-milvus-rerank --split $${SPLIT:-development}

compare-milvus:
	RAGLAB_VECTOR_BACKEND=both go run ./cmd/raglab compare --baseline v5-ollama-rerank --candidate v5-milvus-rerank --split $${SPLIT:-development}

scale-10k:
	go run ./cmd/ragbench all --chunks 10000 --dimensions 1024 --topics 100 --tenants 100 --profile easy-v1 --batch-size 100 --queries 100 --warmup 20 --top-k 10 --concurrency 8 --ef 32,64,128

scale-100k:
	go run ./cmd/ragbench all --chunks 100000 --dimensions 1024 --topics 1000 --tenants 100 --profile hard-v2 --batch-size 200 --queries 300 --warmup 50 --top-k 10 --concurrency 8 --hnsw-m 8 --ef-build 160 --ef 16,32,64,128 --collection-prefix raglab_bench_100k --collection-version v2 --timeout 90m

scale-bench:
	go run ./cmd/ragbench run --chunks 10000 --dimensions 1024 --topics 100 --tenants 100 --queries $${QUERIES:-100} --top-k $${TOP_K:-10} --concurrency $${CONCURRENCY:-8} --ef $${EF:-32,64,128}

serve-lab:
	go run ./cmd/raglab serve-lab --model $${RAGLAB_OLLAMA_MODEL:-qwen3-embedding:4b-local}

serve-lab-eval:
	RAGLAB_LIFECYCLE_COLLECTION=$${RAGLAB_EVAL_LIFECYCLE_COLLECTION:-raglab_lifecycle_eval_v1} \
	RAGLAB_LIFECYCLE_ALIAS=$${RAGLAB_EVAL_LIFECYCLE_ALIAS:-raglab_knowledge_eval} \
	RAGLAB_LIFECYCLE_STATE=$${RAGLAB_EVAL_LIFECYCLE_STATE:-data/lifecycle/eval-state.json} \
	RAGLAB_INGESTION_JOB_STATE=$${RAGLAB_EVAL_INGESTION_JOB_STATE:-data/ingestion/eval-jobs.json} \
	go run ./cmd/raglab serve-lab --addr $${RAGLAB_EVAL_ADDR:-127.0.0.1:8081} --model $${RAGLAB_OLLAMA_MODEL:-qwen3-embedding:4b-local}

regression-smoke:
	python3 scripts/regression_smoke.py --api $${RAGLAB_API_URL:-http://127.0.0.1:8080}

web-dev:
	npm --prefix web run dev
