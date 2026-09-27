"""Unit tests for JSON contract helpers."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from mediatools.contract import ContractError, load_json, require_str, write_json


def test_load_and_write_roundtrip(tmp_path: Path) -> None:
    src = tmp_path / "in.json"
    dst = tmp_path / "out.json"
    src.write_text(json.dumps({"a": 1, "b": "x"}), encoding="utf-8")
    data = load_json(src)
    assert data == {"a": 1, "b": "x"}
    write_json(dst, {"ok": True})
    assert json.loads(dst.read_text(encoding="utf-8")) == {"ok": True}


def test_load_missing_file(tmp_path: Path) -> None:
    with pytest.raises(ContractError, match="cannot read"):
        load_json(tmp_path / "nope.json")


def test_load_invalid_json(tmp_path: Path) -> None:
    p = tmp_path / "bad.json"
    p.write_text("{not-json", encoding="utf-8")
    with pytest.raises(ContractError, match="invalid JSON"):
        load_json(p)


def test_load_strips_utf8_bom(tmp_path: Path) -> None:
    p = tmp_path / "bom.json"
    p.write_bytes(b'\xef\xbb\xbf{"ok": true}')
    assert load_json(p) == {"ok": True}


def test_require_str() -> None:
    assert require_str({"text": " hi "}, "text") == "hi"
    with pytest.raises(ContractError, match="missing"):
        require_str({}, "text")
    with pytest.raises(ContractError, match="non-empty"):
        require_str({"text": "  "}, "text")
