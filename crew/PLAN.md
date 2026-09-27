# Plan — owned by lead

Updated: 2026-09-28 · Phase: **P1 Foundation** · **Phase branch: `phase/p1-foundation`** (new ticket branches start here; D23)

## North star

First income as early as possible. The P2 exit gives the first real proof: one approved Short per channel
published through the API, with metrics at +24h. Everything in P1 exists to make that safe and hands-off.

## Done — P1 batch 1 (2026-09-28)

M2-101, M2-102, M2-107, M2-109, M2-111, M2-206, M2-207, M2-208 are **done**, merged into
`phase/p1-foundation`, and pushed. PR ready to open: [docs/audit/p1-foundation/README.md](../docs/audit/p1-foundation/README.md).
M2-109 and M2-207 still need a qa/sec crew-role pass (flagged `needs-sec: yes`, built by background
sub-agents and only audited directly by ship so far).

## Now (P1 batch 2 — all `ready`, no overlapping paths)

| Role | Ticket | Why now |
|---|---|---|
| be | **M2-103** durable job queue | Critical path: 108 (scheduler), 202 (scout) wait on it |
| be | **M2-104** events log + SSE | Independent of 103; dashboard needs it next |
| ai | **M2-114** run with only the keys you have | Direct answer to Mayank's "one free Gemini key should work end to end" |
| be | **M2-110** DPAPI secrets vault | Needed before any real OAuth (YouTube etc.) |
| be | **M2-201** channel + format config | Unblocks the P2 content pipeline (202→205) |
| be | **M2-213** storage retention + R2 | Independent, small |
| qa | pass on M2-109 and M2-207 (not yet reviewed by a qa role) | |
| sec | pass on M2-109 and M2-207 (both flagged `needs-sec: yes`) | |
| ship | idle until Mayank opens/merges the P1 PR, then rebase phase-p1 onto main and clean up merged `m2/*` branches | |

## Next

M2-108 scheduler (needs 103+105) · M2-105 Telegram (needs 103) · M2-106 HTTP API (needs 104) ·
P2 pipeline starts once 111+201+103 are all done (M2-202 Niche Scout).

## Later

P2 pipeline 201→212 · P3 distribution · P4 blog · P5 Builder · P6 revenue lines.

## Mayank must do (long waits first)

1. **YouTube API audit + quota increase.** Weeks of waiting. Start now (CONTEXT §6). Until approved, uploads stay private.
2. Meta app (Instagram Professional + Page), X developer, Pinterest, LinkedIn apps.
3. Answer CONTEXT §5 open questions (Builder repo list, channel names, Hindi voice).
4. ~~Upgrade Go, install gh~~ done 2026-09-28 (Go 1.27.0 in Program Files; gh 2.101.0). Still to do: `gh auth login`; Git Bash still resolves Go 1.23.4 via ~/.bashrc.
5. Decide: should the GitHub repo `Mayankmishra110/Me2.0` stay **public**? (See Risks.)

## Product suggestions (not decided; Mayank to confirm)

- The agency line is the fastest cash (CONTEXT §2), but it sits last in D21. Consider a small P1.5 ticket: a
  one-page offer + lead list + proposal template, built by hand, not by Builder.
- Start the YouTube channels now with manual uploads, so the channel age and watch history exist when
  automation arrives.

## Risks

| Risk | Mitigation |
|---|---|
| Public repo exposes strategy, channel plans and infra layout | Make the repo private, or keep `docs/` strategy out of it |
| Token leak via commit or log | Hooks block `.env`/config/data and token patterns; sec review on flagged tickets |
| 16 GB RAM: TTS + render + local LLM at once | `heavy` class = 1 job; doctor checks RAM |
| Free LLM tiers change limits | Router falls back; limits live in config |
| Account strikes | Compliance gates fail closed; approval before every publish |
