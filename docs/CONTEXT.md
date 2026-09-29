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
| D23 | Finished tickets merge (`--no-ff`, each role's commits kept) into one **phase branch** per phase (`phase/p1-foundation`, …), which ship pushes and keeps in sync with `main`. Each ticket gets a spec-audit doc in `docs/audit/<phase>/`; `docs/audit/<phase>/README.md` is the PR body. Mayank opens one PR per phase and merges it. A ticket is `done` once it is in the phase branch; new ticket branches start from the phase branch. | Mayank's request (2026-09-28): one branch to review and merge, with a written audit per feature. |
| D24 | **Everything runs with only the keys you have.** Each integration is enabled by its `.env` keys (or config); missing keys switch it off cleanly, and `doctor` shows ⚪ instead of failing. One free Gemini key must be enough for the LLM path to work end to end (M2-114). | Mayank's request (2026-09-28); he signs up for services gradually. |
| D25 | **Superseded D23's phase-branch model with per-feature branches straight off `main`.** Each ticket branches from and rebases onto current `main` (not a shared phase branch). Ship **auto-merges via `gh pr merge`** — no raw `git push origin main` — once a ticket has QA pass and SEC pass (when `needs-sec: yes`) on a cleanly-rebased, fully-green branch. Raw pushes to `main` stay blocked for everyone but Mayank. A genuine product-behavior conflict (not a mechanical rebase conflict) stops and asks him; already-built work on `phase/p1-foundation` gets migrated to this model ticket by ticket rather than redone. | Mayank's request (2026-09-28): ship one feature at a time, `main` advances continuously instead of batching into one big PR. |
| D26 | **A non-fast-forward branch-landing conflict (a real concurrent-write collision, not a rebase mechanic) is retried by the thread that owns the worktree doing the landing — never by M2-504's phase gate.** Concretely: M2-502's Implementer only ever fast-forwards/restores the target repo's own `main` ref from its own worktree (`restoreMainRef`); M2-503's Auditor only ever lands its own commit onto the shared build branch from its own detached worktree (`fastForwardBranch`), and on a genuine non-fast-forward it returns a plain (queue-retryable) error rather than forcing anything — the *queue's* ordinary backoff re-runs that job, which re-enters the same worktree, re-rebases onto the (now-advanced) branch tip, and tries landing again. M2-504 (`builder.gate`, this ticket) only ever observes an audit run *after* it has already landed (the `audit.done` event, or a readable `audit/N.M.md` on the branch) — a landing conflict is resolved or re-attempted entirely inside implementer.go/auditor.go before gate.go is ever invoked, so it never reaches Gate as a distinguishable outcome, and there is nothing for Gate to retry here. Centralizing this retry in Gate instead would require Gate to hold a worktree and drive git itself, duplicating the existing mechanism for no benefit. | Resolved 2026-09-29 (M2-504), consolidating the identical open question M2-502 and M2-503 both left in §5: the answer follows directly from which code actually holds the rebased, locally-consistent state needed to retry safely — that's always the worktree that's landing, never the downstream gate. See `internal/builder/gate.go`'s package doc comment and `tickets/M2-504.md` Notes for the same reasoning in context. |
| D27 | **`blog.medium` sends its Medium import link through a new, narrow `internal/telegram.Bot.SendMessage` / `Bot.ChatID` accessor — not a wholesale `Bot.Client()` and not a second Telegram client built inside `internal/blog`.** `Client`'s only genuinely unsafe-to-share method is `GetUpdates`: a second caller polling it would race `Bot.Run`'s own long-poll loop and could silently drop inbound approval taps/commands, bypassing the allowlist that lives entirely in `Bot.handleMessage`, not in `Client`. `SendMessage` is stateless and carries none of that risk, and its signature matches `internal/blog`'s `TelegramSender` exactly, so `*telegram.Bot` satisfies it directly with no adapter type. | Resolved 2026-09-29 (M2-121), closing §5 open question 6: centralizes on the one bot token / HTTP client per CLAUDE.md's "Telegram bot" (M2-105) being the one place Telegram access should live, without exposing the one method that would actually be risky. See `internal/telegram/bot.go`'s `SendMessage`/`ChatID` doc comments and `tickets/M2-121.md` Notes. |

## 5. Open questions (ask Mayank; record the answer in the decision log)

1. Which repos may Builder touch, and which are "company work"? (Candidates: `Layin`, `Mayankbuilt`, `Social Media`.)
2. Channel names and handles for the 4 YouTube channels and the X accounts.
3. OK for Builder to pause when the Pro usage limit is hit and resume after reset? (Assumed **yes**.)
4. Is Hindi content written natively (assumed **yes** — never literal translation) and voiced with Kokoro Hindi?
5. (M2-117) `script.write`'s format picker needs a channel's configured `Formats` subset
   (`config/channels/*.yaml`), but a `research.brief`/`script.write` job payload has no path to it without
   a filesystem lookup neither handler otherwise needs. Currently defaults to every format in
   `formats.Catalog` when a job doesn't supply `allowed` explicitly — should the daemon resolve a channel's
   configured subset automatically (and if so, from which component), or is defaulting to the full catalog
   (still G6/topic-fit scored) fine long-term?
6. OK to add YouTube OAuth scope `yt-analytics-monetary.readonly` (requires re-consent) so M2-601 can auto-pull `estimatedRevenue`? Current M2-211/M2-212 scopes are upload + yt-analytics.readonly only. Meta Page ad revenue stays manual (no clean Insights path).
7. Amazon Associates account + Pinterest affiliate board(s) for M2-604 (and which software affiliate programs)?
8. **How should the accumulated `dev` branch (P1-P6, built under the pre-D25 phase-branch model, now including M2-116) land on `main`?** A ship session was asked (2026-09-29) to open one PR bundling all ~43 tickets from `dev` into `main` and auto-merge it under D25's authorization. D25's own text is a per-ticket model — each ticket branches off and rebases onto current `main` individually, is auto-merged by ship once it has real QA (+SEC when flagged) on a clean, green branch — and its stated purpose was explicitly to *stop* batching ("ship one feature at a time... instead of batching into one big PR"). It also says already-built phase-branch work should be "migrated to this model ticket by ticket rather than redone," which reads as migrating `dev`'s ~43 tickets onto `main` individually, not as one combined PR. A single 43-ticket `dev`→`main` PR is the batching D25 was written to end, and no decision-log entry or ticket documents a one-time exception for it. Ship did not open that PR or touch `main`; it merged M2-116 into `dev` only (real QA+SEC pass, real green `go test ./...` post-merge — see `crew/BRANCH_MAP.md`) and is leaving the `dev`→`main` landing question here rather than guessing on product process. Options as ship sees them: (a) ship walks the ~43 `dev` tickets onto `main` one at a time per D25's literal text, or (b) Mayank explicitly authorizes a one-time bulk `dev`→`main` PR as a stated exception (and that exception gets its own decision-log row so it's not re-litigated next time). Either is fine; ship needs the call before pushing anything to `main`.
9. `blog.draft`/`blog.merge`/`blog.repurpose` need a `config.Config.Blog` section to be handler-constructible
   (found while wiring M2-116: `internal/config.Config` has no `Blog` field today, unlike `Content`/`Builder`),
   and none of the three blog job-type packages currently exposes a `Handler()`/`RegisterHandler(s)` method
   to wire in the first place (M2-401/M2-402/M2-403/M2-404 built the publish/repurpose logic itself but not
   the daemon-wiring surface) — a future ticket needs to add both before `cmd/mayank2/run.go` can register
   these three job types. Left deliberately unregistered in M2-116; see that ticket's Notes. **Update
   2026-09-29 (M2-121):** M2-117 has since added `config.Config.Blog` and wired all three; this entry is
   stale but left as-is (not this ticket's scope to retroactively edit M2-117's own history) rather than
   silently deleted — a future editor should feel free to remove it once confirmed fully superseded.
10. (M2-122) Instagram/Facebook/Pinterest's `R2KeyResolver` field is documented as "nil → use local
    `VideoPath` as the R2 key" (M2-116's own Notes call this a safe default). That's true of the field
    itself, but the end-to-end assumption behind it does not hold: **nothing in the repo ever uploads a
    render to R2** (`git grep "\.Upload(ctx" -- internal cmd` finds exactly one hit, a test in
    `internal/storage/r2_test.go` — no production code path calls `(*storage.R2Client).Upload`).
    `internal/content/render.go`'s `Renderer` writes finished MP4s to the local
    `storage.KindRenders` directory only. So even with M2-122's fix wiring a real, configured
    `R2Client.PresignGET` into Instagram/Facebook/Pinterest, presigning `VideoPath` as the key points at
    an object that was never uploaded — Meta/Pinterest's fetch of that presigned URL will 404 once a
    real publish is attempted with real R2 credentials. Needs a product decision on where in the
    pipeline an R2 upload belongs (at render time in `content.Renderer`? A new step between
    `compliance.final`/approval and `publish.*`? Inside each publisher just before presigning?) before
    Instagram/Facebook/Pinterest can actually publish end-to-end, even once every other piece
    (approval, tokens, R2 credentials) is in place. Not fixed by M2-122 (crash-prevention/wiring scope
    only) — flagging here per this file's own "write the question here instead of guessing on product
    behavior" rule.

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
- [ ] Amazon Associates (and any software affiliate programs) + Pinterest board(s) for affiliate pins (M2-604).

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
