#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODEL_PATH="${RAGLAB_LOCAL_QWEN_GGUF:-$ROOT/../Qwen3-Embedding-4B-Q4_K_M.gguf}"
LLAMA_SERVER="${LLAMA_SERVER_BIN:-$(command -v llama-server || true)}"
PORT="${RAGLAB_LOCAL_EMBEDDING_PORT:-18081}"
LOG_PATH="${TMPDIR:-/tmp}/raglab-local-qwen-embedding.log"

if [[ -z "$LLAMA_SERVER" ]]; then
  echo "error: llama-server was not found; install llama.cpp or set LLAMA_SERVER_BIN" >&2
  exit 1
fi
if [[ ! -f "$MODEL_PATH" ]]; then
  echo "error: local GGUF was not found; set RAGLAB_LOCAL_QWEN_GGUF" >&2
  exit 1
fi
if curl -fsS "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then
  echo "error: port $PORT is already serving another process; choose RAGLAB_LOCAL_EMBEDDING_PORT" >&2
  exit 1
fi

"$LLAMA_SERVER" \
  -m "$MODEL_PATH" \
  --embedding --pooling last \
  --host 127.0.0.1 --port "$PORT" \
  -ngl 99 -c 8192 -b 512 -ub 512 \
  >"$LOG_PATH" 2>&1 &
SERVER_PID=$!
cleanup() {
  kill "$SERVER_PID" >/dev/null 2>&1 || true
  wait "$SERVER_PID" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

for _ in $(seq 1 120); do
  if curl -fsS "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$SERVER_PID" >/dev/null 2>&1; then
    echo "error: llama-server exited while loading; see $LOG_PATH" >&2
    exit 1
  fi
  sleep 1
done
if ! curl -fsS "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then
  echo "error: llama-server did not become healthy; see $LOG_PATH" >&2
  exit 1
fi

cd "$ROOT"
RAGLAB_EMBEDDING_BACKEND=openai-compatible \
RAGLAB_EMBEDDING_BASE_URL="http://127.0.0.1:$PORT/v1" \
RAGLAB_EMBEDDING_API_KEY=local-loopback-only \
RAGLAB_EMBEDDING_MODEL=Qwen3-Embedding-4B-Q4_K_M \
RAGLAB_EMBEDDING_DIMENSIONS=2560 \
RAGLAB_EMBEDDING_BATCH_SIZE=16 \
RAGLAB_RERANK_BACKEND=heuristic \
python3 scripts/run_medical_retrieval_eval.py --provider \
  --json-report eval/reports/medical-public-retrieval-local-qwen-latest.json \
  --markdown-report eval/reports/medical-public-retrieval-local-qwen-latest.md
