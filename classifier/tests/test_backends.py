"""Contract tests for the LLM backends (no API keys required).

Each backend is tested against a recorded, realistic provider response:
  - request shape: URL, auth header, model field, prompt contains the samples
  - response parsing: provider-specific envelope -> Classification
  - error path: non-200 raises httpx.HTTPStatusError
"""

import asyncio
import json

import httpx
import pytest

from app.backends import AnthropicBackend, OllamaBackend, OpenAIBackend

SAMPLES = [
    {"detection_name": "Suspicious PowerShell", "risk_score": 85, "host": "ws-01"},
    {"detection_name": "LSASS Dump", "risk_score": 92, "host": "ws-02"},
]

LLM_JSON = {
    "category": "edr_detection",
    "confidence": 0.9,
    "json_schema": {
        "type": "object",
        "required": ["detection_name", "risk_score", "host"],
        "properties": {
            "detection_name": {"type": "string"},
            "risk_score": {"type": "integer"},
            "host": {"type": "string"},
        },
    },
    "routing_yaml": "target: postgres",
}


class FakeResponse:
    def __init__(self, payload, status_code=200, url="http://test"):
        self._payload = payload
        self.status_code = status_code
        self._url = url

    def json(self):
        return self._payload

    def raise_for_status(self):
        if self.status_code >= 400:
            req = httpx.Request("POST", self._url)
            resp = httpx.Response(self.status_code, request=req)
            raise httpx.HTTPStatusError(f"error {self.status_code}", request=req, response=resp)


class FakeAsyncClient:
    """Stand-in for httpx.AsyncClient that records post() calls."""

    def __init__(self, response, calls, **kwargs):
        self._response = response
        self._calls = calls

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        return False

    async def post(self, url, headers=None, json=None):
        self._calls.append({"url": url, "headers": headers or {}, "json": json or {}})
        return self._response

    async def get(self, url):
        self._calls.append({"url": url, "headers": {}, "json": {}})
        return self._response


def patch_client(monkeypatch, response):
    calls = []

    def factory(*args, **kwargs):
        return FakeAsyncClient(response, calls, **kwargs)

    monkeypatch.setattr(httpx, "AsyncClient", factory)
    return calls


# --- Anthropic ---

ANTHROPIC_RESPONSE = {
    "id": "msg_01XyZ",
    "type": "message",
    "role": "assistant",
    "model": "claude-sonnet-4-5",
    "content": [{"type": "text", "text": json.dumps(LLM_JSON)}],
    "usage": {"input_tokens": 312, "output_tokens": 150},
}


def test_anthropic_request_shape_and_parsing(monkeypatch):
    calls = patch_client(monkeypatch, FakeResponse(ANTHROPIC_RESPONSE))
    backend = AnthropicBackend(api_key="sk-ant-test", model="claude-sonnet-4-5")
    result = asyncio.run(backend.classify("llm_eval_shape", SAMPLES))

    assert len(calls) == 1
    call = calls[0]
    assert call["url"] == "https://api.anthropic.com/v1/messages"
    assert call["headers"]["x-api-key"] == "sk-ant-test"
    assert call["headers"]["anthropic-version"] == "2023-06-01"
    assert call["json"]["model"] == "claude-sonnet-4-5"
    prompt = call["json"]["messages"][0]["content"]
    assert "Suspicious PowerShell" in prompt and "risk_score" in prompt

    assert result.category == "edr_detection"
    assert result.confidence == 0.9
    assert result.json_schema["required"] == ["detection_name", "risk_score", "host"]
    assert result.routing_yaml == "target: postgres"
    assert result.model == "claude-sonnet-4-5"
    assert result.input_tokens == 312
    assert result.output_tokens == 150


def test_anthropic_error_path(monkeypatch):
    patch_client(monkeypatch, FakeResponse({"error": {"message": "invalid x-api-key"}}, status_code=401))
    backend = AnthropicBackend(api_key="bad-key")
    with pytest.raises(httpx.HTTPStatusError):
        asyncio.run(backend.classify("llm_eval_shape", SAMPLES))


# --- OpenAI ---

OPENAI_RESPONSE = {
    "id": "chatcmpl-abc",
    "object": "chat.completion",
    "model": "gpt-4o-mini",
    "choices": [
        {
            "index": 0,
            "message": {"role": "assistant", "content": json.dumps(LLM_JSON)},
            "finish_reason": "stop",
        }
    ],
    "usage": {"prompt_tokens": 280, "completion_tokens": 140, "total_tokens": 420},
}


def test_openai_request_shape_and_parsing(monkeypatch):
    calls = patch_client(monkeypatch, FakeResponse(OPENAI_RESPONSE))
    backend = OpenAIBackend(api_key="sk-test", model="gpt-4o-mini")
    result = asyncio.run(backend.classify("llm_eval_shape", SAMPLES))

    assert len(calls) == 1
    call = calls[0]
    assert call["url"] == "https://api.openai.com/v1/chat/completions"
    assert call["headers"]["Authorization"] == "Bearer sk-test"
    assert call["json"]["model"] == "gpt-4o-mini"
    assert call["json"]["response_format"] == {"type": "json_object"}
    prompt = call["json"]["messages"][0]["content"]
    assert "LSASS Dump" in prompt

    assert result.category == "edr_detection"
    assert result.json_schema["properties"]["risk_score"]["type"] == "integer"
    assert result.model == "gpt-4o-mini"
    assert result.input_tokens == 280
    assert result.output_tokens == 140


def test_openai_error_path(monkeypatch):
    patch_client(monkeypatch, FakeResponse({"error": {"message": "rate limited"}}, status_code=429))
    backend = OpenAIBackend(api_key="sk-test")
    with pytest.raises(httpx.HTTPStatusError):
        asyncio.run(backend.classify("llm_eval_shape", SAMPLES))


# --- Ollama ---

OLLAMA_RESPONSE = {
    "model": "llama3.1",
    "created_at": "2026-09-18T00:00:00Z",
    "response": json.dumps(LLM_JSON),
    "done": True,
    "prompt_eval_count": 240,
    "eval_count": 120,
}


def test_ollama_request_shape_and_parsing(monkeypatch):
    calls = patch_client(monkeypatch, FakeResponse(OLLAMA_RESPONSE))
    backend = OllamaBackend(base_url="http://ollama:11434", model="llama3.1")
    result = asyncio.run(backend.classify("llm_eval_shape", SAMPLES))

    assert len(calls) == 1
    call = calls[0]
    assert call["url"] == "http://ollama:11434/api/generate"
    assert call["json"]["model"] == "llama3.1"
    assert call["json"]["stream"] is False
    assert call["json"]["format"] == "json"
    assert "Suspicious PowerShell" in call["json"]["prompt"]

    assert result.category == "edr_detection"
    assert result.routing_yaml == "target: postgres"
    assert result.model == "llama3.1"
    assert result.input_tokens == 240
    assert result.output_tokens == 120


def test_ollama_error_path(monkeypatch):
    patch_client(monkeypatch, FakeResponse({"error": "model not found"}, status_code=404))
    backend = OllamaBackend(base_url="http://ollama:11434")
    with pytest.raises(httpx.HTTPStatusError):
        asyncio.run(backend.classify("llm_eval_shape", SAMPLES))


# --- Engine: proposal category must match the quarantine category ---


def test_engine_proposal_keeps_quarantine_category(monkeypatch):
    """Regression: an LLM category rename must NOT be adopted in the proposal,
    or the normalizer's approval-driven replay can't find the events."""
    import types

    from app.backends import Classification
    from app.engine import Cluster, Engine

    calls = patch_client(monkeypatch, FakeResponse({"proposal_id": "p-123"}))
    cfg = types.SimpleNamespace(registry_token="tok", schema_registry_url="http://registry", llm_provider="heuristic")
    engine = Engine(cfg, {}, None)
    cluster = Cluster(cluster_id="k", category_hint="quarantine_category", event_ids=["e1"])
    classification = Classification(
        category="llm_suggested_name",
        confidence=0.9,
        json_schema={"type": "object"},
        routing_yaml="target: postgres",
        model="m",
    )

    proposal_id = asyncio.run(engine._submit_proposal(cluster, classification))

    assert proposal_id == "p-123"
    body = calls[0]["json"]
    assert body["category"] == "quarantine_category"
    assert calls[0]["headers"]["Authorization"] == "SignalYard tok"
