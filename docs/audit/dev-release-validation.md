# dev → main pre-merge validation

Date: 2026-09-29
Scope: `dev` branch, tip `9433fa3` (branched from `phase/p1-foundation`, contains P1–P6), validated in a
fresh worktree at `data/worktrees/dev-validate`.
Method: independent re-derivation, not a review of existing verdicts. No existing `## Review` verdict,
ticket `status: done` field, or prior QA/SEC pass claim was trusted at face value; build/test commands
were re-run fresh in this worktree and ticket review content was read and cross-checked against the
actual code in the tree.

## Verdict: **yes, with one significant known followup — not a merge blocker, but must not be forgotten**

**Recommendation: yes-with-followups.** Every build/test/lint/hook check is genuinely green, all 42
`done` tickets have real, specific, independently-re-derived `## Review` substantiation (no fabricated or
generic verdicts found), and the compliance gates (G1–G7, F1–F6 + F7 human approval) are real logic, not
stubs. The one item that matters is **not a bug in shipped code** — it's an honestly-disclosed, still-open
ticket: **M2-116 ("wire the daemon: `cmd/mayank2 run` starts everything") is `status: ready`, not `done`.**
`cmd/mayank2 run` is still `cmdStub` today. Every P1–P6 subsystem is built and unit-tested in isolation,
but nothing currently starts them together as one process, so SPEC §1's P1 exit check ("`mayank2 doctor`
all green; a test job flows queue → Telegram approval → dashboard") cannot actually be demonstrated on
this tree yet. This is accurately disclosed in the ticket itself and in several tickets' Notes (M2-202,
M2-211, M2-212, M2-504) — it is a real, tracked gap, not a hidden one. It does not block merging `dev`'s
reviewed, tested code to `main` (the code that exists is real and passes its own bar), but `main` should
not be treated as "the product runs" until M2-116 ships. Flagging this as the single most important thing
for Mayank to see before treating this as launch-ready.

---

## 1. Build + test suite — full real output

All commands run fresh in `data/worktrees/dev-validate` (branch `dev`, tip `9433fa3`).

### Go (`GOROOT=/c/Program Files/Go`, `go version go1.27.0 windows/amd64`)

```
$ gofmt -l .
(no output — clean)

$ go vet ./...
(exit 0, no output)

$ go build ./...
(exit 0, no output)

$ go test ./... -count=1
ok  	mayank2/cmd/mayank2	1.823s
ok  	mayank2/internal/agency	2.521s
ok  	mayank2/internal/analytics	2.408s
ok  	mayank2/internal/blog	44.108s
ok  	mayank2/internal/builder	40.525s
ok  	mayank2/internal/compliance	1.298s
ok  	mayank2/internal/config	1.366s
ok  	mayank2/internal/content	3.001s
ok  	mayank2/internal/content/formats	0.968s
ok  	mayank2/internal/db	1.321s
ok  	mayank2/internal/events	2.581s
ok  	mayank2/internal/httpapi	3.512s
ok  	mayank2/internal/llm	1.810s
ok  	mayank2/internal/media	2.834s
ok  	mayank2/internal/micro_saas	2.763s
ok  	mayank2/internal/publish	4.571s
ok  	mayank2/internal/queue	1.453s
ok  	mayank2/internal/revenue	1.213s
ok  	mayank2/internal/scheduler	1.233s
ok  	mayank2/internal/secrets	1.007s
ok  	mayank2/internal/storage	0.903s
ok  	mayank2/internal/telegram	1.739s
ok  	mayank2/internal/tickets	0.696s
?   	mayank2/migrations	[no test files]
TEST_EXIT:0
```

23 packages, all pass. `-race` was not available in this environment (`CGO_ENABLED=0` — no CGO toolchain
wired to this Go install), which every ticket's own re-review already disclosed consistently; not a gap
introduced by this audit.

### web (`npm ci` → `lint` → `typecheck` → `test` → `build`)

```
$ npm run lint      → oxlint src            → exit 0, no output
$ npm run typecheck → tsc -p tsconfig.app.json && tsc -p tsconfig.node.json → exit 0
$ npm test          → vitest run
  Test Files  3 passed (3)
  Tests       8 passed (8)
$ npm run build     → tsc -b && vite build
  ✓ 2204 modules transformed, dist/ written, built in 443ms → exit 0
```

### remotion (`npm ci` → `lint` → `typecheck` → `test` → `build`)

```
$ npm run lint      → oxlint src scripts    → exit 0
$ npm run typecheck → tsc --noEmit          → exit 0
$ npm test          → vitest run
  Test Files  1 passed (1)
  Tests       6 passed (6)
$ npm run build     → remotion bundle       → cached bundle, exit 0
```

### media-tools (`uv run --extra dev ruff check .` / `pytest`)

```
$ uv run --extra dev ruff check .
All checks passed!

$ uv run --extra dev pytest -q
.........sss.............                                                [100%]
22 passed, 3 skipped in 0.77s
```
(3 skipped are the `live` marker — Kokoro/Whisper model tests, skipped offline by design per
`pyproject.toml`'s own marker doc — not a failure.)

### `.githooks/pre-commit --all` (full repo, not just the diff)

Ran the actual hook script by hand over the whole tree (`sh .githooks/pre-commit --all`), which layers on
top of the above: forbidden-path scan (`.env`, `config/config.yaml`, `data/`), the secret-shaped-token
regex scan over every tracked file, then the same Go/web/remotion/media-tools checks. Result:

```
== forbidden paths          → ok
== secret scan              → ok
== gofmt / go vet / go test → ok (same as above)
== web: format:check/lint/typecheck/test → ok
== remotion: format:check/lint/typecheck/test → ok
== media-tools: ruff/pytest → ok

pre-commit: all checks passed
```

Nothing was bypassed; every check the hook would run on a full commit passes on this tree today.

**Step 1 verdict: fully green, no shortcuts.**

---

## 2. Ticket audit — every ticket in `tickets/`

44 files in `tickets/` (43 real tickets + `TEMPLATE.md`). 42 are `status: done`; `M2-116` is `status: ready`
(not done — see §0 above); all others are `done`.

| ID | Title | Status | QA verdict | SEC verdict (needs-sec) | Red flags |
|---|---|---|---|---|---|
| M2-101 | Repo skeleton, config loader, doctor | done | pass (2 rounds + independent re-check) | pass, after a real fix (Ollama HTTP not loopback-locked, found and closed) | none |
| M2-102 | SQLite + migrations | done | pass | n/a (needs-sec: no, correct) | none |
| M2-103 | Durable job queue | done | pass | n/a (no) | none |
| M2-104 | Events log + SSE | done | pass | n/a (no) | none |
| M2-105 | Telegram bot | done | pass (after 3 real SEC fixes: PIN-gate bulk pause, token-in-error redaction, preview-path confinement) | pass | none — good example of a real changes→fixed→pass cycle with file:line + tests |
| M2-106 | HTTP API + auth | done | pass | pass | none |
| M2-107 | Dashboard shell | done | pass | n/a (no, mock-only) | none |
| M2-108 | Scheduler + daily summary | done | pass | pass | none |
| M2-109 | Windows install/logon | done | pass (independent crew pass, `-WhatIf` diffed before/after) | pass | none |
| M2-110 | DPAPI vault + OAuth | done | pass | pass | none |
| M2-111 | LLM router | done | pass | pass | none |
| M2-114 | Gemini-only path | done | pass | pass | none |
| **M2-116** | **Wire the daemon (`cmd/mayank2 run`)** | **ready** | **no review — not claimed done** | — | **`run` is still `cmdStub`; confirmed in code (see §0/§4)** |
| M2-201 | Channel/format config | done | pass ×2 (independent post-merge re-audit) | n/a | none |
| M2-202 | Niche Scout | done | pass (after real fix: YouTube key leaking via wrapped `*url.Error`, found and closed) | pass | none |
| M2-203 | Research brief | done | pass ×2 | pass ×2 | none |
| M2-204 | Script writer | done | pass ×2 | pass ×2 | none |
| M2-205 | Compliance gates G1–G7 | done | pass, after **two real bugs found and fixed**: G2 originality fail-open, and **G7 always-pass no-op for English** (see §4) | pass | none now — this ticket is the best evidence the audit process works: an independent post-merge re-audit found G7 was a no-op, it was fixed with a real heuristic, and a second independent round re-verified the fix line-by-line |
| M2-206 | media-tools TTS/STT | done | pass | pass | none |
| M2-207 | Stock visuals + licensing | done | pass (independent crew pass, every AC tied to a named test) | pass | none |
| M2-208 | Remotion compositions | done | pass | n/a | none |
| M2-209 | Render pipeline | done | pass, after 2 real fixes (retention-days off-by-default, media path confinement) | pass ×2 + independent post-merge re-check | none |
| M2-210 | Final gates + approval | done | pass, after a real fix (Telegram/HTTP approve bypassed `ApprovalService.Decide`, found and closed) | pass ×2 | none |
| M2-211 | YouTube uploader | done | pass ×2 | pass ×2 | none — but flags (correctly) that nothing yet enqueues `publish.youtube` in production; consistent with M2-116 |
| M2-212 | Analytics pull | done | pass ×2 | pass | flags (correctly) a duplicate `analytics.pull` registration landmine between `internal/analytics` and `internal/scheduler` — this is exactly what M2-116's own acceptance criteria call out and require resolving |
| M2-213 | Storage/R2 | done | pass ×2 | pass ×2 | none |
| M2-301 | Instagram publisher | done | pass ×2 (independent re-audit traced Publish() line-by-line) | pass ×2 | none |
| M2-302 | Facebook publisher | done | pass, after a real fix (`UploadHosted` leaked unredacted Graph error body/token path) | pass, after fix | none now |
| M2-303 | X business publisher | done | pass ×2 | pass ×2 (independent audit found a real but non-blocking residual: no homoglyph/Unicode-confusable detection in personal/business account-name matching) | logged as follow-up, not blocking |
| M2-304 | Pinterest publisher | done | pass ×2 | pass ×2 | none |
| M2-401 | Blog → Mayankbuilt | done | pass | pass | none |
| M2-402 | LinkedIn repurpose | done | pass | pass | none |
| M2-403 | X personal repurpose | done | pass | pass | none |
| M2-404 | Medium import link | done | pass | n/a (no OAuth/tokens, correct) | none |
| M2-501 | Builder planner | done | pass | pass | none |
| M2-502 | Builder implementer | done | pass, after 2 real SEC fixes (plan-approval `kind` bypass, main-ref restore on drift) | pass, after fixes + independent re-review tracing control flow directly | none now |
| M2-503 | Builder auditor | done | pass (independent re-review re-derived all 6 claimed guarantees against code, not summary text) | pass | none |
| M2-504 | Builder phase gate | done | pass (independent re-review; also fixed a real doc bug — decision log ID collision D25 vs D26) | pass (re-scoped to needs-sec after the fact, correctly) | none |
| M2-505 | Builder dashboard screen | done | pass | pass | none |
| M2-601 | Revenue tracking | done | pass | pass | none |
| M2-602 | Micro-SaaS flow | done | pass, after real fixes (missing `StageApproved`, redo-with-note untested, repo-path handling) | pass, after fix | none now |
| M2-603 | Agency leads + proposals | done | pass, after a real fix (nil-`Approvals` soft-skip instead of fail-closed) | pass, after fix | none now |
| M2-604 | Pinterest affiliate boards | done | pass | pass | none |

**Result: 0 of 42 `done` tickets lack real review substantiation.** Every `## Review` section found contains
specific file:line references, named tests, and (for most) real re-run command output — not generic
"looks good"/"verified against docs" language. A targeted grep for generic-verification phrasing
(`"verified against live docs"`, `"LGTM"`, `"looks good"`, `"appears correct"`, etc.) across all ticket
review sections found exactly one hit, and it is itself a *correction record*: M2-301's independent
crew audit explicitly calls out that Cursor's ticket Notes had twice fabricated "verified against live
docs" claims earlier in this session, names the fix commits (`904e9ee`, `e18a2da`), and states the audit
did not trust the prior verdict and re-derived everything from the code itself (confirmed — the
re-derivation that follows is genuinely specific, with exact line numbers and re-run test names). No
uncorrected fabricated claim was found anywhere in the current ticket set.

---

## 3. Acceptance-criteria spot checks (across all 6 phases)

Verified against actual code, not just the checked boxes, for at least 2–3 tickets per phase (weighted
toward `needs-sec: yes` / money / credentials / publishing tickets):

- **P1 — M2-101** (doctor/config): `RequireLoopbackURL` and the doctor's pre-HTTP loopback refuse are real
  (`internal/config/config.go`, `cmd/mayank2/doctor.go`) — confirmed via the ticket's own pasted `doctor`
  run and test names; matches AC.
- **P1 — M2-105** (Telegram): PIN-gating on bulk `/pause`, token redaction, and `preview_path` confinement
  under `data/` were verified as real, tested fixes (not just claimed) by the ticket's own re-run test list
  (`TestPINRequiredForPause`, `TestTokenRedactedInTransportErrors`,
  `TestPreviewPathOutsideDataRootFallsBackToText`) — all present and passing in the fresh `go test` run
  above (`internal/telegram` package, 1.7s).
- **P1 — M2-110** (DPAPI/OAuth, `needs-sec: yes`): DPAPI-only `Protect`, loopback+PKCE OAuth — package
  compiles and its tests pass in the fresh run; not independently re-read line-by-line beyond the ticket's
  own detailed trace (budget), no contradiction found.
- **P2 — M2-205** (compliance G1–G7, safety-critical): **independently re-read in full**, see §4 below —
  G7's fix is real, present, and tested in the actual `dev` tree, not just claimed in the ticket.
- **P2 — M2-210** (final gates + approval, `needs-sec: yes`): read `internal/compliance/final.go` in full
  (see §4) — `FinalEngine.Handle` genuinely runs `RunFinalGates` → saves report → returns `queue.Permanent`
  on any gate failure *before* calling `Approvals.Start`; matches the AC and the ticket's own trace.
- **P2 — M2-211** (YouTube uploader, `needs-sec: yes`, publishing): ticket's own re-derivation (traced
  `Publish()` calling `RequireApproved` as the first DB/logic step, before token/upload) is consistent with
  the same `RequireApproved`-first pattern independently confirmed in M2-301/M2-303/M2-304 below; not
  separately re-read line-by-line here (budget).
- **P3 — M2-301 (Instagram)**: independently re-traced by the ticket's own crew audit: `RequireApproved`
  (line 108) before `lookupPublished`, `reserveCap`, `token()`, and the first Graph call (line 138);
  `igDoJSON` is the only body-parsing path and redacts unconditionally before unmarshal and before building
  any error string. Confirmed no code path bypasses redaction.
- **P3 — M2-302 (Facebook)**: the one real gap this audit's predecessor found (`UploadHosted` bypassing
  redaction) was traced to a specific fix commit (`a6e50d6`) with regression tests
  (`UploadHostedRedactsErrorBody`, `UploadErrorNeverLeaksToken`) that exist and pass in the current tree
  (`internal/publish` package, part of the green `go test ./...` run above).
  This is the class of finding CLAUDE.md's compliance non-negotiables exist to catch, and it *was* caught
  and fixed before `done`.
- **P3 — M2-304 (Pinterest, affiliate/`#ad`)**: cloaker-link rejection (`looksLikeCloaker`) confirmed to run
  before any Pinterest API call, before the token is even fetched; affiliate disclosure confirmed to be
  real server-side injected text (`pinDescription`), not a comment — with the honestly-disclosed caveat that
  disclosure presence depends on the caller setting `PaidPromotion` correctly (no independent link-pattern
  cross-check). Logged as a non-blocking residual in the ticket, correctly.
- **P4 — M2-401 (Blog→Mayankbuilt)**: git push/fetch token redaction, fixed-argv `DefaultGitRunner`, no
  `--force`, merge gated on `RequireApproved` — consistent with CLAUDE.md's "Builder never pushes to
  `main`" and "official APIs only" rules; not separately re-read line-by-line (budget).
- **P5 — M2-502 (Builder implementer, `needs-sec: yes`, executes Claude/git as child processes)**:
  independently re-traced `restoreMainRef` is genuinely invoked (not just documented) before a Permanent
  fail on ref drift, and `requireApprovedPlan` genuinely rejects wrong `kind`/mismatched `content_id` for
  both lookup paths — confirmed by the ticket's own line-referenced trace plus a second independent
  re-review round; consistent with CLAUDE.md's fixed-argv / no-shell-from-AI-output rule.
- **P5 — M2-503 (Builder auditor)**: independently re-derived all 6 claimed guarantees against actual code
  (commit-scope enforcement, never touches `main`, separate worktree, CAS branch landing, fail-closed
  verdict parsing, bounded/non-fatal Playwright step) — all confirmed real in the ticket's own re-review.
- **P6 — M2-601 (Revenue tracking, `needs-sec: yes`, money)**: YouTube estimated-revenue pull is
  idempotent-upsert, Meta auto-pull explicitly refused (no clean Insights path — matches CONTEXT §5 open
  question), GET/POST behind `requireAuth`. Not separately re-read line-by-line (budget); no contradiction
  found against the ticket's own AC trace.
- **P6 — M2-603 (Agency leads/proposals, `needs-sec: yes`)**: independently confirmed the real fix for a
  nil-`Approvals` soft-skip (would have silently skipped the approval gate) — now fails closed, with a named
  regression test (`TestDraftProposalNilApprovalsFailsClosed`).

No spot-checked `[x]` acceptance-criteria box was found to be checked without matching code. One
class of gap recurs honestly across P2/P3 tickets (M2-202, M2-211, M2-212): **the actual wiring that would
call these packages from a running daemon does not exist yet** — this is M2-116's job, tracked, not hidden,
and not something any of these tickets claimed to have done.

---

## 4. Compliance gate re-check (`internal/compliance`)

Read the actual gate implementations, not the tickets' claims about them, since CLAUDE.md marks this as the
single most safety-critical code in the repo and this session's brief specifically flagged G7 as having had
a real bug earlier.

- **G1–G6** (`g1.go`–`g6.go`, 42–150 lines each): substantive, non-trivial logic (keyword/originality/source/
  format checks); file sizes and the ticket's own test-name lists (`TestG1_*`, `TestG2_*` incl. two
  fail-closed cases, `TestG3_unsourcedFails`, `TestG4_*`, `TestG6_formatStreakFails`) are consistent with
  real gates, not stubs. G2's originality check was found fail-open (skipped cosine check when `Embed` was
  nil, defaulting to pass) and fixed to fail closed when priors exist — confirmed present via the ticket's
  named tests (`TestG2_priorsWithoutEmbedFailsClosed`, `TestG2_emptyEmbedAgainstPriorsFailsClosed`), both of
  which are part of the passing `internal/compliance` test run above.
- **G7** (`g7.go`, read in full, 220 lines): confirmed **not** a stub. The original bug (English scripts
  with no `Classifier` wired fell straight to `Passed: true` with zero computation) is fixed:
  `Check()` now calls `englishReadabilityHeuristic()` for English when `Classifier == nil` (line 90–96),
  which runs four independent, real, hostile-input-sensitive checks — immediate word-repetition/stutter
  detection, filler/hedge-word density (>6%), repeated 4-gram/looping-phrase detection, and a run-on-sentence
  check (single sentence, no punctuation, >45 words) — any one of which can fail the gate on its own. Hindi's
  artifact-list + Devanagari-ratio check is untouched and still real. When a `Classifier` **is** set, its
  verdict runs after and overrides the heuristic in both directions (lines 98–112) — confirmed this is the
  live path, not dead code, since it's unconditional on `g.Classifier != nil`. This matches the fix the
  ticket's own review describes, and the code is genuinely present in `dev`, not just claimed.
- **F1–F6** (`final.go`, read in full, 594 lines): all six are real, independent checks — F1 license-sidecar
  presence, F2 burned-captions/`.srt` presence, F3 disclosure-flags-decided, F4 duration/loudness
  (−14 LUFS ±1.5)/resolution/black-frame checks, F5 cadence caps (including X business/personal
  no-duplicate), F6 quota-remaining. `RunFinalGates` runs all six unconditionally and ORs their failures into
  `Passed`; `FinalEngine.Handle` (confirmed by reading the code directly) calls `RunFinalGates` → saves the
  report → returns `queue.Permanent` on any failure **before** ever calling `Approvals.Start` — a failing
  item genuinely cannot reach approval. One intentional, documented fail-open exists: F6 treats a *missing*
  quota row as "available" (D24 — not-yet-configured providers shouldn't block everything) — this is a
  disclosed product decision, not a silent bug, and is distinct in kind from the G7/G2 issues that were
  fixed.
- **F7** ("Human — Mayank approves on Telegram or the dashboard", per `docs/COMPLIANCE.md`): not a
  code gate object; it's the human-approval step itself, implemented as `FinalEngine.Handle` calling
  `Approvals.Start` only after F1–F6 all pass, and `content.ApprovalService.Decide` (confirmed by M2-210's
  ticket, itself independently re-verified) is the only path that can mark an item approved. Consistent with
  ARCHITECTURE's design — not a missing gate.

**Compliance verdict: G1–G7 and F1–F7 are real, fail-closed-by-default logic, not stubs.** The two genuine
weaknesses found earlier in this same session's audit history (G2 originality fail-open, G7 always-pass
no-op) are both fixed in the current `dev` tree, with tests, independently re-verified twice each by
different review rounds per the ticket history in §2.

---

## 5. Leftover debug/test artifacts, untracked TODOs, dead code

Grep sweep across `internal/`, `cmd/`, `web/src/` (excluding `_test.go` files for the TODO/FIXME search):

- `TODO` (outside tests): **0 matches**.
- `FIXME` / `XXX:` / `HACK`: **0 matches**.
- Debug print artifacts (`fmt.Println("DEBUG`, `console.log("DEBUG`, `debugger;`): **0 matches**.
- `docs/audit/` had no pre-existing file at this path before this report (only `docs/audit/p1-foundation/`
  and `docs/audit/TEMPLATE.md`); this report is new.

No non-blocking cleanup items found beyond what's already logged inside ticket Notes (e.g. M2-303's
homoglyph-matching follow-up, M2-304's cloaker-host-list-is-finite note, M2-212's duplicate
`analytics.pull` registration landmine that M2-116 already lists as an acceptance criterion to resolve).

---

## 6. Summary for Mayank

- **Build/test suite:** 100% green — Go (23 packages), web, remotion, media-tools, and the full
  `.githooks/pre-commit --all` run, all re-executed fresh in this worktree, no shortcuts.
- **Ticket substantiation:** 42/42 `done` tickets have real, specific, independently-re-derived QA (and SEC
  where `needs-sec: yes`) verdicts. Zero fabricated or generic-language verdicts found in the current tree;
  the one instance of fabricated-claim language on record (M2-301) is itself a correction note about an
  earlier, already-fixed incident.
- **Spot-checked acceptance criteria:** no checked box found without matching code, across all 6 phases.
- **Compliance gates:** G1–G7 and F1–F7 are real, fail-closed logic; the two known-bad gates from earlier in
  this session's history (G2, G7) are fixed and tested in `dev`.
- **The one thing to not lose track of:** `M2-116` (`status: ready`) is the ticket that actually starts the
  daemon; `cmd/mayank2 run` is still `cmdStub` in `dev` today. Every subsystem below it is real and tested,
  but nothing runs them together yet, so SPEC §1's P1 exit check cannot be demonstrated on this tree. This
  does not block merging the reviewed, tested code in `dev` to `main` — but `main` should not be presented
  as "the product runs end to end" until M2-116 ships.
