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
| M2-205 | `m2/M2-205` | Compliance script gates G1?G7 | be | `52f0fa3`, `3add33a`, `64d05a9`, `6d7eef1`, `c0f8eb5`, `4d5f7f8`, `f84318e` | `a76a141` | merged, done, QA+SEC pass |
| M2-210 | `m2/M2-210` | Final gates and approval flow | be | `a997223`, `7417577`, `406ac54` | `9646b4b` + `d283fb4` | merged, done, QA+SEC pass |
| M2-211 | `m2/M2-211` | YouTube uploader for 4 channels | be | (see ticket) | `e88f6f8` | merged, done, QA+SEC pass |
| M2-212 | `m2/M2-212` | Analytics pull and scores | be | (see ticket) | `904a475` | merged, done, QA+SEC pass |
| ? | ? | Independent qa+sec audit of M2-201..M2-213 (first review not by ship/self; Cursor's own agents had self-reviewed the whole batch) | qa/sec | (this commit) | (direct to phase branch) | housekeeping, not a ticket â€” found a real gap in M2-205 (G7 fails open for English with no LLM classifier wired, untested); everything else in the batch independently re-verified pass. See ticket Review sections. |
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
| M2-501 | **done** Â· merge `1f20dce` | 103, 105 (done) | `internal/builder/planner.go`, `config/config.example.yaml` |
| M2-502 | **done** Â· merge `23e708f` | 501 (done) | `internal/builder/implementer.go` |
| M2-503 | **done** Â· merge `e6e28f6` | 502 (done) | `internal/builder/auditor.go` |
| M2-504 | todo | 503 | `internal/builder/gate.go` |
| M2-505 | merged on phase, **in-review** (QA+SEC pass; done blocked on M2-504) | 504 (not done), 106 (done) | `internal/builder/pr.go`, Builder dashboard |

| Ticket | Branch | Feature | Role | Commits (own range) | Merge commit | State |
|---|---|---|---|---|---|---|
| M2-501 | `m2/M2-501` | Builder planner | be | `c64c2cf`, `4a38ce5`, `05d922f`, `ae8c68b`, `6bc991c`, `f4e4c91` | `1f20dce` | merged, done, QA+SEC pass |
| M2-502 | `m2/M2-502` | Builder implementer | be (Cursor) | `c0e480e`, `df5b117`, `53f9b40`, `b4d2f34`, `1db9897`, `72a3d73` | `23e708f` | merged, done, QA+SEC pass (independent qa/sec re-review this session, re-derived from code + real go test/vet/gofmt output) |
| M2-503 | `m2/M2-503` | Builder auditor (parallel audit thread) | be | `c997653`, +2 review commits (`6058579`, `7e980df`) | `e6e28f6` | merged, done, QA+SEC pass (independent qa/sec re-review, re-derived from code + real go test/vet/gofmt output); reconciled `defaultGitRunner(opts.LookPath)` calls (auditor.go + auditor_test.go, 4 call sites) onto phase's `defaultImplementerGitRunner` rename (M2-502's dedupe vs. M2-505's `pr.go` `defaultGitRunner(ctx, dir, args...)`); post-merge `gofmt -l .` / `go vet ./...` / `go test ./... -count=1` all green |
| M2-505 | `m2/M2-505` | Builder dashboard + PR link | fe | `48a9881`, `853f6bb`, `c7d1fc8` | `c10146a` | merged on phase, in-review (QA+SEC @ c7d1fc8; depends M2-504 blocks done) |

## In progress (branched from `phase/p1-foundation`)

| Ticket | Branch | Feature | Role | Commits (own range) | Merge commit | State |
|---|---|---|---|---|---|---|
| — | — | (none open) | — | — | — | — |

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
