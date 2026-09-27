# P1 Foundation — phase audit index and PR body

Phase branch: `phase/p1-foundation` → `main` (D23).
Compare / open PR: https://github.com/Mayankmishra110/Me2.0/compare/main...phase/p1-foundation

Everything below the line is the ready-to-paste PR body (from `.github/pull_request_template.md`).
Audit docs sit next to this file, one per ticket.

**Before merging, see "Blocking before merge" at the top of the PR body.**

---

## Phase / feature
`phase/p1-foundation` → `main`: first P1 foundation slice. It adds the Go CLI with config and doctor, the web dashboard shell on a mock API, and the media-tools TTS/STT CLIs.

### Blocking before merge
1. **`go test ./...` fails on the merged tree** (`internal/tickets` `TestRepoTicketsParse`: `tickets/M2-111.md: no frontmatter`). The phase branch has a UTF-8 BOM in `tickets/M2-111.md`. `main` already fixed it in `254176d fix(M2-111): strip UTF-8 BOM from ticket`, but `main` has not been merged into the phase branch yet. It is behind by `254176d` and `b057177`. Fix: in `data/worktrees/phase-p1`, run `git merge main`, then re-run `go test ./...`. Ship did not do it in this session because the sync merge was refused by the session's permission policy.
2. **The web checks were not re-run on the merged tree.** `web/node_modules` is missing and `npm ci` was refused by the session's permission policy. The `web/` tree is identical to the QA-passed `m2/M2-107` (`git diff --stat m2/M2-107 -- web` is empty). Re-run the web line under "Checks" before merging.

## Features in this PR
| Ticket | Role | Branch | Merge commit | Audit doc | Reviews |
|---|---|---|---|---|---|
| M2-101 Repo skeleton, config loader, doctor | be | `m2/M2-101` | `db0f66f` | [M2-101.md](M2-101.md) | QA pass · SEC pass (after `changes` → loopback fix `b1508d3`) |
| M2-107 Dashboard shell: Home, Approvals, Logs | fe | `m2/M2-107` | `c859184` | [M2-107.md](M2-107.md) | QA pass · SEC n/a (`needs-sec: no`) |
| M2-206 media-tools: Kokoro TTS and faster-whisper STT | ai | `m2/M2-206` | `fb3f404` | [M2-206.md](M2-206.md) | QA pass · SEC pass |

Verdict lines from the tickets:
- M2-101 `QA: pass — 2026-09-28 — re-pass after loopback lock` · `SEC: pass — 2026-09-28 — loopback lock holds`
- M2-107 `QA: pass — 2026-09-28 — web shell: MSW §4 paths, Approvals decision + double-submit tests, DESIGN tokens/dark/layout; lint/typecheck/format/test/build green; needs-sec unset (mock-only).`
- M2-206 `QA: pass — 2026-09-28 — media-tools TTS/STT JSON contract, offline flags, ruff+pytest (22 pass / 3 skip); live models not re-run; needs-sec left for sec.` · `SEC: pass — 2026-09-28 — fixed CLI argv, no shell from JSON, offline hub flags, real deps (kokoro/faster-whisper/soundfile/numpy); no secrets in README; no Claude/D18`

### Still in progress on other branches (not in this PR)
| Ticket | Role / owner | Where | Status |
|---|---|---|---|
| M2-102 SQLite + migrations + schema | be (Cursor) | `m2/M2-102` | in-progress |
| M2-111 LLM router + providers | ai (Cursor) | `m2/M2-111` | in-progress |
| M2-208 Remotion compositions | fe (Cursor) | `m2/M2-208` | in-progress |
| M2-109 Windows build + install-task | be-2 | `m2/M2-109` | in-progress |
| M2-207 Stock visuals with license records | be-3 | `m2/M2-207` | in-progress |
| M2-114 Run with only the keys you have (D24) | — | not started | todo (depends on M2-111) |

## Commits
`git log --oneline main..phase/p1-foundation` (before this audit's two bookkeeping commits):
```
fb3f404 Merge M2-206 (ai): media-tools: Kokoro TTS and faster-whisper STT
c859184 Merge M2-107 (fe): Dashboard shell: Home, Approvals, Logs
db0f66f Merge M2-101 (be): Repo skeleton, config loader, doctor
96c5b4d chore(M2-107): qa review
df92124 feat(M2-107): dashboard shell with MSW mock
75397aa chore(M2-101): sec re-review
a872a08 chore(M2-101): qa re-pass
b1508d3 fix(M2-101): lock ollama base_url to loopback
3317b9a chore(M2-101): sec review
442e040 chore(M2-101): qa review
d21f4ea chore(M2-101): handoff in-review with check notes
67399ab feat(M2-101): mayank2 CLI stubs and doctor command
f2da6c5 feat(M2-101): full config loader and Go 1.24+ module
973fae1 chore(M2-206): sec review
432c825 chore(M2-206): qa review
247d9b7 chore(M2-206): correct pytest count in Notes
f560a20 chore(M2-206): mark in-review with check notes
5c8fea8 fix(M2-206): accept UTF-8 BOM in --in JSON
c81e5d8 test(M2-206): fixture WAV and mocked TTS/STT contract tests
dd5dfae feat(M2-206): scaffold mediatools TTS/STT CLIs
```
Plus, on top: `docs(crew): P1 spec audit for M2-101, M2-107, M2-206` and `chore(crew): mark M2-101, M2-107, M2-206 done; ready dependants`.

## Checks (real output)
Merged tree `phase/p1-foundation` at `fb3f404`, 2026-09-28, Windows 11. Each exit code is the command's own, not a pipe's.

**Go** (Go 1.27):
```
$ go version
go version go1.27.0 windows/amd64
exit=0
$ gofmt -l .
exit=0
$ go vet ./...
exit=0
$ go test -count=1 ./...
ok  	mayank2/cmd/mayank2	0.688s
ok  	mayank2/internal/config	0.654s
--- FAIL: TestRepoTicketsParse (0.00s)
    tickets_test.go:86: ..\..\tickets\M2-111.md: no frontmatter
FAIL
FAIL	mayank2/internal/tickets	0.644s
FAIL
exit=1
```
The failure is the M2-111 ticket BOM. It is already fixed on `main` (`254176d`) and needs `git merge main` into the phase branch. No Go code in this PR fails.

**web/** (`npm run format:check && npm run lint && npm run typecheck && npm test && npm run build`):
```
NOT RUN on the merged tree: web/node_modules missing; `npm ci` refused by the session permission policy.
web/ is byte-identical to m2/M2-107 (git diff --stat m2/M2-107 -- web → empty).
QA output on m2/M2-107 (from the ticket):
  lint (oxlint src)        exit 0, no findings
  typecheck                exit 0
  format:check             All matched files use Prettier code style!
  test (vitest run)        Test Files 1 passed (1) · Tests 4 passed (4)
  build (tsc -b && vite build)  ✓ built in 616ms → web/dist
```

**media-tools/** (uv 0.11.16, fresh `.venv`):
```
$ uv run ruff check .
error: Failed to spawn: `ruff`
  Caused by: program not found
exit=2
$ uv run pytest -q
error: Failed to spawn: `pytest`
  Caused by: program not found
exit=2
$ uv run --extra dev ruff check .
All checks passed!
exit=0
$ uv run --extra dev pytest -q
.........sss.............                                                [100%]
22 passed, 3 skipped in 0.60s
exit=0
```
`ruff` and `pytest` are an optional extra (`[project.optional-dependencies] dev`), so the bare `uv run …` form fails on a fresh checkout. The README's `uv sync --extra dev` works around it. The code itself is green. Follow-up: move them to `[dependency-groups] dev`.

## Config and keys
- **No new `.env` keys and no new `config.yaml` keys.** `internal/config` now loads the full existing `config/config.example.yaml` schema plus `config/channels/*.yaml` and `.env`.
- `llm.providers.*.base_url` for `kind: ollama` must be loopback (`127.0.0.1`, `::1`, `localhost`), otherwise config load fails. This was a SEC fix.
- `doctor` falls back to `config/config.example.yaml` when `config/config.yaml` is missing.
- What works with which keys: **nothing in this PR needs a key to work.** Doctor lists the 24 `.env.example` key names as present or missing (values never printed), but today it reports every missing key as ❌ and exits 1. This conflicts with **D24** and is tracked in **M2-114**.
- `web/`: `VITE_USE_MSW=false` (build-time) turns the mock API off. The mock is **on by default, including in `vite build`**.
- `media-tools/`: `MEDIATOOLS_OFFLINE=1` (no hub calls after models are downloaded), `MEDIATOOLS_LIVE=1` (live tests), `MEDIATOOLS_ALLOW_COMPUTE=1` (tests only). The first run downloads Kokoro-82M and the requested Whisper model. espeak-ng is needed for HI.

## Risks and follow-ups
1. **D24 / M2-114:** doctor fails on every missing `.env` key. It should show ⚪ and not fail. Also, an empty `KEY=` counts as "present" (`EnvKeyPresent` uses `LookupEnv`), so `cp .env.example .env` turns every key ✅. Fold both into M2-114.
2. **MSW in production (M2-107):** `web/src/main.tsx` starts the mock worker unless `VITE_USE_MSW=false`. When M2-106 embeds `web/dist`, gate it on `import.meta.env.DEV`.
3. **media-tools dev deps (M2-206):** the pre-commit hook and the PR template run `uv run ruff` / `uv run pytest`, which fail in a fresh worktree until `uv sync --extra dev`. Move them to `[dependency-groups] dev`.
4. **media-tools runtime contract for the Go side (M2-209):** always set `MEDIATOOLS_OFFLINE=1`, register as the `heavy` class, use a timeout, and pass only paths under `data/media/work/<job-id>/`.
5. **Phase branch is behind `main`** by 2 commits (`254176d`, `b057177`). Merge `main` before opening the PR. That fixes the `internal/tickets` failure.
6. **DESIGN drift in the shell:** the mobile tabs are Home/Approvals/Logs/More, not Home/Approvals/Pipeline/Builder/More. Inline title/description edit and a light theme toggle are missing. 9:16 previews render letterboxed. The status pill shows "Degraded" while health loads.
7. **Deferred scope:** `config/formats/*.yaml` loading (M2-201). The `retry` and `approve` CLI commands (queue and approval tickets). Tests for `sse.ts`, Home, Logs and the doctor report path. `web/README.md` is still the Vite template.
8. HI TTS word timings are proportional (open question in M2-206). The default voices are `af_heart` / `hf_alpha` until the brand kits set `voice_id`.
9. `go test -race` was not run (no CGO). No Python dependency vulnerability audit was run (`pip-audit` not installed). `govulncheck` passed for Go (SEC, M2-101).

## How to test
```
git fetch origin && git switch phase/p1-foundation
git merge main                       # picks up the M2-111 BOM fix (see Blocking)

# Go (Git Bash on this laptop: export GOROOT="/c/Program Files/Go" PATH="/c/Program Files/Go/bin:$PATH")
gofmt -l . && go vet ./... && go test -count=1 ./...
go run ./cmd/mayank2 doctor          # ✅/❌ per tool, disk, RAM, channels, env key names; exit 1 until tools/keys exist
go run ./cmd/mayank2 status          # stub: "not implemented yet"

# Web
cd web && npm ci
npm run format:check && npm run lint && npm run typecheck && npm test && npm run build
npm run dev                          # http://localhost:5173 on the MSW mock; try Approvals → Approve/Reject/Redo, resize ≤640px for bottom tabs
cd ..

# media-tools
cd media-tools && uv sync --extra dev
uv run ruff check . && uv run pytest -q
# optional live smoke (downloads models): see media-tools/README.md "Smoke test"
```

🤖 Generated with [Claude Code](https://claude.com/claude-code)
