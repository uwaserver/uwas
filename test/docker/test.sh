#!/usr/bin/env bash
set -euo pipefail

# ─── Configuration ───────────────────────────────────────────────
UWAS_HOST="${UWAS_HOST:-uwas}"
UWAS_PORT="${UWAS_PORT:-80}"
ADMIN_HOST="${ADMIN_HOST:-uwas}"
ADMIN_PORT="${ADMIN_PORT:-9443}"
API_KEY="${API_KEY:-test-key-12345}"

ADMIN_URL="http://${ADMIN_HOST}:${ADMIN_PORT}"
HTTP_URL="http://${UWAS_HOST}:${UWAS_PORT}"

AUTH_HEADER="Authorization: Bearer ${API_KEY}"

# ─── Color output ────────────────────────────────────────────────
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color
BOLD='\033[1m'

# ─── Counters ────────────────────────────────────────────────────
PASS=0
FAIL=0
TOTAL=0

# ─── Test helpers ────────────────────────────────────────────────
pass() {
    PASS=$((PASS + 1))
    TOTAL=$((TOTAL + 1))
    printf "  ${GREEN}PASS${NC} %s\n" "$1"
}

fail() {
    FAIL=$((FAIL + 1))
    TOTAL=$((TOTAL + 1))
    printf "  ${RED}FAIL${NC} %s\n" "$1"
    if [ -n "${2:-}" ]; then
        printf "       ${RED}→ %s${NC}\n" "$2"
    fi
}

section() {
    printf "\n${CYAN}${BOLD}── %s ──${NC}\n" "$1"
}

# assert_status URL EXPECTED_STATUS DESCRIPTION [EXTRA_CURL_ARGS...]
assert_status() {
    local url="$1" expected="$2" desc="$3"
    shift 3
    local status
    status=$(curl -s -o /dev/null -w "%{http_code}" "$@" "$url" 2>/dev/null) || true
    if [ "$status" = "$expected" ]; then
        pass "$desc (HTTP $status)"
    else
        fail "$desc" "expected HTTP $expected, got $status"
    fi
}

# assert_json_field URL FIELD DESCRIPTION [EXTRA_CURL_ARGS...]
# Checks that a JSON field exists and is non-null in the response
assert_json_field() {
    local url="$1" field="$2" desc="$3"
    shift 3
    local body
    body=$(curl -s "$@" "$url" 2>/dev/null) || true
    local value
    value=$(echo "$body" | jq -r "$field" 2>/dev/null) || true
    if [ -n "$value" ] && [ "$value" != "null" ]; then
        pass "$desc ($field=$value)"
    else
        fail "$desc" "field $field missing or null in response: $(echo "$body" | head -c 200)"
    fi
}

# assert_json_array URL DESCRIPTION [EXTRA_CURL_ARGS...]
# Checks that the response is a JSON array
assert_json_array() {
    local url="$1" desc="$2"
    shift 2
    local body
    body=$(curl -s "$@" "$url" 2>/dev/null) || true
    local arr_type
    arr_type=$(echo "$body" | jq -r 'type' 2>/dev/null) || true
    if [ "$arr_type" = "array" ]; then
        local length
        length=$(echo "$body" | jq 'length' 2>/dev/null) || true
        pass "$desc (array, length=$length)"
    else
        fail "$desc" "expected JSON array, got $arr_type: $(echo "$body" | head -c 200)"
    fi
}

# assert_json_object URL DESCRIPTION [EXTRA_CURL_ARGS...]
# Checks that the response is a JSON object
assert_json_object() {
    local url="$1" desc="$2"
    shift 2
    local body
    body=$(curl -s "$@" "$url" 2>/dev/null) || true
    local obj_type
    obj_type=$(echo "$body" | jq -r 'type' 2>/dev/null) || true
    if [ "$obj_type" = "object" ]; then
        pass "$desc (JSON object)"
    else
        fail "$desc" "expected JSON object, got $obj_type: $(echo "$body" | head -c 200)"
    fi
}

# assert_header URL HEADER_NAME EXPECTED_VALUE DESCRIPTION [EXTRA_CURL_ARGS...]
assert_header() {
    local url="$1" header="$2" expected="$3" desc="$4"
    shift 4
    local headers
    headers=$(curl -s -D - -o /dev/null "$@" "$url" 2>/dev/null) || true
    local value
    # Case-insensitive header match, strip carriage returns
    value=$(echo "$headers" | grep -i "^${header}:" | head -1 | sed 's/^[^:]*: *//' | tr -d '\r\n') || true
    if [ -n "$value" ]; then
        if [ "$expected" = "*" ]; then
            pass "$desc ($header present: $value)"
        elif [ "$value" = "$expected" ]; then
            pass "$desc ($header=$value)"
        else
            fail "$desc" "$header expected '$expected', got '$value'"
        fi
    else
        fail "$desc" "header $header not found"
    fi
}

# ─── Wait for UWAS ──────────────────────────────────────────────
printf "${BOLD}Waiting for UWAS to be ready...${NC}\n"
MAX_WAIT=60
WAITED=0
while [ $WAITED -lt $MAX_WAIT ]; do
    if curl -s -o /dev/null -w "" "${ADMIN_URL}/api/v1/health" 2>/dev/null; then
        printf "${GREEN}UWAS is ready (waited ${WAITED}s)${NC}\n"
        break
    fi
    sleep 1
    WAITED=$((WAITED + 1))
done

if [ $WAITED -ge $MAX_WAIT ]; then
    printf "${RED}UWAS did not become ready within ${MAX_WAIT}s${NC}\n"
    exit 1
fi

# Give services a moment to fully initialize
sleep 2

# ═════════════════════════════════════════════════════════════════
# ADMIN API TESTS
# ═════════════════════════════════════════════════════════════════

section "Admin API — Health & System"

assert_status "${ADMIN_URL}/api/v1/health" "200" "GET /api/v1/health"
assert_json_field "${ADMIN_URL}/api/v1/health" ".status" "Health status=ok"

assert_status "${ADMIN_URL}/api/v1/system" "200" "GET /api/v1/system" -H "$AUTH_HEADER"
assert_json_field "${ADMIN_URL}/api/v1/system" ".version" "System version exists" -H "$AUTH_HEADER"
assert_json_field "${ADMIN_URL}/api/v1/system" ".go_version" "System go_version exists" -H "$AUTH_HEADER"

section "Admin API — Stats & Domains"

assert_status "${ADMIN_URL}/api/v1/stats" "200" "GET /api/v1/stats" -H "$AUTH_HEADER"
assert_json_field "${ADMIN_URL}/api/v1/stats" ".requests_total" "Stats requests_total exists" -H "$AUTH_HEADER"

assert_status "${ADMIN_URL}/api/v1/domains" "200" "GET /api/v1/domains" -H "$AUTH_HEADER"

# Check that 4 domains are returned
DOMAIN_COUNT=$(curl -s -H "$AUTH_HEADER" "${ADMIN_URL}/api/v1/domains" 2>/dev/null | jq 'length' 2>/dev/null) || true
if [ "$DOMAIN_COUNT" = "4" ]; then
    pass "Domains count = 4"
else
    fail "Domains count = 4" "expected 4, got $DOMAIN_COUNT"
fi

section "Admin API — Config & Metrics"

assert_status "${ADMIN_URL}/api/v1/config" "200" "GET /api/v1/config" -H "$AUTH_HEADER"
assert_json_field "${ADMIN_URL}/api/v1/config" ".global" "Config global settings exist" -H "$AUTH_HEADER"

assert_status "${ADMIN_URL}/api/v1/metrics" "200" "GET /api/v1/metrics" -H "$AUTH_HEADER"

# Verify Prometheus format (should contain a # HELP or # TYPE line, or metric lines)
METRICS_BODY=$(curl -s -H "$AUTH_HEADER" "${ADMIN_URL}/api/v1/metrics" 2>/dev/null) || true
if echo "$METRICS_BODY" | grep -qE '^(#|[a-z_]+)'; then
    pass "Metrics in Prometheus format"
else
    fail "Metrics in Prometheus format" "response does not look like Prometheus metrics"
fi

section "Admin API — Logs & Audit"

assert_status "${ADMIN_URL}/api/v1/logs" "200" "GET /api/v1/logs" -H "$AUTH_HEADER"
assert_json_array "${ADMIN_URL}/api/v1/logs" "Logs returns JSON array" -H "$AUTH_HEADER"

assert_status "${ADMIN_URL}/api/v1/audit" "200" "GET /api/v1/audit" -H "$AUTH_HEADER"
assert_json_array "${ADMIN_URL}/api/v1/audit" "Audit returns JSON array" -H "$AUTH_HEADER"

section "Admin API — Certs & Monitor"

assert_status "${ADMIN_URL}/api/v1/certs" "200" "GET /api/v1/certs" -H "$AUTH_HEADER"
assert_json_array "${ADMIN_URL}/api/v1/certs" "Certs returns JSON array" -H "$AUTH_HEADER"

assert_status "${ADMIN_URL}/api/v1/monitor" "200" "GET /api/v1/monitor" -H "$AUTH_HEADER"
assert_json_object "${ADMIN_URL}/api/v1/monitor" "Monitor returns JSON object" -H "$AUTH_HEADER"

section "Admin API — Domain CRUD"

# Create a new domain
CREATE_BODY='{"host":"new.test","type":"static","root":"/var/www/static"}'
CREATE_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
    -H "$AUTH_HEADER" -H "Content-Type: application/json" \
    -d "$CREATE_BODY" "${ADMIN_URL}/api/v1/domains" 2>/dev/null) || true
if [ "$CREATE_STATUS" = "200" ] || [ "$CREATE_STATUS" = "201" ]; then
    pass "POST /api/v1/domains — create domain (HTTP $CREATE_STATUS)"
else
    fail "POST /api/v1/domains — create domain" "expected HTTP 200 or 201, got $CREATE_STATUS"
fi

# Delete the domain we just created
DELETE_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE \
    -H "$AUTH_HEADER" "${ADMIN_URL}/api/v1/domains/new.test?confirm=true" 2>/dev/null) || true
if [ "$DELETE_STATUS" = "200" ] || [ "$DELETE_STATUS" = "204" ]; then
    pass "DELETE /api/v1/domains/new.test (HTTP $DELETE_STATUS)"
else
    fail "DELETE /api/v1/domains/new.test" "expected HTTP 200 or 204, got $DELETE_STATUS"
fi

section "Admin API — Reload & Cache"

# Reload config
assert_status "${ADMIN_URL}/api/v1/reload" "200" "POST /api/v1/reload" -X POST -H "$AUTH_HEADER"

# Cache purge
PURGE_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
    -H "$AUTH_HEADER" -H "Content-Type: application/json" \
    -d '{"pattern":"*"}' "${ADMIN_URL}/api/v1/cache/purge" 2>/dev/null) || true
if [ "$PURGE_STATUS" = "200" ] || [ "$PURGE_STATUS" = "204" ]; then
    pass "POST /api/v1/cache/purge (HTTP $PURGE_STATUS)"
else
    fail "POST /api/v1/cache/purge" "expected HTTP 200 or 204, got $PURGE_STATUS"
fi

# Cache stats
assert_status "${ADMIN_URL}/api/v1/cache/stats" "200" "GET /api/v1/cache/stats" -H "$AUTH_HEADER"
assert_json_object "${ADMIN_URL}/api/v1/cache/stats" "Cache stats returns JSON object" -H "$AUTH_HEADER"

section "Admin API — Backups"

assert_status "${ADMIN_URL}/api/v1/backups" "200" "GET /api/v1/backups" -H "$AUTH_HEADER"
assert_json_array "${ADMIN_URL}/api/v1/backups" "Backups returns JSON array" -H "$AUTH_HEADER"

assert_status "${ADMIN_URL}/api/v1/backups/schedule" "200" "GET /api/v1/backups/schedule" -H "$AUTH_HEADER"
assert_json_object "${ADMIN_URL}/api/v1/backups/schedule" "Backup schedule returns JSON object" -H "$AUTH_HEADER"


# ═════════════════════════════════════════════════════════════════
# STATIC FILE SERVING TESTS
# ═════════════════════════════════════════════════════════════════

section "Static File Serving"

assert_status "${HTTP_URL}/" "200" "GET http://static.test/ → 200" -H "Host: static.test"

# Verify we get actual HTML content
STATIC_BODY=$(curl -s -H "Host: static.test" "${HTTP_URL}/" 2>/dev/null) || true
if echo "$STATIC_BODY" | grep -q "UWAS Static File Serving"; then
    pass "Static index.html content correct"
else
    fail "Static index.html content correct" "expected 'UWAS Static File Serving' in body"
fi

assert_status "${HTTP_URL}/nonexistent" "404" "GET http://static.test/nonexistent → 404" -H "Host: static.test"

assert_status "${HTTP_URL}/style.css" "200" "GET http://static.test/style.css → 200" -H "Host: static.test"

# ETag / 304 Conditional Request
ETAG=$(curl -s -D - -o /dev/null -H "Host: static.test" "${HTTP_URL}/" 2>/dev/null \
    | grep -i "^etag:" | head -1 | sed 's/^[^:]*: *//' | tr -d '\r\n') || true
if [ -n "$ETAG" ]; then
    COND_STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
        -H "Host: static.test" -H "If-None-Match: ${ETAG}" \
        "${HTTP_URL}/" 2>/dev/null) || true
    if [ "$COND_STATUS" = "304" ]; then
        pass "ETag conditional request returns 304"
    else
        fail "ETag conditional request returns 304" "got HTTP $COND_STATUS (ETag=$ETAG)"
    fi
else
    fail "ETag conditional request returns 304" "no ETag header in initial response"
fi


# ═════════════════════════════════════════════════════════════════
# REDIRECT DOMAIN TESTS
# ═════════════════════════════════════════════════════════════════

section "Redirect Domain"

# Test redirect — curl should NOT follow redirects (-L not used)
REDIR_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: redirect.test" \
    "${HTTP_URL}/" 2>/dev/null) || true
if [ "$REDIR_STATUS" = "301" ]; then
    pass "GET http://redirect.test/ → 301"
else
    fail "GET http://redirect.test/ → 301" "got HTTP $REDIR_STATUS"
fi

# Verify Location header
REDIR_LOCATION=$(curl -s -D - -o /dev/null -H "Host: redirect.test" \
    "${HTTP_URL}/" 2>/dev/null \
    | grep -i "^location:" | head -1 | sed 's/^[^:]*: *//' | tr -d '\r\n') || true
if [ "$REDIR_LOCATION" = "https://example.com" ]; then
    pass "Redirect Location: https://example.com"
else
    fail "Redirect Location: https://example.com" "got Location: $REDIR_LOCATION"
fi


# ═════════════════════════════════════════════════════════════════
# SECURITY HEADERS TESTS
# ═════════════════════════════════════════════════════════════════

section "Security Headers"

assert_header "${HTTP_URL}/" "X-Content-Type-Options" "nosniff" \
    "X-Content-Type-Options: nosniff" -H "Host: static.test"

assert_header "${HTTP_URL}/" "X-Frame-Options" "SAMEORIGIN" \
    "X-Frame-Options: SAMEORIGIN" -H "Host: static.test"

assert_header "${HTTP_URL}/" "Server" "*" \
    "Server header present" -H "Host: static.test"

# Check Server header starts with UWAS
SERVER_HDR=$(curl -s -D - -o /dev/null -H "Host: static.test" "${HTTP_URL}/" 2>/dev/null \
    | grep -i "^server:" | head -1 | sed 's/^[^:]*: *//' | tr -d '\r\n') || true
if echo "$SERVER_HDR" | grep -qi "^UWAS"; then
    pass "Server header starts with UWAS ($SERVER_HDR)"
else
    fail "Server header starts with UWAS" "got Server: $SERVER_HDR"
fi

assert_header "${HTTP_URL}/" "X-Request-ID" "*" \
    "X-Request-ID header present" -H "Host: static.test"


# ═════════════════════════════════════════════════════════════════
# AUTHENTICATION TESTS
# ═════════════════════════════════════════════════════════════════

section "Authentication"

# Request without token → 401
assert_status "${ADMIN_URL}/api/v1/stats" "401" "No token → 401"

# Request with wrong token → 401
assert_status "${ADMIN_URL}/api/v1/stats" "401" "Wrong token → 401" \
    -H "Authorization: Bearer wrong-key"

# Request with correct token → 200
assert_status "${ADMIN_URL}/api/v1/stats" "200" "Correct token → 200" \
    -H "$AUTH_HEADER"

# Health endpoint should be public (no auth required)
assert_status "${ADMIN_URL}/api/v1/health" "200" "Health endpoint is public (no auth)"


# ═════════════════════════════════════════════════════════════════
# SUMMARY
# ═════════════════════════════════════════════════════════════════

printf "\n${BOLD}═══════════════════════════════════════════════${NC}\n"
if [ $FAIL -eq 0 ]; then
    printf "${GREEN}${BOLD}  ALL TESTS PASSED: ${PASS}/${TOTAL}${NC}\n"
else
    printf "${RED}${BOLD}  TESTS FAILED: ${PASS}/${TOTAL} passed, ${FAIL} failed${NC}\n"
fi
printf "${BOLD}═══════════════════════════════════════════════${NC}\n\n"

if [ $FAIL -gt 0 ]; then
    exit 1
fi

exit 0
