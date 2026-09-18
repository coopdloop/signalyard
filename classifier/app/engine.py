"""Classification engine: clusters unknown payloads, runs backends, submits proposals."""
from __future__ import annotations

import asyncio
import logging
import time
import uuid
from dataclasses import dataclass, field
from typing import Any

import httpx

from .backends import Backend, Classification
from .schema_infer import shape_key

log = logging.getLogger("classifier")


@dataclass
class Cluster:
    cluster_id: str
    category_hint: str
    event_ids: list[str] = field(default_factory=list)
    representative_payload: dict[str, Any] = field(default_factory=dict)
    samples: list[dict[str, Any]] = field(default_factory=list)
    proposal_submitted: bool = False
    created_at: float = field(default_factory=time.time)


@dataclass
class Job:
    job_id: str
    status: str = "pending"  # pending | running | completed | failed
    event_ids: list[str] = field(default_factory=list)
    inferred_category: str = ""
    proposal_id: str = ""
    error: str = ""
    created_at: float = field(default_factory=time.time)


class Engine:
    def __init__(self, cfg, backends: dict[str, Backend], runs_store=None):
        self.cfg = cfg
        self.backends = backends
        self.active_backend_name = cfg.llm_provider
        self.runs_store = runs_store  # optional classifier_runs recorder
        self.clusters: dict[str, Cluster] = {}
        self.jobs: dict[str, Job] = {}
        self._lock = asyncio.Lock()

    @property
    def active_backend(self) -> Backend:
        return self.backends.get(self.active_backend_name) or self.backends["heuristic"]

    def set_backend(self, name: str, active: bool, config: dict[str, Any]) -> None:
        if name not in self.backends:
            raise KeyError(name)
        if active:
            self.active_backend_name = name
        if config:
            backend = self.backends[name]
            for k, v in config.items():
                if hasattr(backend, k):
                    setattr(backend, k, v)

    async def add_event(self, event: dict[str, Any]) -> Cluster:
        """Add a quarantined envelope to its shape cluster; auto-propose at threshold."""
        payload = event.get("payload") or {}
        category_hint = event.get("category", "")
        key = f"{category_hint}:{shape_key(payload)}"
        async with self._lock:
            cluster = self.clusters.get(key)
            if cluster is None:
                cluster = Cluster(cluster_id=key, category_hint=category_hint,
                                  representative_payload=payload)
                self.clusters[key] = cluster
            event_id = event.get("event_id", "")
            if event_id and event_id not in cluster.event_ids:
                cluster.event_ids.append(event_id)
            if len(cluster.samples) < 5:
                cluster.samples.append(payload)
            should_propose = (
                len(cluster.event_ids) >= self.cfg.cluster_threshold
                and not cluster.proposal_submitted
            )
        if should_propose:
            job = await self.start_job(cluster.event_ids[:5], cluster)
            asyncio.create_task(self._run_job_safe(job.job_id))
        return cluster

    async def start_job(self, event_ids: list[str], cluster: Cluster | None = None) -> Job:
        job = Job(job_id=str(uuid.uuid4()), event_ids=event_ids)
        self.jobs[job.job_id] = job
        if cluster is not None:
            # stash cluster reference for the runner
            job.__dict__["_cluster"] = cluster
        return job

    async def _run_job_safe(self, job_id: str) -> None:
        try:
            await self.run_job(job_id)
        except Exception:
            log.exception("classification job %s crashed", job_id)

    async def run_job(self, job_id: str) -> None:
        job = self.jobs[job_id]
        job.status = "running"
        cluster: Cluster | None = job.__dict__.get("_cluster")
        try:
            if cluster is None:
                cluster = await self._cluster_from_store(job.event_ids)
            classification = await self.active_backend.classify(
                cluster.category_hint, cluster.samples or [cluster.representative_payload]
            )
            await self._record_run(cluster, classification, "success", "")
            proposal_id = await self._submit_proposal(cluster, classification)
            cluster.proposal_submitted = True
            job.status = "completed"
            job.inferred_category = classification.category
            job.proposal_id = proposal_id
        except Exception as exc:
            job.status = "failed"
            job.error = str(exc)
            await self._record_run(cluster, None, "failed", str(exc))
            raise

    async def _cluster_from_store(self, event_ids: list[str]) -> Cluster:
        """Build an ad-hoc cluster from quarantine_events rows (manual jobs)."""
        if self.runs_store is None:
            raise RuntimeError("no event store configured for manual jobs")
        events = await self.runs_store.load_quarantined(event_ids)
        if not events:
            raise RuntimeError("no quarantined events found for given ids")
        payloads = [e.get("payload") or {} for e in events]
        hint = events[0].get("category", "")
        return Cluster(
            cluster_id=f"manual:{uuid.uuid4()}",
            category_hint=hint,
            event_ids=event_ids,
            representative_payload=payloads[0],
            samples=payloads[:5],
        )

    async def _submit_proposal(self, cluster: Cluster, c: Classification) -> str:
        # The proposal category must match the quarantined events' category:
        # the normalizer's approval-driven replay looks up quarantine rows by
        # raw_payload category, so an LLM rename would strand the events
        # (producers keep sending the original category anyway). The LLM's
        # suggested name is advisory and logged, not adopted.
        if cluster.category_hint and cluster.category_hint != c.category:
            log.info("LLM suggested rename %s -> %s; keeping original category",
                     cluster.category_hint, c.category)
        body = {
            "category": cluster.category_hint or c.category,
            "generated_by": f"classifier-agent/{c.model}",
            "json_schema_patch": c.json_schema,
            "routing_yaml_diff": c.routing_yaml,
            "sample_event_ids": cluster.event_ids[:5],
            "confidence": c.confidence,
        }
        headers = {}
        if self.cfg.registry_token:
            headers["Authorization"] = f"SignalYard {self.cfg.registry_token}"
        async with httpx.AsyncClient(timeout=15) as client:
            resp = await client.post(
                f"{self.cfg.schema_registry_url}/v1/proposals", json=body, headers=headers
            )
            if resp.status_code >= 400 and body["sample_event_ids"]:
                # Sample ids may reference quarantine rows that no longer exist
                # (e.g. dev DB resets); retry without them rather than drop the proposal.
                log.warning("proposal with sample ids rejected (%s); retrying without", resp.status_code)
                body["sample_event_ids"] = []
                resp = await client.post(
                    f"{self.cfg.schema_registry_url}/v1/proposals", json=body, headers=headers
                )
            resp.raise_for_status()
            return resp.json()["proposal_id"]

    async def _record_run(self, cluster: Cluster | None, c: Classification | None,
                          status: str, error: str) -> None:
        if self.runs_store is None or cluster is None:
            return
        try:
            await self.runs_store.record_run(
                quarantine_event_id=cluster.event_ids[0] if cluster.event_ids else None,
                model_name=c.model if c else self.active_backend_name,
                input_tokens=c.input_tokens if c else 0,
                output_tokens=c.output_tokens if c else 0,
                duration_ms=c.duration_ms if c else 0,
                status=status,
                error_message=error,
            )
        except Exception:
            log.warning("failed to record classifier run", exc_info=True)
