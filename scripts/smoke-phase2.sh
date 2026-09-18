#!/usr/bin/env bash
# Phase 2 E2E: schema registry + normalizer/router against a running stack.
# Exercises the product's core loop:
#   known event -> routed to postgres -> queryable
#   unknown event -> quarantined -> proposal -> approve -> auto-replay -> queryable
set -euo pipefail

GW="${GATEWAY_URL:-http://localhost:8080}"
REG="${REGISTRY_URL:-http://localhost:8082}"
NORM="${NORMALIZER_URL:-http://localhost:8081}"
ADMIN="${DEV_ADMIN_TOKEN:-dev-admin-token}"
fail() { echo "FAIL: $*" >&2; exit 1; }

echo "== service health =="
curl -sf "$REG/health" | grep -q ok || fail "registry health"
curl -sf "$NORM/health" | grep -q ok || fail "normalizer health"

echo "== create schema for soar_alert (admin) =="
curl -sf -X POST "$REG/v1/schemas" \
  -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{
    "category": "soar_alert",
    "json_schema": {
      "type": "object",
      "required": ["alert_id", "severity"],
      "properties": {
        "alert_id": {"type": "string"},
        "severity": {"type": "string", "enum": ["low", "medium", "high"]}
      }
    },
    "routing_yaml": "target: postgres"
  }' | grep -q '"category":"soar_alert"' || fail "create schema"

echo "== schema lookups =="
curl -sf "$REG/v1/schemas/soar_alert" -H "Authorization: Bearer $ADMIN" | grep -q '"version"' || fail "get schema"
curl -sf "$REG/v1/schemas/soar_alert/versions" -H "Authorization: Bearer $ADMIN" | grep -q 'soar_alert' || fail "versions"
curl -sf "$REG/v1/categories" -H "Authorization: Bearer $ADMIN" | grep -q 'soar_alert' || fail "categories"

echo "== register agent + key =="
AGENT_ID=$(curl -sf -X POST "$GW/register" -H 'Content-Type: application/json' \
  -d '{"agent_name":"phase2-agent","contact_email":"p2@example.com"}' | sed -n 's/.*"agent_id":"\([^"]*\)".*/\1/p')
[ -n "$AGENT_ID" ] || fail "register"
API_KEY=$(curl -sf -X POST "$GW/v1/api-keys" -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d "{\"agent_id\":\"$AGENT_ID\"}" | sed -n 's/.*"api_key":"\([^"]*\)".*/\1/p')
[ -n "$API_KEY" ] || fail "api key"

echo "== known-shape event routes to postgres =="
curl -sf -X POST "$GW/v1/collect" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d '{"category":"soar_alert","timestamp":"2026-09-18T20:00:00Z","payload":{"alert_id":"ALT-1","severity":"high"},"source":"smoke"}' \
  | grep -q accepted || fail "collect known"
sleep 2
curl -sf -X POST "$GW/mcp/tools/query_events" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d '{"category":"soar_alert"}' | grep -q 'ALT-1' || fail "query_events did not return routed event"

echo "== schema-violating event is quarantined =="
curl -sf -X POST "$GW/v1/collect" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d '{"category":"soar_alert","timestamp":"2026-09-18T20:01:00Z","payload":{"alert_id":"ALT-2","severity":"bogus"}}' \
  | grep -q accepted || fail "collect invalid"
sleep 2
curl -sf "$NORM/v1/quarantine" -H "Authorization: Bearer $ADMIN" | grep -q 'schema_validation_failed' || fail "invalid event not quarantined"

echo "== unknown-shape event is quarantined =="
curl -sf -X POST "$GW/v1/collect" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d '{"category":"mystery_shape","timestamp":"2026-09-18T20:02:00Z","payload":{"weird_field":"value-1"}}' \
  | grep -q accepted || fail "collect unknown"
sleep 2
QEVENT_ID=$(curl -sf "$NORM/v1/quarantine" -H "Authorization: Bearer $ADMIN" \
  | python3 -c "import json,sys; evs=json.load(sys.stdin)['events']; print(next(e['event_id'] for e in evs if e['raw_payload'].get('category')=='mystery_shape'))")
[ -n "$QEVENT_ID" ] || fail "unknown event not in quarantine"
curl -sf "$NORM/v1/quarantine/$QEVENT_ID" -H "Authorization: Bearer $ADMIN" | grep -q 'weird_field' || fail "get quarantined event"

echo "== proposal submitted (as classifier would) =="
PROPOSAL_ID=$(curl -sf -X POST "$REG/v1/proposals" \
  -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d "{
    \"category\": \"mystery_shape\",
    \"generated_by\": \"classifier-agent-smoke\",
    \"json_schema_patch\": {
      \"type\": \"object\",
      \"required\": [\"weird_field\"],
      \"properties\": {\"weird_field\": {\"type\": \"string\"}}
    },
    \"routing_yaml_diff\": \"target: postgres\",
    \"sample_event_ids\": [\"$QEVENT_ID\"]
  }" | sed -n 's/.*"proposal_id":"\([^"]*\)".*/\1/p')
[ -n "$PROPOSAL_ID" ] || fail "create proposal"
curl -sf "$REG/v1/proposals/$PROPOSAL_ID" -H "Authorization: Bearer $ADMIN" | grep -q '"status":"pending"' || fail "get proposal"

echo "== human approves -> git commit + registry update + replay trigger =="
curl -sf -X POST "$REG/v1/proposals/$PROPOSAL_ID/approve" \
  -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"reviewer_notes":"looks right"}' | grep -q '"status":"approved"' || fail "approve"
sleep 3

echo "== quarantined event auto-replayed and queryable =="
PENDING=$(curl -sf "$NORM/v1/quarantine" -H "Authorization: Bearer $ADMIN" \
  | python3 -c "import json,sys; evs=[e for e in json.load(sys.stdin)['events'] if e['raw_payload'].get('category')=='mystery_shape']; print(len(evs))")
[ "$PENDING" = "0" ] || fail "mystery_shape still pending ($PENDING)"
curl -sf -X POST "$GW/mcp/tools/query_events" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d '{"category":"mystery_shape"}' | grep -q 'weird_field' || fail "replayed event not queryable"

echo "== gateway MCP proxies against live registry =="
curl -sf -X POST "$GW/mcp/tools/list_categories" -H "Authorization: SignalYard $API_KEY" | grep -q 'soar_alert' || fail "list_categories proxy"
curl -sf -X POST "$GW/mcp/tools/propose_schema" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d '{"category":"agent_metric","json_schema":{"type":"object"},"routing_yaml_diff":"target: postgres"}' \
  | grep -q '"status":"pending"' || fail "propose_schema proxy"

echo "== routing stats =="
curl -sf "$NORM/v1/routing-stats" -H "Authorization: Bearer $ADMIN" | grep -q 'quarantine_rate' || fail "routing-stats"

echo "== agent registry endpoints =="
curl -sf "$REG/v1/agents" -H "Authorization: Bearer $ADMIN" | grep -q 'phase2-agent' || fail "list agents"
curl -sf "$REG/v1/agents/$AGENT_ID" -H "Authorization: Bearer $ADMIN" | grep -q 'api_key_metadata' || fail "get agent"

echo "PHASE 2 SMOKE OK"
