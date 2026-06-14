#!/bin/bash
set -euo pipefail

# Integration test script for ComposeCockpit
# Requirements: Docker daemon running, PostgreSQL available

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

echo "=== ComposeCockpit Integration Test ==="
echo ""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

pass() { echo -e "${GREEN}PASS${NC}: $1"; }
fail() { echo -e "${RED}FAIL${NC}: $1"; exit 1; }

# 1. Build
echo "--- Building ---"
cd "$PROJECT_ROOT"
make build || fail "Build failed"
pass "Build succeeded"

# 2. Start dependencies
echo ""
echo "--- Starting test dependencies ---"
docker compose -f deployments/docker-compose.yml up -d postgres
sleep 3

export DATABASE_URL="postgres://cockpit:cockpit_secret@localhost:5432/cockpit?sslmode=disable"
export JWT_SECRET="test-integration-secret-at-least-32-chars"
export COMPOSE_ROOT="$PROJECT_ROOT/tests/e2e/testdata"
export PORT=18080

# 3. Run migrations
echo ""
echo "--- Running migrations ---"
if command -v goose &> /dev/null; then
    goose -dir internal/store/migrations postgres "$DATABASE_URL" up || fail "Migration failed"
    pass "Migrations applied"
else
    echo "SKIP: goose not installed, skipping migration"
fi

# 4. Unit tests
echo ""
echo "--- Running unit tests ---"
go test -race -count=1 ./internal/auth/... ./internal/discovery/... ./internal/orchestrator/... || fail "Unit tests failed"
pass "Unit tests passed"

# 5. Start server in background
echo ""
echo "--- Starting server ---"
./bin/cockpit &
SERVER_PID=$!
sleep 2

cleanup() {
    echo ""
    echo "--- Cleanup ---"
    kill $SERVER_PID 2>/dev/null || true
    docker compose -f deployments/docker-compose.yml down -v 2>/dev/null || true
}
trap cleanup EXIT

# 6. Health check
echo ""
echo "--- Health checks ---"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:$PORT/health)
[ "$HTTP_CODE" = "200" ] || fail "Health check returned $HTTP_CODE"
pass "Liveness health check OK"

# 7. Register and login
echo ""
echo "--- Auth flow ---"
REGISTER_RESP=$(curl -s -X POST http://localhost:$PORT/api/v1/auth/register \
    -H "Content-Type: application/json" \
    -d '{"username":"admin","email":"admin@test.com","password":"testpass123"}')
echo "Register: $REGISTER_RESP"

LOGIN_RESP=$(curl -s -X POST http://localhost:$PORT/api/v1/auth/login \
    -H "Content-Type: application/json" \
    -d '{"username":"admin","password":"testpass123"}')
TOKEN=$(echo "$LOGIN_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('data',{}).get('access_token',''))" 2>/dev/null || echo "")

if [ -z "$TOKEN" ]; then
    echo "Login response: $LOGIN_RESP"
    fail "Failed to obtain token"
fi
pass "Auth flow: register + login OK"

# 8. Trigger scan
echo ""
echo "--- Project discovery ---"
SCAN_RESP=$(curl -s -X POST http://localhost:$PORT/api/v1/projects/scan \
    -H "Authorization: Bearer $TOKEN")
echo "Scan: $SCAN_RESP"
pass "Project scan triggered"

# 9. List projects
PROJECTS_RESP=$(curl -s http://localhost:$PORT/api/v1/projects \
    -H "Authorization: Bearer $TOKEN")
echo "Projects: $PROJECTS_RESP"
pass "Projects listed"

echo ""
echo "=== All integration tests passed ==="
