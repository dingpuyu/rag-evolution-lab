.PHONY: fmt test validate validate-v4 ingest eval compare compare-metadata compare-routing compare-rerank eval-gate reliability-test dataset-eval dataset-eval-isolated answer-eval answer-eval-blind answer-eval-stream answer-eval-blind-stream answer-eval-blind-isolated enterprise-eval enterprise-eval-build serve-embedding milvus-up milvus-down milvus-status milvus-seed query-milvus eval-milvus compare-milvus postgres-up postgres-down postgres-status scale-10k scale-100k scale-bench serve-lab serve-lab-eval regression-smoke web-dev stack-up stack-smoke stack-down stack-status observability-up observability-down observability-status

DOCKER_COMPOSE ?= docker-compose
STACK_COMPOSE = $(DOCKER_COMPOSE) -f deploy/stack/docker-compose.yml
OBSERVABILITY_COMPOSE = $(DOCKER_COMPOSE) -f deploy/observability/docker-compose.yml

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

reliability-test:
	go test ./internal/retrieval -run 'TestRRF(CanServeHealthySourceWhenPeerFails|BoundsSlowSourceAndKeepsFastSource)'
	go test -race ./internal/retrieval

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

enterprise-eval:
	go run ./cmd/raglab enterprise-eval

enterprise-eval-build:
	go run ./cmd/raglab enterprise-eval --build --publish

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

stack-up:
	$(STACK_COMPOSE) up -d --build

stack-smoke:
	./scripts/stack_smoke.sh

stack-down:
	$(STACK_COMPOSE) down

stack-status:
	$(STACK_COMPOSE) ps

observability-up:
	$(OBSERVABILITY_COMPOSE) up -d

observability-down:
	$(OBSERVABILITY_COMPOSE) down

observability-status:
	$(OBSERVABILITY_COMPOSE) ps
