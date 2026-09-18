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
| `classifier_agent_service` | Python (FastAPI) | 8083 | ✅ Phase 3 implemented |
| `webhook_adapter_service` | Go (chi) | 8084 | ✅ Phase 3 implemented |
| React dashboard | React (Vite/zustand) | 8085 | ✅ Phase 4 implemented |
| Grafana / Loki / Tempo / Mimir / OTel Collector | — | 3000/3100/3200/9009/4317-8 | ✅ Phase 4 implemented |
| Keycloak OIDC | — | 8180 | ✅ Phase 4 (compose `sso` profile) |
| Helm chart | — | — | ✅ `helm/signalyard` |

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

Smoke tests: `./scripts/smoke.sh` (Phase 1), `./scripts/smoke-phase2.sh` (registry/normalizer loop), `./scripts/smoke-phase3.sh` (webhooks + classifier: the full autonomous loop).

### schema_registry_service (:8082)

- Schemas/categories CRUD with versioning; every create/update/approve is a new version + git commit (ADR-003). Dev runs with `NoopGit` when `GIT_REPO_URL` is unset — Postgres stays the source of truth.
- Proposals queue: `POST /v1/proposals` (classifier or MCP-proxied), approve/reject with `reviewer_notes`, audit-logged. Approval emits `schema.approved` on the `schema_events` JetStream stream.
- Agent registry admin CRUD incl. API key metadata.
- Contract reconciliation: the spec marks `routing_yaml_diff`/`sample_event_ids` required on `POST /v1/proposals`, but the gateway's MCP `propose_schema` tool omits them; the registry treats them as optional so both producers work.

### webhook_adapter_service (:8084)

- Dedicated receivers for GitHub (`X-Hub-Signature-256`), PagerDuty (`X-PagerDuty-Signature`), Jira and marble-jar (`X-SignalYard-Signature` HMAC). Unset secrets fail closed.
- Normalizes each tool's payload into the common envelope and publishes to `events.ingest.<category>` — from there the standard validate/route/quarantine pipeline takes over, so new tools need no pipeline code.
- Deliveries tracked in Postgres (`webhook_sources`/`webhook_deliveries`); forward failures dead-letter to the `webhooks` stream; manual retry via `/v1/webhook-deliveries/{id}/retry`.
- Deviation: `POSTGRES_DSN` added to this service's env (the spec's db_schema owns the webhook tables but its env list omitted the DSN).

### classifier_agent_service (:8083, Python/FastAPI)

- Durable JetStream consumer on the `quarantine` stream; clusters payloads by shape fingerprint (category + sorted field types) to avoid duplicate proposals.
- At `CLUSTER_THRESHOLD` (default 3) same-shape events, auto-classifies and submits a schema proposal to the registry; manual batches via `POST /v1/classification-jobs`.
- Pluggable backends (`GET/PUT /v1/llm-backends`): `anthropic`, `openai`, `ollama`, plus `heuristic` (local schema inference, dev default — the full loop works with no LLM key). LLM calls go over httpx directly rather than langchain/instructor to keep the image lean; structured output via prompt + tolerant JSON parsing.
- LLM runs recorded in `classifier_runs` when `POSTGRES_DSN` is set.

### normalizer_router (:8081)

- Consumes `events.ingest.>` (durable JetStream consumer), validates payloads with gojsonschema against registry schemas (30s TTL cache, last-known fallback per ADR-003).
- Routes per `routing_yaml` (`target:` or `targets:`): `postgres` (structured events table) and `loki` (push API) implemented; `tempo`/`mimir` deferred to the Phase 4 OTel Collector path (events stay durable in the stream).
- Quarantines unknown/invalid shapes to Postgres + the `quarantine` stream; subscribes `schema.approved` for automatic replay; manual replay via `/v1/quarantine/{id}/replay` and `/v1/replay/category/{category}`.
- `/v1/routing-stats` reports throughput, validation failure rate, quarantine rate.

## Phase 4 — Human surface + observability backbone

- **Dashboard** (`dashboard/`, served on :8085): Login (Keycloak SSO via PKCE + dev-token fallback), exec rollup Overview, Approval Queue + Proposal Review (approve/reject with notes), Schema Registry, Agent Registry, human view of `/start-here-agents`, Settings. CORS is enabled on the Go APIs for browser access (dev-permissive; restrict at the proxy in prod).
- **Observability backbone** (ADR-005): OTel Collector receives OTLP from the gateway (`/v1/otlp/*` forwards to `:4318`) and fans out: traces → Tempo, logs → Loki, metrics → Mimir remote-write. The normalizer also routes `loki`-target categories straight to Loki's push API.
- **Grafana** (:3000, admin/admin): provisioned Loki/Tempo/Mimir/Postgres datasources + a "Signal Yard — Exec Rollup" dashboard.
- **Keycloak** (optional, `docker compose --profile sso up`, :8180): imports the `signalyard` realm with a public PKCE client (`signalyard-dashboard`), a confidential `signalyard-core` client, and dev users `approver`/`approver` and `viewer`/`viewer`. The gateway verifies ID tokens via JWKS discovery (`OIDC_ISSUER_URL`).
- **Helm** (`helm/signalyard`): all 5 services + dashboard + Postgres/NATS StatefulSets; `helm template`/`helm lint` clean.

Smoke tests: `./scripts/smoke.sh`, `smoke-phase2.sh`, `smoke-phase3.sh`, `smoke-phase4.sh` (infra + OTLP pipelines + dashboard).

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
