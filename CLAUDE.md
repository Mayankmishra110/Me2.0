# Rules for AI coding threads (Claude Code, Cursor, Builder)

## Before you start
1. Read [docs/CONTEXT.md](docs/CONTEXT.md). It is the product memory; decisions there are final unless Mayank changes them.
2. Pick and claim a ticket per [docs/SPEC.md §2](docs/SPEC.md#2-tickets-and-parallel-work). One ticket per thread. Stay inside its `touches` paths.
3. Read the doc sections the ticket links to (ARCHITECTURE, DESIGN, COMPLIANCE) before writing code.
4. If Mayank names a crew role ("you are be"), follow [crew/BOOT.md](crew/BOOT.md) and that role's card; it resumes from `crew/state/<role>.md`.

## Non-negotiables
- **Official platform APIs only.** No browser automation, scraping behind logins, or automated likes/follows/comments/DMs.
- **Nothing public without approval.** Every publish path goes through `approval.request` and checks the approval status.
- **Never weaken or bypass a compliance gate** ([docs/COMPLIANCE.md](docs/COMPLIANCE.md)). Never add code whose purpose is to hide automation from a platform.
- **Claude is for Builder and blog only.** Content agents use `internal/llm` routing (local/free tiers).
- **Secrets:** only in `.env` or the DPAPI vault. Never log tokens. Never commit `config/config.yaml`, `.env`, or `data/`.
- **Builder never pushes to `main`.**
- **16 GB RAM:** anything that renders, runs TTS/STT, or loads a local model registers as the `heavy` resource class.
- Child processes use fixed argument lists and timeouts — never build a shell string from AI output.

## Code conventions
- Go: standard layout in `internal/`, small packages, `context.Context` first, errors wrapped with `%w`, `log/slog` for logs, table-driven tests next to code. No global state besides `main`.
- SQL: migrations are append-only files in `migrations/`; never edit a shipped migration.
- Web: TypeScript strict, function components, TanStack Query for server state, tokens from [docs/DESIGN.md §1.3](docs/DESIGN.md#13-visual-system), mobile-first.
- Python (`media-tools/`): uv project, one CLI per tool with the JSON in/out contract from ARCHITECTURE §2.
- Remotion: one composition per format, parameterized by brand kit and script beats.
- Commit messages: `type(M2-xxx): summary` (feat, fix, chore, docs, test, refactor); non-ticket scopes `crew`, `docs`, `repo`. Hooks: `git config core.hooksPath .githooks`; never `--no-verify`.

## When done
- Run the checks in SPEC §7, update the ticket (`status: in-review`, notes on how you verified), and add a decision-log row to CONTEXT.md if you changed a decision.
- If something is ambiguous and not covered by the docs, write the question under **Open questions** in CONTEXT.md instead of guessing on product behavior.
