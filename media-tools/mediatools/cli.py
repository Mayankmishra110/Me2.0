"""CLI: `mediatools tts|stt --in <json> --out <json>` (ARCHITECTURE §2)."""

from __future__ import annotations

import argparse
import sys
import traceback

from mediatools.contract import ContractError, load_json, write_json
from mediatools.stt import transcribe
from mediatools.tts import synthesize


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="mediatools",
        description="Mayank 2.0 media-tools (Kokoro TTS, faster-whisper STT)",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    tts = sub.add_parser("tts", help="Kokoro text-to-speech → wav + word timings")
    tts.add_argument("--in", dest="in_path", required=True, help="input JSON path")
    tts.add_argument("--out", dest="out_path", required=True, help="output JSON path")

    stt = sub.add_parser("stt", help="faster-whisper speech-to-text → segments + words")
    stt.add_argument("--in", dest="in_path", required=True, help="input JSON path")
    stt.add_argument("--out", dest="out_path", required=True, help="output JSON path")

    return parser


def main(argv: list[str] | None = None) -> int:
    parser = _build_parser()
    args = parser.parse_args(argv)
    try:
        inp = load_json(args.in_path)
        if args.command == "tts":
            out = synthesize(inp)
        elif args.command == "stt":
            out = transcribe(inp)
        else:
            print(f"unknown command: {args.command}", file=sys.stderr)
            return 2
        write_json(args.out_path, out)
        return 0
    except ContractError as e:
        print(f"error: {e}", file=sys.stderr)
        return 1
    except Exception as e:  # noqa: BLE001 — surface unexpected failures to the job runner
        print(f"error: {e}", file=sys.stderr)
        traceback.print_exc(file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
