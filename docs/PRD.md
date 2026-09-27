# Mayank 2.0 — Product Requirements (PRD)

> Context and decisions: [CONTEXT.md](CONTEXT.md). Build breakdown: [SPEC.md](SPEC.md).

## 1. Problem

Mayank works a full-time job. The hours he is away are idle hours for his laptop and for his income. He wants a system that keeps producing content, publishing it and building software for him — safely, cheaply and without him watching — so it becomes an income pipeline and later an agency.

## 2. Users

| User | Needs |
|---|---|
| **Mayank (operator)** | Approve drafts in seconds from his phone; see what every agent is doing; pause anything instantly; trust that nothing breaks platform rules. |
| **Viewers** (US/EU English, India Hindi) | Useful, accurate, watchable videos worth subscribing to. |
| **Future agency clients** | The same pipeline, set up for their business. (Not in v1; architecture must not block it.) |

## 3. Goals and success metrics

| Goal | Metric | Target |
|---|---|---|
| Runs unattended | Days without Mayank touching the laptop | 7+ |
| Output | Approved items published per day at full speed | 8 Shorts + 2 long videos, plus blog/social posts |
| Approval speed | Time for Mayank to review one item on his phone | < 60 seconds |
| Account safety | Strikes, demonetization, suspensions | **0** |
| Cost | Extra spend beyond Claude Pro + electricity | ~₹0 (free tiers) |
| Growth loop | Share of new scripts shaped by analytics from past posts | 100% after week 4 |
| Money | First revenue from each line (agency, affiliate, ads) | Tracked on the Revenue screen |

## 4. Scope — modules

### M1. Foundation (daemon)
- Starts at Windows logon, restarts after a crash, keeps working with the laptop locked.
- Durable job queue: every job survives restarts; failed jobs retry with backoff; jobs that keep failing go to a dead-letter list with the error.
- Heavy jobs (render, TTS, transcription, local LLM) run **one at a time**; light jobs run in parallel.
- Global and per-agent pause/resume.
- Structured logs and an audit trail of every action.

### M2. Control — Telegram + Dashboard
- **Telegram:** responds only to Mayank's user ID and chat. Commands: `/status`, `/today`, `/queue`, `/pause [agent|all]`, `/resume [agent|all]` (PIN required), `/help`. Approval messages carry a preview and buttons **Approve / Reject / Redo with note**. Each button works once.
- **Dashboard:** live view of all agents, the pipeline, approvals with video player and compliance report, content calendar, per-channel analytics, Builder progress, revenue, logs, settings. Reachable from the phone over Tailscale only.
- Daily summary message at a fixed time: published, pending, failed, top performer, usage-limit status.

### M3. Content Engine (faceless channels)
- **Niche Scout:** finds topics per channel from YouTube Data API statistics, news RSS feeds, and (if access is granted) the Google Trends API. Scores each topic on demand, competition, freshness and fit.
- **Research:** builds a sourced brief (facts with URLs) for each topic. Accepts inputs from Mayank: a topic, a link, an article, or a video URL (used as research only — see [COMPLIANCE.md](COMPLIANCE.md)).
- **Script:** writes an original script per channel and language, in one of the formats in [CONTENT_STRATEGY.md](CONTENT_STRATEGY.md). Hindi is written natively, not translated word-for-word.
- **Compliance & Originality Engine:** every script and final video passes all gates before approval.
- **Voice:** Kokoro TTS per channel voice.
- **Visuals:** stock footage search (Pexels, Pixabay), Remotion motion graphics, captions, channel brand kit.
- **Render:** long video 16:9, Short/Reel 9:16, X clip, thumbnail, subtitle file.
- **Scheduler:** fills each channel's calendar at its audience's peak times, obeys caps and the ramp-up plan.
- **Analytics loop:** pulls views, retention and click-through after 24h and 7d; ranks topics, formats and hooks; feeds the Scout and Script agents.

### M4. Distribution
YouTube (4 channels), Instagram Reels, Facebook Page (Reels/video), X business account, Pinterest (Idea/video pins with affiliate-safe links). Each publisher: official API, idempotent (never double-posts), records post ID and URL.

### M5. Blog pipeline (personal tech brand)
Topic backlog → Claude drafts MDX → branch in Mayankbuilt → Vercel preview → approval → merge → LinkedIn post + X personal thread → Medium import link. Never Instagram.

### M6. Builder
- Input: `ARCHITECTURE.md`, design files, `SPEC.md` in the target repo.
- **Planner** splits the spec into phases → subphases (`plans/phase-N/N.M-*.md`); Mayank approves the plan once.
- **Implementer** (Opus 5.5) builds subphase N on a phase branch in its own worktree.
- **Auditor** (Sonnet 5) runs in parallel on the previous finished subphase: checks the diff against architecture, writes and runs tests, compares UI to designs with Playwright screenshots, writes findings to `audit/N.M.md`. Findings become fix tasks before the phase closes.
- Phase gate: all subphases audited and green → PR → Telegram link. Never pushes to `main`.
- Pauses cleanly when the Claude usage limit is hit; resumes after reset.

### M7. Revenue lines (later phases)
Micro-SaaS/Chrome extensions (built by Builder), automation agency (lead list + proposal **drafts** only; Mayank sends them), Pinterest affiliate, newsletter, digital products.

## 5. Non-goals

- No browser automation of social platforms, no scraping behind logins.
- No bought or automated engagement (likes, follows, comments, sub-for-sub).
- No reuploading or remixing other creators' footage or audio.
- No automatic sending of proposals, emails or DMs to people.
- No personalised financial advice, stock tips, or guaranteed-return claims.
- No public internet exposure of the laptop.

## 6. Risks

| Risk | Mitigation |
|---|---|
| Demonetization for mass-produced content | Compliance & Originality Engine, format variety, human approval, ramp-up, analytics-driven quality |
| Free AI tiers change or run out | Model router with multiple providers and a local fallback; quality gate blocks weak scripts instead of publishing them |
| 16 GB RAM contention | Heavy-job semaphore; local model loaded only when needed; night-time rendering window |
| API quota (YouTube ~6 uploads/day default) | Apply for a quota increase; scheduler never exceeds the current quota |
| Laptop sleeps, loses power or Wi-Fi | Task Scheduler restart, power settings, jobs resume from the database, Telegram alert on reconnect |
| Hindi finance content breaks SEBI rules | Education-only scripts, standard disclaimer, compliance gate blocks tips/returns claims |
| Token or secret leak | Secrets in `.env` + DPAPI-encrypted tokens; bot ignores strangers; no shell via Telegram |
