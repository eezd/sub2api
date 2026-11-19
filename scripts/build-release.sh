#!/usr/bin/env bash
# Run in the isolated build container, never in the publisher environment.
set -euo pipefail
export GOPATH=/tmp/gopath GOCACHE=/tmp/go-cache HOME=/tmp/builder
mkdir -p "$HOME/source" /work/output
cp -R /work/source/. "$HOME/source/"
cd -- "$HOME/source"

pnpm --dir frontend install --frozen-lockfile
make test-frontend
make -C backend test-unit
(cd backend && golangci-lint run --timeout=30m ./...)
export SUB2API_TEST_POSTGRES_DSN='postgres://sub2api:fixture@postgres:5432/sub2api_test?sslmode=disable'
export SUB2API_TEST_REDIS_URL='redis://redis:6379/0'
export PROMPT_AUDIT_TEST_POSTGRES_DSN='postgres://sub2api:fixture@postgres:5432/sub2api_audit?sslmode=disable'
export PROMPT_AUDIT_TEST_REDIS_ADDR='redis:6379'
make -C backend test-integration
pnpm --dir frontend run build

bash /work/package.sh /work/output

/work/output/sub2api-linux-amd64 -version 2>&1 | grep -F "$VERSION" | grep -F "$SOURCE_SHA"
export DATA_DIR=$(mktemp -d)
export CONFIG_FILE=$DATA_DIR/config.yaml AUTO_SETUP=true SERVER_HOST=127.0.0.1 SERVER_PORT=18080
export DATABASE_HOST=postgres DATABASE_PORT=5432 DATABASE_USER=sub2api DATABASE_DBNAME=sub2api_smoke DATABASE_PASSWORD=fixture DATABASE_SSLMODE=disable
export REDIS_HOST=redis REDIS_PORT=6379 REDIS_DB=1 ADMIN_EMAIL=release@example.test ADMIN_PASSWORD=release-test-password
(
    cd -- "$DATA_DIR"
    exec /work/output/sub2api-linux-amd64
) >"$DATA_DIR/server.log" 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null || true; wait "$pid" || true' EXIT
if ! curl --noproxy '*' --retry 30 --retry-all-errors --retry-delay 1 --max-time 2 -fsS http://127.0.0.1:18080/health | jq -e '.status == "ok"'; then
    tail -n 25 "$DATA_DIR/server.log" >&2
    exit 1
fi
curl -fsS http://127.0.0.1:18080/setup/status | jq -e '.data.needs_setup == false'
curl -fsS http://127.0.0.1:18080/ | grep '<script type="module"' >/dev/null
curl -fsS http://127.0.0.1:18080/api/v1/auth/login -H 'Content-Type: application/json' \
    --data '{"email":"release@example.test","password":"release-test-password"}' | jq -e '.data.access_token | length > 0'
