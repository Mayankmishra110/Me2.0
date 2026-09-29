# Ship queue — owned by ship

Which branches need a PR, where each one stands, and the PR text. Newest at the top.
Remote: `https://github.com/Mayankmishra110/Me2.0` · Compare URL: `https://github.com/Mayankmishra110/Me2.0/compare/main...<branch>`

## D25 main PRs (queued — `gh` not authenticated)

`gh auth status` → not logged in. Ship cannot `gh pr create` / `gh pr merge`. Next for Mayank:
`"C:\Program Files\GitHub CLI\gh.exe" auth login` then ship resumes D25 per-ticket PRs onto `main`.

| Ticket | Branch | Phase merge | QA | SEC | State |
|---|---|---|---|---|---|
| M2-603 | `m2/M2-603` | `6c2f61f` on `phase/p1-foundation` | pass @ `bdfdab1` | pass @ `bdfdab1` / feat `f6d192a` (pre-rebase `fcb0801`/`755bc78`) | **merged-to-phase** · **done** · audit [M2-603.md](../docs/audit/p1-foundation/M2-603.md) · main PR queued |
| M2-602 | `m2/M2-602` | `efd8c6a` on `phase/p1-foundation` | pass @ `0a03a34` | pass @ `0a03a34` / feat `c5d6aff` (pre-rebase `77e2d92`/`f852093`/`ec604db`) | **merged-to-phase** · **done** · audit [M2-602.md](../docs/audit/p1-foundation/M2-602.md) · main PR queued |
| M2-604 | `m2/M2-604` | `77313c2` on `phase/p1-foundation` | pass @ `bc6f728` | pass @ `bc6f728` / feat `38558ac` (pre-rebase `87acc8c`/`5707ad0`) | **merged-to-phase** · **done** · audit [M2-604.md](../docs/audit/p1-foundation/M2-604.md) · main PR queued |
| M2-601 | `m2/M2-601` | `6fac776` on `phase/p1-foundation` | pass @ `aa4fe3b` | pass @ `aa4fe3b` / feat `47bc03f` | **merged-to-phase** · **done** · audit [M2-601.md](../docs/audit/p1-foundation/M2-601.md) · unlocked 602/603/604 · main PR queued |
| M2-505 | `m2/M2-505` | `c10146a` on `phase/p1-foundation` | pass @ `c7d1fc8` | pass @ `c7d1fc8` | **merged-to-phase** · **done** (M2-504 dep landed) · audit [M2-505.md](../docs/audit/p1-foundation/M2-505.md) · main PR queued |
| M2-504 | `m2/M2-504` | `8729db2` on `phase/p1-foundation` | pass @ `cd19fde` | pass @ `cd19fde` | **merged-to-phase** · **done** · audit [M2-504.md](../docs/audit/p1-foundation/M2-504.md) · main PR queued |
| M2-503 | `m2/M2-503` | `e6e28f6` on `phase/p1-foundation` | pass @ `7e980df` | pass @ `7e980df` | **merged-to-phase** · **done** · audit [M2-503.md](../docs/audit/p1-foundation/M2-503.md) · main PR queued |
| M2-502 | `m2/M2-502` | `23e708f` on `phase/p1-foundation` | pass @ `72a3d73` | pass @ `72a3d73` / feat `1db9897` | **merged-to-phase** · **done** · audit [M2-502.md](../docs/audit/p1-foundation/M2-502.md) · main PR queued |
| M2-501 | `m2/M2-501` | `1f20dce` on `phase/p1-foundation` | pass @ `f4e4c91` | pass @ `f4e4c91` | **merged-to-phase** · audit [M2-501.md](../docs/audit/p1-foundation/M2-501.md) · main PR queued |
| M2-402 | `m2/M2-402` | `84361f9` on `phase/p1-foundation` | pass @ phase | pass @ phase | **merged-to-phase** · main PR queued |
| M2-404 | `m2/M2-404` | `bd09d1c` on `phase/p1-foundation` | pass @ phase | n/a | **merged-to-phase** · main PR queued |
| M2-403 | `m2/M2-403` | `0e48987` (+ `fe4bede`; fold `710e35c`/`465693b`) | pass @ `5fe3733` | pass @ `a8ce2d3` | **merged-to-phase** · main PR queued |
| M2-401 | `m2/M2-401` | `4aec99f` on `phase/p1-foundation` | pass @ `9dbb996` | pass @ `9dbb996` | **merged-to-phase** · review stamp `1b0a9e0` · main PR queued |
| M2-302 | `m2/M2-302` | `e28652a` on `phase/p1-foundation` | pass @ `8eb3de4` | pass @ `a6e50d6` | **merged-to-phase** · main PR queued |
| M2-301 | `m2/M2-301` | `636baff` on `phase/p1-foundation` | pass | pass | **merged-to-phase** · main PR queued |
| M2-303 | `m2/M2-303` | `53a8b60` | pass | pass | **merged-to-phase** · main PR queued |
| M2-304 | `m2/M2-304` | `d40a796` | pass | pass | **merged-to-phase** · main PR queued |
| M2-101 (migrate) | `m2/M2-101` @ `21d3736` | n/a (D25 onto main) | pass | pass | rebased onto prior main tip; force-with-lease push pending; checks green |

Compare phase: https://github.com/Mayankmishra110/Me2.0/compare/main...phase/p1-foundation

**2026-09-28: D25 supersedes D23 for new landings**, but while `gh` is down, ship keeps merging QA+SEC-ready
tickets into `phase/p1-foundation` and queues main PRs here.

## Phase branches (D23 backlog still on phase)

| Phase | Branch | Tickets in it | Checks | State |
|---|---|---|---|---|
| P1 Foundation + P3/P4/P5/P6 | `phase/p1-foundation` | M2-101…212 + **301–304** + **401–404** + **M2-501…505** + **M2-601…604** (all `done`) | P6 revenue complete (601–604) | **pushed** |

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
| M2-117 | `m2/M2-117` | pass @ `d067b4a` (independent re-review) | pass @ `d067b4a` (`needs-sec: yes`, satisfied) | **merged** · PR [#2](https://github.com/Mayankmishra110/Me2.0/pull/2) · rebased onto `origin/main` (`e236e5d`, clean, no conflicts) · force-with-lease pushed as `f4b85cc`/`d067b4a` · `--merge` commit `14312b7` on `main` (2026-09-29) |
| M2-118 | `m2/M2-118` | pass @ `1fe7abb` (independent re-review) | pass @ `1fe7abb` (loopback-lock confirmed intact) | **merged** · PR [#4](https://github.com/Mayankmishra110/Me2.0/pull/4) · rebased onto `origin/main` (`0c89460`, first rebase since M2-116/117 landed; `run.go` auto-merged clean, only `crew/BRANCH_MAP.md` conflicted) · force-with-lease pushed as `403bbe6`/`1fe7abb` · `--merge` commit `f43371d` on `main` (2026-09-29) |
| M2-119 | `m2/M2-119` | pass @ `d858ab0` (independent re-review) | n/a (`needs-sec: no`) | **merged** · PR [#6](https://github.com/Mayankmishra110/Me2.0/pull/6) · already rebased onto `origin/main` (`39d88d7`, clean, no conflicts; main hadn't moved) · ship found+fixed a real startup/shutdown race in the same worktree, pushed as `4ecbcf1` · `--merge` commit `8004df6` on `main` (2026-09-29) |
| M2-120 | `m2/M2-120` | pass @ `64a6c78` (independent re-review; found dedup-normalization gap, fixed `09f85ff`) | n/a (`needs-sec: no`) | **merged** · PR [#8](https://github.com/Mayankmishra110/Me2.0/pull/8) · rebased onto `origin/main` (`8004df6`, after M2-119/M2-118 landed) · `--merge` commit `ad1e20b` on `main` (2026-09-29) |

### M2-117 — Wire remaining job handlers (research, script, visuals, blog)
Branch: `m2/M2-117` · PR: https://github.com/Mayankmishra110/Me2.0/pull/2 · Merge commit: `14312b7`

**Goal** Register the 6 SPEC §5 job types M2-116 left unwired (`research.brief`, `script.write`,
`visuals.fetch`, `blog.draft`, `blog.merge`, `blog.repurpose`) so the P2 content pipeline and P4 blog
pipeline run end to end in the daemon. Full detail in `tickets/M2-117.md`.

**Rebase** `git rebase origin/main` (main at `e236e5d`) — clean, no conflicts. Only shared-touches overlap
with concurrent main activity was `docs/SPEC.md`; no colliding lines.

**Checks** (real output, post-rebase, `data/worktrees/m2-117`, 2026-09-29)
```
$ export GOROOT="/c/Program Files/Go" PATH="/c/Program Files/Go/bin:$PATH"
$ gofmt -l .
(no output — clean)
$ go vet ./...
(no output — clean)
$ go test ./... -count=1
ok all 22 packages (cmd/mayank2, internal/agency, analytics, blog, builder, compliance, config,
content, content/formats, db, events, httpapi, llm, media, micro_saas, publish, queue, revenue,
scheduler, secrets, storage, telegram, tickets); migrations has no test files
```

**Review** QA: pass @ `d067b4a` — independent re-review, 8 points traced against real line numbers and
re-run tests (`go test ./cmd/mayank2/... -run TestRunDaemon -count=8` clean, no flake). SEC: pass @
`d067b4a` (`needs-sec: yes`) — no secret/token logging in new handlers; path-traversal guard on
`content_id` confirmed real+tested; both blog-repurpose legs gated behind `ApprovalStarter.Start` before
publish.

**Risks / follow-ups** `-race` not runnable in this sandbox (`CGO_ENABLED=0`, no cgo toolchain) — same
disclosed residual as M2-116; re-run `go test -race ./cmd/mayank2/... ./internal/content/...
./internal/media/... ./internal/blog/... -count=1` on a cgo-enabled machine before production. Two open
questions flagged to `docs/CONTEXT.md` §5 (per-channel `Allowed` formats fallback; `blog.medium` needing a
public `*telegram.Client` accessor) — not blockers, not resolved by this ticket.

**How to test** `mayank2 run` with `config.Blog.Enabled()` true, then drive `research.brief` →
`script.write` → `compliance.script` → … and `blog.draft` → `blog.merge` → `blog.repurpose` through the
queue; confirm no "not registered" errors and approval gates still fire.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

### M2-118 — httpapi per-address bind resilience and serve built dashboard
Branch: `m2/M2-118` · PR: https://github.com/Mayankmishra110/Me2.0/pull/4 · Merge commit: `f43371d`

**Goal** Fix two bugs: (1) the httpapi server failed to start entirely when its configured Tailscale
address wasn't available, instead of falling back to loopback; (2) the daemon served a placeholder page
instead of the real built `web/` dashboard. Full detail in `tickets/M2-118.md`.

**Rebase** `git rebase origin/main` (main at `0c89460`) — first rebase since M2-116/M2-117 landed, both of
which also touch `cmd/mayank2/run.go`. `run.go` itself auto-merged with **no conflicts**: M2-118's
`httpapi.New(...)` construction changes and M2-116/M2-117's job-handler registrations (research, script,
visuals, blog, analytics, etc.) coexist. The only real conflict was in `crew/BRANCH_MAP.md` (this
tracking file, not product code) — resolved by keeping M2-118's in-progress row rather than the
just-emptied queue placeholder.

**Checks** (real output, post-rebase, `data/worktrees/m2-118`, 2026-09-29)
```
$ export GOROOT="/c/Program Files/Go" PATH="/c/Program Files/Go/bin:$PATH"
$ gofmt -l .
(no output — clean)
$ go vet ./...
(no output — clean)
$ go test ./... -count=1
ok all 22 packages (cmd/mayank2, internal/agency, analytics, blog, builder, compliance, config,
content, content/formats, db, events, httpapi, llm, media, micro_saas, publish, queue, revenue,
scheduler, secrets, storage, telegram, tickets); migrations has no test files
$ go test ./cmd/mayank2/... -run TestRunDaemon -v
--- PASS: TestRunDaemon_integration (0.23s)
--- PASS: TestRunDaemon_everySpecJobTypeRegistered (0.33s)
PASS
$ sh .githooks/pre-commit --all
pre-commit: all checks passed
  (remotion/node_modules was missing in this worktree — a pre-existing environment gap unrelated to
   this ticket's touches; ran `npm install` in remotion/ to unblock the hook, no code change)
```

**Review** QA: pass @ `1fe7abb` — independent re-review, re-derived from real code + real gofmt/go
vet/go test + web lint/typecheck/test/build + a manual run (built binary, confirmed loopback-only bind
with Tailscale absent, real dashboard served at `/`, SPA fallback at `/approvals`, `doctor` correctly
flags Tailscale unavailable, `git check-ignore` confirmed on a freshly-populated
`internal/httpapi/dist/`). SEC: pass @ `1fe7abb` — specific check confirmed the per-address fallback
never introduces a wildcard/`0.0.0.0` bind; loopback-lock property held throughout.

**Risks / follow-ups** None new. Same `-race` sandbox limitation disclosed on M2-116/M2-117 applies
repo-wide (no cgo toolchain here).

**How to test** Run `mayank2 run` with and without Tailscale present; confirm the dashboard is reachable
on loopback either way and never on a wildcard address, and that `/` serves the real built SPA (not a
placeholder) with `/approvals` falling back correctly.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

### M2-119 — Wire real scout.topics handler onto the scheduler's daily trigger (M2-202)
Branch: `m2/M2-119` · PR: https://github.com/Mayankmishra110/Me2.0/pull/6 · Merge commit: `8004df6`

**Goal** The scheduler's daily cron trigger has fired `scout.topics` since M2-108/116, but it always hit
`internal/scheduler/handlers.go`'s `placeholderHandler` — a no-op returning
`{"status":"placeholder","type":"scout.topics"}`. M2-202 (Niche Scout, `internal/content/scout.go`) built
the real topic-discovery service, but nobody wired the scheduler onto it, so the product's core
end-to-end loop (SPEC §1 P2 exit check: "Topic → published YouTube Short") never actually started on its
own. This ticket wires the real handler in, and along the way closes a channel-sync prerequisite gap
(`Scout.Run` requires the channel already exist in the `channels` table via `SyncChannels`, which nothing
in `cmd/mayank2` outside tests called before). Full detail in `tickets/M2-119.md`.

**Rebase** Arrived at ship already rebased onto `origin/main` (`39d88d7`) with zero conflicts by the
independent QA+SEC re-review pass. Re-verified in ship: `git fetch origin && git ls-remote origin main`
still `39d88d7`; `git merge-base --is-ancestor origin/main HEAD` confirmed up to date — no further rebase
needed.

**Found and fixed in ship: a startup/shutdown race** Re-running the full check suite surfaced a real
~1-in-5 flake in the ticket's own acceptance test, `TestRunDaemon_scoutTopicsIsRealHandler` (one repro
also hit `storage.cleanup`):
```
runDaemon returned an error on shutdown: run: start scheduler: scheduler: write last fire scout.topics: context canceled
```
Root cause: `cmd/mayank2/run.go` started queue workers (`q.StartWorkers`) before `sched.Start(ctx)`
finished. `sched.Start` runs `catchUpLocked`'s `ctx`-scoped watermark writes; workers could drain an
already-enqueued job to success fast enough that a caller cancels `ctx` right after observing success,
racing `Start`'s own writes on the same `ctx` and turning a clean shutdown into a fatal error. Fixed by
starting the scheduler before the workers (commit `4ecbcf1`, on top of the reviewed `d858ab0`) — `Start`
now always completes before any job can run, so no shutdown can race it.

**Checks** (real output, post-fix, `data/worktrees/m2-119`, 2026-09-29)
```
$ export GOROOT="/c/Program Files/Go" PATH="/c/Program Files/Go/bin:$PATH"
$ gofmt -l .
(no output — clean)
$ go vet ./...
(no output — clean)
$ go test ./... -count=1
ok all 22 packages (cmd/mayank2, internal/agency, analytics, blog, builder, compliance, config,
content, content/formats, db, events, httpapi, llm, media, micro_saas, publish, queue, revenue,
scheduler, secrets, storage, telegram, tickets); migrations has no test files
$ go test ./cmd/mayank2/... -run TestRunDaemon_scoutTopicsIsRealHandler -count=20
ok (20/20 — was ~1-in-5 flaky before 4ecbcf1)
$ sh .githooks/pre-commit --all
pre-commit: all checks passed
  (web/ and remotion/ node_modules were missing in this worktree — a pre-existing environment gap
   unrelated to this ticket's touches; ran `npm install` in both to unblock the hook, no code change)
```

**Review** QA: pass @ `d858ab0` — independent re-review prior to ship. SEC: n/a — `needs-sec: no`.

**Risks / follow-ups** Same `-race` sandbox limitation disclosed on M2-116/117/118 applies (no cgo
toolchain here) — re-run with `-race` on a cgo-enabled machine before production. `RSSFeeds`/
`TrendsBaseURL` remain unexposed via `config.yaml` (pre-existing, flagged in the ticket, out of scope);
YouTube is the only signal source reachable from the running daemon today.

**How to test** `export YOUTUBE_API_KEY=<key>; mayank2 run` — daily cron fires `scout.topics`; with
channels configured under `config/channels/*.yaml` it now loads, syncs, and scouts them via the real
Niche Scout service instead of returning a fixed placeholder.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

### M2-120 — Make GET/POST /api/topics real (was a stub)
Branch: `m2/M2-120` · PR: https://github.com/Mayankmishra110/Me2.0/pull/8 · Merge commit: `ad1e20b`

**Goal** `POST /api/topics` was a pure echo (decode body, return it back) with no DB or queue effect —
the one manual entry point SPEC §4 defines for a human to add a topic outside the automatic daily
`scout.topics` discovery run did nothing. `GET/POST /api/topics` now read and write real `topics` rows,
mirroring `content.Scout.AddManual`'s existing scoring/dedup convention (M2-202) so a manually-added
topic seeds the same table the research stage reads from. Full detail in `tickets/M2-120.md`.

**Rebase** Branch started off `origin/main` at `39d88d7`, rebased onto `origin/main` at `8004df6` after
M2-119/M2-118 landed.

**Found and fixed: a dedup-normalization gap** Independent qa/sec re-review (`64a6c78`, QA: changes)
found a real drift between `internal/httpapi/topics.go`'s `normalizeTopicTitle` and
`internal/content/scout.go`'s `normalizeTitle`: the latter stripped non-alphanumeric characters, the
former did not, so a manual topic could dodge the "same normalized title within the dedup window"
refusal the ticket claims to match. Fix (`09f85ff`) exports `scout.go`'s normalizer as
`content.NormalizeTopicTitle` (pure rename, all 16 internal call sites updated, no behavior change) and
`topics.go`'s `recentDuplicateTopic` now calls it directly for both the incoming title and every scanned
row, with the old weaker `normalizeTopicTitle` genuinely deleted. Confirmed closed and re-verified in
`e0c1d44`.

**Review** QA: pass @ `64a6c78` (independent re-review; found + `09f85ff` fixed the dedup-normalization
gap above). SEC: n/a — `needs-sec: no`.

**Risks / follow-ups** Same `-race` sandbox limitation disclosed on M2-116/117/118/119 applies (no cgo
toolchain here) — re-run with `-race` on a cgo-enabled machine before production.

**How to test** `POST /api/topics` with `{channel_id, title, source_url?}` for a real channel; confirm a
real `topics` row is written (`source='manual', status='new'`) and returned, a punctuation-variant
duplicate within the dedup window is refused with 400, and `GET /api/topics` lists it back filterable by
`channel_id`/`status`.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

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
