# Handover note for Mayank

Written by the `qa` crew role as the final verification pass on `main` at `7eda93c`, 2026-10-04.
This is meant to be read once, start to finish, before you touch the app yourself.

## Current state, in plain language

This session built and shipped tickets M2-116 through M2-128 onto `main`: the daemon now actually
starts and wires up everything (job handlers, scheduler, queue workers, R2 upload for renders, the
Instagram/Facebook/Pinterest presign path), topics and the agents panel are real (no more stub JSON),
the whole LLM/compliance pipeline runs on one Gemini API key with no Ollama required, a login screen
and session-cookie auth guard exist on the dashboard, `set-pin`/the resume PIN check are real (bcrypt,
fail-closed), and the dead-letter/retry logic checks live job registration instead of trusting a job's
own `max_attempts`. The Go side of this is solid: `gofmt`, `go vet`, `go build`, and `go test ./...`
(23 packages) are all clean on a fresh build, and the HTTP API — tested directly, bypassing the
browser — behaves exactly as each ticket describes: real login issuing an `HttpOnly` session cookie,
a real per-agent `/api/agents` response, a pause/resume flow that correctly 401s on a wrong PIN and
200s on the right one, and a `POST /api/topics` that accepts the new camelCase body and returns 201.

**There is one confirmed, unfixed, ship-blocking defect, and it's the one that matters most: the
dashboard you will actually see in a browser does not yet reflect any of this.** See the first item
under Known limitations below before you assume the login screen works. Everything else in this
paragraph is genuinely true and verified; this one thing is not, and it needs a fix (not just
re-testing) before the login/PIN/agents work can be trusted from the browser.

## Before you start

1. Install ffmpeg if you haven't already (done this session via `winget install Gyan.FFmpeg`; verify
   with `ffmpeg -version`).
2. Optional: install Ollama if you want a local model fallback. Not required — the product runs fully
   on one Gemini key now (decision D28, `docs/CONTEXT.md`).
3. Get a Gemini API key and put it in `.env` as `GEMINI_API_KEY=...`. Without it, `doctor` will report
   `llm embeddings (route)` broken and the compliance originality gate (G2) fails closed on every
   script — by design, not a bug.
4. Fill in the rest of `.env` from `.env.example` (`DASHBOARD_TOKEN` especially — `doctor` checks for
   it, and `/api/login` needs the real value).
5. Run `bin/mayank2.exe doctor` and read every line. ✅/❌/⚪ map to "fine" / "broken, fix it" /
   "optional feature, off." A fresh install should show the same two ❌s this session saw before a key
   was configured — ffmpeg (fixed by installing it) and the embeddings route (fixed by step 3).
6. Run `set-pin` for real: `bin/mayank2.exe set-pin`, typed twice with echo off on a real terminal.
   **This session set a test PIN (`123456`) while verifying the resume flow** — the hash is already in
   your `data/mayank2.db` (`settings.pin_hash`, a 60-char bcrypt hash, confirmed by direct query, not
   just a successful exit code). Overwrite it with your own PIN before you rely on it for anything.
7. Run `bin/mayank2.exe run`.

## Known limitations — stated plainly, no spin

1. **Ship-blocking: the production dashboard build ships with Mock Service Worker (MSW) still active,
   and it silently intercepts the real login/auth flow.** `web/src/main.tsx` starts MSW on every build
   except `MODE === 'test'`, unless the build sets `VITE_USE_MSW=false` — and nothing in
   `scripts/build.ps1` or the repo's env files ever sets that flag. This exact risk was flagged and
   never fixed: see `docs/audit/p1-foundation/M2-107.md`, which says outright *"Once M2-106 embeds
   web/dist, the real dashboard would show mock data unless the build sets that flag"* and recommends
   gating on `import.meta.env.DEV`. That recommendation was never acted on.
   **Verified effect, this session, on the exact binary built from current `main`:** opening
   `http://127.0.0.1:7070/` in a real browser (Playwright/Chromium) with zero cookies — a genuinely
   unauthenticated visitor — never shows the login screen at all. It loads straight into a fully
   populated dashboard with fake agent/job/approval data, because `mockServiceWorker.js` is physically
   present in the built `dist` (confirmed: `internal/httpapi/dist/mockServiceWorker.js` exists after
   `scripts/build.ps1`) and intercepts the `fetch('/api/agents')` call `RequireAuth` depends on before
   it ever reaches the real server. This is *only* a browser/fetch-layer problem — I confirmed the real
   server is correctly gated the entire time (plain `curl`, Node's own `fetch`, and Playwright's
   non-browser `APIRequestContext` all correctly got `401` with no cookie; a direct browser *navigation*
   to `/api/agents` as a URL also correctly got a real `401` — only fetch calls made from inside the
   already-loaded mocked page are affected). So M2-125's actual code (`LoginPage.tsx`, `RequireAuth.tsx`,
   the real `/api/login` wiring) is correct and the server-side PIN/session/agents work from M2-126–128
   is correct — none of it has shipped to what a browser actually loads yet.
   **Fix is small:** either flip `main.tsx`'s default so MSW only starts when `import.meta.env.DEV` is
   true (opt-in, not opt-out), or set `VITE_USE_MSW=false` as a build-time env var in
   `scripts/build.ps1`'s `npm run build` step. I did not make this change — it's a real code fix, not a
   docs change, and this ticket (QA/handover) was scoped to verification and write-up, not shipping a
   fix. Recommend filing it as the next ticket and re-running the exact browser check above once it
   lands before trusting the login screen.
2. **`go test -race` has never run.** No cgo toolchain was available in this build environment this
   session, and the race detector requires cgo on Windows. `go test ./...` (non-race) is clean, but
   that doesn't rule out data races in the queue/scheduler/session-store concurrency paths. Recommend
   running `go test ./... -race` once on a Linux machine or under WSL before trusting any
   concurrency-heavy path (the job queue, the scheduler, `sessionStore`) in production.
3. **The repo is still public on GitHub.** Nothing in this session's scope changed that.
4. **One open product question, unresolved:** channel-specific script-format selection — see
   `docs/CONTEXT.md` §5, item 5. `script.write` currently defaults to the full format catalog instead
   of a channel's configured subset from `config/channels/*.yaml`. Not wrong, just unresolved.
5. **Nothing has been tested against real platform credentials.** YouTube, Meta (Instagram/Facebook),
   X, Pinterest, and LinkedIn are all unconfigured in this environment (`doctor` shows all five as ⚪,
   feature off). Every bit of plumbing — R2 upload, the presign paths, the publisher job handlers — has
   been verified to the extent it can be without real OAuth tokens and API access; the live
   integrations themselves are not verified at all. Expect to find real-world surprises the first time
   you connect an actual account.

## Test PIN set this session

`123456`, set via `echo "123456" | bin/mayank2.exe set-pin` while verifying the M2-127 resume flow.
Confirmed stored as a real 60-character bcrypt hash in `settings.pin_hash` (not trusted on exit code
alone — queried directly). Overwrite it with your own PIN (step 6 above) before using this for real.

## If something looks broken

Full history of every decision and fix made this session lives in three places:

- **`docs/CONTEXT.md`** — the decision log (numbered `D1`, `D2`, ...) and the open-questions list
  (§5). If something seems like an intentional tradeoff rather than a bug, it's probably explained
  here.
- **`crew/BRANCH_MAP.md`** — which branch/commit each ticket actually shipped from.
- **`crew/SHIP_QUEUE.md`** — the real check output (lint/typecheck/test/build) and the QA/SEC review
  notes recorded at merge time for every ticket, including the rebase and conflict history for each
  one.

Start with `CONTEXT.md`'s decision log; it's the one most likely to answer "why does it work this way"
before you conclude something's wrong.
