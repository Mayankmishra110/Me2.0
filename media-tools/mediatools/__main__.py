"""Allow `python -m mediatools`."""

from mediatools.cli import main

if __name__ == "__main__":
    raise SystemExit(main())
