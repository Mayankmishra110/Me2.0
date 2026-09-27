"""Optional live tests — skipped unless MEDIATOOLS_LIVE=1 and models are present."""

from __future__ import annotations

import json
import os
from pathlib import Path

import pytest

from mediatools.stt import transcribe
from mediatools.tts import synthesize

pytestmark = pytest.mark.live

LIVE = os.environ.get("MEDIATOOLS_LIVE", "").strip() in {"1", "true", "yes"}


@pytest.mark.skipif(not LIVE, reason="set MEDIATOOLS_LIVE=1 to run real Kokoro/Whisper")
def test_live_tts_en(tmp_path: Path) -> None:
    wav = tmp_path / "en.wav"
    out = synthesize(
        {
            "text": "Hello from Mayank two.",
            "lang": "en",
            "voice": "af_heart",
            "speed": 1.0,
            "out_wav": str(wav),
        }
    )
    assert wav.is_file()
    assert out["duration_sec"] > 0
    assert out["words"]


@pytest.mark.skipif(not LIVE, reason="set MEDIATOOLS_LIVE=1 to run real Kokoro/Whisper")
def test_live_tts_hi(tmp_path: Path) -> None:
    wav = tmp_path / "hi.wav"
    out = synthesize(
        {
            "text": "नमस्ते, यह एक परीक्षा है।",
            "lang": "hi",
            "voice": "hf_alpha",
            "out_wav": str(wav),
        }
    )
    assert wav.is_file()
    assert out["duration_sec"] > 0


@pytest.mark.skipif(not LIVE, reason="set MEDIATOOLS_LIVE=1 to run real Kokoro/Whisper")
def test_live_stt_roundtrip(tmp_path: Path) -> None:
    wav = tmp_path / "round.wav"
    synthesize(
        {
            "text": "Testing whisper.",
            "lang": "en",
            "voice": "af_heart",
            "out_wav": str(wav),
        }
    )
    out = transcribe({"audio_path": str(wav), "model_size": "tiny", "language": "en"})
    assert out["segments"]
    (tmp_path / "stt.json").write_text(json.dumps(out, indent=2), encoding="utf-8")
