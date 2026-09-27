# Cursor phase run — paste-ready prompts

Reusable for every phase. Change `PHASE_BRANCH` and the ticket lists; everything else stays the same.
Claude Code (lead) audits each pushed phase branch and replies "go" before the next run.

---

## 1. Lead chat (paste into ONE Cursor agent first)

```
You are lead for Mayank 2.0. Follow CLAUDE.md and crew/BOOT.md exactly, then crew/roles/lead.md.
Current phase branch: phase/p1-foundation (worktree data/worktrees/phase-p1). Do not create a new one.

Your job this run is to plan and coordinate, not to write code:
1. Read tickets/*.md frontmatter, crew/PLAN.md, crew/BRANCH_MAP.md, crew/SHIP_QUEUE.md, and every
   crew/state/*.md. Check them against git: `git branch -a`, `git worktree list`,
   `git log --oneline --merges phase/p1-foundation`. Fix any drift before planning.
2. Tickets owned by crew-be (M2-103) and crew-ai (M2-114) may still be in progress by another session.
   Do NOT touch a ticket unless its status is `ready`, or `in-review` with its work committed on its branch
   (`git -C data/worktrees/m2-xxx status` is clean).
3. Plan waves so tickets that run in parallel never share a `touches` path. Remaining P1 work:
   - Review + merge wave: M2-104, M2-201, M2-213, plus M2-103 and M2-114 once committed and in-review.
   - Build wave A (ready now): M2-110 (after M2-114 merges; both touch cmd/mayank2/).
   - Build wave B (after M2-103 merges): M2-105 Telegram. After M2-104 merges: M2-106 HTTP API.
   - Build wave C (after 103 + 105 merge): M2-108 scheduler.
   Flip todo → ready as deps finish (SPEC §2 step 6), on the phase branch AND on main.
4. For each wave, output the exact one-line prompts from section 2 below (filled in) so Mayank can paste
   them into parallel Cursor agents. One agent per ticket, one ticket per agent.
5. Claim each ticket on main before handing it out (status in-progress, owner crew-<role>, plus its
   BRANCH_MAP.md row), and create its worktree:
   git worktree add data/worktrees/m2-xxx -b m2/M2-xxx phase/p1-foundation
6. When every P1 ticket is done and merged, tell Mayank: "P1 pushed, ready for Claude Code audit", then stop.
```

## 2. Per-ticket builder (paste one per parallel Cursor agent)

```
You are <be|fe|ai>. Follow crew/BOOT.md. Your ticket is M2-xxx, already claimed for you.
Work only in data/worktrees/m2-xxx on branch m2/M2-xxx, based on phase/p1-foundation.
Stay inside the ticket's `touches`. Follow crew/roles/_engineer.md: plan in ticket Notes first, then
table-driven tests including failure paths, with no real network in tests.
Commit messages: type(M2-xxx): summary. Never --no-verify, never push, never commit on main.
Before handing off, run the SPEC §7 checks and paste the REAL output into the ticket Notes. Tick each
acceptance box with its proof, add a needs-sec line, set status: in-review, commit, and update
crew/state/<role>.md in the main checkout. End with: what changed, real check output, what's unverified,
any product questions for Mayank.
```

## 3. Reviewer (paste into one Cursor agent after a build wave)

```
You are qa and sec. Follow crew/BOOT.md, crew/roles/qa.md and crew/roles/sec.md.
Review every ticket with status in-review and no QA: line. Read the code yourself and re-run the checks
yourself; don't trust the ticket's claims. Scope: the diff touches only `touches` plus tests.
Write "QA: pass|changes — <date> — ..." and, for needs-sec: yes, "SEC: pass|changes — <date> — ..."
under ## Review, committed on the ticket's branch. For changes, give file:line and the fix, and set status
back to in-progress.
```

## 4. Ship (paste into one Cursor agent after reviews pass)

```
You are ship. Follow crew/BOOT.md and crew/roles/ship.md step 5 exactly.
For each ticket with QA pass (and SEC pass if needs-sec: yes), in dependency order:
1. In its worktree: git rebase phase/p1-foundation. Then refresh line endings:
   git rm --cached -r -q . && git reset -q --hard
   (stale CRLF otherwise gives false gofmt/prettier failures).
2. Re-run checks there. In data/worktrees/phase-p1:
   git merge --no-ff m2/M2-xxx -m "Merge M2-xxx (<role>): <title>"
3. Write docs/audit/p1-foundation/M2-xxx.md from docs/audit/TEMPLATE.md (real file refs, commits, real
   check output, deviations, gaps). Set the ticket status: done, and flip dependants to ready.
4. Update docs/audit/p1-foundation/README.md (the PR body) and crew/BRANCH_MAP.md (move the row to
   merged with the real commit list and merge hash).
5. Sync tickets/*.md statuses back onto main and commit there (chore(crew): sync ticket statuses).
After all merges, run the FULL suite on the phase tree with real exit codes:
  Go: gofmt -l . ; go vet ./... ; go test -count=1 ./...
  web/ and remotion/: npm ci if node_modules is missing, then format:check, lint, typecheck, test, build
  media-tools/: uv run --extra dev ruff check . ; uv run --extra dev pytest -q
Then: git push origin phase/p1-foundation (never push main). Remove merged worktrees whose
`git status` is clean. Report the pushed commit hash.
```

## Environment facts every agent needs

- Go 1.27 is in `C:\Program Files\Go`. In Git Bash, `go` resolves to an old 1.23.4, so prefix commands with
  `export GOROOT="/c/Program Files/Go" PATH="/c/Program Files/Go/bin:$PATH";` (PowerShell is fine).
- `-race` needs cgo, which isn't available here; say so instead of skipping silently.
- The repo is public: never commit secrets. The hooks block `.env`, `config/config.yaml`, `data/` and token patterns.
- Commit trailer used by Cursor agents: whatever Cursor adds; don't copy Claude's trailer.

## Loop

Cursor runs 1 → 2 (parallel) → 3 → 4 → pushes → tells Mayank.
Mayank tells Claude Code: "audit phase/p1-foundation".
Claude Code audits the pushed branch → "go" (next wave or next phase, with the branch name) or a fix list.
Next phase: new branch `phase/p2-content` from `main` (after the P1 PR merges), with the same prompts
and `p1-foundation` replaced by `p2-content`.
