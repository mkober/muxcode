# The Run Chain Reported Success Three Minutes Before the Script Finished — and It Failed

**Tracking:** filed 2026-09-10 on the user's explicit request. Same class as the 2026-09-09 precheck
incident already recorded in `CLAUDE.md` (a lone passing `go vet` fired test→review before the suite
ran), and the reason the test precheck now feeds the chain only on failure.

The run agent reported it unprompted, which is how it was caught at all:

> an automatic run-succeeded chain message fired to watch prematurely at 10:31 (before the script
> actually completed at 10:34+) — that early signal was wrong, this is the real result

`scripts/test-prompt-mode.sh` then exited **1** (26 passed / 2 failed / 1 skipped). The chain had
reported success for a run that had neither finished nor passed.

## Context

### Observed (2026-09-10, session `muxcode`)

| When | Evidence | Source | Provenance |
|------|----------|--------|------------|
| 10:31:39 | `workflow watching from=idle trigger=chain:run:success` | lifecycle log | **machine-written (daemon)** |
| 10:31:39 | run-history row for the `bash scripts/test-prompt-mode.sh …` call: **`exit 0`, outcome `success`** | `run-history.jsonl` | **machine-written (hook)** |
| 10:31:45 | `workflow idle from=watching trigger=chain:watch:success` | lifecycle log | **machine-written (daemon)** |
| **10:35:53** | `/tmp/test-prompt-mode-run.log` last modified | filesystem `stat` | **machine-written (fs)** |
| — | that log's final line: `EXIT_CODE=1`, above `=== 26 passed, 2 failed, 1 skipped ===` | file content | **machine-written (script)** |
| 10:33 | run agent: *"my own bash call is still running as background task `bn857kq0r`"* | bus history | agent self-report |
| 10:36 | run agent: the 10:31 signal "was wrong, this is the real result" | bus history | agent self-report |

**The defect does not depend on any agent's account.** Two independent machine sources bracket it: the
daemon logged `chain:run:success` at **10:31:39**, and the filesystem shows the script still writing at
**10:35:53**, ending `EXIT_CODE=1`. That is a **4 m 14 s** gap between the declared success and the
actual failing finish, established without reading a single agent message.

The success row and the chain edge share a timestamp to the second.

### Second occurrence (2026-09-10 15:46, session `muxcode`) — and it narrows the mechanism

Recorded by plan 2026-09-10 16:08 on edit's request (`1789070744`), verified in the primary records
rather than from the report. Same script, same defect, a wider gap this time:

| When | Evidence | Source | Provenance |
|------|----------|--------|------------|
| 15:46:16 | edit dispatches `bash scripts/test-prompt-mode.sh` to run (task `1789069576`) | task store | **machine-written** |
| **15:46:55** | run-history row: `bash scripts/test-prompt-mode.sh` → **`exit 0`, outcome `success`** | `run-history.jsonl` | **machine-written (hook)** |
| 15:46 | `run → watch [request:watch]` "**Run succeeded** (bash scripts/test-prompt-mode.sh)" | bus history | **machine-written (chain)** |
| 15:47, 15:48, 15:49 | `watch → edit [event:notify]` "logs look healthy after deploy" — three false all-clears | bus history | **machine-written (chain)** |
| **15:54:09** | background task output `tasks/bqxzncyob.output` final write: `=== 26 passed, 2 failed, 1 skipped ===` then `[exited with code 1]` | filesystem `stat` + content | **machine-written (fs)** |
| 15:54 | `run → edit [response:run]` "test-prompt-mode.sh exit 1" — the real result | bus history | agent self-report |

**Gap: 7 m 14 s** between the declared success (15:46:55) and the actual failing finish (15:54:09),
bracketed by two machine sources exactly as the 10:31 occurrence was.

**This occurrence eliminates candidate 3.** The recorded command is bare — `bash
scripts/test-prompt-mode.sh`, a single statement with **no compound, no trailing `cat`**. The
trailing-`cat`-supplies-the-exit-status explanation cannot account for it. The prescribed Phase 1
experiment (re-run the compound command in the foreground) was never needed: a command with no `cat`
at all reproduced the defect, which is the stronger result.

**And it corroborates candidate 1.** Backgrounding was uncorroborated at 10:31 — the claimed id
`bn857kq0r` appeared in neither `proc.jsonl` nor `spawn.jsonl`. This time the background artefact is
**on disk**: `tasks/bqxzncyob.output` holds the script's full output ending `[exited with code 1]`,
and a hook-recorded run command at 15:51:06 tails that very path. That is no longer an agent's
account. Candidates 1 (background wrapper returns at dispatch) and 2 (hook records at dispatch)
remain live; the experiment to separate them is still owed.

**The countermeasure already exists — it just does not cover `run`.**
`evidenceGuardRoles = {"build", "test", "deploy"}` (`bus/evidence_guard.go:21`); `CheckEvidenceGuard`
returns nil for any other role at line 35-37. Lines 47-49 already block a backgrounded evidence
statement (`seps[i] == "&"` → `evidenceBackgroundReason`). So the exact shape that fired here is
**already refused for build, test and deploy, and permitted for `run`** — a gap in role coverage, as
the spec suspected, rather than a missing mechanism.

### Mechanism — a hypothesis, and deliberately labelled as one

**What is established:** an authoritative `exit 0` row exists for a call whose script was still
running, and the chain fired on it. The row describes something that had not finished.

**What is hypothesised:** that the call was *backgrounded*, and that a backgrounded call returns at
dispatch. This rests on the run agent's own account — *"my own bash call is still running as
background task `bn857kq0r`"* — and that account is **uncorroborated**: the id `bn857kq0r` appears in
neither `proc.jsonl` nor `spawn.jsonl`, so no ledger confirms a background task by that name existed.

This distinction was raised by edit and is worth stating plainly, because an earlier finding in this
session (MUX-173) asserted a cause from a single agent-adjacent log and had to be retracted. The
timeline here survives that standard; **the mechanism does not yet**. Phase 1 exists to settle it.

Candidate mechanisms, all consistent with the machine evidence:

| Candidate | Would explain the exit-0 row by |
|-----------|---------------------------------|
| Backgrounded call returns at dispatch | the wrapper genuinely exiting 0 once the child is launched |
| Hook records at dispatch, not completion | the PostToolUse path writing before the call ends |
| Compound statement's last command | the row taking `cat`'s exit status rather than the script's — the call was `bash … > log 2>&1; echo "EXIT_CODE=$?" >> log; cat log` |

The third deserves particular attention: it needs no backgrounding at all, and the recorded command
string genuinely ends in `cat log`, which exits 0 whatever the script did. `CheckEvidenceGuard` exists
to refuse exactly this bundled shape for build/test/deploy — **the run role is not covered by it**.
That would make this a gap in the guard's role coverage rather than a timing bug, and it is testable
immediately without reproducing any race.

### Downstream consequence — not a stray row

Watch acted on the early signal and posted a "logs look healthy after deploy" all-clear on a **failing
run**. Anything gated on the run chain's success edge would have proceeded on it. In a graph run, that
edge is what advances a node.

### Precedent, and why the existing countermeasure does not cover this

`CLAUDE.md` already records the 2026-09-09 case where a passing `go vet` precheck fired test→review
before the suite ran. The fix there was narrow and correct for that shape: **the precheck feeds the
chain only on failure**, so a passing vet writes no row.

That countermeasure does not reach this one. The problem here is not *which command* fed the chain but
*when* its result was believed — a call that has not finished has no result to believe. The evidence
guard (`CheckEvidenceGuard`) likewise addresses a different failure: bundling statements, not
backgrounding one.

### Scope boundary

In scope: a chain edge must not fire on a call that has not completed, and the run chain specifically
must key on the script's own completion. Not in scope: the two section-5 failures inside that script
(see MUX-173's open question), the evidence guard's bundling rules, or the watch agent's behaviour —
watch acted correctly on the signal it was given.

## Requirements

### Acceptance criteria

- [ ] The cause is **established by experiment** — dispatch-time recording vs background-wrapper exit
      — and recorded here before any fix lands
- [ ] A backgrounded or otherwise incomplete call writes **no** authoritative success row, and fires
      no chain success edge
- [ ] A run that completes normally still fires its success edge exactly as it does today, pinned by a
      test — so the fix does not buy correctness by disabling the chain
- [ ] When a call's completion cannot be determined, the chain **fails closed** (no success edge)
      rather than assuming success
- [ ] A lifecycle row records a withheld edge, so a chain that did not fire is visible rather than
      silent

### Technical approach

Settle the mechanism first. If the hook records at dispatch, the fix is in the PostToolUse path: an
authoritative row must be written on completion, and a call still running writes nothing. If the
background wrapper returns 0, the fix is that a backgrounded call is not chain-eligible at all —
its exit code describes the launch, not the work.

The load-bearing test is the **negative control**: a normal completing run must still fire its
success edge. A fix that simply stops firing passes any "no premature success" assertion, and would
silently disable every run chain in the repo.

Worth considering alongside: the run chain's `OnSuccess` is a `command_match` allowlist, so it already
distinguishes verification runs from other commands. Whatever gates completion should compose with
that rather than replace it.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/hook.go` | PostToolUse classification; where the authoritative exit code is recorded |
| `tools/muxcode/bus/evidence_guard.go` | `CheckEvidenceGuard`, `parseShellStatements` — adjacent, different failure |
| `tools/muxcode/bus/profile.go` | `EventChain` / `ResolveChain`; the run chain's `OnSuccess` allowlist |
| `tools/muxcode/bus/proc.go` | background execution — the wrapper whose exit code is in question |

## Implementation

### Phase 1: Diagnose

- [ ] Determine which candidate holds: backgrounded-dispatch, hook-records-at-dispatch, or the
      compound statement's trailing `cat` supplying the exit status
- [x] Check the third first — it is the cheapest: re-run the same compound command in the foreground
      and see whether the recorded row reports the script's status or `cat`'s — _settled by the
      2026-09-10 15:46 recurrence instead of by the prescribed re-run: the command there was **bare**
      (`bash scripts/test-prompt-mode.sh`, no compound, no `cat`) and still produced an `exit 0`
      success row for a script that exited 1. Candidate 3 is eliminated — it is not necessary to
      produce the defect_
- [x] Establish whether `CheckEvidenceGuard` covers the `run` role, and record the answer here —
      _**it does not.** `evidenceGuardRoles = map[string]bool{"build": true, "test": true, "deploy": true}`
      (`bus/evidence_guard.go:21`); `CheckEvidenceGuard` returns nil for every other role
      (lines 35-37). The guard already blocks a **backgrounded** evidence statement at lines 47-49,
      so the shape that fired here is refused for build/test/deploy and permitted for `run`_
- [ ] Record the discriminating evidence, then choose the fix

### Phase 2: Fix

- [ ] Ensure an incomplete call writes no authoritative success row and fires no success edge
- [ ] Fail closed when completion is indeterminate
- [ ] Emit a lifecycle row for a withheld edge

### Phase 3: Tests

- [ ] Positive: a backgrounded long-running call fires no success edge before completion
- [ ] **Negative control:** a normal completing run still fires its success edge
- [ ] Indeterminate completion fails closed

### Phase 4: Docs

- [ ] `CLAUDE.md` hook-driven-chains bullet: extend the precheck precedent to state that an incomplete
      call is never chain evidence
- [ ] [`docs/hooks.md`](../../hooks.md): the same, with this incident named

### Phase 5: Integration test

- [ ] `scripts/test-chain-completion.sh`: background a sleeping script, assert no success edge fires
      before it ends, and that a normal run's edge still fires
- [ ] Coverage floor so a skipped section cannot read as green
- [ ] Run through the run agent and record the row here

## Notes

**Caught by a person, not a mechanism.** The premature edge produced a false all-clear that nothing in
the system contradicted; it surfaced only because the run agent volunteered that its earlier signal
had been wrong. A chain edge that can be wrong and leave no trace is the part worth fixing — the
lifecycle row for a withheld edge in Phase 2 exists for that reason.

**Observed three times, all on 2026-09-10.** 10:31 on the first run; per watch again on the rerun; and
15:46:55 with a bare (non-compound) command — the occurrence that eliminated candidate 3 and put the
background artefact on disk. The defect is reproducible enough that Phase 1's remaining question is only
*which* of candidates 1 and 2 holds, not whether the premature edge is real.

## Status

Backlog
