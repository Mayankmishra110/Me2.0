# P1 Foundation — phase audit index and PR body

Phase branch: `phase/p1-foundation` → `main` (D23).
Compare / open PR: https://github.com/Mayankmishra110/Me2.0/compare/main...phase/p1-foundation

Everything below the line is the ready-to-paste PR body (from `.github/pull_request_template.md`).
Audit docs sit next to this file, one per ticket.

---

## Phase / feature
`phase/p1-foundation` → `main`: 8 P1 tickets. The Go CLI with config, `doctor` and SQLite (WAL, migrations);
the durable-storage-backed LLM router (Ollama, Groq/Cerebras/OpenRouter/Gemini, `claude -p`); the web
dashboard shell on a mock API; the media-tools TTS/STT CLIs; Pexels/Pixabay stock-clip fetching with license
records; Remotion compositions for the first three formats; and the Windows build + start-at-logon scripts.

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

All 8 tickets now have a QA pass, and every `needs-sec: yes` ticket (M2-101, M2-111, M2-206, M2-109, M2-207)
has a SEC pass. **No open blockers.** M2-109 and M2-207 were initially built by background sub-agents and
only self-audited by ship; a dedicated qa+sec review (`cd5590a`) then independently re-read the code, re-ran
the checks itself (not just re-trusted the claims), and found no issues on either ticket — full verdicts are
in [M2-109.md](M2-109.md) and [M2-207.md](M2-207.md) `## Review`.

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
ok  	mayank2/internal/db
ok  	mayank2/internal/llm
ok  	mayank2/internal/media
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

Tickets flipped to `ready` on this branch (all `depends` now satisfied): **M2-103** (queue), **M2-104**
(events/SSE), **M2-110** (secrets vault), **M2-114** (single-key LLM path), **M2-201** (channel config),
**M2-213** (storage/R2). Not flipped: M2-202/M2-209 still wait on M2-103/M2-201.

## How to test
```
git fetch origin && git checkout phase/p1-foundation
go test ./...
cd web && npm ci && npm run lint && npm run typecheck && npm test && npm run build && cd ..
cd remotion && npm ci && npm run lint && npm run typecheck && npm test && npm run build && cd ..
cd media-tools && uv sync --extra dev && uv run ruff check . && uv run pytest -q
```

🤖 Generated with [Claude Code](https://claude.com/claude-code)
