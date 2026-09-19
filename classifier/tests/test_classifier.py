"""Unit tests for the pure parts of the classifier: shape clustering,
schema inference, LLM response parsing, and heuristic classification."""

import asyncio

from app.backends import HeuristicBackend
from app.schema_infer import infer_schema, parse_llm_json, shape_key


def test_shape_key_same_shape_different_values():
    a = {"alert_id": "A1", "severity": "high"}
    b = {"alert_id": "A2", "severity": "low"}
    assert shape_key(a) == shape_key(b)


def test_shape_key_differs_on_fields_or_types():
    a = {"alert_id": "A1"}
    b = {"alert_id": "A1", "extra": 1}
    c = {"alert_id": 123}
    assert shape_key(a) != shape_key(b)
    assert shape_key(a) != shape_key(c)


def test_infer_schema_required_and_types():
    schema = infer_schema(
        [
            {"alert_id": "A1", "severity": "high", "count": 3},
            {"alert_id": "A2", "severity": "low"},
        ]
    )
    assert schema["type"] == "object"
    assert set(schema["required"]) == {"alert_id", "severity"}
    assert schema["properties"]["count"]["type"] == "integer"
    assert schema["properties"]["alert_id"]["type"] == "string"


def test_infer_schema_type_union():
    schema = infer_schema([{"v": "x"}, {"v": 5}])
    assert schema["properties"]["v"]["type"] == ["integer", "string"]


def test_parse_llm_json_plain():
    assert parse_llm_json('{"category": "x", "json_schema": {}}')["category"] == "x"


def test_parse_llm_json_code_fence():
    text = '```json\n{"category": "y", "json_schema": {}}\n```'
    assert parse_llm_json(text)["category"] == "y"


def test_parse_llm_json_surrounding_prose():
    text = 'Here is my answer:\n{"category": "z", "json_schema": {}}\nHope that helps!'
    assert parse_llm_json(text)["category"] == "z"


def test_heuristic_backend_classify():
    backend = HeuristicBackend()
    result = asyncio.run(backend.classify("mystery_shape", [{"weird_field": "v1"}, {"weird_field": "v2"}]))
    assert result.category == "mystery_shape"
    assert result.json_schema["required"] == ["weird_field"]
    assert result.routing_yaml == "target: postgres"
    assert result.model == "heuristic-infer-v1"
