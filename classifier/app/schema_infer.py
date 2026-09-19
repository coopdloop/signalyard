"""Shape clustering and JSON Schema inference from sample payloads.

Used by the heuristic backend (dev default, no LLM required) and as a
fallback when an LLM returns an unusable response.
"""

from __future__ import annotations

import hashlib
import json
from typing import Any


def shape_key(payload: dict[str, Any]) -> str:
    """Stable fingerprint of a payload's shape: sorted top-level fields + types."""
    shape = sorted((k, _type_name(v)) for k, v in payload.items())
    return hashlib.sha256(json.dumps(shape).encode()).hexdigest()[:16]


def _type_name(value: Any) -> str:
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, int):
        return "integer"
    if isinstance(value, float):
        return "number"
    if isinstance(value, str):
        return "string"
    if isinstance(value, list):
        return "array"
    if isinstance(value, dict):
        return "object"
    return "string"


def infer_schema(payloads: list[dict[str, Any]]) -> dict[str, Any]:
    """Infer a JSON Schema covering all sample payloads.

    Required = present in every sample; types unioned across samples.
    """
    if not payloads:
        return {"type": "object"}
    keys: dict[str, set[str]] = {}
    counts: dict[str, int] = {}
    for p in payloads:
        for k, v in p.items():
            keys.setdefault(k, set()).add(_type_name(v))
            counts[k] = counts.get(k, 0) + 1
    properties = {}
    for k, types in sorted(keys.items()):
        if len(types) == 1:
            properties[k] = {"type": next(iter(types))}
        else:
            properties[k] = {"type": sorted(types)}
    required = sorted(k for k, c in counts.items() if c == len(payloads))
    return {
        "type": "object",
        "required": required,
        "properties": properties,
    }


def parse_llm_json(text: str) -> dict[str, Any]:
    """Extract the first JSON object from an LLM response (tolerates code fences)."""
    cleaned = text.strip()
    if cleaned.startswith("```"):
        lines = cleaned.splitlines()
        lines = [line for line in lines if not line.strip().startswith("```")]
        cleaned = "\n".join(lines).strip()
    start = cleaned.find("{")
    end = cleaned.rfind("}")
    if start == -1 or end == -1 or end <= start:
        raise ValueError("no JSON object found in LLM response")
    return json.loads(cleaned[start : end + 1])
