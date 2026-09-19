"""Configuration for classifier_agent_service. Names match docs/product.json."""

from __future__ import annotations

import os
from dataclasses import dataclass, field


@dataclass
class Config:
    port: int = 8083
    nats_url: str = ""
    schema_registry_url: str = ""
    llm_provider: str = "heuristic"  # anthropic | openai | ollama | heuristic (dev default)
    anthropic_api_key: str = ""
    openai_api_key: str = ""
    ollama_base_url: str = "http://localhost:11434"
    quarantine_stream: str = "quarantine"
    postgres_dsn: str = ""  # optional: records classifier_runs
    registry_token: str = ""  # token used when submitting proposals (SCHEMA_REGISTRY_TOKEN)
    hec_token_salt: str = ""  # auth for the HTTP API
    jwt_signing_secret: str = ""
    dev_admin_token: str = ""
    cluster_threshold: int = 3  # payloads per shape before auto-proposing
    extra: dict = field(default_factory=dict)


def load() -> Config:
    cfg = Config(
        port=int(os.environ.get("PORT", "8083")),
        nats_url=os.environ.get("NATS_URL", ""),
        schema_registry_url=os.environ.get("SCHEMA_REGISTRY_URL", ""),
        llm_provider=os.environ.get("LLM_PROVIDER", "heuristic"),
        anthropic_api_key=os.environ.get("ANTHROPIC_API_KEY", ""),
        openai_api_key=os.environ.get("OPENAI_API_KEY", ""),
        ollama_base_url=os.environ.get("OLLAMA_BASE_URL", "http://localhost:11434"),
        quarantine_stream=os.environ.get("QUARANTINE_STREAM_NAME", "quarantine"),
        postgres_dsn=os.environ.get("POSTGRES_DSN", ""),
        registry_token=os.environ.get("SCHEMA_REGISTRY_TOKEN", ""),
        hec_token_salt=os.environ.get("HEC_TOKEN_SALT", ""),
        jwt_signing_secret=os.environ.get("JWT_SIGNING_SECRET", ""),
        dev_admin_token=os.environ.get("DEV_ADMIN_TOKEN", ""),
        cluster_threshold=int(os.environ.get("CLUSTER_THRESHOLD", "3")),
    )
    missing = [
        name
        for name, val in [
            ("NATS_URL", cfg.nats_url),
            ("SCHEMA_REGISTRY_URL", cfg.schema_registry_url),
            ("HEC_TOKEN_SALT", cfg.hec_token_salt),
        ]
        if not val
    ]
    if missing:
        raise RuntimeError(f"required environment variables not set: {', '.join(missing)}")
    return cfg
