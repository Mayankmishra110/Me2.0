# Plan — owned by lead

Updated: 2026-09-28 · Phase: **P1 Foundation** · **Phase branch: `phase/p1-foundation`** (new ticket branches start here; D23)

## North star

First income as early as possible. The P2 exit gives the first real proof: one approved Short per channel
published through the API, with metrics at +24h. Everything in P1 exists to make that safe and hands-off.

## Done — P1 batch 1 (2026-09-28)

M2-101, M2-102, M2-107, M2-109, M2-111, M2-206, M2-207, M2-208 are **done**, merged into
`phase/p1-foundation`, and pushed. PR ready to open: [docs/audit/p1-foundation/README.md](../docs/audit/p1-foundation/README.md).
M2-109 and M2-207 have now had their independent qa+sec pass too (2026-09-28, `cd5590a`) — both pass, no
findings. **No open blockers on the P1 batch 1 PR.**

## Now (P1 batch 2 — review, then merge)

All five build branches are committed, worktrees clean, status `in-review` on the branch. Main ticket files were still `in-progress`; lead synced them to `in-review` on 2026-09-28.

| Role | Ticket | Why now |
|---|---|---|
| qa/sec | **M2-104, M2-201, M2-213, M2-103, M2-114** | Review wave. No shared `touches`. 103 and 114 are included because their worktrees are clean and `in-review`. |
| ship | merge passed tickets into `phase/p1-foundation` | After QA (and SEC where `needs-sec: yes`: M2-114, M2-213). |
| be | **M2-110** after M2-114 merges | Both touch `cmd/mayank2/`. |
| be | **M2-105** after M2-103 · **M2-106** after M2-104 | Then **M2-108** after 103+105. |

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
