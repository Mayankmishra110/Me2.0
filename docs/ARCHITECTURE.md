# Mayank 2.0 — Architecture

> Why each choice was made: [CONTEXT.md §4](CONTEXT.md#4-decision-log). What gets built when: [SPEC.md](SPEC.md).

## 1. System overview

```
                       ┌──────────────────────── MAYANK (phone / browser) ───────────────────────┐
                       │  Telegram: approve · reject · redo · pause · status · daily summary      │
                       │  Dashboard (React) over Tailscale: agents · pipeline · approvals · stats │
                       └──────────────┬─────────────────────────────────────┬────────────────────┘
                                      │ Bot API long-poll (outbound only)    │ HTTPS on tailnet IP only
┌─────────────────────────────── LAPTOP · Windows · always on ─────────────┼────────────────────────────────┐
│                                      ▼                                     ▼                                │
│ ┌──────────────────────────── mayank2.exe (Go, single process) ─────────────────────────────────────────┐ │
│ │ telegram/   httpapi/ (REST + SSE + embedded web/dist)   scheduler/ (cron)   events/ (audit + live bus) │ │
│ │ ────────────────────────────────────────────────────────────────────────────────────────────────────── │ │
│ │ queue/  durable jobs in SQLite · lease + retry + dead-letter · resource classes: heavy=1 light=4 net=2 │ │
│ │ ────────────────────────────────────────────────────────────────────────────────────────────────────── │ │
│ │ content/  scout → research → script → compliance → voice → visuals → render → package → approval      │ │
│ │ publish/  youtube ×4 · instagram · facebook · x(business) · pinterest · linkedin · x(personal) · blog  │ │
│ │ analytics/   blog/   builder/ (planner · implementer · auditor)   llm/ (router)   secrets/   storage/  │ │
│ └──────┬───────────────┬─────────────────┬───────────────────┬────────────────┬──────────────────┬──────┘ │
│        ▼               ▼                 ▼                   ▼                ▼                  ▼        │
│  SQLite (WAL)    media-tools (Python)  remotion (Node)     ffmpeg        Ollama (local)     git worktrees │
│  data/mayank2.db  Kokoro TTS           React video         cut · mux ·   qwen/gemma ~8B     + claude -p   │
│                   faster-whisper       templates           captions      nomic-embed-text   (Pro plan)    │
│  data/media/  raw · work · renders · published (cleanup after 7 days)                                      │
└────────┼───────────────────────────────────────────────────────────────────────────────────────┼──────────┘
         ▼                                                                                        ▼
  Free LLM tiers (Groq · Cerebras · OpenRouter free · Gemini free)       GitHub (branches, PRs) · Vercel (portfolio)
  Pexels · Pixabay · YouTube Data/Analytics API · Meta Graph API · X API v2 · Pinterest API v5 · LinkedIn API
  Cloudflare R2 (temporary public media URLs) · Telegram Bot API
```

Everything runs inside **one Go process** plus short-lived child processes (Python, Node, ffmpeg, claude). There are no always-on servers besides Ollama.

## 2. Components

| Package | Responsibility |
|---|---|
| `cmd/mayank2` | CLI: `run` (daemon), `status`, `doctor` (checks tools, keys, disk, RAM), `migrate`, `retry <job>`, `approve <id>`, `set-pin`, `auth <platform> <account>` (OAuth sign-in) |
| `internal/config` | Load `config/config.yaml`, `config/channels/*.yaml`, `config/formats/*.yaml`, `.env` |
| `internal/db` | Open SQLite (WAL, `busy_timeout`), run embedded migrations from `migrations/*.sql` |
| `internal/queue` | Enqueue, claim with lease, heartbeat, complete, retry with backoff, dead-letter; worker pool per resource class |
| `internal/scheduler` | Cron triggers (scout, daily summary, analytics pulls, cleanup) and channel calendars |
| `internal/events` | Append to `events` table and fan out to SSE subscribers |
| `internal/telegram` | Long-poll updates, allowlist, commands, approval messages, PIN check |
| `internal/httpapi` | REST + SSE for the dashboard; serves the embedded `web/dist`; binds only to localhost and the Tailscale IP |
| `internal/secrets` | Encrypt/decrypt OAuth tokens with Windows DPAPI (`golang.org/x/sys/windows`) |
| `internal/llm` | Model router over providers: `ollama`, `openai_compat` (Groq, Cerebras, OpenRouter, Gemini), `claude_cli` |
| `internal/content` | Content pipeline stages (one file per stage) |
| `internal/compliance` | The Compliance & Originality Engine ([COMPLIANCE.md](COMPLIANCE.md)) |
| `internal/media` | Wrappers for ffmpeg, media-tools, remotion (JSON in / JSON out) |
| `internal/publish` | One adapter per platform behind a `Publisher` interface |
| `internal/analytics` | Pull metrics, compute scores, write `metrics` and `topic_scores` |
| `internal/blog` | Blog pipeline into the Mayankbuilt repo |
| `internal/builder` | Planner, Implementer, Auditor, worktrees, phase gates |
| `internal/tickets` | Parse/update markdown tickets (exists) |
| `internal/storage` | Local media paths, R2 upload + presigned URLs, retention cleanup |
| `media-tools/` | Python (uv): `tts` (Kokoro), `stt` (faster-whisper) |
| `remotion/` | Node + React: video compositions per format and channel brand kit |
| `web/` | React dashboard |

### Child-process contract

Every external tool is called the same way so it can be tested and swapped:

```
<tool> --in <job-dir>/input.json --out <job-dir>/output.json
exit 0 = success, output.json holds results; exit != 0 = failure, stderr tail goes into the job error
```

Each job gets its own working folder `data/media/work/<job-id>/`.

## 3. Data flows

### 3.1 Faceless video pipeline (per channel)

```
scheduler (daily per channel) ─► scout.topics ─► topics table (scored)
Mayank input (topic / URL / article / video URL) ─► topics table (source=manual, priority boost)
        │
        ▼
research.brief     free-tier LLM + fetched sources → brief.json {facts[], sources[], angle}
        ▼
script.write       free-tier LLM, channel voice + chosen format → script.json {hook, beats[], cta, title, desc, tags, thumb_text}
        ▼
compliance.script  originality, facts-have-sources, claims, metadata honesty, disclosures  ── fail → script.write (max 2) → dead-letter
        ▼
voice.tts          media-tools tts (Kokoro) → voice.wav + word timings
        ▼
visuals.fetch      Pexels/Pixabay search per beat → licensed clips (license URL stored in assets)
        ▼
render.long        remotion composition (format × brand kit) + ffmpeg mux, loudness −14 LUFS → long.mp4 + thumb.png + subs.srt
render.short       9:16 cut(s) from the same script beats → short.mp4 (reused for Reel, X, Pinterest)
        ▼
compliance.final   duration, captions present, music licensed, disclosures set, caps respected
        ▼
approval.request   Telegram preview + dashboard; Approve / Reject / Redo(note → script.write)
        ▼
publish.<platform> per destination, at the channel's scheduled slot; idempotency key = content_id+platform
        ▼
analytics.pull     +24h and +7d → metrics → topic_scores / format_scores → next scout & script
```

English and Hindi versions of one topic share `research.brief` but get **separate native scripts**.

### 3.2 Blog pipeline

```
blog backlog (config/blog-topics.md or /blog <topic> in Telegram)
 → blog.draft (Claude via claude -p, Mayank's voice) → MDX on branch blog/<slug> in Mayankbuilt
 → push branch → Vercel preview URL → approval.request
 → approve: merge to main (fast-forward, via git) → live
 → blog.repurpose → linkedin.post + x_personal.thread drafts → approval → publish
 → medium: Telegram message with the "Import a story" link for one-tap manual import
```

### 3.3 Builder

```
target repo: ARCHITECTURE.md + designs/ + SPEC.md
 → builder.plan (Opus) → plans/phase-N/N.M-<slug>.md → approval.request (plan) → approved
 → Implementer thread (worktree A, branch build/phase-N):
      for each subphase: claude -p → run checks → commit "N.M: …" → emit subphase.done
 → Auditor thread (worktree B, same branch, read-mostly), triggered by subphase.done:
      claude -p (Sonnet): diff vs ARCHITECTURE.md, tests for acceptance criteria, Playwright
      screenshots vs designs → audit/N.M.md {verdict, findings[]}; findings → fix tasks for A
 → phase gate: every N.M audited "pass" + checks green → push branch → PR/compare link → Telegram
 → usage limit detected → both threads pause until reset
```

The Auditor commits only tests and `audit/` files; the Implementer owns source code. Both rebase on the phase branch before committing.

### 3.4 Control

```
Telegram update → allowlist(user_id, chat_id) → command | callback(approve:<id>:<nonce>)
 → state change in SQLite → events → SSE → dashboard updates live
```

## 4. Database schema (SQLite)

All timestamps are UTC ISO-8601 text. IDs are ULIDs (sortable). JSON columns are `TEXT` checked with `json_valid`.

| Table | Key columns |
|---|---|
| `jobs` | `id, type, status(queued/running/succeeded/failed/dead/cancelled), resource(heavy/light/net), priority, payload json, result json, run_at, attempts, max_attempts, lease_until, worker, last_error, parent_id, content_id, created_at, updated_at` |
| `channels` | `id, platform, handle, language, niche, account_ref, status(active/paused/warming), warmup_started_at` |
| `topics` | `id, channel_id, title, source(scout/manual), source_url, score, signals json, status(new/picked/used/rejected), created_at` |
| `content_items` | `id, topic_id, channel_id, kind(long/short/blog/post), format, language, stage, script json, compliance json, created_at` |
| `assets` | `id, content_id, kind(voice/clip/render/thumb/subs/mdx), path, r2_key, license_url, sha256, bytes, delete_after` |
| `approvals` | `id, content_id, kind, summary, preview_path, status(pending/approved/rejected/redo/expired), nonce, note, telegram_message_id, decided_at` |
| `publications` | `id, content_id, platform, account, scheduled_at, status, external_id, url, idempotency_key UNIQUE, error, published_at` |
| `metrics` | `publication_id, captured_at, views, watch_seconds, avg_view_pct, likes, comments, shares, subs_gained, ctr` |
| `scores` | `scope(topic/format/hook/time_slot), channel_id, key, score, samples, updated_at` |
| `script_fingerprints` | `content_id, shingles_hash blob, embedding blob` (originality checks) |
| `oauth_tokens` | `platform, account, encrypted_blob (DPAPI), expires_at` |
| `builds` | `id, repo, plan_path, phase, subphase, thread(implementer/auditor), status, branch, worktree, session_id, attempts, audit_verdict, pr_url, last_error` |
| `quotas` | `provider, window_start, used, limit` (YouTube units, X posts, LLM free-tier requests) |
| `revenue` | `id, line(ads/affiliate/agency/saas/sponsor), source, amount, currency, date, note` |
| `events` | `id, at, actor(agent/user/system), kind, ref, message, data json` (append-only audit) |
| `settings` | `key, value` (pause flags, PIN hash, telegram offset) |

## 5. Job queue design

- **Claim:** `UPDATE jobs SET status='running', lease_until=now+5m, worker=? WHERE id = (SELECT id FROM jobs WHERE status='queued' AND run_at<=now AND resource=? ORDER BY priority DESC, run_at LIMIT 1) RETURNING *`.
- **Heartbeat:** long jobs extend `lease_until` every minute. On startup, `running` jobs with an expired lease go back to `queued`.
- **Retry:** exponential backoff (1m, 5m, 30m, 2h), `max_attempts` per type, then `dead` + Telegram alert.
- **Resource classes:** `heavy` = 1 worker (render, TTS, STT, local LLM), `light` = 4, `net` = 2 (uploads). This protects the 16 GB RAM.
- **Idempotency:** publishers check `publications.idempotency_key` before calling the platform.
- **Pause:** workers check `settings` pause flags (global and per agent) before claiming.

## 6. Model router (`internal/llm`)

```go
type Task string // "research", "script", "metadata", "translate_cleanup", "classify", "blog", "builder"

type Provider interface {
    Name() string
    Complete(ctx context.Context, req Request) (Response, error) // Request: system, messages, max_tokens, json_schema?
    Available(ctx context.Context) bool                          // quota + health
}
```

Routing table in `config/config.yaml`, first available wins:

| Task | Chain |
|---|---|
| research, script | free hosted large open models (e.g. Groq → Cerebras → OpenRouter free → Gemini free) → local Ollama (last resort, flagged "low quality") |
| metadata, classify, translate_cleanup | local Ollama → free hosted |
| embeddings | local Ollama `nomic-embed-text` |
| blog | Claude via `claude -p` (Sonnet 5) |
| builder | Claude via `claude -p` (Opus 5.5 implementer, Sonnet 5 auditor) |

Provider quotas are tracked in `quotas`. A `429` marks the provider unavailable until its window resets. Exact model IDs and free limits live in config, not code, because free tiers change.

## 7. Security

| Area | Rule |
|---|---|
| Telegram | Accept updates only when `from.id` **and** `chat.id` match config; everything else is dropped and logged. No command runs a shell. Callback data = `approve:<id>:<nonce>`; the nonce is single-use. `/resume` and bulk actions need a PIN (bcrypt hash in `settings`). |
| Dashboard | Binds to `127.0.0.1` and the Tailscale IP only. Login with a long random token stored as a hash; session cookie `HttpOnly`, `SameSite=Strict`. |
| Secrets | API keys in `.env` (gitignored). OAuth refresh tokens encrypted with DPAPI in `oauth_tokens`. Never log tokens. |
| Builder | Claude runs with an allowlist of tools and Bash patterns; `git push`, `git reset --hard`, and edits to `.env*` are denied. Never pushes to `main`. |
| Child processes | Fixed argument lists (no shell string building from AI output). Timeouts on every call. |
| Content | Anything an LLM writes is data, never instructions to the daemon. |

## 8. Runtime and deployment

- **Start:** Windows Task Scheduler task "Mayank2" at logon → `bin/mayank2.exe run` (built with `-H windowsgui`, no console window). Restart on failure ×3, no time limit, ignore new instance.
- **Power:** sleep on AC = never, lid close = do nothing, battery charge limit 80% (vendor app). `scripts/install-task.ps1` sets the Windows parts.
- **Processes on the laptop:** `mayank2.exe` (always), `ollama` (service, models load on demand and unload after 5 min idle), children as needed.
- **Resource budget (16 GB):** OS + apps ~6 GB, Ollama 8B Q4 ~5–6 GB when loaded, ffmpeg/Remotion ~2–3 GB, whisper small ~1 GB. Heavy class = 1 keeps peaks safe. Rendering preferred 00:00–08:00 IST (also US prime time for English uploads).
- **Disk:** `data/media` retention job deletes work files after publish and renders 7 days after publish; alert if free space < 100 GB.
- **Clock:** all scheduling in UTC; channel posting windows defined in each channel's time zone.

## 9. Repository layout

```
Mayank2.0/
├─ cmd/mayank2/                 main.go
├─ internal/                    config, db, queue, scheduler, events, telegram, httpapi, secrets,
│                               llm, content, compliance, media, publish, analytics, blog, builder,
│                               tickets, storage
├─ migrations/                  001_init.sql …  (embedded)
├─ media-tools/                 Python (uv): pyproject.toml, mediatools/{tts,stt}.py
├─ remotion/                    Node: package.json, src/compositions/{formats}/, src/brand/
├─ web/                         React + Vite dashboard → web/dist (embedded)
├─ config/                      config.example.yaml, channels/*.yaml, formats/*.yaml, blog-topics.md
├─ docs/                        CONTEXT, PRD, ARCHITECTURE, SPEC, DESIGN, COMPLIANCE, CONTENT_STRATEGY
├─ tickets/                     work items for parallel threads (see SPEC.md §2)
├─ scripts/                     install-task.ps1, build.ps1, dev.ps1
├─ .cursor/rules/               Cursor rules
├─ CLAUDE.md                    Claude Code rules
└─ data/  (gitignored)          mayank2.db, media/, worktrees/, logs/
```
