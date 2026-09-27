# Ship queue — owned by ship

Which branches need a PR, where each one stands, and the PR text. Newest at the top.
Remote: `https://github.com/Mayankmishra110/Me2.0` · Compare URL: `https://github.com/Mayankmishra110/Me2.0/compare/main...<branch>`

| Ticket | Branch | QA | SEC | Rebased on main | Checks | PR | State |
|---|---|---|---|---|---|---|---|
| M2-206 | m2/M2-206 | pass | pass | yes (already on main tip f972837; `git rebase main` no-op) | ruff+pytest pass; pre-commit --all pass (see body) | held — do not push until public-repo decision | ready-for-pr |

States: `waiting-review` → `ready-for-pr` → `pr-open` → `merged` (or `sent-back`).

<!--
Entry template — copy under "PR bodies", fill in, and add a row above:

### M2-xxx — <title>
Branch: m2/M2-xxx · Compare: https://github.com/Mayankmishra110/Me2.0/compare/main...m2/M2-xxx

**Goal** …
**Acceptance** - [x] … — proved by …
**Checks** (real output)
```
…
```
**Review** QA: … · SEC: …
**Risks / follow-ups** …
**How to test** …

🤖 Generated with [Claude Code](https://claude.com/claude-code)
-->

## PR bodies

### M2-206 — media-tools: Kokoro TTS and faster-whisper STT
Branch: `m2/M2-206` @ `973fae1` · Worktree: `data/worktrees/m2-206` · Compare: https://github.com/Mayankmishra110/Me2.0/compare/main...m2/M2-206  
**Push held:** do not `git push` / `gh pr create` until Mayank decides whether `Mayankmishra110/Me2.0` stays public. Remote verified: `https://github.com/Mayankmishra110/Me2.0.git`

**Goal** Python CLIs for Kokoro TTS and faster-whisper STT with the ARCHITECTURE §2 JSON in/out contract (`mediatools tts|stt --in/--out`).

**Acceptance**
- [x] uv project; `uv run mediatools tts --in in.json --out out.json` → wav + word timings; EN and HI voices — live EN/HI runs in ticket Notes.
- [x] `uv run mediatools stt --in in.json --out out.json` → segments + words (faster-whisper, CPU int8, model size from input) — live STT in ticket Notes.
- [x] Non-zero exit with message on failure; no network at runtime after models downloaded — unit tests + `MEDIATOOLS_OFFLINE` / hub offline flags.
- [x] README with setup and smoke test.

**Checks** (real output, worktree `data/worktrees/m2-206`, 2026-09-28)

```
# media-tools (SPEC §7 for this ticket)
uv run ruff check .
All checks passed!
RUFF_EXIT=0

uv run pytest
============================= test session starts =============================
platform win32 -- Python 3.12.13, pytest-9.1.1, pluggy-1.6.0
rootdir: .../data/worktrees/m2-206/media-tools
configfile: pyproject.toml
testpaths: tests
plugins: anyio-4.15.1
collected 25 items

tests\test_cli.py ....                                                   [ 16%]
tests\test_contract.py .....                                             [ 36%]
tests\test_live.py sss                                                   [ 48%]
tests\test_stt.py .....                                                  [ 68%]
tests\test_tts.py ........                                               [100%]

======================== 22 passed, 3 skipped in 0.10s ========================
PYTEST_EXIT=0

# pre-commit --all via Git for Windows (not WSL):
#   "C:\Program Files\Git\bin\sh.exe" .githooks/pre-commit --all
# First run: gofmt FAIL on internal/config|tickets — working-tree CRLF with
# core.autocrlf=true; git blobs identical to main (outside M2-206 touches).
# After ephemeral gofmt -w (not committed; restored), re-run:

== forbidden paths
ok

== secret scan
ok

== gofmt
ok

== go vet

== go test
?   	mayank2/internal/config	[no test files]
ok  	mayank2/internal/tickets	(cached)

== media-tools: ruff
All checks passed!

== media-tools: pytest
.........sss.............                                                [100%]
22 passed, 3 skipped in 0.11s

pre-commit: all checks passed
HOOK_EXIT=0
```

**Review** QA: pass — 2026-09-28 — media-tools TTS/STT JSON contract, offline flags, ruff+pytest (22 pass / 3 skip); live models not re-run · SEC: pass — 2026-09-28 — fixed CLI argv, no shell from JSON, offline hub flags, real deps; no secrets; no Claude/D18

**Risks / follow-ups**
- First-run model download is heavy (16 GB laptop; register as `heavy` when Go shells these).
- HI word timings are proportional (Kokoro tokens EN-oriented); channel `voice_id` still TBD in DESIGN.
- Do not push until public-vs-private decision on the GitHub remote.

**How to test**
1. `cd data/worktrees/m2-206/media-tools && uv sync`
2. `uv run ruff check . && uv run pytest`
3. Optional live: `MEDIATOOLS_LIVE=1 uv run pytest tests/test_live.py` (needs models + espeak-ng for TTS).
4. Smoke: see `media-tools/README.md` for sample `--in` JSON for `tts` / `stt`.

**Open PR (after push allowed)** — from main checkout, after `git -C data/worktrees/m2-206 push -u origin m2/M2-206`:

```
gh pr create --repo Mayankmishra110/Me2.0 --base main --head m2/M2-206 --title "media-tools: Kokoro TTS and faster-whisper STT (M2-206)" --body-file - < crew/SHIP_QUEUE.md
```

(Prefer copying this PR body section into the `gh pr create` body; `--body-file` on the whole queue is a fallback.)
