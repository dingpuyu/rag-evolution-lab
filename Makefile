.PHONY: fmt test validate validate-v4 ingest eval compare compare-metadata compare-routing compare-rerank dataset-eval serve-embedding milvus-up milvus-down milvus-status milvus-seed query-milvus eval-milvus compare-milvus postgres-up postgres-down postgres-status scale-10k scale-100k scale-bench serve-lab web-dev

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

dataset-eval:
	go run ./cmd/raglab dataset-eval

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

web-dev:
	npm --prefix web run dev
