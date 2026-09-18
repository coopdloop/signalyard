# Signal Yard

> One dock. Every agent. Every signal finds its place.

A unified ingestion and observability hub connecting SOAR tooling, dev agents, PM systems, and human operators. Any agent can self-onboard and ship data immediately; unknown payload shapes are quarantined, classified by an AI agent, and proposed as schema patches for human review.

**North-star spec:** [`docs/product.json`](docs/product.json) (mirrored from [a-single-go-backend-service-api-that-is-spec](https://github.com/coopdloop/a-single-go-backend-service-api-that-is-spec/blob/main/product.json)). DB schema: [`docs/schema.sql`](docs/schema.sql) → [`migrations/0001_init.sql`](migrations/0001_init.sql). Build order: [`ROADMAP.md`](ROADMAP.md).

## Services (from spec)

| Service | Lang | Port | Status |
|---|---|---|---|
| `core_api_gateway` | Go (chi) | 8080 | ✅ Phase 1 implemented |
| `normalizer_router` | Go | 8081 | ✅ Phase 2 implemented |
| `schema_registry_service` | Go (chi) | 8082 | ✅ Phase 2 implemented |
| `classifier_agent_service` | Python (FastAPI) | 8083 | ⬜ Phase 3 |
| `webhook_adapter_service` | Go (chi) | 8084 | ⬜ Phase 3 |
| React dashboard + Keycloak/Grafana/Loki/Tempo/Mimir infra | — | — | ⬜ Phase 4 |

## Quickstart (dev)

```sh
make up    # builds and starts postgres + nats + core-api-gateway
```

End-to-end smoke test:

```sh
# 1. Agent self-onboards
curl -s localhost:8080/start-here-agents | jq
curl -s -X POST localhost:8080/register \
  -H 'Content-Type: application/json' \
  -d '{"agent_name":"my-agent","contact_email":"ops@example.com","category_hint":"dev_agent_trace"}'

# 2. Human approver issues an API key (dev shortcut: DEV_ADMIN_TOKEN)
curl -s -X POST localhost:8080/v1/api-keys \
  -H 'Authorization: Bearer dev-admin-token' \
  -H 'Content-Type: application/json' \
  -d '{"agent_id":"<agent_id from step 1>","scopes":["ingest"]}'

# 3. Agent ships events (HEC-style token auth)
curl -s -X POST localhost:8080/v1/collect \
  -H 'Authorization: SignalYard <api_key from step 2>' \
  -H 'Content-Type: application/json' \
  -d '{"category":"dev_agent_trace","timestamp":"2026-09-18T19:00:00Z","payload":{"tool_calls":3}}'

# 4. MCP discovery + tools
curl -s localhost:8080/mcp/manifest | jq
```

## The core loop (Phase 2)

```
agent → POST /v1/collect → JetStream ingest → normalizer validates vs registry schema
  ├─ valid   → routed per routing_yaml (postgres, loki; tempo/mimir via collector in Phase 4)
  └─ unknown/invalid → quarantine_events + quarantine stream
        → (Phase 3: classifier proposes schema) → human approves via /v1/proposals/{id}/approve
        → git commit + new schema version + NATS schema.approved → normalizer auto-replays
```

Smoke tests: `./scripts/smoke.sh` (Phase 1) and `./scripts/smoke-phase2.sh` (full loop above).

### schema_registry_service (:8082)

- Schemas/categories CRUD with versioning; every create/update/approve is a new version + git commit (ADR-003). Dev runs with `NoopGit` when `GIT_REPO_URL` is unset — Postgres stays the source of truth.
- Proposals queue: `POST /v1/proposals` (classifier or MCP-proxied), approve/reject with `reviewer_notes`, audit-logged. Approval emits `schema.approved` on the `schema_events` JetStream stream.
- Agent registry admin CRUD incl. API key metadata.
- Contract reconciliation: the spec marks `routing_yaml_diff`/`sample_event_ids` required on `POST /v1/proposals`, but the gateway's MCP `propose_schema` tool omits them; the registry treats them as optional so both producers work.

### normalizer_router (:8081)

- Consumes `events.ingest.>` (durable JetStream consumer), validates payloads with gojsonschema against registry schemas (30s TTL cache, last-known fallback per ADR-003).
- Routes per `routing_yaml` (`target:` or `targets:`): `postgres` (structured events table) and `loki` (push API) implemented; `tempo`/`mimir` deferred to the Phase 4 OTel Collector path (events stay durable in the stream).
- Quarantines unknown/invalid shapes to Postgres + the `quarantine` stream; subscribes `schema.approved` for automatic replay; manual replay via `/v1/quarantine/{id}/replay` and `/v1/replay/category/{category}`.
- `/v1/routing-stats` reports throughput, validation failure rate, quarantine rate.

## Configuration

Environment variables follow the spec exactly (`services[].environment_variables` in `docs/product.json`):

| Var | Required | Notes |
|---|---|---|
| `POSTGRES_DSN` | ✅ | Agent registry, API keys, users |
| `NATS_URL` | ✅ | JetStream; gateway ensures stream `ingest` (`events.>`) |
| `JWT_SIGNING_SECRET` | ✅ | Signs human session JWTs from `/login` |
| `HEC_TOKEN_SALT` | ✅ | HMAC salt for API key hashes (plaintext keys never stored) |
| `OIDC_ISSUER_URL` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | spec-required | `/login` returns 401 until configured (Keycloak in Phase 4) |
| `PORT` | | default `8080` |
| `OTEL_COLLECTOR_ENDPOINT` | | used when OTLP forwarding lands |
| `SCHEMA_REGISTRY_URL` | | Phase 2; enables `list_categories`/`propose_schema` proxying |
| `RUN_MIGRATIONS` | | `true` applies `migrations/0001_init.sql` at boot |
| `DEV_ADMIN_TOKEN` | | **dev only** — static bearer token for session-authed routes |

## Auth model (ADR-004)

- **Machines/agents:** HEC-style API keys — `Authorization: SignalYard sy_<prefix>_<secret>` (also accepts `Splunk`/`Bearer` schemes). Keys are HMAC-SHA256 hashed with `HEC_TOKEN_SALT` before storage.
- **Humans:** OIDC (Keycloak) → `POST /login` exchanges an ID token for an 8h session JWT used on management routes (`/v1/api-keys*`).

## Development

```sh
make build test vet   # or: go build ./... && go test ./...
make up down logs     # docker compose lifecycle
```
