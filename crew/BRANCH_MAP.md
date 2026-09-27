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
| M2-104 | `m2/M2-104` | Events log and SSE broadcaster | be-2 | through `5403142` qa | `e520b9e` | merged, done (audit `8af5451`; push in flight) |
| M2-110 | `m2/M2-110` | DPAPI token vault and OAuth helper | be-3 | `a701d57`, `ea83c08`, `ca751c8`, `dd3a2f7`, `2ce1355` | `880b852` | merged, done, QA+SEC pass |

**Not a ticket branch:** `phase/p1-foundation` itself ? one `--no-ff` merge per ticket, in the order above.
Full graph: `git log --oneline --graph phase/p1-foundation`.

## Phase P1 batch 2 remaining (ship + push each)

| Ticket | Branch | Feature | Role | Commits so far | State |
|---|---|---|---|---|---|
| M2-114 | `m2/M2-114` | Run with only the keys you have (single-key LLM) | ai | ? + `5d126ae` qa/sec | QA+SEC pass · ship next after 213 |
| M2-201 | `m2/M2-201` | Channel and format config | be-3 | `5817bad`, `e15b720`, `45d536c` (+ uncommitted QA) | QA pass · ship after 114 |
| M2-213 | `m2/M2-213` | Storage retention and R2 | be-4 | ? + `13bc7b2` | QA+SEC pass · ship merging now |

**Mayank override:** push `origin phase/p1-foundation` after each merge ? do not wait for end of P1.

## In progress (branched from `phase/p1-foundation`)

| Ticket | Branch | Feature | Role | Commits so far | State |
|---|---|---|---|---|---|
| M2-108 | `m2/M2-108` | Scheduler and daily summary | be | ? | in-progress |
| M2-203 | `m2/M2-203` | Research brief with sources | ai | — | in-progress |
| M2-105 | `m2/M2-105` | Telegram bot | be | SEC fixes in flight | in-progress (QA changes)
| M2-106 | `m2/M2-106` | HTTP API | be-2 | tip in-review | in-review awaiting qa/sec
| M2-202 | `m2/M2-202` | Niche Scout | ai | tip in-review | in-review awaiting qa/sec

## How to regenerate/verify this file

```bash
git branch -a                                  # every branch that exists
git log --oneline --merges phase/p1-foundation # merge commits, one per merged ticket
git log --oneline <merge>^1..<merge>^2          # that ticket's own commits
git show phase/p1-foundation:tickets/M2-xxx.md | grep touches:   # confirm branch scope matches ticket
```

## Stale / to clean up

`m2/*` branches for done tickets are kept (not deleted) after merging, in case another session still has
context on one ? see `crew/SHIP_QUEUE.md` Notes. Safe to delete once Mayank confirms `phase/p1-foundation`
is merged into `main`: `git branch -d m2/M2-101 m2/M2-102 m2/M2-107 m2/M2-109 m2/M2-111 m2/M2-206 m2/M2-207 m2/M2-208`.
