# Mayank 2.0 — Core Context

> **Read this first.** This file is the memory of every product conversation so far. Any new Claude/Cursor thread should load it before touching code. When a decision changes, update the **Decision log** here in the same commit.
>
> Owner: Mayank Mishra · Started: 2026-09-27 · Status: documentation phase, no running code yet.

## 1. What we are building, in one paragraph

**Mayank 2.0** is an always-on system that runs on Mayank's laptop while he is at work. It is a team of AI agents that (1) runs a **faceless content business** — 4 YouTube channels plus Instagram, Facebook, X and Pinterest — aimed at views, virality and money; (2) publishes Mayank's **personal tech blog** to his portfolio, LinkedIn, Medium and a personal X account; and (3) runs **Builder**, two parallel coding agents that build software (Mayank's company work, micro-SaaS products, agency client work) from architecture docs, designs and specs. Mayank controls it from his phone over **Telegram** and watches it on a **dashboard**. Nothing is published until he taps Approve.

## 2. Why — the goal

An **income pipeline** that grows into an **agency across many domains**:

1. **Content business** → ad revenue (YouTube, Facebook, X), affiliates, sponsors. Targets high-paying US/EU audiences in English plus mass reach in Hindi.
2. **Micro-SaaS / Chrome extensions** built by Builder → recurring income.
3. **Automation agency** for US small businesses → sell setups like Mayank 2.0 itself. The fastest cash.
4. **Pinterest + affiliate** traffic → Amazon and software affiliate income.
5. Later: newsletter (Beehiiv), digital products (templates, kits), and running the same pipeline for other creators.

## 3. Hard constraints

| Constraint | Detail |
|---|---|
| **Minimal money** | No paid AI APIs for content. Claude (Pro plan) is reserved for **Builder and Mayank's company work only**. Content uses free hosted open-model tiers and local models. |
| **Hardware** | One Windows laptop: Intel Core Ultra 5 225H (14 cores), **16 GB RAM**, Intel integrated GPU (no NVIDIA), 1 TB SSD (~760 GB free). Runs 24/7 on the charger. |
| **Account safety** | Never violate YouTube, Meta, X, Pinterest or LinkedIn rules. **Official APIs only**, no browser bots, no fake engagement. Compliance is designed in, not bolted on (see [COMPLIANCE.md](COMPLIANCE.md)). |
| **Human approval** | Every public post needs Mayank's one-tap approval on Telegram. Builder never pushes to `main`. |
| **Parallel work** | Docs must let several Claude/Cursor threads work at the same time without this chat. |

## 4. Decision log

| # | Decision | Why |
|---|---|---|
| D1 | Backend is **Go** (one `mayank2.exe`). | A single binary is easy to run as a Windows background process and hard to crash. Mayank already uses Go (Olympus). |
| D2 | **SQLite** (WAL) for data and **the job queue** — no Redis. | One machine, low traffic. Every extra service is one more thing that can die while he's away. |
| D3 | **Telegram bot** for phone control and approvals. | Free push notifications, video previews, inline buttons. |
| D4 | **React + Vite dashboard** embedded in the Go binary, reached remotely over **Tailscale**. | Mayank is a frontend developer. No public exposure. |
| D5 | **Publish only after approval.** | One bad AI post under a real account costs more than the time saved. |
| D6 | Builder uses **headless Claude Code** (`claude -p`) in **git worktrees**, not GUI control of Cursor. | GUI automation breaks on popups and screen lock. Mayank can still open the worktrees in Cursor and watch. |
| D7 | Builder runs **two threads in parallel**: **Implementer** (Opus 5.5) builds subphase N; **Auditor** (Sonnet 5) audits the finished subphase against architecture and design, writes tests and finds gaps, while the Implementer moves to N+1. | Mayank's requirement; it catches drift early. |
| D8 | Tickets and specs are **markdown files in each repo** (`tickets/*.md`, `plans/`). | Reviewable in git, no external tool. |
| D9 | The content business is **faceless and niche-driven**, not Mayank's personal brand. The personal tech brand lives only in the blog pipeline. | Goal is money and virality in high-value regions. |
| D10 | **4 YouTube channels** = 2 niches × 2 languages: **Money & Side Hustles** (EN, HI) and **AI Tools & Tech Explained** (EN, HI). | Highest ad rates and affiliate programs; one research job feeds both languages; AI/tech feeds the SaaS and agency lines. See [CONTENT_STRATEGY.md](CONTENT_STRATEGY.md). |
| D11 | Cadence at full speed: **8 Shorts + 2 long videos per day in total** (2 Shorts/day/channel, 3–4 long/week/channel), after a **4-week ramp-up**. | Consistent over time, never flooding. |
| D12 | Video accounts: YouTube ×4, Instagram, Facebook Page, **X "business" account**, Pinterest. Blog accounts: portfolio (Mayankbuilt), LinkedIn, Medium, **X "personal" account**. Blog never goes to Instagram. | Mayank's channel map. |
| D13 | **Model router**, cheapest capable model first: local **Ollama** (~8B Qwen/Gemma) for small text tasks → **free hosted open-model tiers** (Groq, Cerebras, OpenRouter free, Gemini free tier — verify limits) for research and scripts → Claude only for Builder/blog. | Minimal spend; 16 GB RAM can't run large models well. Local 8B models are **not** Claude-level. |
| D14 | Voice: **Kokoro TTS** locally (English + Hindi). ElevenLabs is an optional paid upgrade later. | Free; faceless channels need a voice. |
| D15 | Visuals: **Pexels/Pixabay** stock footage (free license) + **Remotion** (React-rendered motion graphics) + **ffmpeg**. | Free, legal, and uses Mayank's React skills. |
| D16 | Speech-to-text: **faster-whisper** (Python, CPU int8). | Free, Hindi + English, runs on this CPU. |
| D17 | Other people's videos are **research input only** (their transcript becomes notes). Their footage or audio is never reused. | Reused content is not monetizable and gets copyright strikes. |
| D18 | Mayank asked for an algorithm to "dodge" platform detection. **We build a Compliance & Originality Engine instead** — content genuinely meets policy. We do **not** build anything whose purpose is to hide mass production from platform classifiers. | Evasion gets whole channels demonetized/banned later; real compliance keeps them. |
| D19 | Medium has no usable publishing API → portfolio post is canonical; the agent prepares a Medium **"Import a story"** link for a one-tap manual import. | Medium stopped issuing integration tokens. |
| D20 | Media storage: local disk; **Cloudflare R2** (free tier) for temporary public URLs that Instagram/Facebook APIs require. Auto-cleanup ~7 days after publishing. | Keeps the 1 TB SSD from filling up. |
| D21 | Build order: **1 Foundation + dashboard + Telegram → 2 Content engine + compliance + YouTube → 3 Meta/X/Pinterest → 4 Blog pipeline → 5 Builder → 6 Micro-SaaS/agency.** | The content engine is the main job. |
| D22 | Manual AI chats (Cursor, Claude Code) work as a **crew of roles**: lead, fe, be, ai, qa, sec, ship. They are defined in [`crew/`](../crew/README.md), and each role keeps local memory so any chat resumes where that role stopped. Flow: build → `in-review` → qa (+ sec when flagged) → ship rebases and opens the PR → Mayank merges. Git hooks in `.githooks/` enforce format/lint/types/tests, a secret scan, the commit format (non-ticket scopes `crew`, `docs`, `repo` allowed) and no pushes to `main`. | Mayank wants parallel chats that act like a senior team and resume without re-explaining. Builder stays headless (D6/D7). |

## 5. Open questions (ask Mayank; record the answer in the decision log)

1. Which repos may Builder touch, and which are "company work"? (Candidates: `Layin`, `Mayankbuilt`, `Social Media`.)
2. Channel names and handles for the 4 YouTube channels and the X accounts.
3. OK for Builder to pause when the Pro usage limit is hit and resume after reset? (Assumed **yes**.)
4. Is Hindi content written natively (assumed **yes** — never literal translation) and voiced with Kokoro Hindi?

## 6. External accounts Mayank must set up (real waiting time)

- [ ] 4 YouTube channels (2 EN, 2 HI) under one Google account (Brand Accounts).
- [ ] Google Cloud project → YouTube Data API v3 → **API audit** (uploads stay private until approved) + **quota increase** (default quota allows about 6 uploads/day).
- [ ] Meta developer app → Instagram **Professional** account linked to a **Facebook Page**; permissions for content publishing.
- [ ] X developer account for both X accounts (check the current free-tier posting limits).
- [ ] Pinterest developer app (API v5).
- [ ] LinkedIn developer app with "Share on LinkedIn".
- [ ] Telegram bot via @BotFather; Tailscale on laptop + phone; Cloudflare R2 bucket.
- [ ] Free-tier keys: Groq / Cerebras / OpenRouter / Gemini; Pexels and Pixabay API keys.
- [ ] Laptop: battery charge limit 80%, lid close = do nothing, sleep on AC = never.

## 7. Documents map

| File | What it answers |
|---|---|
| [PRD.md](PRD.md) | What each module must do, and how we know it works |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Components, data flow, database schema, queue, security |
| [SPEC.md](SPEC.md) | Phases → subphases → tickets, interfaces, job types, APIs |
| [DESIGN.md](DESIGN.md) | Dashboard screens, Telegram UX, video visual system |
| [COMPLIANCE.md](COMPLIANCE.md) | Platform rules and the Compliance & Originality Engine |
| [CONTENT_STRATEGY.md](CONTENT_STRATEGY.md) | Channels, niches, formats, cadence, monetization |
| [../README.md](../README.md) | Stack, libraries, how to run |
| [../CLAUDE.md](../CLAUDE.md) · [../.cursor/rules/](../.cursor/rules/) | Rules every agent thread follows |
| [../crew/](../crew/README.md) | AI crew roles for manual chats (`you are be`), plan, ship queue |
