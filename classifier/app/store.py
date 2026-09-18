"""Optional Postgres access: classifier_runs recording + quarantine event lookup."""
from __future__ import annotations

import json
import uuid
from typing import Any

import asyncpg


class RunsStore:
    def __init__(self, dsn: str):
        self.dsn = dsn
        self.pool: asyncpg.Pool | None = None

    async def connect(self) -> None:
        self.pool = await asyncpg.create_pool(self.dsn, min_size=1, max_size=4)

    async def close(self) -> None:
        if self.pool:
            await self.pool.close()

    async def record_run(self, quarantine_event_id: str | None, model_name: str,
                         input_tokens: int, output_tokens: int, duration_ms: int,
                         status: str, error_message: str) -> None:
        if not self.pool or not quarantine_event_id:
            return
        try:
            qid = uuid.UUID(quarantine_event_id)
        except ValueError:
            return
        async with self.pool.acquire() as conn:
            await conn.execute(
                """
                INSERT INTO classifier_runs
                    (quarantine_event_id, model_name, input_tokens, output_tokens,
                     duration_ms, status, error_message)
                VALUES ($1, $2, $3, $4, $5, $6, $7)
                """,
                qid, model_name, input_tokens, output_tokens, duration_ms, status,
                error_message or None,
            )

    async def load_quarantined(self, event_ids: list[str]) -> list[dict[str, Any]]:
        if not self.pool:
            return []
        uuids = []
        for eid in event_ids:
            try:
                uuids.append(uuid.UUID(eid))
            except ValueError:
                continue
        if not uuids:
            return []
        async with self.pool.acquire() as conn:
            rows = await conn.fetch(
                "SELECT raw_payload FROM quarantine_events WHERE id = ANY($1::uuid[])", uuids
            )
        return [json.loads(r["raw_payload"]) for r in rows]
