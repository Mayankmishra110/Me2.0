# Mayank 2.0

An always-on team of AI agents running on one Windows laptop. It runs a faceless content business (4 YouTube channels + Instagram, Facebook, X, Pinterest), publishes Mayank's tech blog (portfolio, LinkedIn, Medium, X), and builds software with two parallel coding agents. Everything public waits for a one-tap approval on Telegram.

**Status:** documentation phase. Start with [docs/CONTEXT.md](docs/CONTEXT.md).

## Docs

| Doc | Read it for |
|---|---|
| [docs/CONTEXT.md](docs/CONTEXT.md) | The whole story, decisions, open questions, account setup checklist |
| [docs/PRD.md](docs/PRD.md) | What each module must do |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Components, data flow, DB schema, queue, security, runtime |
| [docs/SPEC.md](docs/SPEC.md) | Phases, tickets, interfaces, API, job types, parallel-work protocol |
| [docs/DESIGN.md](docs/DESIGN.md) | Dashboard, Telegram UX, video brand kits |
| [docs/COMPLIANCE.md](docs/COMPLIANCE.md) | Platform rules and the Compliance & Originality Engine |
| [docs/CONTENT_STRATEGY.md](docs/CONTENT_STRATEGY.md) | Channels, formats, cadence, monetization |
| [CLAUDE.md](CLAUDE.md), [.cursor/rules/](.cursor/rules/) | Rules for every AI coding thread |
| [tickets/](tickets/) | Work items; pick one with the protocol in SPEC.md §2 |
| [crew/](crew/README.md) | AI crew: open any chat and type `you are lead` / `fe` / `be` / `ai` / `qa` / `sec` / `ship` |
| [crew/SHIP_QUEUE.md](crew/SHIP_QUEUE.md) | Branches that need a PR, their review state and PR text |

## Git setup (once per clone)

```powershell
git config core.hooksPath .githooks   # prettier/eslint/tsc, gofmt/vet/test, ruff, secret scan, commit format, no push to main
```

## Stack

| Layer | Technology | Libraries / tools |
|---|---|---|
| Daemon / backend | **Go 1.24+**, one binary | `net/http` (routing), `log/slog`, `modernc.org/sqlite` (pure Go, no C compiler), `gopkg.in/yaml.v3`, `github.com/robfig/cron/v3`, `github.com/oklog/ulid/v2`, `github.com/fsnotify/fsnotify`, `golang.org/x/oauth2`, `google.golang.org/api` (YouTube Data v3, YouTube Analytics v2), `golang.org/x/sys/windows` (DPAPI), `golang.org/x/crypto/bcrypt`, `github.com/aws/aws-sdk-go-v2/service/s3` (Cloudflare R2) |
| Database + queue | **SQLite** (WAL) | Embedded SQL migrations; the queue is the `jobs` table (no Redis) |
| Dashboard | **React 19 + Vite + TypeScript** | Tailwind CSS, shadcn/ui (Radix), TanStack Query, React Router, Recharts, Vitest + Testing Library, Playwright; built into `web/dist` and embedded in the Go binary |
| Live updates | Server-Sent Events | — |
| Phone control | Telegram Bot API | Raw HTTPS long-polling (no SDK) |
| Remote access | Tailscale | — |
| Video render | **Remotion** (Node 20+, React) + **ffmpeg** | `@remotion/cli`, `@remotion/captions` |
| Voice / speech | **Python 3.12 + uv** | `kokoro` (TTS, EN + HI), `faster-whisper` (STT), `soundfile` |
| Local AI | **Ollama** | ~8B Qwen/Gemma instruct (Q4), `nomic-embed-text` for originality embeddings |
| Hosted AI (free tiers) | OpenAI-compatible HTTP | Groq, Cerebras, OpenRouter free models, Gemini free tier — models and limits in config |
| Claude | Claude Code CLI (`claude -p`, Pro plan) | Builder (Opus 5.5 implementer, Sonnet 5 auditor) and blog only |
| Stock media | Pexels API, Pixabay API | YouTube Audio Library for music |
| Publishing | Official APIs | YouTube Data v3, Meta Graph (Instagram + Facebook Pages), X API v2, Pinterest v5, LinkedIn Posts |
| Storage | Local disk + Cloudflare R2 | Temporary public URLs for Meta uploads |
| Runtime | Windows Task Scheduler | Start at logon, restart on failure |

## Prerequisites (Windows)

```powershell
winget install GoLang.Go OpenJS.NodeJS.LTS astral-sh.uv Gyan.FFmpeg Ollama.Ollama GitHub.cli Tailscale.Tailscale
ollama pull nomic-embed-text
# plus one ~8B instruct model chosen in config (e.g. a current Qwen or Gemma build)
npm install -g @anthropic-ai/claude-code   # already installed
```

Go is currently 1.23.4 on this laptop; upgrade to 1.24+ for the SQLite driver.

## Quick start with one free key

Everything runs with only the keys you have (CONTEXT D24, M2-114): every integration is enabled
by its own `.env` key, and `doctor` reports a missing one as ⚪ "not configured," never a failure.
One free [Gemini API key](https://aistudio.google.com/apikey) is enough to run the LLM path end
to end — nothing else needs to be signed up for first.

```powershell
copy config\config.example.yaml config\config.yaml   # defaults already point at Gemini's free tier
copy .env.example .env
# edit .env: paste your key into GEMINI_API_KEY=
go run ./cmd/mayank2 doctor                           # ✅ ok · ⚪ not configured · ❌ broken
go run ./cmd/mayank2 llm ask "Say hello in one sentence."
go run ./cmd/mayank2 llm ask --task script "Write a 3-beat hook about compound interest."
```

`doctor` exits non-zero only on ❌ (a core tool missing, or a key that's set but rejected by its
provider) — a ⚪ for every other provider, Pexels/Pixabay, publish platform, R2, or Telegram is
expected and does not fail the run. Add more keys to `.env` any time; each one flips its own row
from ⚪ to ✅ (or ❌ if the provider rejects it) the next time you run `doctor`.

Get a free key at [aistudio.google.com/apikey](https://aistudio.google.com/apikey); keep Google
Cloud billing **off** on that project so usage stays on the free tier. `config.example.yaml`'s
`llm.providers.gemini.model` comment records which model id is current and where it was checked.

## Run (available after M2-101 … M2-109)

```powershell
copy config\config.example.yaml config\config.yaml   # then edit
copy .env.example .env                               # then fill secrets
go run ./cmd/mayank2 doctor                          # checks tools, keys, disk, RAM
go run ./cmd/mayank2 run                             # start the daemon in this console
cd web; npm install; npm run dev                     # dashboard dev server (proxies /api)
```

## Build and start at logon (Windows)

Run from the main checkout (the task points at the folder you run it from). If PowerShell blocks
scripts, keep the `-ExecutionPolicy Bypass` shown here; it applies to that one process only.

```powershell
# 1. Build: web\dist (npm ci only if web\node_modules is missing, then npm run build),
#    then go build -ldflags "-H windowsgui" -o bin\mayank2.exe ./cmd/mayank2
powershell -ExecutionPolicy Bypass -File scripts\build.ps1
powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -SkipWeb     # Go only

# 2. Preview the install: prints every action, changes nothing
powershell -ExecutionPolicy Bypass -File scripts\install-task.ps1 -WhatIf

# 3. Install (as yourself; safe to re-run, it replaces the task in place)
powershell -ExecutionPolicy Bypass -File scripts\install-task.ps1

# Remove the task (power settings are left as they are)
powershell -ExecutionPolicy Bypass -File scripts\install-task.ps1 -Uninstall

# Script tests (mocked; never touch the real Task Scheduler or power plan)
powershell -ExecutionPolicy Bypass -File scripts\tests\install-task.tests.ps1
```

`install-task.ps1` does exactly this:

- Task Scheduler task **Mayank2** for the current user: at logon → `bin\mayank2.exe run`, working
  directory = repo root; restart on failure 3× every 5 minutes; no execution time limit; a second start
  while it runs is ignored; not elevated; also runs on battery.
- Current power plan, on AC only: sleep = never, hibernate = never, lid close = do nothing
  (`powercfg /change standby-timeout-ac 0`, `/change hibernate-timeout-ac 0`,
  `/setacvalueindex SCHEME_CURRENT SUB_BUTTONS LIDACTION 0`, `/setactive SCHEME_CURRENT`).
- Prints a reminder to set the **80% battery charge limit** in the laptop vendor app (Windows can't set it).

If registering fails with "access denied", run it again from an elevated PowerShell. `bin\mayank2.exe`
is a windowless build, so it prints nothing in a console; use `go run ./cmd/mayank2 doctor` for output.

## Repository layout

See [docs/ARCHITECTURE.md §9](docs/ARCHITECTURE.md#9-repository-layout).
