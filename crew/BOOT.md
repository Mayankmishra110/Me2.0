# Crew boot — every role runs this first

You are one member of Mayank's crew. Mayank starts a chat (Cursor, Claude Code, any tool) with a line like
`you are fe` or `crew: be`. From then on, follow this file plus your role card in [roles/](roles/).

## 1. Load (in this order, every session)

1. [../CLAUDE.md](../CLAUDE.md): non-negotiables. They beat anything in this folder.
2. [../docs/CONTEXT.md](../docs/CONTEXT.md): product memory. Decisions there are final unless Mayank changes them.
3. Your role card: `crew/roles/<role>.md`.
4. **Your memory**: `crew/state/<role>.md` in the **main checkout** (run `git worktree list`; the first path is main).
   If it does not exist, copy [templates/state.md](templates/state.md) there.
5. [PLAN.md](PLAN.md): what matters now, from the product side.
6. Only the doc sections your current ticket links to.

## 2. Resume

Read the **Now** and **Next step** lines in your state file, then check reality:

- `git worktree list`, `git status`, `git log --oneline -5` on your branch.
- The ticket file's `status` and `owner`.

If the state file and git disagree, trust git and fix the state file. Then say in 2–4 lines what you are
resuming and continue. Do not ask Mayank to repeat anything already written down.

If there is no current work, pick new work with your role card's **Pick-up** rules.

## 3. Rules every role follows

- **One ticket at a time.** Claim per [SPEC §2](../docs/SPEC.md#2-tickets-and-parallel-work): set `status: in-progress`
  and `owner: crew-<role>`, commit only that change on `main` (`chore(M2-xxx): claim`).
- **Work in a worktree off the current phase branch** (named in [PLAN.md](PLAN.md), e.g. `phase/p1-foundation`):
  `git worktree add data/worktrees/m2-xxx -b m2/M2-xxx phase/p1-foundation`. `data/` is gitignored.
  Claims and queue bookkeeping still go on `main`. Add a row for the new branch to the "in progress" table
  in [BRANCH_MAP.md](BRANCH_MAP.md) (ticket, branch, feature, role) in the same claim commit — every branch
  that exists must be in that file.
- **Stay inside the ticket's `touches`** plus tests next to them. Need a path another in-progress ticket owns?
  Stop, write it in the ticket's Notes, and tell Mayank.
- **Never push to `main`.** Never skip hooks (`--no-verify`) unless Mayank says so for that commit.
- **Product questions go to Mayank**, and are recorded under CONTEXT §5. Don't guess product behavior.
  Technical questions the docs answer: decide, then write the reason in the ticket Notes.
- **Show real output.** Paste the actual output of the checks, never "should pass".

## 4. Status flow

```
todo → ready → in-progress (fe/be/ai) → in-review → qa pass (+ sec pass when flagged)
  → ship rebases onto current main, pushes, opens a PR, auto-merges via `gh pr merge` (D25) → done
  (D23's phase-branch model is superseded but still has a backlog to migrate — see crew/roles/ship.md)
                    ▲                                    │
                    └────────── changes requested ◄──────┘
```

Review verdicts go in the ticket under `## Review` as one line each:
`QA: pass|changes — <date> — <summary>` and `SEC: pass|changes|n/a — <date> — <summary>`.

## 5. Before you stop (always, even mid-task)

Update `crew/state/<role>.md`:

- **Now:** ticket, branch, worktree path.
- **Done this session:** bullet list with commit hashes.
- **Next step:** one concrete action that someone else could run.
- **Blocked on:** question or dependency, or "nothing".

Keep the state file under ~60 lines: move old sessions into **Log** as one line each and drop the details.
Durable facts go in tickets and docs, not in state files.

End your chat reply with: what changed · what's verified (real output) · what's left · questions for Mayank.
