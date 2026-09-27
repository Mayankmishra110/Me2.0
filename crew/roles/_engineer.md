# Engineer bar (shared by fe, be, ai)

Work like a senior engineer (SDE2/3) who owns the outcome, not only the diff.

## Before code

1. Read the ticket, its **Docs** links, and the matching `.cursor/rules/*.mdc` for your paths.
2. Write a 5–10 line plan in the ticket **Notes**: files, interfaces, test cases, risks. If the plan
   contradicts ARCHITECTURE or SPEC, stop and raise it. Don't drift quietly.
3. Check interfaces with neighbours: SPEC §3 (Go interfaces), §4 (HTTP API), §5 (job types),
   ARCHITECTURE §2 (child-process JSON contract), §4 (schema). Build against the contract, not a guess.

## While coding

- Small commits: `type(M2-xxx): summary` (feat, fix, chore, docs, test, refactor). Hooks run on each commit.
- Tests go in with the code, not after it:
  - **Table-driven unit tests** for logic, including the failure paths (bad config, timeouts, retries, empty input).
  - **Contract tests** at the boundaries: HTTP handlers with `httptest`, child processes with fixture JSON, and
    platform APIs with recorded responses. No real network in unit tests.
  - One **acceptance test or script** per acceptance criterion where possible, so QA can re-run it.
- Errors are wrapped with `%w` and name the input involved. Logs use `slog` with fields, and never log tokens or full payloads.
- Anything that renders, runs TTS/STT, or loads a local model registers as the `heavy` resource class.
- Child processes: `exec.CommandContext`, fixed argument lists, a timeout. Never build a shell string from AI output.
- New config keys go in `config/config.example.yaml`, and new secrets go in `.env.example` (names only).

## Before handing off

1. Run the SPEC §7 checks for everything you touched, and paste the real output into the ticket Notes:
   - Go: `gofmt -l .` (must print nothing), `go vet ./...`, `go test ./...`
   - web / remotion: `npm run lint && npm run typecheck && npm test && npm run build`
   - media-tools: `uv run ruff check . && uv run pytest`
2. Tick the acceptance boxes you proved, and say *how* each was proved.
3. Update docs if behaviour changed: the SPEC interface or API, an ARCHITECTURE component, or a README run step.
   A changed decision gets a row in CONTEXT §4.
4. Flag reviews: add `needs-sec: yes` in Notes if you touched secrets, auth, OAuth, httpapi, telegram,
   publish, builder, exec/child processes, or storage/R2.
5. Set `status: in-review`, commit, and update your state file. QA picks it up from there.

## When QA or SEC request changes

Fix them on the same branch, reply under `## Review` with what changed, and set `status: in-review` again.
