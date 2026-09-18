# Signal Yard Roadmap

Phased build-out of the spec in `docs/product.json`. Each phase is independently deployable and keeps the system coherent end-to-end.

## Phase 1 — Core API Gateway ✅ (this commit)

Single Go binary: public API + MCP server + auth + JetStream publishing.

- [x] Repo scaffold, spec vendored to `docs/product.json`, migration from spec DDL
- [x] `GET /health`, `GET /metrics` (Prometheus, per-service registry)
- [x] `GET /start-here-agents` (capability manifest, MCP manifest, key instructions, example payloads)
- [x] `POST /register` — agent self-registration (`pending` status)
- [x] `POST /login` — OIDC ID-token verify (JWKS via discovery) → 8h session JWT
- [x] `POST/GET/DELETE /v1/api-keys*` — issue/inspect/revoke, HMAC-hashed storage, session-auth'd
- [x] `POST /v1/collect` — envelope validation → JetStream `events.ingest.<category>`
- [x] `POST /v1/otlp/{traces,metrics,logs}` — raw OTLP passthrough → JetStream `events.otlp.*`
- [x] MCP: `POST /mcp/tools/{ingest_event,query_events,propose_schema,list_categories,get_manifest}` + `GET /mcp/manifest`
- [x] Dual auth (ADR-004): HEC-style machine tokens, OIDC→JWT human sessions, `DEV_ADMIN_TOKEN` dev escape hatch
- [x] docker-compose dev stack (postgres + nats + gateway), Dockerfile, Makefile
- [ ] Unit/integration coverage expansion (table-driven route contract tests vs spec)

## Phase 2 — Schema Registry + Normalizer/Router ✅

- [x] `schema_registry_service` (Go/chi, :8082): schemas + categories CRUD with versioning, proposals queue, approve/reject with git-backed double-write (go-git; NoopGit in dev), audit log, agent registry admin, `schema.approved` events to NATS
- [x] `normalizer_router` (Go, :8081): durable consume of `events.ingest.>`, gojsonschema validation with 30s cached registry lookups (last-known fallback), routing to Postgres/Loki per routing_yaml, quarantine to Postgres + `quarantine` stream, auto-replay on approval + manual replay endpoints, `/v1/routing-stats`
- [x] Gateway `SCHEMA_REGISTRY_URL` proxying live (`list_categories`, `propose_schema` with MCP→registry contract translation)
- [x] Migration ledger (`schema_migrations`) + `0002_quarantine_attempts.sql`
- [x] Full-loop E2E (`scripts/smoke-phase2.sh`): unknown → quarantine → proposal → approve → auto-replay → queryable
- [ ] Compose: add loki, tempo, mimir, grafana (moved to Phase 4 with dashboards)

## Phase 3 — Webhook Adapter + Classifier Agent ✅

- [x] `webhook_adapter_service` (Go/chi, :8084): `/webhooks/{github,jira,pagerduty,marble-jar}` with per-tool HMAC signature verification (fail-closed), envelope normalization into `events.ingest.<category>`, delivery tracking, dead-letter stream, retry endpoint
- [x] `classifier_agent_service` (Python/FastAPI, :8083): durable quarantine consumer, shape-fingerprint clustering, auto-proposal at threshold, manual classification jobs, pluggable LLM backends (anthropic/openai/ollama/heuristic) with runtime switching, `classifier_runs` recording, Prometheus metrics
- [x] Full autonomous loop E2E (`scripts/smoke-phase3.sh`): webhook → quarantine → cluster → auto-proposal → human approve → replay → queryable

## Phase 4 — Human Surface + Production Infra ✅

- [x] React dashboard (Vite/zustand/react-router): Login (PKCE SSO + dev token), exec rollup Overview, Approval Queue + Proposal Review, Schema Registry, Agent Registry, Onboarding view, Settings
- [x] Keycloak realm import (public PKCE dashboard client + confidential core client + dev users), gateway JWKS verification wired via compose
- [x] Grafana provisioning: Loki/Tempo/Mimir/Postgres datasources + exec rollup dashboard
- [x] OTel Collector: gateway `/v1/otlp/*` forwards → traces to Tempo, logs to Loki, metrics to Mimir remote-write
- [x] Normalizer Loki routing live in compose (`targets: [postgres, loki]`)
- [x] Helm chart (`helm/signalyard`): 5 services + dashboard + Postgres/NATS StatefulSets, lint/template clean
- [x] CORS on Go APIs for browser access (dev-permissive)
- [x] `scripts/smoke-phase4.sh`: infra health, datasource/dashboard provisioning, Loki routing, OTLP trace/metric round-trips, dashboard serving

## Cross-cutting / hardening (ongoing)

- [ ] OpenAPI spec generation published at `/openapi.json` (currently a stub URL in the manifest)
- [ ] Idempotency keys on `/v1/collect`; rate limiting per API key
- [ ] JWT (short-lived, per-agent) as alternative to long-lived API keys
- [ ] Approval audit-log surfacing (table exists in migration)
- [ ] Tracing propagation end-to-end (gateway → JetStream → normalizer)
