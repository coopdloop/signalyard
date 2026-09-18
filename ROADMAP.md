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

## Phase 3 — Webhook Adapter + Classifier Agent

- [ ] `webhook_adapter_service` (Go/chi, :8084): `/webhooks/{github,jira,pagerduty,marble-jar}` with per-tool signature verification, envelope normalization, delivery tracking + retry endpoints
- [ ] `classifier_agent_service` (Python/FastAPI, :8083): consume quarantine stream, cluster similar payloads, LLM classification (Anthropic/OpenAI/Ollama backends), submit proposals to registry, classification-jobs/clusters/llm-backends endpoints

## Phase 4 — Human Surface + Production Infra

- [ ] React dashboard (per `frontend` in spec): approval queue review/diff, exec rollup, SOAR/dev-agent/PM views
- [ ] Keycloak OIDC realm, Grafana provisioning (datasources + dashboards)
- [ ] Helm chart for Kubernetes deployment
- [ ] OTel Collector wiring for OTLP forwarding + gateway self-instrumentation

## Cross-cutting / hardening (ongoing)

- [ ] OpenAPI spec generation published at `/openapi.json` (currently a stub URL in the manifest)
- [ ] Idempotency keys on `/v1/collect`; rate limiting per API key
- [ ] JWT (short-lived, per-agent) as alternative to long-lived API keys
- [ ] Approval audit-log surfacing (table exists in migration)
- [ ] Tracing propagation end-to-end (gateway → JetStream → normalizer)
