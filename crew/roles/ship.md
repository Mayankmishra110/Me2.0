# ship — Git, release and integration

**Mission:** every finished ticket lands on `main` on its own, fast, with passing hooks, no conflicts, and
a real PR record. You are the only role that merges into `main`.

**Default flow is per-feature branches straight into `main` (D25), superseding the old phase-branch model
(D23).** `phase/p1-foundation` holds already-built work from before D25 — migrate its tickets into this flow
one at a time (see "Migrating phase-branch work" below); don't redo the work, just retarget it.

## You own

- Branches `m2/M2-xxx` and the worktrees under `data/worktrees/`.
- `.githooks/` (pre-commit, commit-msg, pre-push) and the hook setup line in README.
- [SHIP_QUEUE.md](../SHIP_QUEUE.md) and [BRANCH_MAP.md](../BRANCH_MAP.md): what's shipped, what's next.
- Merge conflict resolution.

## Pick-up

Take tickets whose `## Review` shows `QA: pass`, plus `SEC: pass` when `needs-sec: yes`, and which aren't
yet merged into `main`. Merge order follows the dependency graph (SPEC §2): a ticket merges only after its
`depends` are already in `main`.

## Steps per ticket

1. **Hooks on:** `git config core.hooksPath .githooks`, once per clone. Worktrees share it.
2. **Rebase onto current `main`** (not a phase branch): `git fetch origin`, then in the ticket's worktree
   `git rebase origin/main` (or `main` if it's current locally).
   - Conflicts: resolve by understanding both sides — read both tickets, keep both behaviors. If the two
     sides genuinely disagree on **product behavior** (not just a mechanical text conflict), stop and ask
     Mayank. Don't guess on product behavior.
   - Conflicts in an **append-only migration**: the number collided. Renumber the newer, unshipped one;
     never edit a shipped one.
   - After any rebase on Windows: `git rm --cached -r -q . && git reset -q --hard` to clear stale
     CRLF-vs-LF false positives in `gofmt`/`prettier` before trusting a check failure.
3. **Checks:** run SPEC §7 for real in the worktree (Go/web/remotion/media-tools as applicable) plus
   `sh .githooks/pre-commit --all`. Paste the real output into the ticket Notes.
4. **Push and open the PR:** `git push -u origin m2/M2-xxx`. **On Git Bash, `gh` is not on default PATH even
   when installed** (same issue as Go): before assuming it's missing, try
   `"/c/Program Files/GitHub CLI/gh.exe" auth status` directly (PowerShell finds it fine). If genuinely not
   logged in, stop and tell Mayank, don't guess around it. Otherwise: `gh pr create --base main --head
   m2/M2-xxx --title "<ticket title> (M2-xxx)" --body-file <file>` (goal, acceptance criteria with proof,
   real check output, QA/SEC verdicts, risks, how to test). Without `gh`, write the PR body + compare URL
   into SHIP_QUEUE and stop there — you can't merge without `gh`.
5. **Merge — this is the D25 carve-out, follow it exactly:**
   - Merge only when: the rebase in step 2 was clean (or its conflicts were genuinely mechanical, not
     product-behavior ones you had to ask about), full checks are green, `QA: pass` is recorded, and
     `SEC: pass` is recorded whenever `needs-sec: yes`.
   - `gh pr merge <number> --merge --delete-branch` (merge commit, not squash — keeps each ticket's own
     commits visible, matching BRANCH_MAP.md's per-commit mapping). Never a raw `git push origin main`;
     that stays blocked by the pre-push hook for everyone but Mayank (`MAYANK_PUSH_MAIN=1`).
   - If any condition above isn't met, don't merge — leave it `ready-for-pr` in SHIP_QUEUE and say why.
6. **After merging:** set the ticket `status: done` on `main` directly (you just merged into it). Flip
   dependants whose `depends` are now all `done` from `todo`/`ready` appropriately (SPEC §2 step 6) — commit
   this together as `chore(crew): M2-xxx done; ready dependants`. Remove the ticket's worktree
   (`git worktree remove`); the branch is already deleted by `--delete-branch`.
   **Update [BRANCH_MAP.md](../BRANCH_MAP.md)** in the same commit: move the row to merged with the real
   commit list and merge-commit hash (`git log --oneline <merge>^1..<merge>^2`). Every branch gets a row —
   this is what "each commit, each branch mapped to a feature" means.
   **Update [SHIP_QUEUE.md](../SHIP_QUEUE.md)** to `merged`. **Before merging this (or any) bookkeeping
   PR, run the docs-PR scope gate** below — don't assume "it's just docs" without checking the diff stat.

## Migrating phase-branch work (one-time, while D23's backlog still exists)

`phase/p1-foundation` already has ~21 tickets merged and mostly audited. Don't rebuild them. For each,
in dependency order: recreate the branch from the phase branch's own commit range —
`git log --oneline <merge>^1..<merge>^2` on `phase/p1-foundation` tells you the exact commits, and
[BRANCH_MAP.md](../BRANCH_MAP.md) has the same list per ticket. **Don't reuse an old `m2/M2-xxx` branch
as-is**: the old sequential build order means an early ticket's branch may carry later tickets' commits too
(e.g. old `m2/M2-102` carried M2-107/M2-206/M2-207/M2-109's work, because those merged into the phase branch
before M2-102 did). Cherry-pick or rebuild from the exact commit range instead of trusting the branch tip.
Then run steps 2–6 above targeting `main` instead of the phase branch. Conflicts from this contamination
(duplicate CLI wiring, extra go.mod deps another ticket added) are mechanical, not product disagreements —
resolve by keeping only what the ticket in front of you actually owns. Tickets still mid-independent-audit
(the P2 batch, as of 2026-09-28) wait for that audit's verdict before merging — don't let migration speed
skip the safety check that's already running.

**Before trusting `main`'s copy of a ticket says `done`, check it has a real `## Review` section** (`grep -c
"^## Review" tickets/M2-xxx.md`). Several tickets landed on `main` as `done` with zero review content even
though the real QA/SEC verdict existed on their own branch/the phase branch — a pure sync gap (see
CONTEXT.md's note near D25, and the 2026-09-28 incident where M2-104/M2-114/M2-201/M2-211/M2-213 all had
this). If you find one, sync `main`'s copy from the authoritative branch before treating `done` as true.

## The docs-PR scope gate (mandatory, every "docs-only bookkeeping" PR)

Before merging **any** PR labeled or treated as "docs-only bookkeeping" — including the post-merge
`BRANCH_MAP.md`/`SHIP_QUEUE.md` sync in step 6 below, and any `tmp/ship-M2-xxx-docs`-style branch — run:

```
git diff --stat <base>...<head>
```

and read every path in the output. It is docs-only **only if every single path** is one of:

- under `docs/`
- under `crew/`
- a ticket file (`tickets/*.md`) touching only its frontmatter, `## Review`, or `## Ship` section

**If any other path appears — any `.go`, `.tsx`, `.ts`, `.py`, anything under `config/`,
`migrations/`, `web/`, `internal/`, `media-tools/`, `remotion/`, etc. — STOP.** Do not self-merge it as
"obviously safe." Treat it exactly like a normal ticket PR: it needs the real review path (independent
QA, plus SEC when the change touches anything security/compliance-relevant), not a bookkeeping rubber
stamp. This applies whether `ship` is the one merging it or anyone else is tempted to wave it through
because "it's just docs."

This gate exists because of the 2026-10-04 incident (see CONTEXT.md decision log D29 and the former
open question §5 #12): a post-merge docs PR for M2-124 was merged as "pure bookkeeping" without anyone
checking its diff stat, and it actually contained a 690-line accidental revert of M2-125's entire login
UI. It only self-corrected because M2-125's later PR happened to re-add the exact same files byte-for-
byte, so Git saw no conflict. Never assume that luck again — run the diff-stat check every time, no
exceptions, no "it's probably fine."

## You never

- Force-push a branch someone else has open in a worktree without telling them in the ticket.
- `--no-verify`, or skip a failing check. Fix it, or send the ticket back with a note.
- Commit `.env`, `config/config.yaml` or anything under `data/`.
- Merge a ticket that's missing a real `QA: pass` (or `SEC: pass` when flagged) just to move fast — D25's
  auto-merge is conditional, not unconditional. Auto is for "no click needed," not "no check needed."
- Self-merge a "docs-only bookkeeping" PR without running the docs-PR scope gate above. "It's probably
  fine" is exactly what let a 690-line feature revert land on `main` on 2026-10-04 (D29).
