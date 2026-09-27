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
4. **Push and open the PR:** `git push -u origin m2/M2-xxx`. With `gh` (run `gh auth status` first — if not
   logged in, stop and tell Mayank, don't guess around it): `gh pr create --base main --head m2/M2-xxx
   --title "<ticket title> (M2-xxx)" --body-file <file>` (goal, acceptance criteria with proof, real check
   output, QA/SEC verdicts, risks, how to test). Without `gh`, write the PR body + compare URL into
   SHIP_QUEUE and stop there — you can't merge without `gh`.
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
   **Update [SHIP_QUEUE.md](../SHIP_QUEUE.md)** to `merged`.

## Migrating phase-branch work (one-time, while D23's backlog still exists)

`phase/p1-foundation` already has ~21 tickets merged and mostly audited. Don't rebuild them. For each,
in dependency order: `git checkout m2/M2-xxx` (the original ticket branch, or recreate it from the phase
branch's commit range if it was deleted: `git log --oneline <merge>^1..<merge>^2` on `phase/p1-foundation`
tells you the exact commits), then run steps 2–6 above targeting `main` instead of the phase branch. Tickets
still mid-independent-audit (the P2 batch, as of 2026-09-28) wait for that audit's verdict before merging —
don't let migration speed skip the safety check that's already running.

## You never

- Force-push a branch someone else has open in a worktree without telling them in the ticket.
- `--no-verify`, or skip a failing check. Fix it, or send the ticket back with a note.
- Commit `.env`, `config/config.yaml` or anything under `data/`.
- Merge a ticket that's missing a real `QA: pass` (or `SEC: pass` when flagged) just to move fast — D25's
  auto-merge is conditional, not unconditional. Auto is for "no click needed," not "no check needed."
