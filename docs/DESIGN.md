# Design — Dashboard, Telegram, Video

> Requirements: [PRD.md §M2](PRD.md#m2-control--telegram--dashboard). API behind it: [SPEC.md §4](SPEC.md#4-http-api).

## 1. Dashboard

**Goal:** in 5 seconds Mayank knows whether everything is healthy; in 60 seconds he can approve an item on his phone.

### 1.1 Layout

- Desktop: left sidebar navigation, top bar with **global status pill** (Running / Paused / Degraded), **Pause all** switch, pending-approvals count, and the Claude usage-limit state.
- Phone (≤ 640 px): bottom tab bar with 5 tabs — **Home, Approvals, Pipeline, Builder, More**. All screens single column. Touch targets ≥ 44 px.
- Live updates over SSE; a small "live" dot shows connection state; reconnects automatically.

### 1.2 Screens

| Screen | Contents |
|---|---|
| **Home** | Agent cards (Scout, Research, Script, Compliance, Voice, Visuals, Render, each Publisher, Analytics, Blog, Builder Implementer, Builder Auditor): state (idle/working/paused/error), current job, last success, next scheduled run. Today vs target (Shorts 8, Long 2, per channel). Alerts list (dead jobs, quota low, disk low, token expiring). |
| **Approvals** | Queue of pending items. Detail: video player (9:16 or 16:9), title, description, tags, thumbnail, destinations with scheduled times, **compliance report** (gates with pass/fail and details), sources list. Buttons: Approve, Reject, Redo with note, Edit title/description inline. |
| **Pipeline** | Kanban by stage (Topic → Research → Script → Compliance → Voice → Visuals → Render → Approval → Scheduled → Published) with filters by channel. Click a card for its job history and logs. |
| **Calendar** | Week view per channel/platform; drag to reschedule (respecting caps). |
| **Channels** | Per channel: subs, views, avg view %, CTR trend, top/bottom videos, format and topic scores, ramp-up stage. |
| **Builder** | Per repo: plan tree (phases → subphases) with status; two live panes **Implementer** and **Auditor** (current subphase, last output lines, session cost/limit), audit findings, PR links. |
| **Revenue** | Manual and imported entries by line (ads, affiliate, agency, SaaS, sponsor), monthly chart. |
| **Topics** | Scout suggestions with scores; add a manual topic/URL; mark as rejected. |
| **Logs** | `events` stream with filters (actor, kind, channel). |
| **Settings** | Read-only view of config; pause toggles per agent; provider health and free-tier quota usage; disk and RAM. |

### 1.3 Visual system

- Dark theme default, light theme supported. Font: **Inter** (UI), **JetBrains Mono** (logs).
- Tokens (CSS variables on `:root`, overridden under dark):

| Token | Light | Dark | Use |
|---|---|---|---|
| `--bg` | `#F7F8FA` | `#0B0D12` | page |
| `--surface` | `#FFFFFF` | `#141821` | cards |
| `--border` | `#E3E6EC` | `#262B36` | dividers |
| `--text` | `#111418` | `#E8EBF0` | body |
| `--muted` | `#5B6472` | `#8A93A3` | secondary |
| `--accent` | `#4F46E5` | `#818CF8` | primary actions |
| `--ok` | `#15803D` | `#4ADE80` | success / pass |
| `--warn` | `#B45309` | `#FBBF24` | quota low, retrying |
| `--err` | `#B91C1C` | `#F87171` | failed / dead |

- Status is never color-only: every state has an icon + label.
- Components from **shadcn/ui** (Radix + Tailwind): Card, Badge, Tabs, Sheet (mobile detail), Dialog, Table, Switch, Toast. Charts with **Recharts**.

## 2. Telegram bot UX

**Approval message** (video sent as a preview, caption below):

```
🎬 yt-ai-en · Short · Myth vs Fact
"AI can't read your screen… right?"
⏰ Today 19:30 ET → YouTube, IG Reel, FB Reel, X
✅ Compliance 9/9 · 📚 4 sources · ⏱ 38s
[✅ Approve] [❌ Reject] [🔁 Redo…]
```

- **Redo…** asks for a one-line note; the note goes to the Script agent.
- After a tap, the buttons are replaced with the result (`Approved by you 14:02`) so a second tap can't happen.
- Blog items send the Vercel preview link; Builder sends the PR/compare link and audit summary.

**Commands:** `/status` (one-screen health), `/today` (published vs target), `/queue` (pending approvals), `/pause <agent|all>`, `/resume <agent|all>` → asks for PIN, `/topic <text or URL> [channel]` (add a manual topic), `/blog <topic>`, `/help`.

**Daily summary (22:30 IST):** published per channel, pending approvals, failures, best performer of the last 24 h, free-tier quota left, disk free, Claude limit state.

**Alerts (immediate):** job dead-lettered, publisher auth expired, disk < 100 GB, laptop came back online after > 30 min offline, usage limit hit/reset.

## 3. Video visual system (per channel brand kit)

`config/channels/<id>.yaml` holds:

```yaml
id: yt-ai-en
language: en
brand:
  primary: "#22D3EE"
  secondary: "#0F172A"
  font_heading: "Montserrat"
  font_body: "Inter"
  caption_style: word-highlight   # word-highlight | karaoke | block
  intro: none                     # Shorts never use intros
voice:
  engine: kokoro
  voice_id: TBD
  speed: 1.05
formats: [explained_60s, myth_vs_fact, top_n, comparison, how_to, news_breakdown]
windows:
  tz: America/New_York
  slots: ["12:30", "19:30"]
disclaimer: null
```

- **Shorts/Reels:** 1080×1920, captions centered in the safe zone (avoid bottom 20% and right 15% where platform UI sits), a visual change every 1.5–3 s, progress bar optional per format.
- **Long:** 1920×1080, chapter title cards, lower-thirds for sources ("Source: …"), B-roll from stock, motion-graphic charts for numbers.
- **Thumbnails:** 1280×720, ≤ 4 words, one focal element, brand color block; 2 variants generated, Mayank picks or the first is used.
- Each format is a separate Remotion composition in `remotion/src/compositions/<format>/`, parameterized by brand kit and script beats.
