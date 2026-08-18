#!/usr/bin/env bash
set -euo pipefail

api_url="${RAGLAB_API_URL:-http://127.0.0.1:${RAGLAB_API_PORT:-8080}}"
web_url="${RAGLAB_WEB_URL:-http://127.0.0.1:${RAGLAB_WEB_PORT:-3000}}"
email="${RAGLAB_STACK_SMOKE_EMAIL:-admin@raglab.local}"
password="${RAGLAB_STACK_SMOKE_PASSWORD:-${RAGLAB_PLATFORM_ADMIN_PASSWORD:-change-this-admin-password}}"
timeout_seconds="${RAGLAB_STACK_SMOKE_TIMEOUT_SECONDS:-240}"

wait_http() {
  local url="$1"
  local deadline=$((SECONDS + timeout_seconds))
  while (( SECONDS < deadline )); do
    if curl --fail --silent --show-error "$url" >/dev/null; then
      return 0
    fi
    sleep 2
  done
  echo "stack_smoke=failed url=$url reason=timeout" >&2
  return 1
}

wait_http "${api_url%/}/healthz"
wait_http "${web_url%/}/portal"

login_payload="$(python3 - "$email" "$password" <<'PY'
import json
import sys
print(json.dumps({"email": sys.argv[1], "password": sys.argv[2]}))
PY
)"
token="$(curl --fail --silent --show-error -X POST "${api_url%/}/api/v1/auth/login" \
  -H 'Content-Type: application/json' --data "$login_payload" | \
  python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')"

datasets="$(curl --fail --silent --show-error "${api_url%/}/api/v1/datasets" -H "Authorization: Bearer ${token}")"
apps="$(curl --fail --silent --show-error "${api_url%/}/api/v1/apps" -H "Authorization: Bearer ${token}")"
python3 - "$datasets" "$apps" <<'PY'
import json
import sys

datasets = json.loads(sys.argv[1]).get("datasets", [])
apps = json.loads(sys.argv[2]).get("applications", [])
if not datasets:
    raise SystemExit("stack_smoke=failed reason=no_datasets")
if not apps:
    raise SystemExit("stack_smoke=failed reason=no_applications")
print(f"datasets={len(datasets)} applications={len(apps)}")
PY

echo "stack_smoke=passed api=${api_url} web=${web_url}"
