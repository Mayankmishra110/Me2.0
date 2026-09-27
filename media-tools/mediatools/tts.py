"""Kokoro TTS: text → wav + word timings (EN + HI)."""

from __future__ import annotations

import os
import re
from pathlib import Path
from typing import Any, Callable

import numpy as np

from mediatools.contract import ContractError, optional_float, require_str

SAMPLE_RATE = 24_000

# Input lang → Kokoro lang_code. Voice prefix should match (af_/am_ → a, hf_/hm_ → h).
LANG_TO_KOKORO = {
    "en": "a",
    "en-us": "a",
    "en-gb": "b",
    "hi": "h",
    "hin": "h",
}

DEFAULT_VOICES = {
    "a": "af_heart",
    "b": "bf_emma",
    "h": "hf_alpha",
}


def _offline() -> bool:
    return os.environ.get("MEDIATOOLS_OFFLINE", "").strip() in {"1", "true", "yes"}


def _configure_offline_env() -> None:
    """After models are on disk, refuse hub downloads (ARCHITECTURE: no network at runtime)."""
    if _offline():
        os.environ.setdefault("HF_HUB_OFFLINE", "1")
        os.environ.setdefault("TRANSFORMERS_OFFLINE", "1")


def _split_words(text: str) -> list[str]:
    return [w for w in re.findall(r"\S+", text) if w]


def _proportional_words(
    text: str, duration_sec: float, offset: float = 0.0
) -> list[dict[str, Any]]:
    words = _split_words(text)
    if not words or duration_sec <= 0:
        return []
    weights = [max(len(w), 1) for w in words]
    total = float(sum(weights))
    out: list[dict[str, Any]] = []
    cursor = offset
    for word, weight in zip(words, weights, strict=True):
        span = duration_sec * (weight / total)
        end = cursor + span
        out.append({"word": word, "start": round(cursor, 4), "end": round(end, 4)})
        cursor = end
    if out:
        out[-1]["end"] = round(offset + duration_sec, 4)
    return out


def _tokens_to_words(tokens: Any, chunk_offset: float) -> list[dict[str, Any]]:
    """Build word timings from Kokoro/misaki MToken list (EN has start_ts/end_ts)."""
    if not tokens:
        return []
    words: list[dict[str, Any]] = []
    parts: list[str] = []
    start: float | None = None
    end: float | None = None
    last_end = chunk_offset

    def flush() -> None:
        nonlocal parts, start, end, last_end
        text = "".join(parts).strip()
        if not text:
            parts, start, end = [], None, None
            return
        s = start if start is not None else last_end
        e = end if end is not None else s
        words.append({"word": text, "start": round(s, 4), "end": round(e, 4)})
        last_end = e
        parts, start, end = [], None, None

    for token in tokens:
        text = getattr(token, "text", None) or ""
        if not text:
            continue
        start_ts = getattr(token, "start_ts", None)
        end_ts = getattr(token, "end_ts", None)
        if start is None and start_ts is not None:
            start = chunk_offset + float(start_ts)
        if end_ts is not None:
            end = chunk_offset + float(end_ts)
        parts.append(str(text))
        whitespace = getattr(token, "whitespace", "") or ""
        if whitespace:
            flush()
    flush()
    return words


def synthesize(
    inp: dict[str, Any],
    *,
    pipeline_factory: Callable[..., Any] | None = None,
) -> dict[str, Any]:
    """Run TTS from an input JSON object. Returns the output JSON object."""
    text = require_str(inp, "text")
    lang = require_str(inp, "lang").lower()
    if lang not in LANG_TO_KOKORO:
        raise ContractError(f"unsupported lang {lang!r}; use en or hi")
    kokoro_lang = LANG_TO_KOKORO[lang]
    voice = inp.get("voice")
    if voice is None or (isinstance(voice, str) and not voice.strip()):
        voice = DEFAULT_VOICES[kokoro_lang]
    elif not isinstance(voice, str):
        raise ContractError("field voice must be a string")
    else:
        voice = voice.strip()
    speed = optional_float(inp, "speed", 1.0)
    if speed <= 0:
        raise ContractError("field speed must be > 0")

    out_wav = inp.get("out_wav")
    if out_wav is None:
        raise ContractError("missing required field: out_wav")
    if not isinstance(out_wav, str) or not out_wav.strip():
        raise ContractError("field out_wav must be a non-empty string")
    out_wav_path = Path(out_wav.strip())

    _configure_offline_env()

    factory = pipeline_factory
    if factory is None:
        try:
            from kokoro import KPipeline  # type: ignore[import-untyped]
        except ImportError as e:
            raise ContractError(
                "kokoro is not installed; run: uv sync --extra dev (see README)"
            ) from e

        def factory(lang_code: str) -> Any:
            return KPipeline(lang_code=lang_code, repo_id="hexgrad/Kokoro-82M")

    try:
        pipeline = factory(kokoro_lang)
        generator = pipeline(text, voice=voice, speed=speed, split_pattern=r"\n+")
    except Exception as e:
        if _offline():
            raise ContractError(
                f"TTS failed offline (models missing or unloadable): {e}"
            ) from e
        raise ContractError(f"TTS failed: {e}") from e

    chunks: list[np.ndarray] = []
    words: list[dict[str, Any]] = []
    offset = 0.0

    try:
        for result in generator:
            # Result supports attribute access; tuple unpack also works.
            if hasattr(result, "audio"):
                audio = result.audio
                graphemes = getattr(result, "graphemes", "") or ""
                tokens = getattr(result, "tokens", None)
            else:
                graphemes, _phonemes, audio = result
                tokens = None

            if hasattr(audio, "detach"):
                audio_np = audio.detach().cpu().numpy()
            else:
                audio_np = np.asarray(audio, dtype=np.float32)
            audio_np = np.asarray(audio_np, dtype=np.float32).reshape(-1)
            duration = float(audio_np.shape[0]) / SAMPLE_RATE
            chunks.append(audio_np)

            token_words = _tokens_to_words(tokens, offset)
            if token_words:
                words.extend(token_words)
            else:
                words.extend(_proportional_words(str(graphemes) or text, duration, offset))
            offset += duration
    except Exception as e:
        raise ContractError(f"TTS synthesis failed: {e}") from e

    if not chunks:
        raise ContractError("TTS produced no audio")

    audio_all = np.concatenate(chunks)
    duration_sec = float(audio_all.shape[0]) / SAMPLE_RATE

    out_wav_path.parent.mkdir(parents=True, exist_ok=True)
    try:
        import soundfile as sf

        sf.write(str(out_wav_path), audio_all, SAMPLE_RATE)
    except Exception as e:
        raise ContractError(f"cannot write wav {out_wav_path}: {e}") from e

    return {
        "wav_path": str(out_wav_path.resolve()),
        "sample_rate": SAMPLE_RATE,
        "duration_sec": round(duration_sec, 4),
        "lang": lang,
        "voice": voice,
        "words": words,
    }
