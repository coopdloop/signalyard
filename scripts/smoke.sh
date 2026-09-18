#!/usr/bin/env bash
# End-to-end smoke test against a running local stack (make up).
set -euo pipefail

BASE="${BASE_URL:-http://localhost:8080}"
ADMIN="${DEV_ADMIN_TOKEN:-dev-admin-token}"
fail() { echo "FAIL: $*" >&2; exit 1; }

echo "== health =="
curl -sf "$BASE/health" | grep -q '"status":"ok"' || fail "/health"

echo "== start-here-agents =="
curl -sf "$BASE/start-here-agents" | grep -q 'capability_manifest' || fail "/start-here-agents"

echo "== mcp manifest =="
curl -sf "$BASE/mcp/manifest" | grep -q 'ingest_event' || fail "/mcp/manifest"

echo "== register agent =="
REG=$(curl -sf -X POST "$BASE/register" -H 'Content-Type: application/json' \
  -d '{"agent_name":"smoke-agent","contact_email":"smoke@example.com","category_hint":"dev_agent_trace"}')
AGENT_ID=$(echo "$REG" | sed -n 's/.*"agent_id":"\([^"]*\)".*/\1/p')
[ -n "$AGENT_ID" ] || fail "register: $REG"
echo "  agent_id=$AGENT_ID"

echo "== issue api key (dev admin token) =="
KEY=$(curl -sf -X POST "$BASE/v1/api-keys" \
  -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d "{\"agent_id\":\"$AGENT_ID\",\"scopes\":[\"ingest\"]}")
API_KEY=$(echo "$KEY" | sed -n 's/.*"api_key":"\([^"]*\)".*/\1/p')
KEY_ID=$(echo "$KEY" | sed -n 's/.*"key_id":"\([^"]*\)".*/\1/p')
[ -n "$API_KEY" ] || fail "api-keys: $KEY"

echo "== key metadata =="
curl -sf "$BASE/v1/api-keys/$KEY_ID" -H "Authorization: Bearer $ADMIN" | grep -q "\"agent_id\":\"$AGENT_ID\"" || fail "get key"

echo "== collect requires auth =="
[ "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/v1/collect" -H 'Content-Type: application/json' -d '{}')" = "401" ] || fail "collect without token should be 401"

echo "== collect event =="
COLLECT=$(curl -sf -X POST "$BASE/v1/collect" \
  -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d '{"category":"dev_agent_trace","timestamp":"2026-09-18T19:00:00Z","payload":{"tool_calls":3},"source":"smoke"}')
echo "$COLLECT" | grep -q '"status":"accepted"' || fail "collect: $COLLECT"

echo "== mcp ingest_event =="
curl -sf -X POST "$BASE/mcp/tools/ingest_event" \
  -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d '{"category":"soar_alert","timestamp":"2026-09-18T19:05:00Z","payload":{"severity":"high"}}' \
  | grep -q '"status":"accepted"' || fail "mcp ingest_event"

echo "== otlp traces =="
curl -sf -X POST "$BASE/v1/otlp/traces" \
  -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d '{"resourceSpans":[]}' | grep -q '"status":"accepted"' || fail "otlp traces"

echo "== list_categories (empty until Phase 2) =="
curl -sf -X POST "$BASE/mcp/tools/list_categories" -H "Authorization: SignalYard $API_KEY" | grep -q 'categories' || fail "list_categories"

echo "== revoke key =="
curl -sf -X DELETE "$BASE/v1/api-keys/$KEY_ID" -H "Authorization: Bearer $ADMIN" | grep -q '"revoked":true' || fail "revoke"

echo "== revoked key rejected =="
[ "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/v1/collect" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' -d '{"category":"x","timestamp":"2026-09-18T19:00:00Z","payload":{}}')" = "401" ] || fail "revoked key should be 401"

echo "== metrics =="
curl -sf "$BASE/metrics" | grep -q 'signalyard_gateway_ingested_events_total' || fail "/metrics"

echo "SMOKE OK"
