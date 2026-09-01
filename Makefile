.PHONY: fmt test agent-test parser-test document-quality-export deploy-test deploy-init deploy-check deploy-up deploy-bootstrap deploy-verify deploy-status deploy-down validate validate-v4 medical-validate medical-eval medical-eval-all medical-compare medical-up medical-ocr-up medical-ocr-smoke medical-bootstrap medical-bootstrap-plan medical-smoke medical-source-audit medical-source-lock medical-public-build medical-dataset-audit medical-retrieval-eval medical-retrieval-local-qwen medical-retrieval-qwen ingest eval compare compare-metadata compare-routing compare-rerank eval-gate reliability-test dataset-eval dataset-eval-isolated answer-eval answer-eval-blind answer-eval-stream answer-eval-blind-stream answer-eval-blind-isolated enterprise-eval enterprise-eval-build serve-embedding milvus-up milvus-down milvus-status milvus-seed query-milvus eval-milvus compare-milvus postgres-up postgres-down postgres-status scale-10k scale-100k scale-bench serve-lab serve-lab-eval regression-smoke web-dev stack-up stack-smoke stack-down stack-status observability-up observability-down observability-status production-preflight

DOCKER_COMPOSE ?= $(shell if docker compose version >/dev/null 2>&1; then echo 'docker compose'; elif command -v docker-compose >/dev/null 2>&1; then echo 'docker-compose'; else echo 'docker compose'; fi)
RAGLAB_ENV_FILE ?= .env
STACK_ENV_FILE = $(if $(wildcard $(RAGLAB_ENV_FILE)),--env-file $(RAGLAB_ENV_FILE),)
STACK_COMPOSE = $(DOCKER_COMPOSE) $(STACK_ENV_FILE) -f deploy/stack/docker-compose.yml
OBSERVABILITY_COMPOSE = $(DOCKER_COMPOSE) -f deploy/observability/docker-compose.yml
WITH_ENV = RAGLAB_ENV_FILE=$(RAGLAB_ENV_FILE) ./scripts/run_with_env.sh

fmt:
	gofmt -w cmd internal

test:
	go test ./...

agent-test:
	cd services/agent-orchestrator && uv run --extra test pytest

parser-test:
	cd services/document-parser && uv run --extra test pytest

document-quality-export:
	$(WITH_ENV) sh -c 'uv run --project services/document-parser python scripts/export_document_quality_artifacts.py --output "$${OUTPUT:-../agent-evaluation/data/document-quality/artifacts-latest.json}" --max-runes "$${MAX_RUNES:-700}" --overlap-runes "$${OVERLAP_RUNES:-80}"'

deploy-test:
	python3 -m unittest discover -s scripts/tests -p 'test_deploy_init.py'

deploy-init:
	python3 scripts/deploy_init.py --output $(RAGLAB_ENV_FILE) --profile $${PROFILE:-remote} --host $${DEPLOY_HOST:-localhost}

deploy-check:
	$(WITH_ENV) ./scripts/deploy_preflight.sh

deploy-up: deploy-check
	$(WITH_ENV) $(STACK_COMPOSE) up -d --build

deploy-bootstrap:
	$(WITH_ENV) python3 scripts/medical_source_audit.py
	$(WITH_ENV) python3 scripts/medical_bootstrap.py --skip-derived --job-report tmp/medical-bootstrap/jobs-deploy-latest.json --source-revision $${MEDICAL_SOURCE_REVISION:-1}
	$(WITH_ENV) python3 scripts/wait_ingestion.py --job-report tmp/medical-bootstrap/jobs-deploy-latest.json

deploy-verify:
	$(WITH_ENV) ./scripts/stack_smoke.sh
	$(WITH_ENV) python3 scripts/medical_smoke.py

deploy-status:
	$(WITH_ENV) $(STACK_COMPOSE) ps

deploy-down:
	$(WITH_ENV) $(STACK_COMPOSE) down

validate:
	go run ./cmd/raglab validate

validate-v4:
	go run ./cmd/raglab validate --split v4-challenge

medical-validate:
	RAGLAB_DATASET_DOMAIN=medical-device go run ./cmd/raglab validate --split development

medical-eval:
	RAGLAB_DATASET_DOMAIN=medical-device go run ./cmd/raglab eval --pipeline $${PIPELINE:-v5-rerank} --split development

medical-eval-all:
	RAGLAB_DATASET_DOMAIN=medical-device go run ./cmd/raglab validate --split development
	RAGLAB_DATASET_DOMAIN=medical-device go run ./cmd/raglab validate --split regression
	RAGLAB_DATASET_DOMAIN=medical-device go run ./cmd/raglab eval --pipeline $${PIPELINE:-v5-rerank} --split development
	RAGLAB_DATASET_DOMAIN=medical-device go run ./cmd/raglab eval --pipeline $${PIPELINE:-v5-rerank} --split regression

medical-compare:
	RAGLAB_DATASET_DOMAIN=medical-device go run ./cmd/raglab compare --baseline v0-keyword --candidate v5-rerank --split development

medical-up: stack-up
	@echo "医疗设备销售顾问与运维 Agent: http://localhost:$${RAGLAB_WEB_PORT:-3000}/medical"

medical-ocr-up:
	RAGLAB_OCR_BACKEND_URL=http://paddle-ocr:8071 $(WITH_ENV) $(STACK_COMPOSE) --profile ocr up -d --build paddle-ocr parser api agent web

medical-ocr-smoke:
	cd services/document-parser && uv run python ../../scripts/ocr_smoke.py

medical-bootstrap:
	$(WITH_ENV) python3 scripts/medical_source_audit.py
	$(WITH_ENV) sh -c 'cd services/document-parser && uv run python ../../scripts/generate_medical_formats.py'
	$(WITH_ENV) python3 scripts/medical_bootstrap.py --plan --plan-report tmp/medical-bootstrap/import-plan-latest.json --source-revision $${MEDICAL_SOURCE_REVISION:-1}
	$(WITH_ENV) python3 scripts/medical_bootstrap.py --job-report tmp/medical-bootstrap/jobs-latest.json --source-revision $${MEDICAL_SOURCE_REVISION:-1}
	$(WITH_ENV) python3 scripts/wait_ingestion.py --job-report tmp/medical-bootstrap/jobs-latest.json

medical-bootstrap-plan:
	python3 scripts/medical_source_audit.py
	cd services/document-parser && uv run python ../../scripts/generate_medical_formats.py
	python3 scripts/medical_bootstrap.py --plan --plan-report tmp/medical-bootstrap/import-plan-latest.json --source-revision $${MEDICAL_SOURCE_REVISION:-1}

medical-smoke:
	$(WITH_ENV) python3 scripts/medical_smoke.py

medical-source-audit:
	python3 scripts/medical_source_audit.py --online --json-report tmp/medical-source-audit/latest.json --markdown-report tmp/medical-source-audit/latest.md

medical-source-lock:
	@test -n "$${REVIEWED_BY}" || (echo "REVIEWED_BY is required" && exit 1)
	python3 scripts/medical_source_audit.py --update-lock --reviewed-by "$${REVIEWED_BY}"

medical-public-build:
	python3 scripts/build_medical_public_corpus.py
	python3 scripts/medical_source_audit.py

medical-dataset-audit:
	python3 scripts/validate_medical_retrieval_dataset.py

medical-retrieval-eval:
	python3 scripts/run_medical_retrieval_eval.py

medical-retrieval-local-qwen:
	./scripts/run_medical_local_qwen_eval.sh

medical-retrieval-qwen:
	./scripts/run_with_env.sh python3 scripts/run_medical_retrieval_eval.py --provider

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
	$(WITH_ENV) $(STACK_COMPOSE) up -d --build

stack-smoke:
	$(WITH_ENV) ./scripts/stack_smoke.sh

stack-down:
	$(WITH_ENV) $(STACK_COMPOSE) down

stack-status:
	$(WITH_ENV) $(STACK_COMPOSE) ps

production-preflight:
	bash scripts/production_preflight.sh

observability-up:
	$(OBSERVABILITY_COMPOSE) up -d

observability-down:
	$(OBSERVABILITY_COMPOSE) down

observability-status:
	$(OBSERVABILITY_COMPOSE) ps
