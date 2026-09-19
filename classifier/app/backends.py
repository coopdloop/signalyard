"""Pluggable LLM backends: anthropic | openai | ollama | heuristic.

The spec's tech stack suggests langchain/instructor; we call the provider
APIs directly over httpx to keep the image lean and the failure modes
explicit. Structured output is enforced by prompt + parse_llm_json.
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from typing import Any

import httpx

from .schema_infer import infer_schema, parse_llm_json

PROMPT = """You are the Signal Yard classifier agent. Given sample event payloads of an
unknown shape (category hint: {category_hint}), produce a JSON object with:
- "category": a snake_case category name for these events
- "confidence": 0.0-1.0
- "json_schema": a JSON Schema (draft-07 object schema) that validates these payloads
- "routing_yaml": routing config, usually "target: postgres"

Respond with ONLY the JSON object.

Sample payloads:
{samples}
"""


@dataclass
class Classification:
    category: str
    confidence: float
    json_schema: dict[str, Any]
    routing_yaml: str
    model: str
    input_tokens: int = 0
    output_tokens: int = 0
    duration_ms: int = 0


@dataclass
class BackendStatus:
    name: str
    active: bool
    healthy: bool
    detail: str = ""
    config: dict[str, Any] = field(default_factory=dict)


class Backend:
    name = "base"

    async def classify(self, category_hint: str, samples: list[dict[str, Any]]) -> Classification:
        raise NotImplementedError

    async def health(self) -> BackendStatus:
        raise NotImplementedError


class HeuristicBackend(Backend):
    """Local schema inference, no LLM. Dev default; always available."""

    name = "heuristic"

    async def classify(self, category_hint: str, samples: list[dict[str, Any]]) -> Classification:
        start = time.monotonic()
        schema = infer_schema(samples)
        return Classification(
            category=category_hint or "unknown_shape",
            confidence=0.5,
            json_schema=schema,
            routing_yaml="target: postgres",
            model="heuristic-infer-v1",
            duration_ms=int((time.monotonic() - start) * 1000),
        )

    async def health(self) -> BackendStatus:
        return BackendStatus(name=self.name, active=True, healthy=True, detail="local inference, no LLM")


class AnthropicBackend(Backend):
    name = "anthropic"

    def __init__(self, api_key: str, model: str = "claude-sonnet-4-5"):
        self.api_key = api_key
        self.model = model

    async def classify(self, category_hint: str, samples: list[dict[str, Any]]) -> Classification:
        import json as _json

        prompt = PROMPT.format(category_hint=category_hint, samples=_json.dumps(samples[:3], indent=2))
        start = time.monotonic()
        async with httpx.AsyncClient(timeout=60) as client:
            resp = await client.post(
                "https://api.anthropic.com/v1/messages",
                headers={"x-api-key": self.api_key, "anthropic-version": "2023-06-01"},
                json={
                    "model": self.model,
                    "max_tokens": 2048,
                    "messages": [{"role": "user", "content": prompt}],
                },
            )
            resp.raise_for_status()
            data = resp.json()
        text = "".join(b.get("text", "") for b in data.get("content", []))
        parsed = parse_llm_json(text)
        usage = data.get("usage", {})
        return Classification(
            category=parsed["category"],
            confidence=float(parsed.get("confidence", 0.7)),
            json_schema=parsed["json_schema"],
            routing_yaml=parsed.get("routing_yaml", "target: postgres"),
            model=self.model,
            input_tokens=usage.get("input_tokens", 0),
            output_tokens=usage.get("output_tokens", 0),
            duration_ms=int((time.monotonic() - start) * 1000),
        )

    async def health(self) -> BackendStatus:
        ok = bool(self.api_key)
        return BackendStatus(
            name=self.name,
            active=ok,
            healthy=ok,
            detail="api key configured" if ok else "ANTHROPIC_API_KEY not set",
        )


class OpenAIBackend(Backend):
    name = "openai"

    def __init__(self, api_key: str, model: str = "gpt-4o-mini"):
        self.api_key = api_key
        self.model = model

    async def classify(self, category_hint: str, samples: list[dict[str, Any]]) -> Classification:
        import json as _json

        prompt = PROMPT.format(category_hint=category_hint, samples=_json.dumps(samples[:3], indent=2))
        start = time.monotonic()
        async with httpx.AsyncClient(timeout=60) as client:
            resp = await client.post(
                "https://api.openai.com/v1/chat/completions",
                headers={"Authorization": f"Bearer {self.api_key}"},
                json={
                    "model": self.model,
                    "messages": [{"role": "user", "content": prompt}],
                    "response_format": {"type": "json_object"},
                },
            )
            resp.raise_for_status()
            data = resp.json()
        parsed = parse_llm_json(data["choices"][0]["message"]["content"])
        usage = data.get("usage", {})
        return Classification(
            category=parsed["category"],
            confidence=float(parsed.get("confidence", 0.7)),
            json_schema=parsed["json_schema"],
            routing_yaml=parsed.get("routing_yaml", "target: postgres"),
            model=self.model,
            input_tokens=usage.get("prompt_tokens", 0),
            output_tokens=usage.get("completion_tokens", 0),
            duration_ms=int((time.monotonic() - start) * 1000),
        )

    async def health(self) -> BackendStatus:
        ok = bool(self.api_key)
        return BackendStatus(
            name=self.name,
            active=ok,
            healthy=ok,
            detail="api key configured" if ok else "OPENAI_API_KEY not set",
        )


class OllamaBackend(Backend):
    name = "ollama"

    def __init__(self, base_url: str, model: str = "llama3.1"):
        self.base_url = base_url.rstrip("/")
        self.model = model

    async def classify(self, category_hint: str, samples: list[dict[str, Any]]) -> Classification:
        import json as _json

        prompt = PROMPT.format(category_hint=category_hint, samples=_json.dumps(samples[:3], indent=2))
        start = time.monotonic()
        async with httpx.AsyncClient(timeout=120) as client:
            resp = await client.post(
                f"{self.base_url}/api/generate",
                json={"model": self.model, "prompt": prompt, "stream": False, "format": "json"},
            )
            resp.raise_for_status()
            data = resp.json()
        parsed = parse_llm_json(data.get("response", ""))
        return Classification(
            category=parsed["category"],
            confidence=float(parsed.get("confidence", 0.6)),
            json_schema=parsed["json_schema"],
            routing_yaml=parsed.get("routing_yaml", "target: postgres"),
            model=self.model,
            input_tokens=data.get("prompt_eval_count", 0),
            output_tokens=data.get("eval_count", 0),
            duration_ms=int((time.monotonic() - start) * 1000),
        )

    async def health(self) -> BackendStatus:
        try:
            async with httpx.AsyncClient(timeout=3) as client:
                resp = await client.get(f"{self.base_url}/api/tags")
            return BackendStatus(
                name=self.name, active=True, healthy=resp.status_code == 200, detail=f"{self.base_url} reachable"
            )
        except Exception as exc:
            return BackendStatus(name=self.name, active=True, healthy=False, detail=str(exc))


def build_backends(cfg) -> dict[str, Backend]:
    return {
        "heuristic": HeuristicBackend(),
        "anthropic": AnthropicBackend(cfg.anthropic_api_key),
        "openai": OpenAIBackend(cfg.openai_api_key),
        "ollama": OllamaBackend(cfg.ollama_base_url),
    }
