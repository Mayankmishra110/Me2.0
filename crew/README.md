# crew — your AI team, one role per chat

Open any chat (Cursor, Claude Code, anything that can read this repo) and type one line:

```
you are be
```

The Cursor rule [../.cursor/rules/crew.mdc](../.cursor/rules/crew.mdc) (and [../CLAUDE.md](../CLAUDE.md)) tells the
chat to run [BOOT.md](BOOT.md). It loads the docs, reads its own memory in `crew/state/<role>.md`, and carries
on where that role stopped. When it stops, it writes the next step back to memory, so the next chat resumes.

| Say | Role | Does |
|---|---|---|
| `you are lead` | [Principal architect / product owner](roles/lead.md) | What to build next and why; tickets; plan; growth, revenue and risk calls |
| `you are fe` | [Frontend engineer](roles/fe.md) | `web/` dashboard, `remotion/` video templates |
| `you are be` | [Backend engineer](roles/be.md) | Go daemon: config, db, queue, API, Telegram, publishers |
| `you are ai` | [AI / pipeline engineer](roles/ai.md) | LLM router, scout → research → script, compliance gates, TTS/STT |
| `you are qa` | [Reviewer + tests + spec keeper](roles/qa.md) | Reviews `in-review` tickets against architecture, adds tests, keeps docs true |
| `you are sec` | [Security + compliance guard](roles/sec.md) | Reviews risky diffs, secret scanning, dependency audits |
| `you are ship` | [Git + release](roles/ship.md) | Rebase, hooks, conflicts, PRs, [SHIP_QUEUE.md](SHIP_QUEUE.md) |

fe, be and ai share the engineering bar in [roles/_engineer.md](roles/_engineer.md).

## Useful extra lines

- `you are be, take M2-102`: point a role at a specific ticket.
- `you are lead, status`: a quick board: critical path, blockers, what needs you.
- `you are lead, idea: <anything>`: lead turns it into doc updates and tickets.
- Several chats can run at once (for example fe + be + ai). Tickets and worktrees keep them apart.
  Don't run two chats as the **same** role.

## Flow

```
lead writes/readies tickets → fe/be/ai build on m2/M2-xxx in data/worktrees/ → in-review
→ qa (+ sec when flagged) → ship rebases, checks, opens PR → you merge → ship marks done, readies dependants
```

## Files

- [BOOT.md](BOOT.md): the startup, resume and handoff protocol every role follows.
- [PLAN.md](PLAN.md): now, next, later, and what you must do yourself (lead keeps it current).
- [SHIP_QUEUE.md](SHIP_QUEUE.md): branches waiting for PRs, and their PR text.
- `state/`: each role's memory. Local only (gitignored), always read from the main checkout.
- [templates/state.md](templates/state.md): blank memory file.
