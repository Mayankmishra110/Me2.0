"""STT unit tests with a fake Whisper model (no model download)."""

from __future__ import annotations

import os
from pathlib import Path
from types import SimpleNamespace

import pytest

from mediatools.contract import ContractError
from mediatools.stt import transcribe

FIXTURE_WAV = Path(__file__).parent / "fixtures" / "tiny.wav"


class FakeModel:
    def __init__(self, segments: list, info: SimpleNamespace):
        self._segments = segments
        self._info = info

    def transcribe(self, audio_path, language=None, word_timestamps=True, vad_filter=True):
        assert Path(audio_path).is_file()
        return iter(self._segments), self._info


def _factory(segments: list, info: SimpleNamespace):
    def factory(size: str, ctype: str):
        assert size == "tiny"
        assert ctype == "int8"
        return FakeModel(segments, info)

    return factory


def test_transcribe_segments_and_words() -> None:
    assert FIXTURE_WAV.is_file()
    words = [
        SimpleNamespace(word=" Hello", start=0.0, end=0.2),
        SimpleNamespace(word=" world", start=0.2, end=0.5),
    ]
    seg = SimpleNamespace(start=0.0, end=0.5, text=" Hello world", words=words)
    info = SimpleNamespace(language="en", duration=0.5)
    out = transcribe(
        {"audio_path": str(FIXTURE_WAV), "model_size": "tiny"},
        model_factory=_factory([seg], info),
    )
    assert out["language"] == "en"
    assert out["model_size"] == "tiny"
    assert out["compute_type"] == "int8"
    assert out["text"] == "Hello world"
    assert len(out["segments"]) == 1
    assert [w["word"] for w in out["segments"][0]["words"]] == ["Hello", "world"]


def test_missing_audio(tmp_path: Path) -> None:
    with pytest.raises(ContractError, match="not found"):
        transcribe(
            {"audio_path": str(tmp_path / "missing.wav"), "model_size": "tiny"},
            model_factory=_factory([], SimpleNamespace(language="en", duration=0)),
        )


def test_bad_model_size() -> None:
    with pytest.raises(ContractError, match="unsupported model_size"):
        transcribe(
            {"audio_path": str(FIXTURE_WAV), "model_size": "huge"},
            model_factory=_factory([], SimpleNamespace(language="en", duration=0)),
        )


def test_rejects_non_int8() -> None:
    with pytest.raises(ContractError, match="int8"):
        transcribe(
            {
                "audio_path": str(FIXTURE_WAV),
                "model_size": "tiny",
                "compute_type": "float16",
            },
            model_factory=_factory([], SimpleNamespace(language="en", duration=0)),
        )


def test_offline_sets_hub_env_and_labels_failure(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv("MEDIATOOLS_OFFLINE", "1")
    monkeypatch.delenv("HF_HUB_OFFLINE", raising=False)
    monkeypatch.delenv("TRANSFORMERS_OFFLINE", raising=False)

    def boom(size: str, ctype: str):
        raise RuntimeError("hub unreachable")

    with pytest.raises(ContractError, match="offline"):
        transcribe(
            {"audio_path": str(FIXTURE_WAV), "model_size": "tiny"},
            model_factory=boom,
        )
    assert os.environ.get("HF_HUB_OFFLINE") == "1"
    assert os.environ.get("TRANSFORMERS_OFFLINE") == "1"
