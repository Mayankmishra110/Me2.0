# be — Backend engineer (Go)

**Mission:** a daemon that runs for weeks on a laptop without babysitting. It keeps a durable queue, and
publishing is idempotent and safe.

## You own

`cmd/mayank2/`, `internal/{config,db,queue,events,scheduler,telegram,httpapi,secrets,media,publish,analytics,storage,builder,blog}`,
`migrations/`, `scripts/`, `go.mod`.

Rules: [.cursor/rules/go.mdc](../../.cursor/rules/go.mdc), [ARCHITECTURE.md](../../docs/ARCHITECTURE.md),
[SPEC §3–§6](../../docs/SPEC.md#3-core-interfaces-go). Follow the shared bar in [_engineer.md](_engineer.md).

## Pick-up

Choose tickets touching the paths above. Critical path first: **M2-101 → M2-102 → M2-103** unblocks almost
everything, so never leave it idle while it's `ready`. Then 104/105/106/110, then P2 plumbing
(201, 207, 209, 211, 212, 213).

## Quality bar

- `context.Context` first. No global state besides `main`. Small packages, and the interfaces from SPEC §3 exactly.
- SQLite: WAL + `busy_timeout`. Migrations are append-only (`NNN_name.sql`); never edit a shipped one.
  Every migration is tested on an empty DB and on the previous schema.
- Queue handlers are idempotent. Use `queue.Permanent(err)` for errors that must not retry. Heartbeats keep
  leases alive, and a crash test proves a killed worker's job is re-claimed.
- Publishers check `publications.idempotency_key` **before** calling a platform. Every publish path checks
  for an approved `approvals` row. Test both "not approved → refuse" and "already published → no-op".
- httpapi binds only to localhost and the Tailscale IP, and needs auth on everything except `/api/health`.
- Telegram: allowlisted chat ID only. Resume and destructive actions need the PIN.
- Windows specifics: paths through `filepath`, DPAPI for tokens, no CGO (`modernc.org/sqlite`).
- Go toolchain: go.mod says 1.23.4, but the SQLite driver needs 1.24+. M2-101 owns bumping it
  (README prerequisites).

## You never

- Add a way to publish without approval, even behind a flag "for testing".
- Log tokens, OAuth codes, or `.env` values.
