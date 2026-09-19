#!/usr/bin/env bash
# Live LLM validation: real Anthropic API key from ~/.signalyard-llm.env.
# Never echoes the key. Fails fast if the file is missing.
set -euo pipefail

ENV_FILE="$HOME/.signalyard-llm.env"
[ -f "$ENV_FILE" ] || { echo "FAIL: $ENV_FILE missing (needs ANTHROPIC_API_KEY or OPENAI_API_KEY)"; exit 1; }
# shellcheck source=/dev/null # operator-provided env file outside the repo
source "$ENV_FILE"
PROVIDER="${LLM_PROVIDER:-anthropic}"
KEY_VAR="${PROVIDER^^}_API_KEY"  # e.g. ANTHROPIC_API_KEY
KEY="${!KEY_VAR:-${ANTHROPIC_API_KEY:-}}"
[ -z "${ANTHROPIC_API_KEY:-}" ] && [ -z "${OPENAI_API_KEY:-}" ] && { echo "FAIL: no ANTHROPIC_API_KEY or OPENAI_API_KEY in $ENV_FILE"; exit 1; }

GW="${GATEWAY_URL:-http://localhost:8080}"
REG="${REGISTRY_URL:-http://localhost:8082}"
CLS="${CLASSIFIER_URL:-http://localhost:8083}"
ADMIN="${DEV_ADMIN_TOKEN:-dev-admin-token}"
CATEGORY="llm_eval_shape_$(date +%s)"
fail() { echo "FAIL: $*" >&2; exit 1; }

echo "== activate $PROVIDER backend =="
KEY="${ANTHROPIC_API_KEY:-${OPENAI_API_KEY:-}}"
curl -sf -X PUT "$CLS/v1/llm-backends/$PROVIDER" -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d "{\"active\": true, \"config\": {\"api_key\": \"$KEY\"}}" >/dev/null || fail "switch backend"

echo "== register agent + key =="
AGENT_ID=$(curl -sf -X POST "$GW/register" -H 'Content-Type: application/json' \
  -d '{"agent_name":"llm-smoke","contact_email":"llm@example.com"}' | sed -n 's/.*"agent_id":"\([^"]*\)".*/\1/p')
API_KEY=$(curl -sf -X POST "$GW/v1/api-keys" -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d "{\"agent_id\":\"$AGENT_ID\"}" | sed -n 's/.*"api_key":"\([^"]*\)".*/\1/p')

echo "== send 3 unknown-shape events ($CATEGORY) =="
NOW_TS=$(python3 -c "from datetime import datetime,timezone;print(datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ'))")
for i in 1 2 3; do
  curl -sf -X POST "$GW/v1/collect" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
    -d "{\"category\":\"$CATEGORY\",\"timestamp\":\"$NOW_TS\",\"payload\":{\"detection_name\":\"Detection-$i\",\"risk_score\":$((80+i)),\"host\":\"ws-0$i\"}}" >/dev/null || fail "collect $i"
done
sleep 20

echo "== waiting for registry proposal =="
PROPOSAL_ID=""
for i in $(seq 1 10); do
  PROPOSAL_ID=$(curl -s "$REG/v1/proposals?status=pending" -H "Authorization: Bearer $ADMIN" \
    | python3 -c "import json,sys; ps=[p for p in json.load(sys.stdin)['proposals'] if p['category']=='$CATEGORY']; print(ps[0]['proposal_id'] if ps else '')" 2>/dev/null || true)
  [ -n "$PROPOSAL_ID" ] && break
  sleep 3
done
[ -n "$PROPOSAL_ID" ] || fail "no proposal for $CATEGORY"
echo "  proposal $PROPOSAL_ID"

echo "== quality check: schema covers sample fields =="
curl -s "$REG/v1/proposals/$PROPOSAL_ID" -H "Authorization: Bearer $ADMIN" | python3 -c "
import json,sys
p=json.load(sys.stdin)
props=p['json_schema_patch'].get('properties',{})
for f in ('detection_name','risk_score','host'):
    assert f in props, f'missing field {f}'
assert 'claude' in p['generated_by'] or 'gpt' in p['generated_by'], 'not LLM-generated'
print('  fields covered:', sorted(props), '| generated_by:', p['generated_by'])" || fail "schema quality"

echo "== approve + replay + query =="
curl -sf -X POST "$REG/v1/proposals/$PROPOSAL_ID/approve" -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"reviewer_notes":"smoke-llm"}' >/dev/null || fail "approve"
sleep 5
curl -sf -X POST "$GW/mcp/tools/query_events" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d "{\"category\":\"$CATEGORY\"}" | grep -q 'detection_name' || fail "events not replayed"

echo "== restore heuristic backend =="
curl -sf -X PUT "$CLS/v1/llm-backends/heuristic" -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"active": true}' >/dev/null || fail "restore heuristic"

echo "LLM SMOKE OK ($PROVIDER)"
