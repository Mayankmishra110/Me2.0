"""TTS unit tests with a fake Kokoro pipeline (no model download)."""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path

import numpy as np
import pytest

from mediatools.contract import ContractError
from mediatools.tts import SAMPLE_RATE, _proportional_words, _tokens_to_words, synthesize


@dataclass
class FakeToken:
    text: str
    whitespace: str = " "
    start_ts: float | None = 0.0
    end_ts: float | None = 0.1


class FakeResult:
    def __init__(self, graphemes: str, audio: np.ndarray, tokens: list[FakeToken] | None):
        self.graphemes = graphemes
        self.phonemes = ""
        self.audio = audio
        self.tokens = tokens


def _factory_with(
    results: list[FakeResult],
) -> Callable[..., Callable[..., object]]:
    def factory(_lang: str):
        def pipeline(text, voice=None, speed=1.0, split_pattern=None):  # noqa: ARG001
            return iter(results)

        return pipeline

    return factory


def test_proportional_words() -> None:
    words = _proportional_words("one two", 1.0)
    assert len(words) == 2
    assert words[0]["word"] == "one"
    assert words[0]["start"] == 0.0
    assert words[-1]["end"] == 1.0


def test_tokens_to_words() -> None:
    tokens = [
        FakeToken("Hel", whitespace="", start_ts=0.0, end_ts=0.1),
        FakeToken("lo", whitespace=" ", start_ts=0.1, end_ts=0.2),
        FakeToken("world", whitespace="", start_ts=0.3, end_ts=0.5),
    ]
    words = _tokens_to_words(tokens, chunk_offset=1.0)
    assert words[0]["word"] == "Hello"
    assert words[0]["start"] == 1.0
    assert words[1]["word"] == "world"
    assert words[1]["start"] == 1.3


def test_synthesize_en_writes_wav(tmp_path: Path) -> None:
    wav = tmp_path / "voice.wav"
    audio = np.zeros(SAMPLE_RATE // 10, dtype=np.float32)  # 0.1 s
    tokens = [
        FakeToken("Hello", whitespace=" ", start_ts=0.0, end_ts=0.05),
        FakeToken("world", whitespace="", start_ts=0.05, end_ts=0.1),
    ]
    out = synthesize(
        {
            "text": "Hello world",
            "lang": "en",
            "voice": "af_heart",
            "out_wav": str(wav),
        },
        pipeline_factory=_factory_with([FakeResult("Hello world", audio, tokens)]),
    )
    assert wav.is_file()
    assert out["sample_rate"] == SAMPLE_RATE
    assert out["duration_sec"] == pytest.approx(0.1, abs=1e-3)
    assert [w["word"] for w in out["words"]] == ["Hello", "world"]
    assert out["voice"] == "af_heart"


def test_synthesize_hi_proportional_fallback(tmp_path: Path) -> None:
    wav = tmp_path / "hi.wav"
    audio = np.zeros(SAMPLE_RATE // 5, dtype=np.float32)
    out = synthesize(
        {
            "text": "नमस्ते दोस्त",
            "lang": "hi",
            "voice": "hf_alpha",
            "out_wav": str(wav),
        },
        pipeline_factory=_factory_with([FakeResult("नमस्ते दोस्त", audio, None)]),
    )
    assert wav.is_file()
    assert len(out["words"]) == 2
    assert out["words"][0]["word"] == "नमस्ते"


def test_synthesize_rejects_bad_lang(tmp_path: Path) -> None:
    with pytest.raises(ContractError, match="unsupported lang"):
        synthesize(
            {"text": "x", "lang": "fr", "out_wav": str(tmp_path / "a.wav")},
            pipeline_factory=_factory_with([]),
        )


def test_synthesize_empty_audio(tmp_path: Path) -> None:
    with pytest.raises(ContractError, match="no audio"):
        synthesize(
            {
                "text": "hi",
                "lang": "en",
                "out_wav": str(tmp_path / "a.wav"),
            },
            pipeline_factory=_factory_with([]),
        )


def test_default_voice(tmp_path: Path) -> None:
    wav = tmp_path / "v.wav"
    audio = np.zeros(100, dtype=np.float32)
    out = synthesize(
        {"text": "x", "lang": "en", "out_wav": str(wav)},
        pipeline_factory=_factory_with([FakeResult("x", audio, None)]),
    )
    assert out["voice"] == "af_heart"
