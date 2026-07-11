#!/usr/bin/env python3
"""Validate StormRelay benchmark JSON with the checked-in schema.

This intentionally supports only the JSON Schema keywords used by
result.schema.json. Keeping the validator dependency-free makes the lightweight
CI profile reproducible without downloading an unpinned package at runtime.
"""

from __future__ import annotations

import argparse
import json
import math
import re
import sys
from datetime import datetime
from pathlib import Path
from typing import Any


class ValidationError(Exception):
    pass


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("schema", type=Path)
    parser.add_argument("instances", nargs="+", type=Path)
    return parser.parse_args()


def resolve_ref(root: dict[str, Any], ref: str) -> dict[str, Any]:
    if not ref.startswith("#/"):
        raise ValidationError(f"unsupported external $ref {ref!r}")
    current: Any = root
    for raw_part in ref[2:].split("/"):
        part = raw_part.replace("~1", "/").replace("~0", "~")
        if not isinstance(current, dict) or part not in current:
            raise ValidationError(f"unresolved $ref {ref!r}")
        current = current[part]
    if not isinstance(current, dict):
        raise ValidationError(f"$ref {ref!r} did not resolve to an object")
    return current


def type_matches(expected: str, value: Any) -> bool:
    if expected == "object":
        return isinstance(value, dict)
    if expected == "array":
        return isinstance(value, list)
    if expected == "string":
        return isinstance(value, str)
    if expected == "integer":
        return isinstance(value, int) and not isinstance(value, bool)
    if expected == "number":
        return (
            isinstance(value, (int, float))
            and not isinstance(value, bool)
            and math.isfinite(float(value))
        )
    if expected == "boolean":
        return isinstance(value, bool)
    if expected == "null":
        return value is None
    raise ValidationError(f"unsupported schema type {expected!r}")


def validate_format(name: str, value: str, path: str, errors: list[str]) -> None:
    if name != "date-time":
        errors.append(f"{path}: unsupported format {name!r}")
        return
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        errors.append(f"{path}: expected RFC3339 date-time")
        return
    if parsed.tzinfo is None:
        errors.append(f"{path}: date-time must include a timezone")


def validate(
    root: dict[str, Any], schema: dict[str, Any], value: Any, path: str, errors: list[str]
) -> None:
    if "$ref" in schema:
        validate(root, resolve_ref(root, schema["$ref"]), value, path, errors)
        return

    if "const" in schema and value != schema["const"]:
        errors.append(f"{path}: expected constant {schema['const']!r}")
    if "enum" in schema and value not in schema["enum"]:
        errors.append(f"{path}: expected one of {schema['enum']!r}")

    expected_type = schema.get("type")
    if expected_type is not None:
        allowed = [expected_type] if isinstance(expected_type, str) else expected_type
        if not any(type_matches(item, value) for item in allowed):
            errors.append(f"{path}: expected type {allowed!r}, got {type(value).__name__}")
            return

    if isinstance(value, dict):
        required = schema.get("required", [])
        for key in required:
            if key not in value:
                errors.append(f"{path}: missing required property {key!r}")
        properties = schema.get("properties", {})
        for key, item in value.items():
            child_path = f"{path}.{key}"
            if key in properties:
                validate(root, properties[key], item, child_path, errors)
            elif schema.get("additionalProperties") is False:
                errors.append(f"{child_path}: additional property is not allowed")

    if isinstance(value, list):
        item_schema = schema.get("items")
        if isinstance(item_schema, dict):
            for index, item in enumerate(value):
                validate(root, item_schema, item, f"{path}[{index}]", errors)

    if isinstance(value, str):
        if "minLength" in schema and len(value) < int(schema["minLength"]):
            errors.append(f"{path}: string is shorter than {schema['minLength']}")
        if "pattern" in schema and re.search(schema["pattern"], value) is None:
            errors.append(f"{path}: value does not match {schema['pattern']!r}")
        if "format" in schema:
            validate_format(schema["format"], value, path, errors)

    if isinstance(value, (int, float)) and not isinstance(value, bool):
        numeric = float(value)
        if "minimum" in schema and numeric < float(schema["minimum"]):
            errors.append(f"{path}: value is below minimum {schema['minimum']}")
        if "maximum" in schema and numeric > float(schema["maximum"]):
            errors.append(f"{path}: value is above maximum {schema['maximum']}")
        if "exclusiveMinimum" in schema and numeric <= float(schema["exclusiveMinimum"]):
            errors.append(
                f"{path}: value must be greater than {schema['exclusiveMinimum']}"
            )


def load_json(path: Path) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValidationError(f"cannot read {path}: {exc}") from exc


def main() -> int:
    args = parse_args()
    try:
        schema = load_json(args.schema)
        if not isinstance(schema, dict):
            raise ValidationError("schema root must be an object")
        for instance_path in args.instances:
            instance = load_json(instance_path)
            errors: list[str] = []
            validate(schema, schema, instance, "$", errors)
            if errors:
                print(f"{instance_path}: validation failed", file=sys.stderr)
                for error in errors:
                    print(f"  - {error}", file=sys.stderr)
                return 1
            print(f"{instance_path}: valid")
    except ValidationError as exc:
        print(exc, file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
