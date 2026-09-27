# Ship queue — owned by ship

Which branches need a PR, where each one stands, and the PR text. Newest at the top.
Remote: `https://github.com/Mayankmishra110/Me2.0` · Compare URL: `https://github.com/Mayankmishra110/Me2.0/compare/main...<branch>`

**Merge order (when push allowed):** merge **M2-101 first** (critical path / foundation for later Go tickets). **M2-206**, **M2-107**, and **M2-208** do not depend on each other or on M2-101 — open in any order; prefer landing 101 before other Go work that touches `internal/config` / `cmd/mayank2`.

**Push held:** do not `git push` / `gh pr create` until Mayank decides whether `Mayankmishra110/Me2.0` stays public. Remote verified: `https://github.com/Mayankmishra110/Me2.0.git`

| Ticket | Branch | QA | SEC | Rebased on main | Checks | PR | State |
|---|---|---|---|---|---|---|---|
| M2-208 | m2/M2-208 | pass | n/a | yes → `4c95713` on main `b7b5f5c` | remotion lint/typecheck/format/test/build pass (no full mp4 re-render) | held — public-repo decision | ready-for-pr |
| M2-101 | m2/M2-101 | pass | pass | yes → `75397aa` on main `bd8f6d8` | go vet + go test pass; gofmt -l lists CRLF-only (autocrlf; content clean — no commit) | held — public-repo decision | ready-for-pr |
| M2-107 | m2/M2-107 | pass | n/a | yes → `96c5b4d` on main `bd8f6d8` | lint/typecheck/test/build pass; format:check fails on CRLF (autocrlf; ignore-cr clean — no commit) | held — public-repo decision | ready-for-pr |
| M2-206 | m2/M2-206 | pass | pass | yes (on prior main tip; re-rebase before push if main moved) | ruff+pytest pass; pre-commit --all pass (see body) | held — public-repo decision | ready-for-pr |

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

### M2-208 — Remotion compositions: explained_60s, myth_vs_fact, top_n
Branch: `m2/M2-208` @ `4c95713` (rebased; was `b978271`) · Worktree: `data/worktrees/m2-208` · Compare: https://github.com/Mayankmishra110/Me2.0/compare/main...m2/M2-208  
**Push held** · Does **not** depend on M2-101 (touches `remotion/` only).

**Goal** First three video formats as Remotion compositions, driven by props (brand kit, beats, voice, word timings, orientation).

**Acceptance**
- [x] Props: brand kit, beats, voice file, word timings, orientation 16:9 or 9:16 — `src/types.ts` + `samples/*.json`.
- [x] Captions per `caption_style`; DESIGN §3 safe zones; thumbnail still composition — unit tests + `thumbnail` 1280×720.
- [x] CLI render with props JSON; sample each format × orientation — fixtures under `fixtures/`; full mp4s gitignored under `out/`.

**Checks** (real output, worktree `data/worktrees/m2-208/remotion`, 2026-09-28)

```
npm run lint
> oxlint src scripts
LINT=0

npm run typecheck
> tsc --noEmit
TYPE=0

npm run format:check
Checking formatting...
All matched files use Prettier code style!
FMT=0

npm test
 Test Files  1 passed (1)
      Tests  6 passed (6)
 Duration  142ms
TEST=0

npm run build
> remotion bundle
○ .../data/worktrees/m2-208/remotion/build
BUILD=0
```

(Full mp4 re-render skipped; fixtures already present. Build = Remotion bundle only.)

**Review** QA: pass — 2026-09-28 — remotion formats×orientations + thumbnail; pure props; lint/typecheck/format/test/build green; full mp4 re-render skipped · SEC: n/a — needs-sec unset / no (fixed argv render scripts; no fetch in compositions)

**Risks / follow-ups**
- Remotion composition IDs use hyphens (Remotion forbids `_` in IDs); folder names keep underscores.
- Full sample mp4 render is heavy; use fixtures for CI-style checks.
- Independent of M2-101 / other queued tickets.

**How to test**
1. `cd data/worktrees/m2-208/remotion && npm install`
2. `npm run lint && npm run typecheck && npm run format:check && npm test && npm run build`
3. Optional: `npm run render` for full sample mp4s (heavy).

**Open PR (after push allowed):**
```
git -C data/worktrees/m2-208 push -u origin m2/M2-208
gh pr create --repo Mayankmishra110/Me2.0 --base main --head m2/M2-208 --title "Remotion compositions: explained_60s, myth_vs_fact, top_n (M2-208)"
```
(Paste this PR body section into `--body`.)

---

### M2-101 — Repo skeleton, config loader, doctor command
Branch: `m2/M2-101` @ `75397aa` (rebased; was `246b6a3`) · Worktree: `data/worktrees/m2-101` · Compare: https://github.com/Mayankmishra110/Me2.0/compare/main...m2/M2-101  
**Push held** · **Merge first** among Go tickets (critical path).

**Goal** `mayank2` binary with subcommands, full-schema config loader, and `doctor` that reports whether this laptop is ready.

**Acceptance**
- [x] Go 1.24+; `cmd/mayank2` subcommands `run`, `status`, `doctor`, `migrate`, `set-pin`, `auth` (stubs OK except doctor).
- [x] `internal/config` loads `config/config.yaml` (+ channels + `.env`); relative paths; clear errors; replaces early Builder-only config.
- [x] `doctor` ✅/❌ probes (ffmpeg, node, uv/python, ollama loopback + models, claude, git, disk, RAM, `.env` key *names* only).
- [x] Table-driven config tests.

**Checks** (real output, worktree `data/worktrees/m2-101`, 2026-09-28)

```
gofmt -l ./cmd/mayank2 ./internal/config
cmd\mayank2\doctor.go
cmd\mayank2\doctor_test.go
cmd\mayank2\main.go
cmd\mayank2\sys_other.go
cmd\mayank2\sys_windows.go
internal\config\channels.go
internal\config\config.go
internal\config\config_test.go
internal\config\envfile.go
GOFMT_EXIT=0
# Note: core.autocrlf=true → working tree CRLF; gofmt -l lists them. Sample
# main.go was CRLF-only (150 CRLF, 0 LF). Did NOT commit gofmt -w (drive-by EOL).
# Ticket paths are content-clean for review purposes; go vet / go test are green.

go vet ./...
VET_EXIT=0

go test ./...
ok  	mayank2/cmd/mayank2	(cached)
ok  	mayank2/internal/config	0.428s
ok  	mayank2/internal/tickets	0.358s
TEST_EXIT=0
```

**Review** QA: pass — 2026-09-28 — re-pass after loopback lock · SEC: pass — 2026-09-28 — loopback lock holds

**Risks / follow-ups**
- Doctor uses `exec.CommandContext` + HTTP to Ollama (loopback-locked).
- Later Go tickets that edit the same packages should rebase after this merges.
- Windows autocrlf vs gofmt -l noise until hooks/EOL policy is fixed.

**How to test**
1. `cd data/worktrees/m2-101`
2. `go test ./...` and `go run ./cmd/mayank2 doctor` (with local tools / Ollama as available)
3. Load path: copy `config/config.example.yaml` → `config/config.yaml` for doctor.

**Open PR (after push allowed):**
```
git -C data/worktrees/m2-101 push -u origin m2/M2-101
gh pr create --repo Mayankmishra110/Me2.0 --base main --head m2/M2-101 --title "Repo skeleton, config loader, doctor command (M2-101)"
```
(Paste this PR body section into `--body`.)

---

### M2-107 — Dashboard shell: Home, Approvals, Logs
Branch: `m2/M2-107` @ `96c5b4d` (rebased; was `774a5e3`) · Worktree: `data/worktrees/m2-107` · Compare: https://github.com/Mayankmishra110/Me2.0/compare/main...m2/M2-107  
**Push held** · Independent of M2-206; does not depend on M2-101.

**Goal** React dashboard skeleton (mobile-first) against MSW mock API; real API later via M2-106.

**Acceptance**
- [x] Vite + React 19 + TS strict + Tailwind + shadcn + TanStack Query + Router; DESIGN §1.3 tokens; dark default.
- [x] Layout: sidebar / bottom tabs ≤640px; top bar status, Pause all, pending count.
- [x] Screens: Home, Approvals, Logs (+ More).
- [x] MSW matching SPEC §4; SSE client with reconnect.
- [x] Vitest Approvals decision flow; builds to `web/dist`.

**Checks** (real output, worktree `data/worktrees/m2-107/web`, 2026-09-28)

```
npm run lint
> oxlint src
LINT=0

npm run typecheck
> tsc --noEmit -p tsconfig.app.json && tsc --noEmit -p tsconfig.node.json
TYPE=0

npm run format:check
Checking formatting...
[warn] ... 37 files ...
Code style issues found in 37 files. Run Prettier with --write to fix.
FMT=1
# Diagnosed: core.autocrlf=true CRLF working copies. Ephemeral prettier --write
# then format:check passed; git diff --ignore-cr-at-eol was empty (EOL only).
# Restored working tree; did NOT commit prettier drive-by.

npm test
 Test Files  1 passed (1)
      Tests  4 passed (4)
TEST=0

npm run build
✓ 2202 modules transformed.
dist/assets/index-BOYIXygY.js    361.57 kB │ gzip: 113.22 kB
✓ built in 506ms
BUILD=0
```

**Review** QA: pass — 2026-09-28 — web shell MSW §4, Approvals tests, DESIGN tokens; needs-sec unset (mock-only) · SEC: n/a — mock dashboard, no secrets/auth/OAuth

**Risks / follow-ups**
- Mock-only until M2-106; SSE/client paths must stay aligned with SPEC §4.
- format:check on Windows with autocrlf is noisy until EOL/`endOfLine` policy is set.
- Independent of M2-206; merge order vs 101 unconstrained by deps (101 first only matters for Go stack).

**How to test**
1. `cd data/worktrees/m2-107/web && npm install`
2. `npm run lint && npm run typecheck && npm test && npm run build`
3. `npm run dev` — exercise Home / Approvals / Logs against MSW.

**Open PR (after push allowed):**
```
git -C data/worktrees/m2-107 push -u origin m2/M2-107
gh pr create --repo Mayankmishra110/Me2.0 --base main --head m2/M2-107 --title "Dashboard shell: Home, Approvals, Logs (M2-107)"
```
(Paste this PR body section into `--body`.)

---

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
