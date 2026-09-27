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

## Run (available after M2-101 … M2-109)

```powershell
copy config\config.example.yaml config\config.yaml   # then edit
copy .env.example .env                               # then fill secrets
go run ./cmd/mayank2 doctor                          # checks tools, keys, disk, RAM
go run ./cmd/mayank2 run                             # start the daemon in this console
cd web; npm install; npm run dev                     # dashboard dev server (proxies /api)
scripts\build.ps1                                    # builds web + bin\mayank2.exe
scripts\install-task.ps1                             # start at logon + power settings (run as you, once)
```

## Repository layout

See [docs/ARCHITECTURE.md §9](docs/ARCHITECTURE.md#9-repository-layout).
