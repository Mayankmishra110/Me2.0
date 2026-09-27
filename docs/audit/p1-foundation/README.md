# P1 Foundation — phase audit index and PR body

Phase branch: `phase/p1-foundation` → `main` (D23).
Compare / open PR: https://github.com/Mayankmishra110/Me2.0/compare/main...phase/p1-foundation

Everything below the line is the ready-to-paste PR body (from `.github/pull_request_template.md`).
Audit docs sit next to this file, one per ticket.

---

## Phase / feature
`phase/p1-foundation` → `main`: batch 1 (8 tickets) plus batch 2 as tickets merge. Batch 1: Go CLI with
config, `doctor` and SQLite; LLM router; dashboard shell; media-tools TTS/STT; stock visuals; Remotion;
Windows start-at-logon. Batch 2 so far: durable job queue and worker pool (**M2-103**, merged
`18c7910`). Events log / SSE bus (**M2-104**, merged `e520b9e`). Storage retention + R2
(**M2-213**, merged `7d0c3fa`). Single-key / Gemini doctor + `llm ask` (**M2-114**, merged `9c0b970`).
Channel + format config (**M2-201**, merged `3f1293e`). Telegram bot (**M2-105**, merged `d7c2558`).
HTTP API + auth + embedded dashboard (**M2-106**, merged `96adf6a`). DPAPI token vault + OAuth helper
(**M2-110**, merged `880b852`). Niche Scout (**M2-202**, merged `560e002`). Research brief
(**M2-203**, merged `1491844`). Scheduler + daily summary (**M2-108**, merged `a485988`).
Script writer native EN/HI (**M2-204**, merged `d69dcae`). Render long/short/thumb/subs
(**M2-209**, merged `878ccb7`). Compliance script gates G1–G7 (**M2-205**, merged `a76a141`).
Final gates F1–F6 + approval flow (**M2-210**, code `9646b4b`, QA/SEC `d283fb4`).

## Features in this PR
| Ticket | Role | Branch | Merge commit | Audit doc | Reviews |
|---|---|---|---|---|---|
| M2-101 Repo skeleton, config loader, doctor | be | `m2/M2-101` | `db0f66f` | [M2-101.md](M2-101.md) | QA pass · SEC pass (after `changes` → loopback fix) |
| M2-107 Dashboard shell: Home, Approvals, Logs | fe | `m2/M2-107` | `c859184` | [M2-107.md](M2-107.md) | QA pass · SEC n/a (`needs-sec: no`) |
| M2-206 media-tools: Kokoro TTS and faster-whisper STT | ai | `m2/M2-206` | `fb3f404` | [M2-206.md](M2-206.md) | QA pass · SEC pass |
| M2-102 SQLite open, embedded migrations, initial schema | be (Cursor) | `m2/M2-102` | `de659ed` | [M2-102.md](M2-102.md) | QA pass · SEC n/a (`needs-sec: no`) |
| M2-111 LLM router and providers | ai (Cursor) | `m2/M2-111` | `a45a85b` | [M2-111.md](M2-111.md) | QA pass · SEC pass |
| M2-208 Remotion compositions | fe (Cursor) | `m2/M2-208` | `42e1133` | [M2-208.md](M2-208.md) | QA pass · SEC n/a (`needs-sec: no`) |
| M2-109 Windows build, start at logon, power settings | be-2 | `m2/M2-109` | `9886b06` | [M2-109.md](M2-109.md) | QA pass · SEC pass — 2026-09-28, independent crew review (`cd5590a`) |
| M2-207 Stock visuals with license records | be-3 | `m2/M2-207` | `ba50300` | [M2-207.md](M2-207.md) | QA pass · SEC pass — 2026-09-28, independent crew review (`cd5590a`) |
| M2-103 Durable job queue and worker pool | be | `m2/M2-103` | `18c7910` | [M2-103.md](M2-103.md) | QA pass · SEC n/a (`needs-sec: no`) — 2026-09-28 |
| M2-104 Events log and SSE broadcaster | be-2 | `m2/M2-104` | `e520b9e` | [M2-104.md](M2-104.md) | QA pass · SEC n/a (`needs-sec: no`) — 2026-09-28 |
| M2-213 Storage retention and R2 | be-4 | `m2/M2-213` | `7d0c3fa` | [M2-213.md](M2-213.md) | QA pass · SEC pass (`needs-sec: yes`) — 2026-09-28 |
| M2-114 Run with only the keys you have | ai | `m2/M2-114` | `9c0b970` | [M2-114.md](M2-114.md) | QA pass · SEC pass (`needs-sec: yes`) — 2026-09-28 |
| M2-201 Channel and format config | be-3 | `m2/M2-201` | `3f1293e` | [M2-201.md](M2-201.md) | QA pass · SEC n/a (`needs-sec: no`) — 2026-09-28 |
| M2-105 Telegram bot: allowlist, commands, approvals, PIN | be | `m2/M2-105` | `d7c2558` | [M2-105.md](M2-105.md) | QA pass · SEC pass (`needs-sec: yes`) — 2026-09-28 |
| M2-106 HTTP API, auth, embedded dashboard | be-2 | `m2/M2-106` | `96adf6a` | [M2-106.md](M2-106.md) | QA pass · SEC pass (`needs-sec: yes`) — 2026-09-28 |
| M2-110 DPAPI token vault and OAuth helper | be-3 | `m2/M2-110` | `880b852` | [M2-110.md](M2-110.md) | QA pass · SEC pass (`needs-sec: yes`) — 2026-09-28 |
| M2-202 Niche Scout | ai | `m2/M2-202` | `560e002` | [M2-202.md](M2-202.md) | QA pass · SEC pass (`needs-sec: yes`) — 2026-09-28 |
| M2-203 Research brief with sources | ai | `m2/M2-203` | `1491844` | [M2-203.md](M2-203.md) | QA pass · SEC pass (`needs-sec: yes`) — 2026-09-28 |
| M2-108 Scheduler and daily summary | be | `m2/M2-108` | `a485988` | [M2-108.md](M2-108.md) | QA pass · SEC pass (`needs-sec: yes`) — 2026-09-28 |
| M2-204 Script writer (native EN and HI) | ai | `m2/M2-204` | `d69dcae` | [M2-204.md](M2-204.md) | QA pass · SEC pass (`needs-sec: yes`) — 2026-09-28 |
| M2-209 Render long, short, thumbnail, subtitles | be | `m2/M2-209` | `878ccb7` | [M2-209.md](M2-209.md) | QA pass · SEC pass (`needs-sec: yes`) — 2026-09-28 |
| M2-205 Compliance script gates G1–G7 | be | `m2/M2-205` | `a76a141` | [M2-205.md](M2-205.md) | QA pass · SEC pass (`needs-sec: yes`) — 2026-09-28 |
| M2-210 Final gates and approval flow | be | `m2/M2-210` | `d283fb4` | [M2-210.md](M2-210.md) | QA pass · SEC pass (`needs-sec: yes`) — 2026-09-28 |

Batch 1 (8 tickets) has full QA, and every `needs-sec: yes` ticket among them has a SEC pass. Batch-2
merge wave **103 / 104 / 213 / 114 / 201 / 105 / 106 / 110 / 202 / 203 / 108** is on the phase branch.
**M2-204**, **M2-209**, **M2-205**, and **M2-210** are merged; **M2-211** is `ready`. Phase→main PR can
open when Mayank is ready (compare URL above).

**M2-109 disclosure:** while building its tests, a debug run briefly registered a real Windows scheduled
task on this laptop, self-detected and removed within the same session; AC power settings were never
touched (mocked throughout). Full account in [M2-109.md](M2-109.md#known-gaps-and-follow-ups).

## Commits
Full graph: `git log --oneline --graph phase/p1-foundation` from the point it branched off `main`. Each
ticket above is one `--no-ff` merge, so `git log --oneline <merge>^1..<merge>^2` shows that ticket's own
commits (also listed per-ticket in each audit doc).

## Checks (real output)
Run by ship on the fully merged `phase/p1-foundation` tree (Go 1.27.0, Node 24.15.0), after `npm ci` in
`web/` and `remotion/` and `uv sync --extra dev` in `media-tools/`:

```
### Go
$ gofmt -l .
(no output)
$ go vet ./...
(exit 0)
$ go test -count=1 ./...
ok  	mayank2/cmd/mayank2
ok  	mayank2/internal/config
ok  	mayank2/internal/content
ok  	mayank2/internal/db
ok  	mayank2/internal/llm
ok  	mayank2/internal/events
ok  	mayank2/internal/media
ok  	mayank2/internal/queue
ok  	mayank2/internal/storage
ok  	mayank2/internal/tickets
?   	mayank2/migrations	[no test files]

### web/
$ npm run format:check   → All matched files use Prettier code style! (exit 0)
$ npm run lint            → exit 0
$ npm run typecheck       → exit 0
$ npm run test             → Test Files 1 passed (1), Tests 4 passed (4)
$ npm run build             → dist/ built, exit 0

### remotion/
$ npm run format:check   → All matched files use Prettier code style! (exit 0)
$ npm run lint            → oxlint, exit 0
$ npm run typecheck       → tsc --noEmit, exit 0
$ npm run test             → Test Files 1 passed (1), Tests 6 passed (6)
$ npm run build             → remotion bundle, exit 0

### media-tools/
$ uv run --extra dev ruff check .   → All checks passed
$ uv run --extra dev pytest -q       → 22 passed, 3 skipped
```

All exit codes above are the real command's own exit code (not a pipeline's). No `-race` (this machine has
no cgo); no live third-party API calls (LLM providers, Pexels/Pixabay, Kokoro/faster-whisper model downloads)
were exercised — every test uses `httptest`/mocked fixtures, matching "no network in unit tests" (CLAUDE.md).

**Infra fix included:** `.githooks/pre-commit` called `uv run ruff`/`uv run pytest` without `--extra dev`,
which fails with "Failed to spawn" on a clean checkout because those tools are an optional dependency group.
Fixed on this branch (commit `ff471a6`) — otherwise every future `media-tools/` commit would be blocked.

**Line-ending note:** this session hit false-positive `gofmt`/prettier failures on two ticket worktrees
(M2-102, M2-111) after rebasing them onto this phase branch — stale CRLF checkouts from before the repo's
`.gitattributes eol=lf` fix (`main` commit `b7b5f5c`) landed. Fixed per-worktree with
`git rm --cached -r . && git reset --hard` to force re-normalization; not a defect in either ticket's code.
Worth remembering if a future rebase shows unexplained format failures on files a ticket didn't touch.

## Config and keys
No secrets are added by this phase. New `.env` names already existed in `.env.example` from the initial
scaffold (`GROQ_API_KEY`, `PEXELS_API_KEY`, `PIXABAY_API_KEY`, etc.). Per **D24**, every integration in this
phase (LLM providers, Pexels, Pixabay) is enabled only when its key is present and non-blank; with no keys,
`doctor` and `Stock`/`Router` degrade cleanly instead of crashing. **Known exception:** `doctor` (M2-101)
currently reports every missing platform-publishing key (`GOOGLE_CLIENT_ID`, `META_APP_ID`, …) as ❌ and
exits non-zero — this predates D24 and is exactly what **M2-114** (now `ready`, depends only on the just-merged
M2-111) is scoped to fix, alongside adding `mayank2 llm ask` for a real one-key smoke test.

## Risks and follow-ups
- LLM quota tracking is in-memory; wire to the `quotas` table now that M2-102 has shipped it.
- Stock-clip reuse tracking is in-memory; wire to a SQLite-backed `UsageStore` now that M2-102 has shipped.
- `assets` table (ARCHITECTURE §4) has `license_url` but no `source_url`/`provider`/`provider_asset_id`
  columns — M2-207's audit doc has the detail; needs Mayank's call on a follow-up migration.
- M2-109: should `-Uninstall` also restore prior AC power-plan values? Needs Mayank's call.
- Free-tier LLM model IDs are still `TBD` in `config.example.yaml` — Mayank to fill in.
- Exact model IDs aside, **M2-114 is now ready** and is the direct path to "put in a free Gemini key and it
  works end to end."

Tickets flipped to `ready` when batch 1 merged: **M2-103**, **M2-104**, **M2-110**, **M2-114**,
**M2-201**, **M2-213**. After **M2-103** merged (`18c7910`): **M2-105** (Telegram) → `ready`. After
**M2-104** merged (`e520b9e`): **M2-106** (HTTP API) → `ready`. After **M2-114** merged (`9c0b970`):
**M2-110** unblocked for build (`cmd/mayank2` free). After **M2-201** merged (`3f1293e`): **M2-202**
(Niche Scout) → `ready` (deps M2-111 + M2-201 + M2-103 all done). After **M2-105** merged (`d7c2558`):
**M2-108** (scheduler / daily summary) → `ready` (deps M2-103 + M2-105 all done). After **M2-202**
merged (`560e002`): **M2-203** (Research brief) → `ready` (deps M2-111 + M2-202 all done). After
**M2-108** merged (`a485988`): P1 scheduler landed; **M2-204** already `ready` from the M2-203 merge.
After **M2-204** merged (`d69dcae`): **M2-205** (Compliance script gates) → `ready`.

## How to test
```
git fetch origin && git checkout phase/p1-foundation
go test ./...
cd web && npm ci && npm run lint && npm run typecheck && npm test && npm run build && cd ..
cd remotion && npm ci && npm run lint && npm run typecheck && npm test && npm run build && cd ..
cd media-tools && uv sync --extra dev && uv run ruff check . && uv run pytest -q
```

🤖 Generated with [Claude Code](https://claude.com/claude-code)
