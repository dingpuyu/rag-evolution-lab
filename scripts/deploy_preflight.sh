#!/usr/bin/env bash
set -euo pipefail

profile="${RAGLAB_DEPLOY_PROFILE:-remote}"
failures=0
fail() { printf 'FAIL: %s\n' "$*" >&2; failures=$((failures + 1)); }
ok() { printf 'OK: %s\n' "$*"; }

command -v docker >/dev/null 2>&1 || fail "Docker is not installed"
docker info >/dev/null 2>&1 || fail "Docker daemon is not reachable"
if docker compose version >/dev/null 2>&1 || command -v docker-compose >/dev/null 2>&1; then
  ok "Docker Compose is available"
else
  fail "Docker Compose V2 or docker-compose is required"
fi

if [[ ! -f "${RAGLAB_ENV_FILE:-.env}" ]]; then
  fail "deployment environment is missing; run make deploy-init"
else
  ok "private deployment environment exists"
fi

if [[ "$profile" == "remote" ]]; then
  [[ -n "${RAGLAB_EMBEDDING_API_KEY:-${QWEN_API_KEY:-${DASHSCOPE_API_KEY:-}}}" ]] \
    && ok "Qwen embedding credential is available in the process environment" \
    || fail "export QWEN_API_KEY (or RAGLAB_EMBEDDING_API_KEY) before remote deployment"
  [[ -n "${RAGLAB_GENERATION_API_KEY:-${DEEPSEEK_API_KEY:-}}" ]] \
    && ok "DeepSeek credential is available in the process environment" \
    || fail "export DEEPSEEK_API_KEY (or RAGLAB_GENERATION_API_KEY) before remote deployment"
else
  ok "offline deterministic model profile selected"
fi

if [[ -f "${RAGLAB_ENV_FILE:-.env}" ]] && grep -Eq '^(QWEN_API_KEY|DEEPSEEK_API_KEY|RAGLAB_(EMBEDDING|GENERATION|RERANK)_API_KEY)=.+' "${RAGLAB_ENV_FILE:-.env}"; then
  fail "model API keys must not be stored in the deployment .env file"
else
  ok "deployment file contains no model API key"
fi

if (( failures > 0 )); then
  printf 'portable_deploy_preflight=failed issues=%d\n' "$failures" >&2
  exit 1
fi
printf 'portable_deploy_preflight=passed profile=%s\n' "$profile"
