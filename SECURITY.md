# Security Policy

## Reporting a vulnerability

Please **do not** open a public issue for security problems.

Report privately via [GitHub Security Advisories](https://github.com/coopdloop/signalyard/security/advisories/new),
or email **coopdevsec@proton.me**.

Include: affected component, version/commit, reproduction steps, and impact.
Expect an acknowledgement within 3 business days and a status update within 10.

## Scope

In scope: the Go services (`core_api_gateway`, `normalizer_router`,
`schema_registry_service`, `webhook_adapter_service`), the Python
`classifier_agent_service`, the React dashboard, and the Helm chart.

Out of scope: the dev-only credentials in `deployments/docker-compose.yml`,
`helm/signalyard/values.yaml`, and `scripts/`. These are deliberate placeholders
for local development and are **not** valid deployment defaults.

## Deployment hardening

This project ships dev defaults for local convenience. Before running anywhere
real, you must override at minimum:

- `JWT_SIGNING_SECRET` — signs agent JWTs
- `HEC_TOKEN_SALT` — salts API key hashes; rotating it invalidates all keys
- `DEV_ADMIN_TOKEN` — **remove entirely**; use OIDC instead
- `POSTGRES_PASSWORD`, Grafana and Keycloak admin passwords
- All `*_WEBHOOK_SECRET` values, which fail closed when unset

Webhook signature verification fails closed on unconfigured secrets. Serve all
services over TLS and keep the schema-registry deploy key read-scoped.

## Automated checks

CI runs gitleaks, `govulncheck`, CodeQL (Go/Python/JS), and a Trivy filesystem
scan on every PR plus a weekly schedule. Dependabot covers Go modules, uv, npm,
GitHub Actions, and Docker base images.
