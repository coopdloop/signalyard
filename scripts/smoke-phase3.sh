#!/usr/bin/env bash
# Phase 3 E2E: webhook adapter + classifier agent against a running stack.
# Covers the product's signature loop with zero hand-written pipelines:
#   webhook -> normalized -> quarantined (unknown shape) -> classifier clusters
#   -> auto-proposal -> human approves -> replay -> queryable
set -euo pipefail

GW="${GATEWAY_URL:-http://localhost:8080}"
REG="${REGISTRY_URL:-http://localhost:8082}"
HOOK="${WEBHOOK_URL:-http://localhost:8084}"
CLS="${CLASSIFIER_URL:-http://localhost:8083}"
ADMIN="${DEV_ADMIN_TOKEN:-dev-admin-token}"
GH_SECRET="${GITHUB_WEBHOOK_SECRET:-dev-github-secret}"
MJ_SECRET="${MARBLE_JAR_WEBHOOK_SECRET:-dev-marble-jar-secret}"
fail() { echo "FAIL: $*" >&2; exit 1; }

sig() { python3 -c "import hmac,hashlib,sys;print(hmac.new(sys.argv[1].encode(),sys.argv[2].encode(),hashlib.sha256).hexdigest())" "$1" "$2"; }

# Reset pipeline state so reruns are deterministic (dev DB only).
if [ "${SMOKE_RESET:-1}" = "1" ] && docker ps --format '{{.Names}}' 2>/dev/null | grep -q signalyard-postgres; then
  docker exec signalyard-postgres-1 psql -U signalyard -q -c \
    "TRUNCATE events, quarantine_events, schema_proposals, approval_audit_log, classifier_runs, schemas, categories CASCADE" || true
  # Classifier keeps in-memory clusters; restart so stale event ids don't linger.
  if docker restart signalyard-classifier-1 >/dev/null 2>&1; then sleep 5; fi
fi

echo "== service health =="
curl -sf "$HOOK/health" | grep -q ok || fail "webhook health"
curl -sf "$CLS/health" | grep -q ok || fail "classifier health"

echo "== bad signature rejected =="
[ "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$HOOK/webhooks/github" -H 'Content-Type: application/json' -d '{"action":"opened"}')" = "401" ] || fail "unsigned github webhook should be 401"

echo "== github webhook accepted and tracked =="
GH_BODY='{"action":"opened","repository":{"full_name":"org/repo"},"sender":{"login":"ada"}}'
curl -sf -X POST "$HOOK/webhooks/github" \
  -H 'Content-Type: application/json' -H 'X-GitHub-Event: pull_request' \
  -H "X-Hub-Signature-256: sha256=$(sig "$GH_SECRET" "$GH_BODY")" \
  -d "$GH_BODY" | grep -q '"status":"accepted"' || fail "github webhook"
sleep 2
DELIVERY_ID=$(curl -sf "$HOOK/v1/webhook-deliveries" -H "Authorization: Bearer $ADMIN" \
  | python3 -c "import json,sys; ds=json.load(sys.stdin)['deliveries']; print(next(d['delivery_id'] for d in ds if d['tool']=='github'))")
[ -n "$DELIVERY_ID" ] || fail "github delivery not recorded"
curl -sf "$HOOK/v1/webhook-deliveries/$DELIVERY_ID" -H "Authorization: Bearer $ADMIN" | grep -q 'pull_request' || fail "get delivery"

echo "== retry on already-forwarded delivery is a 400 =="
[ "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$HOOK/v1/webhook-deliveries/$DELIVERY_ID/retry" -H "Authorization: Bearer $ADMIN")" = "400" ] || fail "retry forwarded should be 400"

echo "== marble-jar events (x3, same shape) flow to quarantine =="
for i in 1 2 3; do
  MJ_BODY="{\"jar\":\"jar-$i\",\"marbles\":$i,\"color\":\"blue\"}"
  curl -sf -X POST "$HOOK/webhooks/marble-jar" \
    -H 'Content-Type: application/json' \
    -H "X-SignalYard-Signature: sha256=$(sig "$MJ_SECRET" "$MJ_BODY")" \
    -d "$MJ_BODY" | grep -q accepted || fail "marble-jar webhook $i"
done
sleep 8  # normalizer quarantine + classifier consume + threshold auto-proposal

echo "== classifier clustered the unknown shape =="
curl -sf "$CLS/v1/clusters" -H "Authorization: Bearer $ADMIN" | grep -q 'marble_jar_event' || fail "no marble_jar_event cluster"
CLUSTER_ID=$(curl -sf "$CLS/v1/clusters" -H "Authorization: Bearer $ADMIN" \
  | python3 -c "import json,sys; cs=json.load(sys.stdin)['clusters']; print(next(c['cluster_id'] for c in cs if c['category_hint']=='marble_jar_event'))")
curl -sf "$CLS/v1/clusters/$CLUSTER_ID" -H "Authorization: Bearer $ADMIN" | grep -q 'representative_payload' || fail "get cluster"

echo "== auto-proposal reached the registry =="
PROPOSAL_ID=$(curl -sf "$REG/v1/proposals?status=pending" -H "Authorization: Bearer $ADMIN" \
  | python3 -c "import json,sys; ps=json.load(sys.stdin)['proposals']; print(next(p['proposal_id'] for p in ps if p['category']=='marble_jar_event'))")
[ -n "$PROPOSAL_ID" ] || fail "no auto-proposal for marble_jar_event"

echo "== llm-backends listed, heuristic active =="
curl -sf "$CLS/v1/llm-backends" -H "Authorization: Bearer $ADMIN" | grep -q '"heuristic"' || fail "llm-backends"

echo "== human approves -> replay -> queryable =="
curl -sf -X POST "$REG/v1/proposals/$PROPOSAL_ID/approve" \
  -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"reviewer_notes":"auto-proposal looks right"}' | grep -q '"status":"approved"' || fail "approve marble_jar_event"
sleep 3

AGENT_ID=$(curl -sf -X POST "$GW/register" -H 'Content-Type: application/json' \
  -d '{"agent_name":"phase3-agent","contact_email":"p3@example.com"}' | sed -n 's/.*"agent_id":"\([^"]*\)".*/\1/p')
API_KEY=$(curl -sf -X POST "$GW/v1/api-keys" -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d "{\"agent_id\":\"$AGENT_ID\"}" | sed -n 's/.*"api_key":"\([^"]*\)".*/\1/p')
curl -sf -X POST "$GW/mcp/tools/query_events" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d '{"category":"marble_jar_event"}' | grep -q 'jar-1' || fail "replayed marble-jar events not queryable"

echo "== manual classification job for github_event =="
GH_QID=$(curl -sf "http://localhost:8081/v1/quarantine" -H "Authorization: Bearer $ADMIN" \
  | python3 -c "import json,sys; evs=json.load(sys.stdin)['events']; print(next(e['event_id'] for e in evs if e['raw_payload'].get('category')=='github_event'))")
JOB_ID=$(curl -sf -X POST "$CLS/v1/classification-jobs" \
  -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d "{\"event_ids\":[\"$GH_QID\"]}" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')
[ -n "$JOB_ID" ] || fail "create job"
for i in $(seq 1 10); do
  sleep 1
  STATUS=$(curl -sf "$CLS/v1/classification-jobs/$JOB_ID" -H "Authorization: Bearer $ADMIN" | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')
  [ "$STATUS" = "completed" ] && break
  [ "$STATUS" = "failed" ] && fail "job failed"
done
[ "$STATUS" = "completed" ] || fail "job did not complete (status=$STATUS)"
GH_PROPOSAL=$(curl -sf "$REG/v1/proposals?status=pending" -H "Authorization: Bearer $ADMIN" \
  | python3 -c "import json,sys; ps=json.load(sys.stdin)['proposals']; print(next(p['proposal_id'] for p in ps if p['category']=='github_event'))")
curl -sf -X POST "$REG/v1/proposals/$GH_PROPOSAL/approve" \
  -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"reviewer_notes":"github schema ok"}' | grep -q approved || fail "approve github_event"
sleep 3
curl -sf -X POST "$GW/mcp/tools/query_events" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d '{"category":"github_event"}' | grep -q 'pull_request' || fail "github event not queryable after approval"

echo "== classifier metrics =="
curl -sf "$CLS/metrics" | grep -q 'signalyard_classifier' || fail "classifier metrics"

echo "PHASE 3 SMOKE OK"
