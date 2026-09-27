"""JSON load/save helpers for the ARCHITECTURE §2 child-process contract."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any


class ContractError(ValueError):
    """Invalid input JSON or missing required fields."""


def load_json(path: str | Path) -> dict[str, Any]:
    p = Path(path)
    try:
        raw = p.read_text(encoding="utf-8")
    except OSError as e:
        raise ContractError(f"cannot read --in {p}: {e}") from e
    try:
        data = json.loads(raw)
    except json.JSONDecodeError as e:
        raise ContractError(f"invalid JSON in --in {p}: {e}") from e
    if not isinstance(data, dict):
        raise ContractError(f"--in {p} must be a JSON object")
    return data


def write_json(path: str | Path, data: dict[str, Any]) -> None:
    p = Path(path)
    try:
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    except OSError as e:
        raise ContractError(f"cannot write --out {p}: {e}") from e


def require_str(data: dict[str, Any], key: str) -> str:
    if key not in data:
        raise ContractError(f"missing required field: {key}")
    val = data[key]
    if not isinstance(val, str) or not val.strip():
        raise ContractError(f"field {key} must be a non-empty string")
    return val.strip()


def optional_float(data: dict[str, Any], key: str, default: float) -> float:
    if key not in data or data[key] is None:
        return default
    try:
        return float(data[key])
    except (TypeError, ValueError) as e:
        raise ContractError(f"field {key} must be a number") from e
