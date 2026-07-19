.PHONY: fmt test validate validate-v4 ingest eval compare compare-metadata compare-routing compare-rerank serve-embedding milvus-up milvus-down milvus-status milvus-seed query-milvus eval-milvus compare-milvus serve-lab web-dev

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

query-milvus:
	RAGLAB_VECTOR_BACKEND=milvus go run ./cmd/raglab query --pipeline v5-milvus-rerank --query "$${QUERY:-当前版本如何配置企业单点登录？}" --tenant "$${TENANT:-tenant_a}" --role "$${ROLE:-admin}"

eval-milvus:
	RAGLAB_VECTOR_BACKEND=milvus go run ./cmd/raglab eval --pipeline v5-milvus-rerank --split $${SPLIT:-development}

compare-milvus:
	RAGLAB_VECTOR_BACKEND=both go run ./cmd/raglab compare --baseline v5-ollama-rerank --candidate v5-milvus-rerank --split $${SPLIT:-development}

serve-lab:
	go run ./cmd/raglab serve-lab --model $${RAGLAB_OLLAMA_MODEL:-qwen3-embedding:4b-local}

web-dev:
	npm --prefix web run dev
