# Mayank 2.0 — media-tools

Kokoro TTS and faster-whisper STT as short-lived child processes. Contract matches
[ARCHITECTURE §2](../docs/ARCHITECTURE.md): JSON in, JSON out, non-zero exit on failure.

Heavy resource class (16 GB RAM): only one TTS/STT/render/local-LLM job at a time.

## Setup

```powershell
$env:Path = [System.Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [System.Environment]::GetEnvironmentVariable('Path','User')
cd media-tools
uv sync --extra dev
```

System deps:

- **espeak-ng** (Kokoro non-English / OOD phonemes). Windows: `winget install eSpeak-NG.eSpeak-NG` or install from [espeak-ng releases](https://github.com/espeak-ng/espeak-ng/releases).
- First run downloads Kokoro weights and the Whisper model size you request. After that, set `MEDIATOOLS_OFFLINE=1` so Hugging Face hub calls are refused.

## CLI

```powershell
uv run mediatools tts --in in.json --out out.json
uv run mediatools stt --in in.json --out out.json
```

Exit `0` = success (`out.json` written). Exit `!= 0` = failure; message on stderr.

### TTS input

```json
{
  "text": "Hello from Mayank two.",
  "lang": "en",
  "voice": "af_heart",
  "speed": 1.0,
  "out_wav": "C:/path/to/voice.wav"
}
```

- `lang`: `en` (Kokoro `a`) or `hi` (Kokoro `h`).
- `voice`: optional; defaults `af_heart` (EN), `hf_alpha` (HI).
- `out_wav`: required path for the 24 kHz mono WAV.

### TTS output

```json
{
  "wav_path": "...",
  "sample_rate": 24000,
  "duration_sec": 1.23,
  "lang": "en",
  "voice": "af_heart",
  "words": [{"word": "Hello", "start": 0.0, "end": 0.4}]
}
```

EN uses Kokoro token timestamps when present. HI falls back to proportional word spans over chunk duration.

### STT input

```json
{
  "audio_path": "C:/path/to/audio.wav",
  "model_size": "tiny",
  "language": "en"
}
```

- `model_size`: Whisper size (`tiny`, `base`, `small`, …). CPU `int8` only.
- `language`: optional hint (`en` / `hi`); omit to auto-detect.

### STT output

```json
{
  "language": "en",
  "duration_sec": 1.23,
  "text": "Hello world",
  "model_size": "tiny",
  "compute_type": "int8",
  "segments": [
    {
      "id": 0,
      "start": 0.0,
      "end": 1.0,
      "text": "Hello world",
      "words": [{"word": "Hello", "start": 0.0, "end": 0.4}]
    }
  ]
}
```

## Smoke test (offline unit)

```powershell
cd media-tools
uv run ruff check .
uv run pytest
```

Live models (downloads on first run; needs network + RAM):

```powershell
$env:MEDIATOOLS_LIVE = "1"
uv run pytest -m live -s
```

Example manual smoke after models are cached:

```powershell
@'
{"text":"Hello.","lang":"en","voice":"af_heart","out_wav":"tmp_en.wav"}
'@ | Set-Content -Encoding utf8 smoke_tts_in.json
uv run mediatools tts --in smoke_tts_in.json --out smoke_tts_out.json

@'
{"audio_path":"tmp_en.wav","model_size":"tiny","language":"en"}
'@ | Set-Content -Encoding utf8 smoke_stt_in.json
uv run mediatools stt --in smoke_stt_in.json --out smoke_stt_out.json
```

Then set `$env:MEDIATOOLS_OFFLINE = "1"` for subsequent runs with no network.
