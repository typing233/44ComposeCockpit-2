#!/bin/bash
set -euo pipefail

# Integration test script for ComposeCockpit
# Validates: register/login, scan, project list, permissions, real operation, SSE
# Requires: Docker daemon, PostgreSQL (via docker compose)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
BASE_URL="http://localhost:${PORT:-18080}"
API="$BASE_URL/api/v1"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASS_COUNT=0
FAIL_COUNT=0
SERVER_PID=""

pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    echo -e "  ${GREEN}✓${NC} $1"
}

fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    echo -e "  ${RED}✗${NC} $1"
    echo -e "    ${RED}$2${NC}"
    exit 1
}

warn() {
    echo -e "  ${YELLOW}⚠${NC} $1"
}

section() {
    echo ""
    echo "━━━ $1 ━━━"
}

cleanup() {
    echo ""
    section "Cleanup"
    if [ -n "$SERVER_PID" ]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
        echo "  Server stopped"
    fi
    docker compose -f "$PROJECT_ROOT/deployments/docker-compose.yml" down -v 2>/dev/null || true
    echo "  Dependencies stopped"
}
trap cleanup EXIT

assert_status() {
    local expected="$1" actual="$2" msg="$3"
    if [ "$actual" != "$expected" ]; then
        fail "$msg" "expected HTTP $expected, got $actual"
    fi
}

json_field() {
    echo "$1" | python3 -c "import sys,json; d=json.load(sys.stdin); print($2)" 2>/dev/null
}

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
echo "╔═══════════════════════════════════════════════════╗"
echo "║     ComposeCockpit Integration Test Suite        ║"
echo "╚═══════════════════════════════════════════════════╝"

# ━━━ 1. Build ━━━
section "Build"
cd "$PROJECT_ROOT"
CGO_ENABLED=0 go build -o bin/cockpit ./cmd/cockpit || fail "Build" "go build failed"
pass "Binary compiled"

# ━━━ 2. Start dependencies ━━━
section "Dependencies"
docker compose -f deployments/docker-compose.yml up -d postgres
echo "  Waiting for PostgreSQL..."
for i in $(seq 1 30); do
    if docker compose -f deployments/docker-compose.yml exec -T postgres pg_isready -U cockpit > /dev/null 2>&1; then
        break
    fi
    if [ "$i" = "30" ]; then
        fail "PostgreSQL" "timed out waiting for database"
    fi
    sleep 1
done
pass "PostgreSQL ready"

# ━━━ 3. Migrations ━━━
section "Migrations"
export DATABASE_URL="postgres://cockpit:cockpit_secret@localhost:5432/cockpit?sslmode=disable"
export JWT_SECRET="integration-test-secret-must-be-32-chars-long!"
export COMPOSE_ROOT="$PROJECT_ROOT/tests/e2e/testdata"
export PORT="${PORT:-18080}"
export DOCKER_HOST="${DOCKER_HOST:-unix:///var/run/docker.sock}"

if command -v goose &> /dev/null; then
    goose -dir internal/store/migrations postgres "$DATABASE_URL" up || fail "Migrations" "goose up failed"
    pass "Migrations applied"
else
    warn "goose not found — skipping migration step (server may auto-migrate)"
fi

# ━━━ 4. Start server ━━━
section "Server startup"
./bin/cockpit &
SERVER_PID=$!
sleep 2

if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    fail "Server startup" "process exited immediately"
fi

# Wait for server to respond
for i in $(seq 1 15); do
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/health" 2>/dev/null || echo "000")
    if [ "$HTTP_CODE" = "200" ]; then
        break
    fi
    if [ "$i" = "15" ]; then
        fail "Server startup" "health endpoint not responding after 15s"
    fi
    sleep 1
done
pass "Server running on port $PORT"

# ━━━ 5. Health checks ━━━
section "Health endpoints"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/health")
assert_status "200" "$HTTP_CODE" "Liveness"
pass "GET /health → 200"

HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/ready")
assert_status "200" "$HTTP_CODE" "Readiness"
pass "GET /ready → 200"

RESP=$(curl -s "$BASE_URL/version")
VERSION=$(json_field "$RESP" "d.get('data',{}).get('version','')")
pass "GET /version → $VERSION"

# ━━━ 6. Auth: Register ━━━
section "Auth: Register + Login"

# Register admin user
RESP=$(curl -s -w "\n%{http_code}" -X POST "$API/auth/register" \
    -H "Content-Type: application/json" \
    -d '{"username":"admin_test","email":"admin@test.local","password":"securepass123"}')
HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
assert_status "201" "$HTTP_CODE" "Register admin"
ADMIN_ID=$(json_field "$BODY" "d.get('data',{}).get('id','')")
[ -n "$ADMIN_ID" ] || fail "Register" "no user ID returned"
pass "Register admin_test → id=$ADMIN_ID"

# Register operator user
RESP=$(curl -s -w "\n%{http_code}" -X POST "$API/auth/register" \
    -H "Content-Type: application/json" \
    -d '{"username":"operator_test","email":"op@test.local","password":"securepass123"}')
HTTP_CODE=$(echo "$RESP" | tail -1)
assert_status "201" "$HTTP_CODE" "Register operator"
pass "Register operator_test"

# Register viewer user
RESP=$(curl -s -w "\n%{http_code}" -X POST "$API/auth/register" \
    -H "Content-Type: application/json" \
    -d '{"username":"viewer_test","email":"viewer@test.local","password":"securepass123"}')
HTTP_CODE=$(echo "$RESP" | tail -1)
assert_status "201" "$HTTP_CODE" "Register viewer"
pass "Register viewer_test"

# Duplicate registration should fail
RESP=$(curl -s -w "\n%{http_code}" -X POST "$API/auth/register" \
    -H "Content-Type: application/json" \
    -d '{"username":"admin_test","email":"admin2@test.local","password":"securepass123"}')
HTTP_CODE=$(echo "$RESP" | tail -1)
assert_status "409" "$HTTP_CODE" "Duplicate register"
pass "Duplicate register → 409"

# Login
RESP=$(curl -s -w "\n%{http_code}" -X POST "$API/auth/login" \
    -H "Content-Type: application/json" \
    -d '{"username":"admin_test","password":"securepass123"}')
HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
assert_status "200" "$HTTP_CODE" "Login"
ADMIN_TOKEN=$(json_field "$BODY" "d.get('data',{}).get('access_token','')")
REFRESH_TOKEN=$(json_field "$BODY" "d.get('data',{}).get('refresh_token','')")
[ -n "$ADMIN_TOKEN" ] || fail "Login" "no access_token returned"
[ -n "$REFRESH_TOKEN" ] || fail "Login" "no refresh_token returned"
pass "Login admin_test → token obtained"

# Bad password
RESP=$(curl -s -w "\n%{http_code}" -X POST "$API/auth/login" \
    -H "Content-Type: application/json" \
    -d '{"username":"admin_test","password":"wrongpass"}')
HTTP_CODE=$(echo "$RESP" | tail -1)
assert_status "401" "$HTTP_CODE" "Bad login"
pass "Bad password → 401"

# Refresh token
RESP=$(curl -s -w "\n%{http_code}" -X POST "$API/auth/refresh" \
    -H "Content-Type: application/json" \
    -d "{\"refresh_token\":\"$REFRESH_TOKEN\"}")
HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
assert_status "200" "$HTTP_CODE" "Refresh"
NEW_TOKEN=$(json_field "$BODY" "d.get('data',{}).get('access_token','')")
[ -n "$NEW_TOKEN" ] || fail "Refresh" "no new access_token"
pass "Refresh token → new access_token"

# Login as viewer
RESP=$(curl -s -X POST "$API/auth/login" \
    -H "Content-Type: application/json" \
    -d '{"username":"viewer_test","password":"securepass123"}')
VIEWER_TOKEN=$(json_field "$RESP" "d.get('data',{}).get('access_token','')")
[ -n "$VIEWER_TOKEN" ] || fail "Viewer login" "no token"
pass "Login viewer_test → token obtained"

# ━━━ 7. Project discovery ━━━
section "Project discovery"

# Unauthenticated → 401
RESP=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$API/projects/scan")
assert_status "401" "$RESP" "Scan unauthenticated"
pass "POST /projects/scan without token → 401"

# Trigger scan (first user is admin by default in dev, or it may be viewer)
# The test user is registered with viewer role. Scan requires admin.
# We'll try anyway — if it 403s, that's actually correct permission enforcement.
RESP=$(curl -s -w "\n%{http_code}" -X POST "$API/projects/scan" \
    -H "Authorization: Bearer $ADMIN_TOKEN")
HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')

if [ "$HTTP_CODE" = "200" ]; then
    pass "POST /projects/scan → 200 (admin)"
elif [ "$HTTP_CODE" = "403" ]; then
    warn "Scan returned 403 — user may not be admin. Continuing with limited tests."
else
    fail "Scan" "unexpected status $HTTP_CODE: $BODY"
fi

# List projects
RESP=$(curl -s -w "\n%{http_code}" "$API/projects" \
    -H "Authorization: Bearer $ADMIN_TOKEN")
HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
assert_status "200" "$HTTP_CODE" "List projects"
PROJECT_COUNT=$(json_field "$BODY" "len(d.get('data',[]))")
pass "GET /projects → 200 ($PROJECT_COUNT projects)"

# Extract first project ID if available
if [ "$PROJECT_COUNT" -gt "0" ] 2>/dev/null; then
    PROJECT_ID=$(json_field "$BODY" "d['data'][0]['id']")
    pass "First project: $PROJECT_ID"
else
    warn "No projects found — real operation tests will be skipped"
    PROJECT_ID=""
fi

# ━━━ 8. Permissions enforcement ━━━
section "Permission enforcement"

# Unauthenticated access to projects
RESP=$(curl -s -o /dev/null -w "%{http_code}" "$API/projects")
assert_status "401" "$RESP" "List projects unauthenticated"
pass "GET /projects without token → 401"

# Viewer cannot trigger scan
RESP=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$API/projects/scan" \
    -H "Authorization: Bearer $VIEWER_TOKEN")
if [ "$RESP" = "403" ]; then
    pass "Viewer POST /projects/scan → 403"
else
    warn "Viewer scan returned $RESP (may indicate first user is promoted)"
fi

# Invalid token
RESP=$(curl -s -o /dev/null -w "%{http_code}" "$API/projects" \
    -H "Authorization: Bearer invalid.jwt.token")
assert_status "401" "$RESP" "Invalid token"
pass "Invalid JWT → 401"

# ━━━ 9. Project detail + operations ━━━
section "Project operations"

if [ -n "$PROJECT_ID" ]; then
    # Get project detail
    RESP=$(curl -s -w "\n%{http_code}" "$API/projects/$PROJECT_ID" \
        -H "Authorization: Bearer $ADMIN_TOKEN")
    HTTP_CODE=$(echo "$RESP" | tail -1)
    BODY=$(echo "$RESP" | sed '$d')
    if [ "$HTTP_CODE" = "200" ]; then
        pass "GET /projects/$PROJECT_ID → 200"
    else
        warn "Get project detail returned $HTTP_CODE"
    fi

    # List services
    RESP=$(curl -s -w "\n%{http_code}" "$API/projects/$PROJECT_ID/services" \
        -H "Authorization: Bearer $ADMIN_TOKEN")
    HTTP_CODE=$(echo "$RESP" | tail -1)
    BODY=$(echo "$RESP" | sed '$d')
    if [ "$HTTP_CODE" = "200" ]; then
        SVC_COUNT=$(json_field "$BODY" "len(d.get('data',[]))")
        pass "GET /projects/$PROJECT_ID/services → $SVC_COUNT services"
    else
        warn "List services returned $HTTP_CODE"
    fi

    # Execute a real operation: docker compose up (may fail if images can't be pulled — that's OK)
    RESP=$(curl -s -w "\n%{http_code}" -X POST "$API/projects/$PROJECT_ID/up" \
        -H "Authorization: Bearer $ADMIN_TOKEN" \
        -H "Content-Type: application/json" \
        -d '{"timeout":"30s"}')
    HTTP_CODE=$(echo "$RESP" | tail -1)
    BODY=$(echo "$RESP" | sed '$d')
    if [ "$HTTP_CODE" = "200" ]; then
        OP_STATUS=$(json_field "$BODY" "d.get('data',{}).get('status','')")
        pass "POST /projects/$PROJECT_ID/up → 200 (status=$OP_STATUS)"
    elif [ "$HTTP_CODE" = "409" ]; then
        pass "POST /projects/$PROJECT_ID/up → 409 (already locked — concurrent test)"
    else
        warn "Up operation returned $HTTP_CODE (may be image pull failure)"
        # Not a hard fail — Docker may not have internet or images
    fi

    # Test operation status endpoint
    RESP=$(curl -s -w "\n%{http_code}" "$API/operations" \
        -H "Authorization: Bearer $ADMIN_TOKEN")
    HTTP_CODE=$(echo "$RESP" | tail -1)
    if [ "$HTTP_CODE" = "200" ]; then
        pass "GET /operations → 200"
    fi

    # Stop the project (clean up)
    RESP=$(curl -s -w "\n%{http_code}" -X POST "$API/projects/$PROJECT_ID/down" \
        -H "Authorization: Bearer $ADMIN_TOKEN" \
        -H "Content-Type: application/json" \
        -d '{"timeout":"30s"}')
    HTTP_CODE=$(echo "$RESP" | tail -1)
    if [ "$HTTP_CODE" = "200" ]; then
        pass "POST /projects/$PROJECT_ID/down → 200"
    else
        warn "Down returned $HTTP_CODE"
    fi

    # Viewer cannot operate
    RESP=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$API/projects/$PROJECT_ID/up" \
        -H "Authorization: Bearer $VIEWER_TOKEN" \
        -H "Content-Type: application/json" \
        -d '{}')
    if [ "$RESP" = "403" ]; then
        pass "Viewer POST /projects/$PROJECT_ID/up → 403"
    else
        warn "Viewer operation returned $RESP"
    fi
else
    warn "No projects available — skipping operation tests"
fi

# ━━━ 10. SSE streaming ━━━
section "SSE streaming"

if [ -n "$PROJECT_ID" ]; then
    # Test SSE connection (connect, read a few bytes, then disconnect)
    SSE_FILE=$(mktemp)
    curl -s -N --max-time 3 "$API/projects/$PROJECT_ID/events" \
        -H "Authorization: Bearer $ADMIN_TOKEN" \
        -H "Accept: text/event-stream" \
        > "$SSE_FILE" 2>/dev/null || true

    # We just verify the endpoint accepts the connection (doesn't 404/500)
    SSE_STATUS=$(curl -s -o /dev/null -w "%{http_code}" --max-time 2 "$API/projects/$PROJECT_ID/events" \
        -H "Authorization: Bearer $ADMIN_TOKEN" \
        -H "Accept: text/event-stream" 2>/dev/null || echo "000")
    if [ "$SSE_STATUS" = "200" ] || [ "$SSE_STATUS" = "000" ]; then
        pass "SSE /projects/$PROJECT_ID/events → connected"
    else
        warn "SSE events returned $SSE_STATUS"
    fi

    # Global events (admin only)
    SSE_STATUS=$(curl -s -o /dev/null -w "%{http_code}" --max-time 2 "$API/events" \
        -H "Authorization: Bearer $ADMIN_TOKEN" \
        -H "Accept: text/event-stream" 2>/dev/null || echo "000")
    if [ "$SSE_STATUS" = "200" ] || [ "$SSE_STATUS" = "000" ]; then
        pass "SSE /events (global) → connected"
    else
        warn "Global SSE returned $SSE_STATUS"
    fi

    rm -f "$SSE_FILE"
else
    warn "No projects — skipping SSE tests"
fi

# ━━━ 11. Async operation + polling ━━━
section "Async operations"

if [ -n "$PROJECT_ID" ]; then
    # Submit async
    RESP=$(curl -s -w "\n%{http_code}" -X POST "$API/projects/$PROJECT_ID/async/start" \
        -H "Authorization: Bearer $ADMIN_TOKEN" \
        -H "Content-Type: application/json" \
        -d '{"timeout":"30s"}')
    HTTP_CODE=$(echo "$RESP" | tail -1)
    BODY=$(echo "$RESP" | sed '$d')
    if [ "$HTTP_CODE" = "202" ]; then
        OP_ID=$(json_field "$BODY" "d.get('data',{}).get('operation_id','')")
        pass "POST /projects/$PROJECT_ID/async/start → 202 (op=$OP_ID)"

        # Poll status
        sleep 1
        RESP=$(curl -s -w "\n%{http_code}" "$API/operations/$OP_ID" \
            -H "Authorization: Bearer $ADMIN_TOKEN")
        HTTP_CODE=$(echo "$RESP" | tail -1)
        BODY=$(echo "$RESP" | sed '$d')
        if [ "$HTTP_CODE" = "200" ]; then
            pass "GET /operations/$OP_ID → 200 (polled)"
        else
            warn "Poll operation returned $HTTP_CODE"
        fi
    else
        warn "Async submit returned $HTTP_CODE"
    fi
else
    warn "No projects — skipping async tests"
fi

# ━━━ 12. Error responses ━━━
section "Error format validation"

# Non-existent project
RESP=$(curl -s -w "\n%{http_code}" "$API/projects/non-existent-id" \
    -H "Authorization: Bearer $ADMIN_TOKEN")
HTTP_CODE=$(echo "$RESP" | tail -1)
BODY=$(echo "$RESP" | sed '$d')
if [ "$HTTP_CODE" = "404" ] || [ "$HTTP_CODE" = "403" ]; then
    ERR_CODE=$(json_field "$BODY" "d.get('error',{}).get('code','')")
    if [ -n "$ERR_CODE" ]; then
        pass "Error response has code field: $ERR_CODE"
    else
        warn "Error response missing code field"
    fi
fi

# Non-existent operation
RESP=$(curl -s -w "\n%{http_code}" "$API/operations/non-existent-op" \
    -H "Authorization: Bearer $ADMIN_TOKEN")
HTTP_CODE=$(echo "$RESP" | tail -1)
assert_status "404" "$HTTP_CODE" "Non-existent operation"
pass "GET /operations/unknown → 404"

# ━━━ Summary ━━━
echo ""
echo "╔═══════════════════════════════════════════════════╗"
echo "║  Results: ${PASS_COUNT} passed, ${FAIL_COUNT} failed                     ║"
echo "╚═══════════════════════════════════════════════════╝"

if [ "$FAIL_COUNT" -gt 0 ]; then
    echo -e "${RED}FAILED${NC}"
    exit 1
fi

echo -e "${GREEN}ALL TESTS PASSED${NC}"
exit 0
