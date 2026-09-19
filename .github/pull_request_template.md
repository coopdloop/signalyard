## Summary

<!-- What changes and why. Link any related issue. -->

## Type

- [ ] feat
- [ ] fix
- [ ] docs
- [ ] refactor
- [ ] test
- [ ] chore

## Services touched

- [ ] core_api_gateway
- [ ] normalizer_router
- [ ] schema_registry_service
- [ ] classifier_agent_service
- [ ] webhook_adapter_service
- [ ] dashboard
- [ ] deployments / helm / observability

## Verification

<!-- Commands you ran and what you observed. -->

- [ ] `pre-commit run --all-files` is clean
- [ ] `go test ./...` passes
- [ ] Relevant smoke script passes (`scripts/smoke*.sh`)

## Checklist

- [ ] No real credentials added (dev placeholders only)
- [ ] Schema or API changes reflected in `docs/openapi.json` and migrations
- [ ] Docs updated (README / ROADMAP) if behavior changed
