"""CLI contract tests: exit codes and --in/--out wiring (mocked engines)."""

from __future__ import annotations

import json
from pathlib import Path

import numpy as np
import pytest

from mediatools import cli
from mediatools.tts import SAMPLE_RATE

FIXTURE_WAV = Path(__file__).parent / "fixtures" / "tiny.wav"


def test_cli_tts_success(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    in_path = tmp_path / "in.json"
    out_path = tmp_path / "out.json"
    wav_path = tmp_path / "voice.wav"
    in_path.write_text(
        json.dumps(
            {
                "text": "Hello",
                "lang": "en",
                "voice": "af_heart",
                "out_wav": str(wav_path),
            }
        ),
        encoding="utf-8",
    )

    def fake_synthesize(inp):
        wav_path.write_bytes(b"RIFF")  # placeholder; real path checked by contract
        # Write a tiny real wav via soundfile if available; else just claim success
        try:
            import soundfile as sf

            sf.write(str(wav_path), np.zeros(100, dtype=np.float32), SAMPLE_RATE)
        except Exception:
            wav_path.write_bytes(b"not-a-wav")
        return {
            "wav_path": str(wav_path),
            "sample_rate": SAMPLE_RATE,
            "duration_sec": 0.01,
            "lang": "en",
            "voice": "af_heart",
            "words": [{"word": "Hello", "start": 0.0, "end": 0.01}],
        }

    monkeypatch.setattr(cli, "synthesize", fake_synthesize)
    code = cli.main(["tts", "--in", str(in_path), "--out", str(out_path)])
    assert code == 0
    data = json.loads(out_path.read_text(encoding="utf-8"))
    assert data["words"][0]["word"] == "Hello"


def test_cli_stt_success(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    in_path = tmp_path / "in.json"
    out_path = tmp_path / "out.json"
    in_path.write_text(
        json.dumps({"audio_path": str(FIXTURE_WAV), "model_size": "tiny"}),
        encoding="utf-8",
    )

    def fake_transcribe(inp):
        return {
            "language": "en",
            "duration_sec": 0.1,
            "text": "hi",
            "model_size": "tiny",
            "compute_type": "int8",
            "segments": [
                {
                    "id": 0,
                    "start": 0.0,
                    "end": 0.1,
                    "text": "hi",
                    "words": [{"word": "hi", "start": 0.0, "end": 0.1}],
                }
            ],
        }

    monkeypatch.setattr(cli, "transcribe", fake_transcribe)
    code = cli.main(["stt", "--in", str(in_path), "--out", str(out_path)])
    assert code == 0
    data = json.loads(out_path.read_text(encoding="utf-8"))
    assert data["segments"][0]["text"] == "hi"


def test_cli_bad_input_nonzero(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    in_path = tmp_path / "in.json"
    out_path = tmp_path / "out.json"
    in_path.write_text("{}", encoding="utf-8")
    code = cli.main(["tts", "--in", str(in_path), "--out", str(out_path)])
    assert code == 1
    err = capsys.readouterr().err
    assert "error:" in err
    assert not out_path.exists()


def test_cli_missing_in_file(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    code = cli.main(
        ["stt", "--in", str(tmp_path / "missing.json"), "--out", str(tmp_path / "o.json")]
    )
    assert code == 1
    assert "error:" in capsys.readouterr().err
