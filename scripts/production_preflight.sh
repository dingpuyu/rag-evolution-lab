#!/usr/bin/env bash
set -euo pipefail

# Production gate for the single-node/Compose profile. It deliberately checks
# configuration, not application data. A failed gate is safer than starting a
# publicly reachable service in local-HS256/demo mode.

failures=0
warn() { printf 'WARN: %s\n' "$*" >&2; }
fail() { printf 'FAIL: %s\n' "$*" >&2; failures=$((failures + 1)); }
ok() { printf 'OK: %s\n' "$*"; }

if [[ "${RAGLAB_REQUIRE_OIDC:-false}" != "true" ]]; then
  fail "RAGLAB_REQUIRE_OIDC=true is required for a customer-facing deployment"
else
  ok "OIDC startup gate enabled"
fi

issuer="${RAGLAB_AUTH_OIDC_ISSUER:-}"
jwks="${RAGLAB_AUTH_JWKS_URL:-}"
if [[ -z "$issuer" && -z "$jwks" ]]; then
  fail "configure RAGLAB_AUTH_OIDC_ISSUER or RAGLAB_AUTH_JWKS_URL"
fi
if [[ -n "$issuer" && "$issuer" != https://* ]]; then
  fail "RAGLAB_AUTH_OIDC_ISSUER must use HTTPS"
fi
if [[ -n "$jwks" && "$jwks" != https://* ]]; then
  fail "RAGLAB_AUTH_JWKS_URL must use HTTPS"
fi

for name in RAGLAB_POSTGRES_PASSWORD RAGLAB_MINIO_ROOT_PASSWORD; do
  value="${!name:-}"
  if [[ -z "$value" || "$value" == *change-this* || "$value" == "raglab-local" || "$value" == "minioadmin" ]]; then
    fail "$name is empty or still uses a development default"
  fi
done

postgres_url="${RAGLAB_POSTGRES_URL:-postgres://raglab:<password>@postgres:5432/raglab?sslmode=disable}"
if [[ "$postgres_url" != *"sslmode=require"* && "$postgres_url" != *"sslmode=verify-"* ]]; then
  fail "RAGLAB_POSTGRES_URL must require TLS (sslmode=require or verify-full)"
fi

if [[ "${RAGLAB_RATE_LIMIT_BACKEND:-redis}" != "redis" ]]; then
  fail "RAGLAB_RATE_LIMIT_BACKEND=redis is required for multi-replica protection"
else
  ok "shared Redis limiter selected"
fi

if [[ -f deploy/stack/docker-compose.yml ]]; then
  compose_config=""
  if docker compose version >/dev/null 2>&1; then
    compose_config="$(docker compose -f deploy/stack/docker-compose.yml config 2>/dev/null || true)"
  elif command -v docker-compose >/dev/null 2>&1; then
    compose_config="$(docker-compose -f deploy/stack/docker-compose.yml config 2>/dev/null || true)"
  fi
  if [[ -n "$compose_config" ]]; then
    if ! grep -q 'host_ip: 127.0.0.1' <<<"$compose_config" || ! grep -q 'target: 19530' <<<"$compose_config"; then
      fail "Milvus host port is not loopback/private-only"
    else
      ok "Milvus host port is loopback-only"
    fi
  else
    warn "docker compose is unavailable; skipped rendered Compose network check"
  fi
fi

if (( failures > 0 )); then
  printf '\nProduction preflight failed with %d issue(s).\n' "$failures" >&2
  exit 1
fi
printf '\nProduction preflight passed. Continue with TLS, private networking, backups and a staged isolation test.\n'
