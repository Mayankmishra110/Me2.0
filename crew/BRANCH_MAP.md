# Branch map ? owned by ship, overlooked by lead

Every branch that has existed in this repo, which ticket/feature it maps to, and what happened to it.
**Ship updates this file in the same commit as every merge** (into a phase branch or into `main`) ? see
`crew/roles/ship.md` step 5. Lead spot-checks it against `git branch -a` / `git log --merges` when picking
up work, per `crew/roles/lead.md`.

Rule enforced by `.githooks/commit-msg`: every commit is `type(M2-xxx): summary` (or scope `crew`/`docs`/
`repo` for non-ticket work) ? so a commit with no ticket in its message cannot exist on a ticket branch, and
`git log --oneline m2/M2-xxx` always tells you what that commit was for even without this file. This file is
the fast index so you don't have to read commit messages to get the map.

## Phase P1 Foundation

Phase branch: `phase/p1-foundation` (pushed). Base for every ticket branch below.

| Ticket | Branch | Feature | Role | Commits (own range) | Merge commit | State |
|---|---|---|---|---|---|---|
| M2-101 | `m2/M2-101` | Repo skeleton, config loader, doctor | be | `f2da6c5`, `67399ab`, +4 review commits | `db0f66f` | merged, done |
| M2-107 | `m2/M2-107` | Dashboard shell: Home, Approvals, Logs | fe | `df92124`, `96c5b4d` | `c859184` | merged, done |
| M2-206 | `m2/M2-206` | media-tools: Kokoro TTS + faster-whisper STT | ai | `dd5dfae`, `c81e5d8`, `5c8fea8`, +3 review commits | `fb3f404` | merged, done |
| M2-102 | `m2/M2-102` | SQLite open, embedded migrations, initial schema | be (Cursor) | `280939d`, `189b6cb`, `52eea9a`, +2 review commits | `de659ed` | merged, done |
| M2-111 | `m2/M2-111` | LLM router and providers | ai (Cursor) | `2b22821`, `386483e`, `e912fb4`, `4e0d0b0`, +2 review commits | `a45a85b` | merged, done |
| M2-208 | `m2/M2-208` | Remotion compositions (explained_60s, myth_vs_fact, top_n) | fe (Cursor) | `2756610`, `05b390a` | `42e1133` | merged, done |
| M2-109 | `m2/M2-109` | Windows build, start at logon, power settings | be-2 | `1523560`, `d745de1`, `af140e7`, `584fc56` | `9886b06` | merged, done, QA+SEC pass (`cd5590a`) |
| M2-207 | `m2/M2-207` | Stock visuals with license records | be-3 | `5feae4b`, `e1da2b5`, `6e9cf35`, `138a845` | `ba50300` | merged, done, QA+SEC pass (`cd5590a`) |
| ? | ? | Sync `main` into the phase branch (BOM fix, claims) | ship | ? | `3575a49` | housekeeping, not a ticket |
| ? | ? | Independent qa+sec review of M2-109 + M2-207 | qa/sec | `cd5590a` | (direct to phase branch) | housekeeping, not a ticket |
| ? | ? | Clear review blocker in audit docs | lead | `f1ed852` | (direct to phase branch) | housekeeping, not a ticket |
| M2-103 | `m2/M2-103` | Durable job queue and worker pool | be | `55c4c53`, `41108ee`, `d547f6b`, `3aaa20d`, `5e87c8a`, `7240e91`, `ee8876e`, `9eb1800`, `82df3c6` | `18c7910` | merged, done |
| M2-104 | `m2/M2-104` | Events log and SSE broadcaster | be-2 | `370ea1d`, `daeaa1b`, `1f5517a`, `d314dd0`, `fe93dd4`, `5403142` | `e520b9e` | merged, done |
| M2-213 | `m2/M2-213` | Storage retention and R2 | be-4 | `51bb8e3`, `b0002d2`, `580f837`, `6627e86`, `8bcd623`, `3def108`, `13bc7b2` | `7d0c3fa` | merged, done |
| M2-114 | `m2/M2-114` | Run with only the keys you have (single-key LLM) | ai | `71d973e`, `d9a8f4e`, `b2666c2`, `0410a86`, `fadc7ad`, `a780106` | `9c0b970` | merged, done |
| M2-201 | `m2/M2-201` | Channel and format config | be-3 | `983be38`, `6ba672b`, `5b40258`, `2ae7005` | `3f1293e` | merged, done |
| M2-105 | `m2/M2-105` | Telegram bot: allowlist, commands, approvals, PIN | be | `60ee164`, `4538c2c`, `d79f039`, `67229fd`, `c5008a4`, `a34657e`, `9db5f4b`, `22f5c37`, `e4c5d6d` | `d7c2558` | merged, done, QA+SEC pass |
| M2-106 | `m2/M2-106` | HTTP API, auth, embedded dashboard | be-2 | `948e4cd`, `53b5c3e`, `b526979`, `e428e5e`, `c0d194f`, `a4a8806`, `62e752a` | `96adf6a` | merged, done, QA+SEC pass |
| M2-110 | `m2/M2-110` | DPAPI token vault and OAuth helper | be-3 | `a701d57`, `ea83c08`, `ca751c8`, `dd3a2f7`, `2ce1355` | `880b852` | merged, done, QA+SEC pass |
| M2-202 | `m2/M2-202` | Niche Scout | ai | `613d633`, `033687a`, `4818e97`, `065c928`, `c530123`, `a1216f8`, `ded5522`, `cbca048`, `7581bb0`, `54f5cde` | `560e002` | merged, done, QA+SEC pass |
| M2-203 | `m2/M2-203` | Research brief with sources | ai | `b2a03f4`, `3149cf9`, `8006d66` | `1491844` | merged, done, QA+SEC pass |
| M2-108 | `m2/M2-108` | Scheduler and daily summary | be | `6f5f09d`, `7978deb`, `b14ddf6`, `4411da0`, `9eb9c00` | `a485988` | merged, done, QA+SEC pass |
| M2-204 | `m2/M2-204` | Script writer (native EN and HI) | ai | `ba40b2e`, `b12417d`, `e5c3849` | `d69dcae` | merged, done, QA+SEC pass |
| M2-209 | `m2/M2-209` | Render long/short/thumb/subs | be | `efd68a4`, `8e5ba45`, `4040c67`, `f109dbd`, `1b9eebd`, `755f696`, `21804f1` | `878ccb7` | merged, done, QA+SEC pass |
| M2-205 | `m2/M2-205` | Compliance script gates G1-G7 | be, then ai (fix), qa (re-review) | `52f0fa3`, `3add33a`, `64d05a9`, `6d7eef1`, `c0f8eb5`, `4d5f7f8`, `f84318e` original + `51ace9e` fix (G7 real English heuristic) + `5bd90b0` independent qa re-review, `QA: pass` | `a76a141` (original) / `1d219dd` (fix merge into phase branch) | merged, done, QA+SEC pass (G7 no-op-for-English gap closed by `51ace9e`, re-verified independently) |
| M2-210 | `m2/M2-210` | Final gates and approval flow | be | `a997223`, `7417577`, `406ac54` | `9646b4b` + `d283fb4` | merged, done, QA+SEC pass |
| M2-211 | `m2/M2-211` | YouTube uploader for 4 channels | be | (see ticket) | `e88f6f8` | merged, done, QA+SEC pass |
| M2-212 | `m2/M2-212` | Analytics pull and scores | be | (see ticket) | `904a475` | merged, done, QA+SEC pass |
| ? | ? | Independent qa+sec audit of M2-201..M2-213 (first review not by ship/self; Cursor's own agents had self-reviewed the whole batch) | qa/sec | (this commit) | (direct to phase branch) | housekeeping, not a ticket — found a real gap in M2-205 (G7 fails open for English with no LLM classifier wired, untested); everything else in the batch independently re-verified pass. See ticket Review sections. |
| M2-301 | `m2/M2-301` | Instagram Reels publisher | be | `3dc5623`..`681cab8` | `636baff` | merged, done, QA+SEC pass |
| M2-303 | `m2/M2-303` | X (business) publisher | be-3 | `9249bd7`..`1818f3b` | `53a8b60` | merged, done, QA+SEC pass |
| M2-304 | `m2/M2-304` | Pinterest video pin publisher | be-4 | `b5c541b`..`9947ace` | `d40a796` | merged, done, QA+SEC pass |
| M2-302 | `m2/M2-302` | Facebook Page publisher | be-2 | `b2c10a9`..`8eb3de4` | `e28652a` | merged, done, QA+SEC pass (UploadHosted redact `a6e50d6`) |
| M2-401 | `m2/M2-401` | Blog draft + merge for Mayankbuilt | be | `9dbb996`, `9f01826` (phase cherry-pick `1b0a9e0`) | `4aec99f` | merged, done, QA+SEC pass |
| M2-404 | `m2/M2-404` | Medium import-story link prep | be-4 | `0e8d1b3` | `bd09d1c` | merged, done, QA pass / SEC n/a |
| M2-403 | `m2/M2-403` | X personal thread repurpose + publisher | be-3 | `ebb2a77`, `fe4bede`, `710e35c`, `465693b` | `0e48987` | merged, done, QA+SEC pass (notes+stamp folded from `5fe3733`/`a8ce2d3`) |
| M2-402 | `m2/M2-402` | LinkedIn repurpose + Posts API publisher | be-2 | `118475d` (+ SourcePost fold) | `84361f9` | merged, done, QA+SEC pass |

**Not a ticket branch:** `phase/p1-foundation` itself - one `--no-ff` merge per ticket, in the order above.
Full graph: `git log --oneline --graph phase/p1-foundation`.

## Phase P1 batch 2 (complete on phase branch)

Batch-2 merge wave complete for 103/104/213/114/201/105/106/110/202/203 + Scheduler **M2-108**. **M2-204**
(phase 2 script writer) merged `d69dcae`. **M2-209** (render) merged `878ccb7`. **M2-205**
(compliance G1?G7) merged `a76a141`. **M2-210** (final gates + approval) merged `9646b4b` /
`d283fb4` (QA+SEC). **M2-211** flipped `ready` (deps 110+210).

## Phase P3 publishers (on phase branch; awaiting D25 main PRs)

**M2-301** merge `636baff`. **M2-303** merge `53a8b60`. **M2-304** merge `d40a796`.
**M2-302** merge `e28652a` (SEC fix `a6e50d6`, tip `8eb3de4`).

## Phase P4 blog (complete on phase branch; awaiting D25 main PRs)

**M2-401** merge `4aec99f` (QA+SEC stamp `1b0a9e0`, feat `9dbb996`).
**M2-404** merge `bd09d1c` (feat `0e8d1b3`; QA pass / SEC n/a).
**M2-403** merge `0e48987` + dedupe `fe4bede`; notes `710e35c` / Review stamp `465693b`
(from tip `5fe3733` / `a8ce2d3`).
**M2-402** merge `84361f9` (feat `118475d`; QA+SEC pass; SourcePost shared-type fold on phase).

## Phase P5 Builder (tickets on phase; branched from `phase/p1-foundation`)

| Ticket | Status | Depends | Touches |
|---|---|---|---|
| M2-501 | **done** · merge `1f20dce` | 103, 105 (done) | `internal/builder/planner.go`, `config/config.example.yaml` |
| M2-502 | **done** · merge `23e708f` | 501 (done) | `internal/builder/implementer.go` |
| M2-503 | **done** · merge `e6e28f6` | 502 (done) | `internal/builder/auditor.go` |
| M2-504 | **done** · merge `8729db2` | 503 (done) | `internal/builder/gate.go` |
| M2-505 | **done** � merge `c10146a` (in-review flip resolved 2026-09-29) | 504 (done), 106 (done) | `internal/builder/pr.go`, Builder dashboard |

**P5 core sequential chain (M2-501 ? M2-502 ? M2-503 ? M2-504 ? M2-505) is now fully merged onto `phase/p1-foundation` and all `done`.** P5 is code-complete (dashboard/PR link no longer blocked); note cmd/mayank2 end-to-end wiring of these jobs is still outstanding per prior review, tracked separately, not part of this ticket's scope.

| Ticket | Branch | Feature | Role | Commits (own range) | Merge commit | State |
|---|---|---|---|---|---|---|
| M2-501 | `m2/M2-501` | Builder planner | be | `c64c2cf`, `4a38ce5`, `05d922f`, `ae8c68b`, `6bc991c`, `f4e4c91` | `1f20dce` | merged, done, QA+SEC pass |
| M2-502 | `m2/M2-502` | Builder implementer | be (Cursor) | `c0e480e`, `df5b117`, `53f9b40`, `b4d2f34`, `1db9897`, `72a3d73` | `23e708f` | merged, done, QA+SEC pass (independent qa/sec re-review this session, re-derived from code + real go test/vet/gofmt output) |
| M2-503 | `m2/M2-503` | Builder auditor (parallel audit thread) | be | `c997653`, +2 review commits (`6058579`, `7e980df`) | `e6e28f6` | merged, done, QA+SEC pass (independent qa/sec re-review, re-derived from code + real go test/vet/gofmt output); reconciled `defaultGitRunner(opts.LookPath)` calls (auditor.go + auditor_test.go, 4 call sites) onto phase's `defaultImplementerGitRunner` rename (M2-502's dedupe vs. M2-505's `pr.go` `defaultGitRunner(ctx, dir, args...)`); post-merge `gofmt -l .` / `go vet ./...` / `go test ./... -count=1` all green |
| M2-504 | `m2/M2-504` | Builder phase gate (pass/fix-retry decision, timeout, limit pause) | be | `79762bb`, +1 review commit (`cd19fde`) | `8729db2` | merged, done, QA+SEC pass (independent qa/sec re-review, re-derived every AC claim from real code + real go test/vet/gofmt output); reconciled one stale `defaultGitRunner(opts.LookPath)` call in `gate.go` (line 191, built before/without seeing M2-502's rename) onto phase's `defaultImplementerGitRunner`; also added the missing CONTEXT.md D25 row (ship auto-merge decision, commit `400e674`, present on `main` but never in the phase-branch lineage � a pre-existing docs-integrity gap flagged by M2-504's own reviewer, fixed here as a documentation fix, not a product decision), keeping M2-504's own D26 row intact; post-merge `gofmt -l .` / `go vet ./...` / `go test ./... -count=1` all green |
| M2-505 | `m2/M2-505` | Builder dashboard + PR link | fe | `48a9881`, `853f6bb`, `c7d1fc8` | `c10146a` | merged, done, QA+SEC pass (QA+SEC @ `c7d1fc8`; re-verified fresh 2026-09-29 against final M2-504 `gate.go` � full Go + web check suite green, no `defaultGitRunner`/`defaultImplementerGitRunner` bit rot in `pr.go`) |

## Phase P6 revenue / income (on phase branch)

| Ticket | Status | Depends | Touches |
|---|---|---|---|
| M2-601 | **done** � merge `6fac776` | 212, 106 (done) | `internal/revenue/`, `/api/revenue`, Revenue dashboard |
| M2-602 | **done** · merge `efd8c6a` | 501 (done), 601 (done) | `internal/micro_saas/` |
| M2-603 | **done** · merge `6c2f61f` | 111 (done), 601 (done) | `internal/agency/` |
| M2-604 | **done** · merge `77313c2` | 304 (done), 601 (done) | `internal/content/affiliate.go`, `config/affiliate/` |

| Ticket | Branch | Feature | Role | Commits (own range) | Merge commit | State |
|---|---|---|---|---|---|---|
| M2-601 | `m2/M2-601` | Revenue tracking: store, API, pull, Revenue dashboard | be | `47bc03f`, `aa4fe3b` | `6fac776` | merged, done, QA+SEC pass |
| M2-602 | `m2/M2-602` | Micro-SaaS idea → spec → Builder | be | `c5d6aff`, `b68b9a3`, `0a03a34` (pre-rebase `77e2d92`/`f852093`/`ec604db`) | `efd8c6a` | merged, done, QA+SEC pass |
| M2-603 | `m2/M2-603` | Agency lead list + proposal drafts | be | `f6d192a`, `a5ec68e`, `bddfa67`, `4693b8d`, `bdfdab1` (pre-rebase `d7ee871`/`fcb0801`/`755bc78`) | `6c2f61f` | merged, done, QA+SEC pass |
| M2-604 | `m2/M2-604` | Pinterest affiliate pin selection and copy | be | `38558ac`, `bc6f728` (pre-rebase `87acc8c`/`5707ad0`) | `77313c2` | merged, done, QA+SEC pass |

## Merged (D25 model — per-feature branch off `dev`/`main`)

| Ticket | Branch | Feature | Role | Commits (own range) | Merge commit | State |
|---|---|---|---|---|---|---|
| M2-116 | `m2/M2-116` (worktree `data/worktrees/m2-116`, based on `dev` at `ad545bd`) | Wire the daemon: `cmd/mayank2 run` starts everything (queue+handlers+scheduler+telegram+http under one context) | be | `76c11fe`, `85d1581` | `85e3f21` (merged into `dev`) | merged into `dev`, done, QA+SEC pass (independent qa/sec re-review, re-derived from real code + real go vet/gofmt/go test output). Post-merge on `dev`: `gofmt -l .` clean, `go vet ./...` exit 0, `go test ./... -count=1` all green (2026-09-29). Not yet in `main` — see Open questions in CONTEXT.md re: how the accumulated `dev` branch (built under the old phase-branch model, now spanning P1-P6) reconciles with D25's per-ticket-off-`main` auto-merge model before it lands on `main`. |
| M2-117 | `m2/M2-117` (worktree `data/worktrees/m2-117`, rebased onto `origin/main` at `e236e5d`) | Wire remaining job handlers: research, script, visuals, blog | be | `f4b85cc` (feat, rebased from `c509601`), `d067b4a` (qa+sec review, rebased) | `14312b7` (PR #2, `--merge`, into `main`) | **merged into `main`, done**, QA+SEC pass (independent qa/sec re-review, re-derived every AC claim from real code + real gofmt/go vet/go test output, incl. `go test ./cmd/mayank2/... -run TestRunDaemon -count=8` clean). Rebase onto `origin/main` (e236e5d) was clean, no conflicts (only shared-touches overlap was `docs/SPEC.md`, non-conflicting). Post-rebase `gofmt -l .` clean, `go vet ./...` exit 0, `go test ./... -count=1` all green (2026-09-29, ship re-run before push). `-race` still not runnable in this sandbox (no cgo toolchain) — disclosed residual, same as M2-116; re-run on a cgo-enabled machine before production.
| M2-118 | `m2/M2-118` (worktree `data/worktrees/m2-118`, rebased onto `origin/main` at `0c89460`, after M2-116/M2-117) | httpapi: per-address bind resilience + serve built dashboard | be, then qa/sec (independent re-review) | `403bbe6` (feat, rebased from `2f07f80`), `1fe7abb` (qa+sec review, rebased) | `f43371d` (PR #4, `--merge`, into `main`) | **merged into `main`, done**, QA+SEC pass (independent qa/sec re-review, re-derived from real code + real go vet/gofmt/go test + web lint/typecheck/test/build + a real manual run: built binary, confirmed loopback-only bind with tailscale absent, real dashboard served at `/`, SPA fallback at `/approvals`, `doctor` ⚪ for tailscale, `git check-ignore` on a freshly-populated `internal/httpapi/dist/`). Loopback-lock security property (no `0.0.0.0`/wildcard fallback) confirmed intact. Rebase onto `origin/main` (0c89460, first rebase since M2-116/M2-117 landed) was clean — `cmd/mayank2/run.go` auto-merged with no conflicts, M2-118's `httpapi.New(...)` construction changes coexist with M2-116/M2-117's job-handler registrations; only conflict was a tracking-doc line in `crew/BRANCH_MAP.md` (this file), resolved by keeping the in-progress row. Post-rebase `gofmt -l .` clean, `go vet ./...` exit 0, `go test ./... -count=1` all green, `go test ./cmd/mayank2/... -run TestRunDaemon -v` both subtests pass (2026-09-29, ship re-run before push). `sh .githooks/pre-commit --all` all green (remotion `node_modules` was missing in the worktree — pre-existing environment gap unrelated to this ticket's scope, `npm install` run to unblock the check, not a code change).
| M2-119 | `m2/M2-119` (worktree `data/worktrees/m2-119`, already rebased onto `origin/main` at `39d88d7` on arrival, after M2-118) | Wire real `scout.topics` handler onto the scheduler's daily trigger (M2-202) | be, then qa/sec (independent re-review), then ship (race fix) | `496a6a6` (feat), `d858ab0` (qa+sec review), `4ecbcf1` (ship: fix scheduler/worker startup race) | `8004df6` (PR #6, `--merge`, into `main`) | **merged into `main`, done**, QA pass (independent qa/sec re-review, re-derived every AC claim from real code: `Scout.Run`'s FK-on-`channels` doc comment confirmed at `internal/content/scout.go:238`, `SyncChannels` confirmed called nowhere else in `cmd/mayank2` production code pre-ticket, `handleJob` confirmed calling `LoadChannels`→`SyncChannels`→`Scout.Run` per channel, `internal/scheduler/handlers.go`'s `placeholderHandler` confirmed fully removed, `scout.topics` confirmed always registered — even unconfigured — via `run.go`, resource class `net` not `heavy`) · SEC n/a (`needs-sec: no`). Confirmed still rebased on arrival in ship (`origin/main` unchanged at `39d88d7` since the prior rebase; `git merge-base --is-ancestor origin/main HEAD` true, no further rebase needed). Re-running the full check suite in ship surfaced a real ~1-in-5 flake in the ticket's own `TestRunDaemon_scoutTopicsIsRealHandler` (`runDaemon returned an error on shutdown: run: start scheduler: scheduler: write last fire scout.topics: context canceled`, one repro also hit `storage.cleanup`): `cmd/mayank2/run.go` started queue workers before `sched.Start(ctx)` finished its `ctx`-scoped catch-up watermark writes, so a fast-completing job could let a caller's `ctx` cancellation race `Start`'s own writes and turn a clean shutdown into a fatal error. Fixed in ship (`4ecbcf1`) by starting the scheduler before the workers, so `Start` always finishes before any job can run. Verified with `go test ./cmd/mayank2/... -run TestRunDaemon_scoutTopicsIsRealHandler -count=20` (20/20 clean, was flaky before the fix) plus full `go test ./... -count=1` and `sh .githooks/pre-commit --all` (web/ and remotion/ `node_modules` were missing in this worktree — pre-existing environment gap, `npm install` run in both to unblock, not a code change), all green (2026-09-29). |

## In progress (D25 model — per-feature branch off `dev`/`main`)

| Ticket | Branch | Feature | Role | Commits (own range) | Merge commit | State |
|---|---|---|---|---|---|---|
| M2-120 | `m2/M2-120` (worktree `data/worktrees/m2-120`, off `origin/main` at `39d88d7`, rebased onto `origin/main` at `8004df6` after M2-119/M2-118 landed) | httpapi: make `GET/POST /api/topics` real (was a stub) | be, then qa/sec (independent re-review) | `d12b2ea` (feat, rebased from `d949908`), `d61d6d6` (qa+sec review, rebased from `ad17a31`), `7fb3624` (fix, rebased from `9466371`: unify dedup normalization with `content.NormalizeTopicTitle`) | not yet merged | **ready for ship**, QA: pass (2026-09-29) — independent qa/sec re-review had found a real dedup-normalization drift between `internal/httpapi/topics.go`'s `normalizeTopicTitle` and `internal/content/scout.go`'s `normalizeTitle` (the latter stripped non-alphanumeric characters, the former did not), so a manual topic could dodge the "same normalized title within the dedup window" refusal the ticket claims to match. Fix (`7fb3624`, pre-rebase `9466371`) exports `scout.go`'s normalizer as `content.NormalizeTopicTitle` (pure rename, all 16 internal call sites updated, no behavior change) and `topics.go`'s `recentDuplicateTopic` now calls it directly for both the incoming title and every scanned row, with the old weaker `normalizeTopicTitle` genuinely deleted. Verified independently: regression test `TestTopicsCreateDuplicatePunctuation` posts a title then a punctuation-variant, asserts 400 not 201, and confirms via a real `SELECT count(*)` that only one row persisted. Rebase onto `origin/main` (8004df6, after M2-119's `scout.go` additions merged) was clean except a non-conflicting `docs/SPEC.md` table-row-order conflict and a `crew/BRANCH_MAP.md` list-append conflict (this row), both resolved by keeping both tickets' entries — no product-behavior disagreement. Post-rebase `gofmt -l .` clean, `go vet ./...` exit 0, `go test ./... -count=1` all green (incl. `cmd/mayank2` — `TestRunDaemon_integration` did not flake), `go test ./internal/httpapi/... -run TestTopics -v -count=1` and `go test ./internal/content/... -count=1` both green. |

## How to regenerate/verify this file

```bash
git branch -a                                  # every branch that exists
git log --oneline --merges phase/p1-foundation # merge commits, one per merged ticket
git log --oneline <merge>^1..<merge>^2          # that ticket's own commits
git show phase/p1-foundation:tickets/M2-xxx.md | grep touches:   # confirm branch scope matches ticket
```

## Stale / to clean up

`m2/*` branches for done tickets are kept (not deleted) after merging, in case another session still has
context on one - see `crew/SHIP_QUEUE.md` Notes. Safe to delete once Mayank confirms `phase/p1-foundation`
is merged into `main`: `git branch -d m2/M2-101 m2/M2-102 m2/M2-107 m2/M2-109 m2/M2-111 m2/M2-206 m2/M2-207 m2/M2-208`.
