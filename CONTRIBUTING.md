# Contributing to Signal Yard

## Prerequisites

| Tool | Version | Used for |
|---|---|---|
| Go | 1.25+ | `core_api_gateway`, `normalizer_router`, `schema_registry`, `webhook_adapter` |
| Python | 3.12+ (via [uv](https://docs.astral.sh/uv/)) | `classifier_agent_service` |
| Node | 20+ | React dashboard |
| Docker + Compose | recent | local stack |
| [pre-commit](https://pre-commit.com/) | 3.5+ | local gates |

## Setup

```sh
git clone https://github.com/coopdloop/signalyard.git
cd signalyard

pre-commit install           # installs pre-commit + commit-msg hooks
(cd classifier && uv sync --all-groups)
(cd dashboard && npm ci)

make up                      # postgres + nats + services
```

## Local gates

Pre-commit runs formatting, secret scanning, shellcheck, `go vet`, `go test`, and
classifier tests. Run everything up front with:

```sh
pre-commit run --all-files
```

Per-language:

```sh
make test                                    # go test ./...
golangci-lint run                            # go lint (v2 config)
(cd classifier && uv run ruff check . && uv run pytest -q)
(cd dashboard && npm run build)
```

## Commit style

Conventional commits — `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`.
Keep the subject imperative and under ~72 characters.

## Pull requests

1. Branch from `main`.
2. Keep the change scoped to one concern.
3. Add or update tests for behavior changes.
4. Make sure `pre-commit run --all-files` is clean before pushing.
5. Fill out the PR template, including how you verified the change.

## Secrets

Never commit real credentials. Every credential in `deployments/`, `helm/`, and
`scripts/` is a dev placeholder and must stay that way; gitleaks enforces this in
pre-commit and CI. Real secrets belong in environment variables or your secret
manager. For live-LLM smoke tests, keep the key in `~/.signalyard-llm.env`
(outside the repo) — see `scripts/smoke-llm.sh`.
