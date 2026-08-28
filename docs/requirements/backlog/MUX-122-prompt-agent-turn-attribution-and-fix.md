# Prompt-Agent Turn Budget — Attribute, Then Fix

Carries forward Phases 2–4 of
[MUX-115](../completed/MUX-115-prompt-agent-turn-budget-exhaustion.md), which closed at 11/32 with
its instrument built and never used.

**The expensive part is already done.** MUX-115 shipped a working per-turn tracer (`harness/trace.go`,
committed `ae39804`, verified by a cache-bypassing run including its negative control). This spec
does not build anything to start with — it **runs** what exists, reads the result, and only then
chooses a fix.

Tracking: _(no GitHub issue yet)_

## Context

### The plateau

`scripts/test-prompt-mode.sh` has returned **26 passed / 2 failed / 1 skipped** across four
consecutive attempts, on four different code states. The same two checks fail every time: the
prompt-agent's `launch` and named-`approve` intents exhaust the 10-turn budget before issuing the
required command.

| Attempt | What it targeted | Result |
|---------|------------------|--------|
| Tool-profile widening | Probes rejected by the profile | 26/2/1 |
| Approve alias fix | Both spellings of the approve verb | 26/2/1 |
| `--wait`/`--track` flag fix | A malformed send burning a turn | 26/2/1 |
| Probe-burn hardening (`765ec06`) | The probe habit itself | 26/2/1 |

Run 10 is the informative one: probe rejections fell 4→3 while turn exhaustion **rose** 1→2 and the
result did not move. Probe-burn is a *contributing factor, not the sole cause*.

### What is already available

`MUXCODE_HARNESS_TURN_TRACE=1` produces a JSONL trace with, per turn: index, tool, arguments,
outcome. `TraceOutcomeRejectedProfile` separates *turns lost to profile-rejected probes* from every
other cause — precisely the split four attempts could not observe. Arguments are scrubbed through
`ScrubPII` before truncation; rows are appended individually so the trace survives a killed run.

> **Do not open this spec by implementing a fix.** Four plausible guesses have each been
> implemented and each refuted by the same number. A fifth guess is not better than the fourth. The
> first deliverable here is **one traced run**, and it is cheap.

## Requirements

### Acceptance criteria

- [ ] A trace captured from a **failing** `launch` intent accounts for all 10 turns, each attributed
      to a named cause
- [ ] The same attribution exists for the failing named-`approve` intent
- [ ] The probe-vs-other split is **recorded as numbers** in this spec, not summarised
- [ ] The attribution states plainly which of the four prior hypotheses it refutes, and which (if
      any) it supports
- [ ] A fix is chosen **from that attribution**, and the spec records why the trace pointed at it
- [ ] After the fix, `scripts/test-prompt-mode.sh` returns a result **other than 26/2/1** — a changed
      number is the minimum bar for believing any fix worked
- [ ] `launch` intent: a named or described graph resolves and starts via `muxcode graph run`
- [ ] Named-gate approval releases the gate, and the paired unnamed-approve negative control reports
      a **validated pass**, not a skip
- [ ] `go vet ./...` and `go test ./...` green in both modules

### Technical approach

Phase 1 is a measurement, not a change. Resist editing anything before the trace is read — the
instruction at the top of this spec exists because four prior attempts each began by choosing a fix.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode-llm-harness/harness/trace.go` | The tracer — already built and verified |
| `tools/muxcode-llm-harness/harness/loop.go` | `processBatch()`, the turn loop and exhaustion path |
| `tools/muxcode-llm-harness/harness/tools.go` | `IsToolAllowed()` — the rejection the trace names |
| `agents/prompt.md` | The "never probe" instruction whose enforcement is in question |
| `scripts/test-prompt-mode.sh` | The suite whose 26/2/1 plateau defines success |

### Candidate fixes carried over (recorded, **not** endorsed)

From MUX-109's close-out, unchanged and still unevidenced:

1. **First-tool-call-must-be-the-command** in the agent definition — narrower and more checkable
   than "never probe".
2. **Raise the harness turn budget** — cheap, treats the symptom; a model that probes four times
   will probe six.

Both become cheap to evaluate once the attribution exists, and one may become obviously wrong.

## Implementation

### Phase 1: Attribute the failure

- [ ] Run `scripts/test-prompt-mode.sh` with `MUXCODE_HARNESS_TURN_TRACE=1`
- [ ] Attribute all 10 turns of the failing `launch` intent to named causes
- [ ] Attribute all 10 turns of the failing named-`approve` intent
- [ ] Record the probe-vs-other split in this spec, as counts
- [ ] State plainly which of the four prior hypotheses the trace refutes

### Phase 2: Fix, chosen from evidence

- [ ] Choose a fix from the Phase 1 attribution
- [ ] Implement it
- [ ] Update the candidate-fix list above with what the trace refuted
- [ ] Re-run the suite and record the new number

### Phase 3: Integration test

- [ ] Extend `scripts/test-prompt-mode.sh` with a trace-enabled assertion that every consumed turn
      is attributed
- [ ] Test: a failing intent produces a trace naming every consumed turn
- [ ] **Negative control**: tracing off produces no trace file and leaves loop behaviour unchanged
- [ ] Test: `launch` intent starts a run that appears in `graph list`
- [ ] Test: named-gate approval releases the gate; unnamed *"approve whatever is waiting"* does not,
      and the negative control reports a validated pass rather than a skip
- [ ] Coverage floor, itemized against the actual check count — **not** an imagined larger number
- [ ] Run the script and confirm the result is no longer 26/2/1

## Risks

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Reaching for a fix before reading the trace | Exactly how four attempts produced four identical results | Phase 1 is a measurement; the standing instruction above |
| The trace shows no single dominant cause | The premise (one attributable cause) may be wrong | Record that honestly — a refuted premise is a result |
| A green suite that never ran the new assertions | A `-run` filter silently skipped a negative control during MUX-115 | Run the package, never a filtered pattern; check the count against the test inventory |
| Fix moves the number without fixing the cause | 26/2/1 → 25/3/1 could be noise | Require the attribution to explain the movement |

## Status

Backlog
