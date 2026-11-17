# MUX-209: `test-force-respond.sh` Fails — a Non-Forced `deliver` No Longer Surfaces the In-Flight Skip

**Tracking:** [mkober/muxcode#180](https://github.com/mkober/muxcode/issues/180)

`scripts/test-force-respond.sh` ends `14 passed, 1 failed`. Its last assertion sends a tracked
request to an OpenCode-configured role, ages the in-flight task six seconds, runs a non-forced
`muxcode deliver test` and expects the output to name the skip (`skip` or `in-flight`). The command
instead attempts the injection and reports it woke the role. Either `deliver` stopped surfacing a
skip the script was written to pin, or the script's premise went stale when the delivery rules
changed — which of the two is this spec's first question.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's instruction relayed by edit, while closing out
[MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md), whose "every
`scripts/test-*.sh` still passes" criterion excludes this script as a failure it did not cause.
Observed by plan in the run agent's logs; nothing here was re-run by plan. Code read at `b8bfcab`
plus the uncommitted MUX-208 tree.

### Observation — verified

| Fact | Where |
|------|-------|
| The script failed in two runs on 2026-11-13: the 15:11 `bash scripts/test-all.sh` and the 15:38 rerun | proc logs `1794600689-proc-f90797b3` and `1794602297-proc-b8c4f9fe`; `/tmp/mux208-rerun-logs/test-force-respond.log` |
| Every other check passes, including `deliver --force bypasses the in-flight skip` and `no skip message under force` on the `run` role | same log: `14 passed, 1 failed` |
| The failing assertion: `MUXCODE_TEST_CLI=opencode`, `muxcode send test skipcheck "skip surfacing payload" --no-notify --track`, `sleep 6`, `muxcode deliver test`, then `grep -qE "skip\|in-flight"` on the output | `scripts/test-force-respond.sh:191-206` |
| Actual output: `[wakeup] test injection not confirmed — text still parked after 3 retries; leaving inbox for next cycle` then `deliver: woke test with 1 pending message(s)` | same log |
| Binary under test: `v0.1.39-2-gb8bfcab-dirty`, installed 15:10 from the MUX-208 tree | `muxcode version` |

### Why this is not MUX-208's — verified

| Fact | Where |
|------|-------|
| `tools/muxcode/bus/deliver.go`, `bus/task.go` and `cmd/deliver.go` are unmodified in the MUX-208 working tree | `git status --short` |
| The script's only MUX-208 change is how it computes its scratch bus directory (`muxcode_bus_dir`, replacing the `/tmp/muxcode-bus-$SESSION` literal) | `git diff -- scripts/test-force-respond.sh` |

The script was **not** run against `main`'s own binary, so "fails the same way before MUX-208" is
inferred from the unchanged code under test, not observed.

### Cause — not determined

Candidates, none verified:

- The delivery rules changed after the script was written. `deliver.go` and `task.go` were last
  touched by `8f0284e` (PR 151 review: owed-reply redelivery, scrape-road identity), `50c5047`
  (MUX-154) and `fd03d39` (MUX-171); [MUX-192](../completed/MUX-192-stale-in-flight-task-starves-codex-delivery.md)
  changed how an in-flight task gates a wake to a Codex or OpenCode agent. A non-forced deliver over
  an unnotified inbox row may now be delivered by design.
- The six-second `sleep` no longer ages the task into the state the skip applies to.
- A regression: the skip is still intended and `ForceDeliver` lost it.

### Blast radius

- `bash scripts/test-all.sh` exits 1 on every run, so any spec with an "all integration scripts
  pass" criterion must carve this script out by hand, as MUX-208 did.
- If the third candidate holds, a non-forced `deliver` types a payload over an in-flight task with no
  message saying so — the "silent success" the assertion's own comment names.

## Requirements

### Acceptance criteria

- [x] The cause is recorded here: stale script premise, or a `deliver` regression — with the commit
  that introduced the change
- [x] If the script is stale: the assertion pins the behaviour `deliver` has today, with a negative
  control that fails when that behaviour is removed
- [x] If `deliver` regressed: a non-forced deliver against an in-flight task surfaces the skip again,
  pinned by a Go test — **not applicable**: Phase 1 established there was no regression, so nothing
  was restored and no Go test was added
- [x] `bash scripts/test-force-respond.sh` exits 0 with every check passing and none removed without
  a replacement
- [x] `docs/agent-bus.md`'s `muxcode deliver` entry matches the behaviour the script pins

### Technical approach

Phase 1 is a read, not a fix: run the script against a binary built from `main` to confirm the
failure predates MUX-208, then bisect the three commits above with the single failing sequence. The
fix follows whichever candidate the bisect confirms.

### Key files

| File | Purpose |
|------|---------|
| `scripts/test-force-respond.sh:191-206` | The failing sequence |
| `tools/muxcode/bus/deliver.go` | `ForceDeliver`, the skip and re-drive branches |
| `tools/muxcode/bus/task.go` | In-flight task state and its guard window |
| `tools/muxcode/cmd/deliver.go` | The messages `deliver` prints |

## Implementation

### Phase 1: Establish the cause

- [x] Run `bash scripts/test-force-respond.sh` against a binary built from `main`; record pass/fail
  and the output here
- [x] Bisect `8f0284e`, `50c5047`, `fd03d39` and the MUX-192 change with the failing sequence;
  record the introducing commit
- [x] Decide: stale premise or regression; record it here

### Results for Phase 1

Recorded 2026-11-17 from the worker's findings (`/tmp/mux209-phase1-findings.md`). Plan read the run
agent's history rows for the bisect, looked up the commit, and read `wakeBlockedByTask`. Boxes are
ticked at `verify-spec`, not here.

**The run on today's tree** (run task `1794949464-spawn-26088880-ab753a3b`, tree-built binaries
first on `PATH`):

| Tree | Result |
|------|--------|
| `HEAD` `51d43ec` | Exit 1 — 14 passed, 1 failed. `FAIL: non-forced deliver surfaces the skip`, output `[wakeup] test injection not confirmed — text still parked after 3 retries; leaving inbox for next cycle`. As filed |
| The working tree, with today's MUX-232, MUX-219 and MUX-227 | The same 14 to 1, the same output |

So the failure predates MUX-208 and is unmoved by today's change to `deliver.go` — the inference at
filing is now an observation.

**The bisect** (run task `1794949663-spawn-26088880-5fab985e`). Each commit ran its own copy of the
script against its own binary, because today's script needs MUX-208's `muxcode bus-dir`.

| Commit | Result |
|--------|--------|
| `05700de`, the parent of `531e003` | Exit 0 — 15 passed, 0 failed; `ok: non-forced deliver surfaces the skip` |
| `531e003` — 2026-09-26, "MUX-192 Bound the wake skip and fix the delivery backstop (Phase 2)" | Exit 1 — 14 passed, 1 failed; the same `FAIL` and output as today |

The assertion was added in `c86594c` (MUX-105). None of this spec's candidates — `8f0284e`,
`50c5047`, `fd03d39` — nor `ba749b0` (MUX-232) is the cause: the behaviour flips at `531e003` and
has not moved since.

**Decision: a stale script premise, not a `deliver` regression.** `531e003` introduced the bounded
wake skip (`wakeSendGraceSecs`, `wakeBlockedByTask`, `bus/wake_gate.go`). Since then an in-flight
task withholds an unforced wake only while all three hold:

| Condition | The script's sequence |
|-----------|-----------------------|
| The task is younger than 5 s (`wakeSendGraceSecs`) | It sleeps 6 s first |
| The task is unanswered | Holds |
| Its request is **no longer pending** in the role's inbox — "a pending request is what the wake delivers, not evidence the agent is working on it" | It sends with `--no-notify`, so the request is still pending |

Two of the three let the wake through, and by design: MUX-192's incident of 2026-09-25 was a Codex
agent kept idle for minutes behind a stale task. The forced-deliver checks on the `run` role are
unaffected and pass.

**What Phase 2 does with it.** The first branch of each either/or applies: rewrite the assertion to
pin today's rule, with a negative control — a fresh, unanswered task whose request was already
consumed still withholds the unforced wake inside the 5 s grace (the skip MUX-192 kept), while a
pending request past the grace is delivered. The regression branch of criterion 3 and of Phase 2's
second step does not arise.

Verified and ticked 2026-11-17. The phase changed no code; its evidence is the two runs above, which
plan read in the run agent's history, and the rule in `bus/wake_gate.go`. The graph's `./build.sh`
(16:11:57) and `./test.sh` (16:13:41) both exited 0, and the review at 16:14:25 returned 0 must-fix,
0 should-fix and 1 nit — this spec's Status still said the cause was not determined; corrected. The
first criterion is ticked on this section.

### Phase 2: Fix

- [x] Stale premise: rewrite the assertion to pin today's behaviour, with a negative control
- [x] Regression: restore the surfaced skip in `ForceDeliver`, with a Go test that fails without it — **not applicable**: no regression (Phase 1)
- [x] Update `docs/agent-bus.md` if the documented behaviour differs

### Results for Phase 2

Recorded 2026-11-17 from the worker's request; plan read the diff of
`scripts/test-force-respond.sh` and the run agent's history row. Boxes are ticked at `verify-spec`,
not here.

**The stale check is rewritten to pin the wake gate from both sides.** No production code changed.

| Side | The sequence | Asserts |
|------|--------------|---------|
| Withheld | Test reads tracked request A — its task in flight, unanswered, no longer pending. Request B queues behind it. A non-forced `deliver test` runs inside the 5 s grace | B is still queued; the output carries `skipping test injection — in-flight task`; it reports **no** wake — never a silent success |
| Delivered — the negative control | Request C is sent and left pending; the script sleeps 6 s, past the grace and past A's task | `deliver test` wakes test, with no skip line |

Either half of the rule reverted fails one of them. The earlier check, a pending request aged 6 s
expecting a skip, is what the gate now delivers by design. Coverage floor 13 → 18.

**Run: 18 passed, 0 failed, exit 0** against a tree build (run task
`1794950160-spawn-26088880-69c03a36`, requested 16:16:00 after the script's last edit at 16:15:50;
the run agent's history row confirms it). Its log is `/tmp/mux209-force-respond.log`; plan read the
history row, not the log.

**`docs/agent-bus.md` did differ** — the `muxcode deliver` entry did not state the rule at all. Plan
added it: the three conditions under which an in-flight task withholds an unforced wake, what
`deliver` prints then, and that `--force` bypasses the task check but never a mid-turn pane.

**The regression branch does not arise.** Phase 2's second step — restore the surfaced skip in
`ForceDeliver`, with a Go test — and the third criterion are conditional on a `deliver` regression.
Phase 1 established there was none.

Verified and ticked 2026-11-17 once all three had run after the last edits (the script at 16:15:50,
plan's docs addition at 16:18:21): `./build.sh` exit 0 at 16:18:56, `./test.sh` exit 0 at 16:20:39,
review at 16:21:09 with 0 must-fix, 0 should-fix, 0 nits. Criteria 2 and 5 are ticked on the
rewritten check and the docs. The regression step and the third criterion are ticked **as not
applicable**, each saying so beside its box: an open box would hold the run on work Phase 1 showed
is not needed, and the annotation is what keeps the tick from reading as done.

### Phase 3: Integration test

- [x] `bash scripts/test-force-respond.sh` through the run agent: every check passes, exit 0; record
  the counts here
- [x] The script carries a coverage floor so a skipped section cannot report green
- [x] `bash scripts/test-all.sh` no longer lists `test-force-respond` as failed

### Results for Phase 3

Recorded 2026-11-17 from the worker's request; plan read the run agent's history rows. Boxes are
ticked at `verify-spec`, not here.

Both runs went through the run agent (task `1794950524-spawn-26088880-cd153843`) against the
installed binary, `v0.1.71-1-g51d43ec-dirty`, built 16:18:48 — after the script's last edit at
16:15:50.

| Command | Result |
|---------|--------|
| `bash scripts/test-force-respond.sh` | Exit 0 — 18 passed, 0 failed |
| `bash scripts/test-all.sh force-respond` | Exit 0 — `test-force-respond PASS`; passed 1, failed 0 |

**Coverage floor.** The script ends `[ "$PASS" -ge 18 ]`, raised from 13 in Phase 2 with the five
checks that replaced the stale one. It prints no floor in its summary line; the guard is in the
script.

**What the second run is, and is not.** It is `test-all.sh` with the `force-respond` filter — one
script. It shows the runner now reports this script as passed. It is not a full `test-all.sh` run,
so it says nothing about the other scripts; this spec's criterion asks only that the runner no
longer list `test-force-respond` as failed.

Verified and ticked 2026-11-17. The phase changed no code and the script is unchanged since its runs
(last edited 16:15:50). The graph's `./build.sh` (16:25:47) and `./test.sh` (16:26:02) exited 0, and
the review at 16:26:34 returned 0 must-fix, 0 should-fix, 0 nits. The fourth criterion is ticked on
the 18-to-0 run: the one stale check was replaced by five, and none was removed without a
replacement.

## Related

- [MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md) — the
  close-out that excluded this script
- [MUX-105](../completed/MUX-105-force-respond-escalation.md) — the spec the script belongs to
- [MUX-192](../completed/MUX-192-stale-in-flight-task-starves-codex-delivery.md),
  [MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md) — changes to the
  delivery rules since

### Findings at activation (2026-11-17)

Activated against the tree at `51d43ec`. Plan did not re-run the script; whether it still fails, and
in the same way, is Phase 1's first step.

**The code under investigation changed on the day of activation.** `bus/deliver.go` was last touched
by `ba749b0` ([MUX-232](../completed/MUX-232-redrive-arrives-truncated-with-no-id-and-no-reply-route.md),
merged today): the re-drive now types the whole request, `ForceDeliver` goes through
`ForceDeliverFor`, and a re-drive counts only what it showed. So the bisect list in Phase 1 —
`8f0284e`, `50c5047`, `fd03d39` and the MUX-192 change — is one commit short, and the script's
failing output may read differently now. The first step is to record what the script does on this
tree before bisecting anything.

Nothing in this spec waits on a user decision: its either/or — a stale script premise or a
`deliver` regression — is settled by the run and the bisect.

## Status

**Complete** — activated and closed 2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 33 then; every better-ranked row was parked — MUX-123, MUX-122, MUX-032 and MUX-191 at that selection). Uncommitted at close-out; the run's ship step commits it. 14/14 — Phases 1–3 complete and all five criteria met, two items as not applicable.

The script failed because its premise was stale, not because `deliver` regressed. Since `531e003`
(MUX-192 Phase 2, 2026-09-26) an in-flight task holds back an unforced wake only while it is under
5 s old, unanswered, and its request has left the role's inbox; the old check expected a skip for a
pending request aged 6 s, which that rule delivers by design. The check now pins the rule from both
sides — withheld inside the grace, delivered past it — and the script passes 18 to 0.
`docs/agent-bus.md` states the rule under `muxcode deliver`. No production code changed.

For a later reader: the third criterion and Phase 2's regression step are ticked as not applicable,
each saying so; and the `test-all.sh` run recorded here was filtered to this one script.

Filed 2026-11-13 on the user's instruction relayed by edit. As filed, the cause was not determined
and that the failure predates MUX-208 was inferred. Both are settled since 2026-11-17: the cause is
a stale script premise, introduced by `531e003` (MUX-192 Phase 2), and the failure reproduces on
`HEAD` — see [Results for Phase 1](#results-for-phase-1).
