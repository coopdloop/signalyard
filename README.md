# Signal Yard

> One dock. Every agent. Every signal finds its place.

A unified ingestion and observability hub connecting SOAR tooling, dev agents, PM systems, and human operators. Any agent can self-onboard and ship data immediately; unknown payload shapes are quarantined, classified by an AI agent, and proposed as schema patches for human review.

**North-star spec:** [`docs/product.json`](docs/product.json) (mirrored from [a-single-go-backend-service-api-that-is-spec](https://github.com/coopdloop/a-single-go-backend-service-api-that-is-spec/blob/main/product.json)). DB schema: [`docs/schema.sql`](docs/schema.sql) → [`migrations/0001_init.sql`](migrations/0001_init.sql). Build order: [`ROADMAP.md`](ROADMAP.md).

## Services (from spec)

| Service | Lang | Port | Status |
|---|---|---|---|
| `core_api_gateway` | Go (chi) | 8080 | ✅ Phase 1 implemented |
| `normalizer_router` | Go | 8081 | ⬜ Phase 2 |
| `schema_registry_service` | Go (chi) | 8082 | ⬜ Phase 2 |
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
