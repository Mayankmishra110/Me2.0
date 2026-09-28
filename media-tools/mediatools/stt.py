"""faster-whisper STT: audio → segments + words (CPU int8)."""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any, Callable

from mediatools.contract import ContractError, require_str

ALLOWED_MODELS = {
    "tiny",
    "tiny.en",
    "base",
    "base.en",
    "small",
    "small.en",
    "medium",
    "medium.en",
    "large-v2",
    "large-v3",
    "distil-large-v3",
}


def _offline() -> bool:
    return os.environ.get("MEDIATOOLS_OFFLINE", "").strip() in {"1", "true", "yes"}


def _configure_offline_env() -> None:
    if _offline():
        os.environ.setdefault("HF_HUB_OFFLINE", "1")
        os.environ.setdefault("TRANSFORMERS_OFFLINE", "1")


def transcribe(
    inp: dict[str, Any],
    *,
    model_factory: Callable[..., Any] | None = None,
) -> dict[str, Any]:
    """Run STT from an input JSON object. Returns the output JSON object."""
    audio_path = require_str(inp, "audio_path")
    path = Path(audio_path)
    if not path.is_file():
        raise ContractError(f"audio_path not found: {path}")

    model_size = require_str(inp, "model_size")
    if model_size not in ALLOWED_MODELS:
        raise ContractError(
            f"unsupported model_size {model_size!r}; "
            f"allowed: {', '.join(sorted(ALLOWED_MODELS))}"
        )

    language = inp.get("language")
    if language is not None:
        if not isinstance(language, str) or not language.strip():
            raise ContractError("field language must be a non-empty string or omitted")
        language = language.strip().lower()

    compute_type = inp.get("compute_type", "int8")
    if not isinstance(compute_type, str) or not compute_type.strip():
        raise ContractError("field compute_type must be a string")
    compute_type = compute_type.strip()
    # Ticket: CPU int8. Allow override for tests only via explicit input.
    if compute_type != "int8" and os.environ.get("MEDIATOOLS_ALLOW_COMPUTE") != "1":
        raise ContractError("compute_type must be int8 (CPU)")

    _configure_offline_env()

    factory = model_factory
    if factory is None:
        try:
            from faster_whisper import WhisperModel  # type: ignore[import-untyped]
        except ImportError as e:
            raise ContractError(
                "faster-whisper is not installed; run: uv sync --extra dev (see README)"
            ) from e

        def factory(size: str, ctype: str) -> Any:
            # device=cpu, compute_type=int8 per D16 / ticket.
            return WhisperModel(size, device="cpu", compute_type=ctype)

    try:
        model = factory(model_size, compute_type)
        segments_iter, info = model.transcribe(
            str(path),
            language=language,
            word_timestamps=True,
            vad_filter=True,
        )
    except Exception as e:
        if _offline():
            raise ContractError(
                f"STT failed offline (models missing or unloadable): {e}"
            ) from e
        raise ContractError(f"STT failed: {e}") from e

    segments_out: list[dict[str, Any]] = []
    full_parts: list[str] = []
    duration_sec = float(getattr(info, "duration", 0.0) or 0.0)
    detected = getattr(info, "language", language) or language

    try:
        for i, seg in enumerate(segments_iter):
            words_out: list[dict[str, Any]] = []
            for w in getattr(seg, "words", None) or []:
                words_out.append(
                    {
                        "word": (getattr(w, "word", "") or "").strip(),
                        "start": round(float(getattr(w, "start", 0.0) or 0.0), 4),
                        "end": round(float(getattr(w, "end", 0.0) or 0.0), 4),
                    }
                )
            text = (getattr(seg, "text", "") or "").strip()
            full_parts.append(text)
            segments_out.append(
                {
                    "id": i,
                    "start": round(float(getattr(seg, "start", 0.0) or 0.0), 4),
                    "end": round(float(getattr(seg, "end", 0.0) or 0.0), 4),
                    "text": text,
                    "words": words_out,
                }
            )
            if segments_out:
                duration_sec = max(duration_sec, float(segments_out[-1]["end"]))
    except Exception as e:
        raise ContractError(f"STT decode failed: {e}") from e

    return {
        "language": detected,
        "duration_sec": round(duration_sec, 4),
        "text": " ".join(p for p in full_parts if p).strip(),
        "model_size": model_size,
        "compute_type": compute_type,
        "segments": segments_out,
    }
