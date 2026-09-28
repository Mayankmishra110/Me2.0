# Ship queue — owned by ship

Which branches need a PR, where each one stands, and the PR text. Newest at the top.
Remote: `https://github.com/Mayankmishra110/Me2.0` · Compare URL: `https://github.com/Mayankmishra110/Me2.0/compare/main...<branch>`

## D25 main PRs (queued — `gh` not authenticated)

`gh auth status` → not logged in. Ship cannot `gh pr create` / `gh pr merge`. Next for Mayank:
`"C:\Program Files\GitHub CLI\gh.exe" auth login` then ship resumes D25 per-ticket PRs onto `main`.

| Ticket | Branch | Phase merge | QA | SEC | State |
|---|---|---|---|---|---|
| M2-301 | `m2/M2-301` | `636baff` on `phase/p1-foundation` | pass | pass | **merged-to-phase** · main PR queued |
| M2-303 | `m2/M2-303` | `53a8b60` | pass | pass | **merged-to-phase** · main PR queued |
| M2-304 | `m2/M2-304` | `d40a796` | pass | pass | **merged-to-phase** · main PR queued |
| M2-302 | `m2/M2-302` | — | prior pass | **changes** @ `cc969a2` | held — be must fix `UploadHosted` redact before ship |
| M2-101 (migrate) | `m2/M2-101` @ `21d3736` | n/a (D25 onto main) | pass | pass | rebased onto prior main tip; force-with-lease push pending; checks green |

Compare phase: https://github.com/Mayankmishra110/Me2.0/compare/main...phase/p1-foundation

**2026-09-28: D25 supersedes D23 for new landings**, but while `gh` is down, ship keeps merging QA+SEC-ready
tickets into `phase/p1-foundation` and queues main PRs here.

## Phase branches (D23 backlog still on phase)

| Phase | Branch | Tickets in it | Checks | State |
|---|---|---|---|---|
| P1 Foundation + P3 | `phase/p1-foundation` | M2-101…212 + **301/303/304** (302 held) | `go test ./internal/publish/...` green after P3 merges | **push pending this session** |

PR body: [docs/audit/p1-foundation/README.md](../docs/audit/p1-foundation/README.md) (everything below its `---`).
Compare: https://github.com/Mayankmishra110/Me2.0/compare/main...phase/p1-foundation

**No open blockers.** M2-109 and M2-207 were initially only audited by ship, but a dedicated qa+sec crew
review (2026-09-28, `cd5590a`, pushed) independently re-verified both — read the code itself, re-ran the
checks itself, found nothing. All 8 tickets now carry a real QA pass, and every `needs-sec: yes` ticket has
a real SEC pass.

States: `in-progress` → `merged-to-phase` → `pushed` → `pr-open` (Mayank opens it) → `merged`.

## Per-ticket PRs (only when Mayank asks for one instead of the phase flow)

| Ticket | Branch | QA | SEC | State |
|---|---|---|---|---|
| — | — | — | — | queue empty (see phase branch above) |

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

## Notes
- Ticket worktrees for M2-101/102/107/109/111/206/207/208 were removed after merging into the phase branch
  (all confirmed clean first, nothing lost). The `m2/*` branches were kept, not deleted, in case another
  session still has context on them — safe to delete once Mayank confirms the phase branch merged to `main`.
- `phase-p1` worktree (`data/worktrees/phase-p1`) stays checked out on `phase/p1-foundation`. New P1 tickets
  branch from there per `crew/BOOT.md`.
- The repo is currently **public**. `phase/p1-foundation` and `main` are pushed. If Mayank wants it private,
  that's a `gh repo edit` call he runs (needs his GitHub auth), not something ship can do unprompted.

## Superseded per-ticket check-output history (pre phase-branch flow)

<details>
<summary>M2-111, M2-102, M2-208, M2-101, M2-107, M2-206 — original per-ticket PR drafts from Cursor's ship role, 2026-09-28</summary>

### M2-111 — LLM router and providers
Branch: `m2/M2-111` @ `212d9e9` (rebased; was `d8ba007`) · Worktree: `data/worktrees/m2-111` (removed after phase merge)

**Checks** (real output, 2026-09-28)
```
gofmt -l ./internal/llm    (no output)
go vet ./...                (exit 0)
go test ./... -count=1
ok  	mayank2/cmd/mayank2	0.800s
ok  	mayank2/internal/config	0.311s
ok  	mayank2/internal/llm	0.338s
ok  	mayank2/internal/tickets	0.299s
```
**Review** QA: pass — 2026-09-28 · SEC: pass — 2026-09-28 — KeyEnv only; claude fixed argv+stdin; content tasks skip Claude

### M2-102 — SQLite open, embedded migrations, initial schema
Branch: `m2/M2-102` @ `cd68e03` (rebased; was `12c2ee9`) · Worktree: removed after phase merge

**Checks** (real output, 2026-09-28)
```
gofmt -l ./cmd/mayank2 ./internal/db   (no output)
go vet ./...                            (exit 0)
go test ./cmd/mayank2 ./internal/config ./internal/db ./migrations -count=1
ok  	mayank2/cmd/mayank2	0.348s
ok  	mayank2/internal/config	0.215s
ok  	mayank2/internal/db	0.328s
?   	mayank2/migrations	[no test files]
```
Note: `go test ./...` (whole repo) failed at the time on `internal/tickets` due to a UTF-8 BOM in
`tickets/M2-111.md` on the base this branch was built from — fixed on `main` (`254176d`) before the phase
merge; confirmed gone on the merged `phase/p1-foundation` tree.

**Review** QA: pass — 2026-09-28 — WAL sqlite + 001_init §4; migrate idempotent · SEC: n/a

### M2-208 — Remotion compositions: explained_60s, myth_vs_fact, top_n
Branch: `m2/M2-208` @ `4c95713` (rebased; was `b978271`) · Worktree: removed after phase merge

**Checks** (real output, 2026-09-28)
```
npm run lint         → oxlint src scripts, exit 0
npm run typecheck     → tsc --noEmit, exit 0
npm run format:check  → All matched files use Prettier code style!
npm test               → Test Files 1 passed (1), Tests 6 passed (6)
npm run build           → remotion bundle, exit 0
```
**Review** QA: pass — 2026-09-28 · SEC: n/a — fixed argv render scripts; no fetch in compositions

### M2-101 — Repo skeleton, config loader, doctor command
Branch: `m2/M2-101` @ `75397aa` (rebased; was `246b6a3`) · Worktree: removed after phase merge

**Checks** (real output, 2026-09-28)
```
gofmt -l ./cmd/mayank2 ./internal/config
  (listed CRLF-only files under core.autocrlf=true; content clean, no commit made — resolved
   repo-wide later by main's `.gitattributes eol=lf` fix, b7b5f5c)
go vet ./...    (exit 0)
go test ./...
ok  	mayank2/cmd/mayank2	(cached)
ok  	mayank2/internal/config	0.428s
ok  	mayank2/internal/tickets	0.358s
```
**Review** QA: pass — 2026-09-28 — re-pass after loopback lock · SEC: pass — 2026-09-28 — loopback lock holds

### M2-107 — Dashboard shell: Home, Approvals, Logs
Branch: `m2/M2-107` @ `96c5b4d` (rebased; was `774a5e3`) · Worktree: removed after phase merge

**Checks** (real output, 2026-09-28)
```
npm run lint          → oxlint src, exit 0
npm run typecheck      → tsc --noEmit ×2 projects, exit 0
npm run format:check   → 37 files flagged under CRLF checkout (EOL-only; content clean) — resolved
                          repo-wide by main's .gitattributes fix; re-verified 0 on the phase tree
npm test                → Test Files 1 passed (1), Tests 4 passed (4)
npm run build             → dist built, ✓ built in 506ms
```
**Review** QA: pass — 2026-09-28 · SEC: n/a — mock dashboard, no secrets/auth/OAuth

### M2-206 — media-tools: Kokoro TTS and faster-whisper STT
Branch: `m2/M2-206` @ `973fae1` · Worktree: removed after phase merge

**Checks** (real output, 2026-09-28)
```
uv run ruff check .   → All checks passed!
uv run pytest         → 22 passed, 3 skipped in 0.10s
pre-commit --all       → all checks passed (after fixing the hook's missing --extra dev, ff471a6)
```
**Review** QA: pass — 2026-09-28 · SEC: pass — 2026-09-28 — fixed CLI argv, no shell from JSON, offline hub flags

</details>
