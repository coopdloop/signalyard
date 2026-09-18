"""classifier_agent_service: consumes quarantined unknown-shape events, classifies
them via a pluggable LLM backend, and submits schema patch proposals to the
schema registry for human review.
"""
from __future__ import annotations

import asyncio
import contextlib
import logging
from typing import Any

import nats
from fastapi import FastAPI, Request
from fastapi.responses import PlainTextResponse
from prometheus_client import CONTENT_TYPE_LATEST, Counter, Histogram, generate_latest
from pydantic import BaseModel

from .auth import TokenAuth
from .backends import build_backends
from .config import load
from .engine import Engine
from .store import RunsStore

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
log = logging.getLogger("classifier")

EVENTS_CONSUMED = Counter("signalyard_classifier_events_consumed_total", "Quarantined events consumed")
PROPOSALS = Counter("signalyard_classifier_proposals_total", "Proposals submitted", ["backend"])
JOB_LATENCY = Histogram("signalyard_classifier_job_seconds", "Classification job latency")

cfg = load()
runs_store = RunsStore(cfg.postgres_dsn) if cfg.postgres_dsn else None
engine = Engine(cfg, build_backends(cfg), runs_store)
auth = TokenAuth(cfg.hec_token_salt, cfg.dev_admin_token, runs_store)

app = FastAPI(title="signal-yard classifier_agent_service", version="0.1.0")
app.middleware("http")(auth.middleware)

_nc = None
_consumer_task: asyncio.Task | None = None


async def _consume_quarantine() -> None:
    """Durable JetStream consumer on the quarantine stream."""
    global _nc
    _nc = await nats.connect(cfg.nats_url, name="classifier-agent")
    js = _nc.jetstream()
    sub = await js.pull_subscribe("quarantine.unknown", durable="classifier", stream=cfg.quarantine_stream)
    log.info("consuming quarantine stream", extra={"stream": cfg.quarantine_stream})
    while True:
        try:
            msgs = await sub.fetch(10, timeout=5)
        except nats.errors.TimeoutError:
            continue
        for msg in msgs:
            try:
                import json

                event = json.loads(msg.data.decode())
                await engine.add_event(event)
                EVENTS_CONSUMED.inc()
                await msg.ack()
            except Exception:
                log.exception("failed to handle quarantined message")
                await msg.nak()


@app.on_event("startup")
async def startup() -> None:
    global _consumer_task
    if runs_store:
        await runs_store.connect()
    _consumer_task = asyncio.create_task(_consume_quarantine())


@app.on_event("shutdown")
async def shutdown() -> None:
    if _consumer_task:
        _consumer_task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await _consumer_task
    if _nc:
        await _nc.drain()
    if runs_store:
        await runs_store.close()


# --- routes (contracts from docs/product.json) ---


@app.get("/health")
async def health() -> dict[str, str]:
    return {"status": "ok"}


@app.get("/metrics", response_class=PlainTextResponse)
async def metrics() -> PlainTextResponse:
    return PlainTextResponse(generate_latest().decode(), media_type=CONTENT_TYPE_LATEST)


@app.get("/v1/classification-jobs")
async def list_jobs() -> dict[str, Any]:
    jobs = sorted(engine.jobs.values(), key=lambda j: j.created_at, reverse=True)[:100]
    return {"jobs": [_job_view(j) for j in jobs]}


class CreateJobRequest(BaseModel):
    event_ids: list[str] = []


@app.post("/v1/classification-jobs", status_code=201)
async def create_job(req: CreateJobRequest) -> dict[str, str]:
    if not req.event_ids:
        return PlainTextResponse('{"error":"event_ids is required"}', status_code=400, media_type="application/json")
    job = await engine.start_job(req.event_ids)
    asyncio.create_task(engine._run_job_safe(job.job_id))
    return {"job_id": job.job_id, "status": job.status}


@app.get("/v1/classification-jobs/{job_id}")
async def get_job(job_id: str) -> Any:
    job = engine.jobs.get(job_id)
    if job is None:
        return PlainTextResponse('{"error":"job not found"}', status_code=404, media_type="application/json")
    return _job_view(job)


def _job_view(job) -> dict[str, Any]:
    return {
        "job_id": job.job_id,
        "status": job.status,
        "inferred_category": job.inferred_category,
        "proposal_id": job.proposal_id,
        "error": job.error,
    }


@app.get("/v1/clusters")
async def list_clusters() -> dict[str, Any]:
    clusters = [
        {
            "cluster_id": c.cluster_id,
            "category_hint": c.category_hint,
            "size": len(c.event_ids),
            "proposal_submitted": c.proposal_submitted,
        }
        for c in engine.clusters.values()
    ]
    return {"clusters": clusters}


@app.get("/v1/clusters/{cluster_id}")
async def get_cluster(cluster_id: str) -> Any:
    cluster = engine.clusters.get(cluster_id)
    if cluster is None:
        return PlainTextResponse('{"error":"cluster not found"}', status_code=404, media_type="application/json")
    return {
        "cluster_id": cluster.cluster_id,
        "event_ids": cluster.event_ids,
        "representative_payload": cluster.representative_payload,
    }


@app.get("/v1/llm-backends")
async def list_backends() -> dict[str, Any]:
    backends = []
    for name, backend in engine.backends.items():
        status = await backend.health()
        backends.append({
            "name": name,
            "active": name == engine.active_backend_name,
            "healthy": status.healthy,
            "detail": status.detail,
        })
    return {"backends": backends}


class UpdateBackendRequest(BaseModel):
    active: bool | None = None
    config: dict[str, Any] = {}


@app.put("/v1/llm-backends/{backend_name}")
async def update_backend(backend_name: str, req: UpdateBackendRequest) -> Any:
    if backend_name not in engine.backends:
        return PlainTextResponse('{"error":"backend not found"}', status_code=404, media_type="application/json")
    engine.set_backend(backend_name, bool(req.active), req.config)
    return {"name": backend_name, "active": backend_name == engine.active_backend_name}


def main() -> None:
    import uvicorn

    # Pass the app instance (not an import string) so `python -m app.main`
    # doesn't import this module twice and double-register metrics.
    uvicorn.run(app, host="0.0.0.0", port=cfg.port, log_level="info")


if __name__ == "__main__":
    main()
