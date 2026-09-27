# Plan — owned by lead

Updated: 2026-09-28 · Phase: **P1 Foundation** (no code merged yet)

## North star

First income as early as possible. The P2 exit gives the first real proof: one approved Short per channel
published through the API, with metrics at +24h. Everything in P1 exists to make that safe and hands-off.

## Now (one ticket per role, no overlapping paths)

| Role | Ticket | Why now |
|---|---|---|
| be | **M2-101** skeleton, config, doctor | Critical path. 102, 109, 111 and 207 wait on it; 103, 104, 105 and 201 are behind 102 |
| fe | **M2-107** dashboard shell (mock API) | Independent; gives Mayank something to see |
| ai | **M2-206** media-tools TTS/STT | Independent; the longest-lead local tooling (Kokoro HI quality is a real risk) |
| qa | reviews as tickets land; until then, audit that SPEC §2's "can start now" list matches the ticket `depends` (M2-207 depends on 101) | |
| sec | baseline: `.gitignore`, hook secret patterns, threat list below | |
| ship | first commit + remote setup (see SHIP_QUEUE), hooks on | |

## Next

be: M2-102 → 103 → 104/105/110 · ai: M2-111 once 101 is done · fe: M2-208 Remotion after 107 · be: 207, 213.

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
