#!/usr/bin/env bash
set -euo pipefail

env_file="${RAGLAB_ENV_FILE:-.env}"
if [[ -f "$env_file" ]]; then
  set -a
  # Generated values contain only shell-safe URL/hex characters.
  # shellcheck disable=SC1090
  source "$env_file"
  set +a
fi

if command -v launchctl >/dev/null 2>&1; then
  [[ -n "${QWEN_API_KEY:-}" ]] || export QWEN_API_KEY="$(launchctl getenv QWEN_API_KEY 2>/dev/null || true)"
  [[ -n "${DEEPSEEK_API_KEY:-}" ]] || export DEEPSEEK_API_KEY="$(launchctl getenv DEEPSEEK_API_KEY 2>/dev/null || true)"
fi

exec "$@"
