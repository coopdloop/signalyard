-- Idempotency keys for POST /v1/collect: duplicate submissions return the original response
CREATE TABLE idempotency_keys (
    key TEXT NOT NULL,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    event_id UUID NOT NULL,
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (key, agent_id)
);
