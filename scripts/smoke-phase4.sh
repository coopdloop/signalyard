#!/usr/bin/env bash
# Phase 4 E2E: observability infra + dashboard against a running stack.
#   OTLP traces -> collector -> Tempo ; logs -> Loki ; metrics -> Mimir
#   Loki routing from normalizer ; Grafana provisioned ; dashboard served
set -euo pipefail

GW="${GATEWAY_URL:-http://localhost:8080}"
REG="${REGISTRY_URL:-http://localhost:8082}"
ADMIN="${DEV_ADMIN_TOKEN:-dev-admin-token}"
fail() { echo "FAIL: $*" >&2; exit 1; }

echo "== infra health =="
curl -sf http://localhost:3100/ready >/dev/null || fail "loki not ready"
curl -sf http://localhost:3200/ready >/dev/null 2>&1 || curl -sf http://localhost:3200/status/version >/dev/null || fail "tempo not ready"
curl -sf http://localhost:9009/ready >/dev/null || fail "mimir not ready"
curl -sf http://localhost:3000/api/health >/dev/null || fail "grafana not ready"

echo "== grafana datasources provisioned =="
curl -sf -u admin:admin http://localhost:3000/api/datasources | grep -q 'Loki' || fail "loki datasource"
curl -sf -u admin:admin http://localhost:3000/api/datasources | grep -q 'Tempo' || fail "tempo datasource"
curl -sf -u admin:admin http://localhost:3000/api/datasources | grep -q 'Mimir' || fail "mimir datasource"

echo "== grafana dashboard provisioned =="
curl -sf -u admin:admin http://localhost:3000/api/dashboards/uid/signalyard-exec | grep -q 'Exec Rollup' || fail "exec dashboard"

echo "== register agent + key =="
AGENT_ID=$(curl -sf -X POST "$GW/register" -H 'Content-Type: application/json' \
  -d '{"agent_name":"phase4-agent","contact_email":"p4@example.com"}' | sed -n 's/.*"agent_id":"\([^"]*\)".*/\1/p')
API_KEY=$(curl -sf -X POST "$GW/v1/api-keys" -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d "{\"agent_id\":\"$AGENT_ID\"}" | sed -n 's/.*"api_key":"\([^"]*\)".*/\1/p')
[ -n "$API_KEY" ] || fail "api key"

echo "== loki routing: schema with loki target =="
curl -sf -X POST "$REG/v1/schemas" -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"category":"ops_log","json_schema":{"type":"object","required":["message"],"properties":{"message":{"type":"string"}}},"routing_yaml":"targets: [postgres, loki]"}' \
  | grep -q ops_log || fail "create ops_log schema"
NOW_TS=$(python3 -c "from datetime import datetime,timezone;print(datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ'))")
curl -sf -X POST "$GW/v1/collect" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d "{\"category\":\"ops_log\",\"timestamp\":\"$NOW_TS\",\"payload\":{\"message\":\"phase4-loki-marker\"},\"source\":\"smoke\"}" \
  | grep -q accepted || fail "collect ops_log"
sleep 3
START_NS=$(python3 -c "import time;print(int((time.time()-3600)*1e9))")
curl -sf -G 'http://localhost:3100/loki/api/v1/query_range' \
  --data-urlencode 'query={category="ops_log"}' --data-urlencode "start=$START_NS" \
  | grep -q 'phase4-loki-marker' || fail "event not found in loki"

echo "== otlp traces: gateway -> collector -> tempo =="
TRACE_ID="4bf92f3577b34da6a3ce929d0e0e4736"
curl -sf -X POST "$GW/v1/otlp/traces" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d "{
    \"resourceSpans\": [{
      \"resource\": {\"attributes\": [{\"key\": \"service.name\", \"value\": {\"stringValue\": \"phase4-smoke\"}}]},
      \"scopeSpans\": [{
        \"scope\": {\"name\": \"smoke\"},
        \"spans\": [{
          \"traceId\": \"$TRACE_ID\",
          \"spanId\": \"00f067aa0ba902b7\",
          \"name\": \"phase4-span\",
          \"kind\": 1,
          \"startTimeUnixNano\": \"1726000000000000000\",
          \"endTimeUnixNano\": \"1726000001000000000\"
        }]
      }]
    }]
  }" | grep -q accepted || fail "otlp traces"
sleep 4
curl -sf "http://localhost:3200/api/traces/$TRACE_ID" | grep -q 'phase4-span' || fail "trace not found in tempo"

echo "== otlp metrics: gateway -> collector -> mimir =="
NOW_MS=$(python3 -c "import time;print(int(time.time()*1000))")
curl -sf -X POST "$GW/v1/otlp/metrics" -H "Authorization: SignalYard $API_KEY" -H 'Content-Type: application/json' \
  -d "{
    \"resourceMetrics\": [{
      \"resource\": {\"attributes\": [{\"key\": \"service.name\", \"value\": {\"stringValue\": \"phase4-smoke\"}}]},
      \"scopeMetrics\": [{
        \"metrics\": [{
          \"name\": \"signalyard_smoke_gauge\",
          \"gauge\": {\"dataPoints\": [{\"asDouble\": 42, \"timeUnixNano\": \"${NOW_MS}000000\"}]}
        }]
      }]
    }]
  }" | grep -q accepted || fail "otlp metrics"
sleep 4
curl -sf -G 'http://localhost:9009/prometheus/api/v1/query' --data-urlencode 'query=signalyard_smoke_gauge' \
  | grep -q 'signalyard_smoke_gauge' || fail "metric not found in mimir"

echo "== dashboard served =="
curl -sf http://localhost:8085/ | grep -q 'Signal Yard' || fail "dashboard index"
curl -sf http://localhost:8085/proposals | grep -q 'Signal Yard' || fail "dashboard SPA fallback"

echo "PHASE 4 SMOKE OK"
