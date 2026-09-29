# Mayank 2.0 — Technical Spec

> Product: [PRD.md](PRD.md) · Structure: [ARCHITECTURE.md](ARCHITECTURE.md) · Rules for agents: [../CLAUDE.md](../CLAUDE.md)

## 1. Phases

| Phase | Outcome | Exit check |
|---|---|---|
| **P1 Foundation** | Daemon runs at logon, durable queue, Telegram locked to Mayank, dashboard shell live over Tailscale, model router works | `mayank2 doctor` all green; a test job flows queue → Telegram approval → dashboard |
| **P2 Content engine + YouTube** | Topic → published YouTube Short and long video on 4 channels, with compliance report and analytics | 1 approved Short per channel published via API; metrics pulled at +24h |
| **P3 Distribution** | Same renders go to Instagram, Facebook, X business, Pinterest | One item published to all 4 from one approval |
| **P4 Blog** | Blog → portfolio, LinkedIn, X personal, Medium import link | One post live end to end |
| **P5 Builder** | Planner + parallel Implementer/Auditor with phase gates | A 3-subphase sample plan completes with audits and a PR link |
| **P6 Revenue lines** | Revenue tracking, micro-SaaS idea→spec→Builder flow, agency lead list + proposal drafts, Pinterest affiliate boards | Revenue screen shows real entries |

## 2. Tickets and parallel work

Tickets live in [`../tickets/`](../tickets/). IDs: `M2-<phase><nn>` (e.g. `M2-103`).

```markdown
---
id: M2-103
title: Durable job queue and worker pool
status: ready          # todo | ready | in-progress | in-review | done | blocked
phase: 1
priority: 1
depends: [M2-102]
owner: ""              # thread name while in progress, e.g. "claude-thread-2"
touches: [internal/queue/, migrations/]
---
## Goal
## Acceptance criteria
## Notes
```

**Protocol for parallel threads (Claude, Cursor, or Builder):**
1. Pick a `ready` ticket whose `depends` are all `done`. Prefer the lowest `priority` number.
2. Claim it: set `status: in-progress` and `owner`, commit **only that change** (`chore(M2-103): claim`), then work on branch `m2/<id>` (use a git worktree if another thread shares the folder).
3. Only edit paths in `touches` (plus tests next to them). If you must touch another ticket's in-progress paths, stop and note it in the ticket.
4. Done = acceptance criteria met, `go test ./...` / `npm test` green, docs updated. Set `status: in-review`, open a PR or leave the branch for Mayank.
   Crew chats ([../crew/](../crew/README.md)): after `in-review`, qa (and sec when `needs-sec: yes`) write verdicts under `## Review`; ship merges it into the phase branch (`phase/p1-foundation`, …) with an audit doc in `docs/audit/<phase>/` and sets `done`; Mayank opens one PR phase → main from `docs/audit/<phase>/README.md` (D23).
5. When a ticket changes a decision, add a row to [CONTEXT.md §4](CONTEXT.md#4-decision-log).
6. When a ticket's deps are all done, flip it from `todo` to `ready`.

### Dependency graph (P1–P2)

```
M2-101 skeleton/config ─► M2-102 db ─┬─► M2-103 queue ─┬─► M2-108 scheduler ─► M2-201 channels ─► M2-202 scout ─► M2-203 research ─► M2-204 script ─► M2-205 compliance ─┐
                                     │                 └─► M2-104 events ─► M2-106 http api ─► M2-107 dashboard                                                                │
                                     ├─► M2-105 telegram (needs 103 for approvals)                                                                                             │
                                     └─► M2-110 secrets ─► M2-211 youtube                                                                                                      │
M2-111 llm router (needs 101) ─────────────────────────────────────────────────────────────► 202/203/204                                                                       │
M2-109 windows install (needs 101)                                                                                                                                              │
M2-206 media-tools ─┐  M2-207 stock visuals ─┐  M2-208 remotion formats ─┴─► M2-209 render ─► M2-210 final gates + approval ◄───────────────────────────────────────────────────┘
                                                                                                    └─► M2-211 youtube ─► M2-212 analytics      M2-213 storage/R2
```

**Can start immediately in parallel:** M2-101, M2-206 (Python), M2-207, M2-208 (Remotion), M2-107 (dashboard against mock API).

### Dependency graph (P3)

```
M2-210 final gates + approval ─┬─► M2-301 instagram ─┐
M2-213 storage/R2 ─────────────┼─► M2-302 facebook   ├─► one item published to all 4 (P3 exit check)
                                ├─► M2-303 x business │
                                └─► M2-304 pinterest ─┘
```

All four are independent of each other once 210 and 213 are done — safe to run in parallel across threads.

### Ticket index

| ID | Title | Depends |
|---|---|---|
| M2-101 | Repo skeleton, config loader, `doctor` command | — |
| M2-102 | SQLite open + embedded migrations + initial schema | 101 |
| M2-103 | Durable job queue + worker pool with resource classes | 102 |
| M2-104 | Events log + SSE broadcaster | 102 |
| M2-105 | Telegram bot: allowlist, commands, approvals, PIN | 103 |
| M2-106 | HTTP API + token auth + serve embedded web | 104 |
| M2-107 | Dashboard shell: Home, Approvals, Logs (mock API first) | — (integrates with 106) |
| M2-108 | Scheduler + daily summary | 103, 105 |
| M2-109 | Windows build + install-task script + power settings | 101 |
| M2-110 | DPAPI token vault + OAuth helper | 102 |
| M2-111 | LLM router + providers (ollama, openai-compatible, claude cli) | 101 |
| M2-116 | Wire the daemon: `cmd/mayank2 run` starts everything | 103, 104, 105, 106, 108, 110, 111 |
| M2-117 | Wire remaining job handlers: research, script, visuals, blog | 116, 203, 204, 207, 401, 402, 403 |
| M2-119 | Wire real `scout.topics` handler onto the scheduler's daily trigger (M2-202) | 116, 202 |
| M2-120 | Make GET/POST /api/topics real (was a stub) | 106, 102 |
| M2-201 | Channel + format config, `channels` sync | 102 |
| M2-202 | Niche Scout | 111, 201, 103 |
| M2-203 | Research brief with sources | 111, 202 |
| M2-204 | Script writer (EN native, HI native) | 203 |
| M2-205 | Compliance script gates G1–G7 | 204 |
| M2-206 | media-tools: Kokoro TTS + faster-whisper STT | — |
| M2-207 | Stock visuals (Pexels, Pixabay) with license records | 101 |
| M2-208 | Remotion compositions: explained_60s, myth_vs_fact, top_n | — |
| M2-209 | Render long + short + thumbnail + subs | 206, 207, 208, 103 |
| M2-210 | Final gates F1–F6 + approval flow | 205, 209, 105 |
| M2-211 | YouTube OAuth + uploader for 4 channels + quota tracking | 110, 210 |
| M2-212 | Analytics pull + scores | 211 |
| M2-213 | Storage retention + R2 presigned URLs | 102 |
| M2-118 | httpapi: per-address bind resilience and serve the built dashboard | 106, 107, 116 |
| M2-301 | Instagram Reels publisher | 210, 213 |
| M2-302 | Facebook Page publisher | 210, 213 |
| M2-303 | X (business) publisher | 210, 213 |
| M2-304 | Pinterest publisher | 210, 213 |
| M2-401 | Blog draft → Mayankbuilt (canonical post) | 105, 111 |
| M2-402 | Repurpose blog post to LinkedIn | 401 |
| M2-403 | Repurpose blog post to X (personal account) | 401 |
| M2-404 | Medium import-story link prep | 401 |
| M2-121 | Wire `blog.medium` into the daemon; chain it off `blog.merge` | 105, 116, 117 |
| M2-501 | Builder planner: spec/architecture → ordered subphase plan | 103, 105 |
| M2-502 | Builder implementer: worktree + headless Opus, never main | 501 |
| M2-503 | Builder auditor: parallel audit against architecture/design | 502 |
| M2-504 | Builder phase gate: pass/fix-retry decision, timeout, limit pause | 503 |
| M2-505 | Builder dashboard screen + PR link | 504, 106 |
| M2-601 | Revenue tracking: manual + semi-automated entries, revenue API | 212, 106 |
| M2-602 | Micro-SaaS idea → spec → Builder flow | 501, 601 |
| M2-603 | Agency lead list + proposal drafts | 111, 601 |
| M2-604 | Pinterest affiliate boards | 304, 601 |

Ticket files exist for P1 and P2. P3+ tickets are written when P2 reaches M2-210. P1–P6 all have real ticket files now (M2-601…604 was the last placeholder row).

## 3. Core interfaces (Go)

```go
// internal/queue
type Handler func(ctx context.Context, job Job) (result json.RawMessage, err error)
type Resource string // "heavy" | "light" | "net"
func (q *Queue) Register(jobType string, res Resource, maxAttempts int, h Handler)
func (q *Queue) Enqueue(ctx context.Context, jobType string, payload any, opts ...EnqueueOpt) (id string, err error)
// EnqueueOpt: RunAt(t), Priority(n), Parent(id), ContentID(id)
// Returning queue.Permanent(err) skips retries.

// internal/publish
type Publisher interface {
    Platform() string                                          // "youtube", "instagram", ...
    Publish(ctx context.Context, p PublishRequest) (PublishResult, error) // must be idempotent on p.IdempotencyKey
}

// internal/compliance
type Gate interface {
    ID() string
    Check(ctx context.Context, item ContentItem) GateResult    // {Passed, Score, Detail}
}

// internal/llm — see ARCHITECTURE.md §6
```

## 4. HTTP API

Base `/api`, JSON, auth via session cookie (login with token at `POST /api/login`).

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/health` | liveness, version, paused state |
| GET | `/api/agents` | agent cards (state, current job, last success, next run) |
| GET | `/api/jobs?status=&type=&limit=` | jobs list |
| POST | `/api/jobs/{id}/retry` | re-queue a failed/dead job |
| GET | `/api/approvals?status=pending` | approvals queue |
| GET | `/api/approvals/{id}` | detail incl. compliance report, sources, destinations |
| POST | `/api/approvals/{id}/decision` | `{decision: approve|reject|redo, note?, edits?}` |
| GET | `/api/content?channel=&stage=` | pipeline items |
| GET | `/api/calendar?from=&to=` | scheduled publications |
| GET | `/api/channels/{id}/metrics?range=` | channel analytics |
| GET/POST | `/api/topics` | list / add manual topic |
| GET | `/api/builder` | plans, threads, audits |
| GET/POST | `/api/revenue` | revenue entries |
| POST | `/api/pause` / `/api/resume` | `{scope: "all" | agent}` (resume needs PIN) |
| GET | `/api/events/stream` | SSE: `job.updated`, `approval.created`, `approval.decided`, `agent.state`, `alert` |
| GET | `/media/{asset-id}` | stream a preview file (auth required) |

## 5. Job types

`scout.topics`, `research.brief`, `script.write`, `compliance.script`, `voice.tts`, `visuals.fetch`, `render.long`, `render.short`, `render.thumbnail`, `compliance.final`, `approval.request`, `publish.youtube`, `publish.instagram`, `publish.facebook`, `publish.x`, `publish.pinterest`, `publish.linkedin`, `blog.draft`, `blog.merge`, `blog.repurpose`, `analytics.pull`, `storage.cleanup`, `summary.daily`, `builder.plan`, `builder.implement`, `builder.audit`, `builder.gate`.

## 6. Configuration

- `config/config.yaml` (gitignored; copy from [`config/config.example.yaml`](../config/config.example.yaml)).
- `config/channels/*.yaml` — one per channel ([DESIGN.md §3](DESIGN.md#3-video-visual-system-per-channel-brand-kit)).
- `.env` (gitignored; copy from [`.env.example`](../.env.example)) — secrets only.

## 7. Definition of done (every ticket)

- Acceptance criteria met and demonstrated (test output or screenshot in the PR/ticket notes).
- `gofmt`, `go vet`, `go test ./...` green; web: `npm run lint && npm test && npm run build` green.
- No secrets in code or logs; new config keys added to `config.example.yaml` / `.env.example`.
- Docs touched if behavior or decisions changed.

## 8. Existing code

- `internal/tickets` — parses/updates ticket frontmatter; reused by Builder and thread tooling.
- `internal/config` — early Builder-only config; **M2-101 replaces it** with the full schema in `config.example.yaml`.
