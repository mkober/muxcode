# Prompt-Agent Turn Budget Exhaustion — Instrument Before Fixing

The prompt-agent's `launch` and named-`approve` intents fail because the model exhausts its
10-turn budget before issuing the required command. **Four fix attempts have produced four
identical results.** This spec's first deliverable is therefore not a fix — it is the
instrumentation that would tell us where the turns actually go.

Split out of [MUX-109](../completed/MUX-109-prompt-mode-graph-control-pane.md) at close-out, where
these two intents are recorded as unmet in the Known Gaps table.

## Context

### The finding that motivates this spec

`scripts/test-prompt-mode.sh` has returned **26 passed / 2 failed / 1 skipped** across four
consecutive runs, on four different code states. The two failures are the same two checks every
time.

| Attempt | What it targeted | Result |
|---------|------------------|--------|
| Tool-profile widening | Probes rejected by the profile | 26/2/1 |
| Approve alias fix | Both spellings of the approve verb | 26/2/1 |
| `--wait`/`--track` flag fix | A malformed mutually-exclusive send burning a turn | 26/2/1 |
| Probe-burn hardening (`765ec06`) | The probe habit itself | 26/2/1 |

The last row is the informative one. Measured against run 9:

| | Run 9 | Run 10 |
|---|-------|--------|
| `not allowed by tool profile` rejections | 4 | **3** |
| Turn exhaustion (`Prompt 10/10`) | 1 | **2** |
| Result | 26/2/1 | 26/2/1 |

**Fewer probes, more exhaustion, identical outcome.** The hardening did what it targeted and did
not move the result, which means probe-burn is a *contributing factor, not the sole cause* —
something else is consuming the budget before the required command is reached.

### Why instrumentation is the deliverable

Four guesses have each been plausible, each been implemented, and each been refuted by the same
number. A fifth guess is not obviously better than the fourth. A per-turn dump of what the model
actually called would settle in **one run** what four attempts have not.

This is the same instruction-versus-enforcement split MUX-109 hit repeatedly: the agent definition
already carries a "never probe" instruction, and the instruction is not self-enforcing.

> **Do not open this spec by implementing candidate fix 1 or 2 below.** They are recorded because
> they were the maintainer's live options at MUX-109 close-out, not because either is established.
> Both become cheap to evaluate — and one of them may become obviously wrong — once Phase 1 lands.

## Requirements

### Acceptance criteria

- [x] A per-turn trace of the prompt-agent's harness loop records, for each turn: the tool called, the arguments, the outcome (accepted / rejected-by-profile / error), and the turn index
- [x] The trace is readable after a failed run without re-running it — written to a file, not only to the pane
- [ ] Running `scripts/test-prompt-mode.sh` with the trace enabled attributes all 10 turns of a failing `launch` intent to named causes
- [ ] The same attribution is produced for the failing named-`approve` intent
- [x] The trace distinguishes *turns spent on rejected probes* from *turns spent on any other cause* — the specific split that four fix attempts could not observe
- [x] Tracing is opt-in and off by default; a normal prompt-agent run is unchanged when it is off
- [x] The trace redacts through the existing PII scrub path — it captures tool arguments, which can carry user prompt text
- [ ] A fix is chosen **from the trace**, not from the candidate list, and the candidate list is updated with what the trace refuted
- [ ] After the chosen fix lands, `scripts/test-prompt-mode.sh` returns a result **other than 26/2/1** — a changed number is the minimum bar for believing any fix worked
- [ ] `launch` intent: a named or described graph resolves and starts via `muxcode graph run`
- [ ] Named-gate approval releases the gate, and the paired unnamed-approve negative control reports a validated pass rather than a skip

### Technical approach

The harness loop is the instrumentation point — `harness/loop.go` `processBatch()` already iterates
turns and already logs tool executions to history. The gap is that a *rejected* call and a
*budget-exhausted* loop are not distinguishable after the fact from what is retained.

Prefer extending the existing history/event path (`harness/events.go`, `logToolToHistory()`) over a
parallel logging channel, so the trace inherits the scrub and sink behaviour already in place.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode-llm-harness/harness/loop.go` | Turn loop, `processBatch()`, `MaxTurns` exhaustion path |
| `tools/muxcode-llm-harness/harness/events.go` | Event kinds and sinks — where a turn-trace event belongs |
| `tools/muxcode-llm-harness/harness/tools.go` | `IsToolAllowed()` — the rejection this trace must name |
| `tools/muxcode-llm-harness/harness/scrub.go` | PII scrub the trace must route through |
| `agents/prompt.md` | The "never probe" instruction whose enforcement is in question |
| `scripts/test-prompt-mode.sh` | The suite whose 26/2/1 plateau defines success |

### Candidate fixes (recorded, not endorsed)

Carried over from MUX-109 as the maintainer's options at close-out:

1. **First-tool-call-must-be-the-command** rule in the agent definition — narrower and more
   checkable than "never probe".
2. **Raise the prompt harness turn budget** — cheap, but treats the symptom; a model that probes
   four times will probe six.

Rung 1 was judged the better first move, for the escalation ladder's usual reason: a bigger budget
makes the failure rarer without making it wrong less often. Neither is established.

## Implementation

### Phase 1: Turn trace — complete

> **Built in spawn worktree `spawn-045c97da`, harvested onto this branch 2026-08-28 ~14:27.**
> Harvest fidelity verified by hash: `trace.go`, `trace_test.go`, `config.go`, `loop.go` are
> byte-identical to the worktree originals. Files: new `harness/trace.go` + `harness/trace_test.go`,
> modified `config.go`, `loop.go`, `loop_test.go` (the last only threading a `nil` tracer through
> `processBatch` call sites).
>
> Checked off against **passing tests**, not inspection: `TestTurnTracer_ScrubsPII`,
> `TestConfig_TurnTraceEnvGate`, `TestConfig_TurnTracePath`,
> `TestProcessBatch_TraceDistinguishesRejectedFromAccepted`, plus `TestTurnTracer_NilSafe`,
> `TestClassifyToolOutcome`, `TestProcessBatch_TraceNamesExhaustion` — 7 green, vet clean.
>
> **The negative control nearly shipped unverified, and the near-miss is worth recording.** The
> `-run` filter used for that run was `Trace|TurnTrace|ClassifyToolOutcome|TurnTracePath`, which
> **cannot match `TestProcessBatch_TracingOffNoFile`** — `Tracing` does not contain the substring
> `Trace`. The run reported a clean **7/7** and the eighth test simply never executed; it was caught
> only by comparing the reported count against the file's actual test inventory. The graph's `test`
> node then reported the full suite green but `cached`, which is not independent confirmation
> either. Settled by a `-count=1` run: `=== RUN TestProcessBatch_TracingOffNoFile` visible, passed in
> 0.01s, `ok 3.875s`, exit 0, 0 FAIL. **Lesson: never delegate a `-run` pattern — run the package;
> and a green count proves nothing until it is checked against the test inventory.**

- [x] Add a turn-trace event carrying turn index, tool name, arguments, and outcome — `TraceEntry`
      (`trace.go`), emitted from `processBatch` at every turn outcome
- [x] Route it through the existing scrub path — `ScrubPII` (`trace.go:149`), applied **before**
      truncation so a secret cannot be split past its redaction pattern; `TestTurnTracer_ScrubsPII` ✅
- [x] Gate it behind an opt-in env var, default off — `MUXCODE_HARNESS_TURN_TRACE`
      (`config.go:82`); `TestConfig_TurnTraceEnvGate` ✅
- [x] Write it to a file readable after the run — per-row `O_CREATE|O_APPEND|O_WRONLY`
      (`trace.go:137`), so the trace survives a killed run; `TestConfig_TurnTracePath` ✅
- [x] Unit test: a rejected tool call and an accepted one produce distinguishable trace entries —
      `TestProcessBatch_TraceDistinguishesRejectedFromAccepted` ✅
- [x] Negative control: with tracing off, no trace file is produced and the loop's behaviour is
      unchanged — `TestProcessBatch_TracingOffNoFile` ✅ **confirmed executing, not cached**: a
      `-count=1` run (exit 0, `ok 3.875s`, 0 FAIL) shows `=== RUN` for it and a 0.01s pass

### Phase 2: Attribute the failure

- [ ] Run `scripts/test-prompt-mode.sh` with tracing on
- [ ] Attribute all 10 turns of the failing `launch` intent to named causes
- [ ] Attribute all 10 turns of the failing named-`approve` intent
- [ ] Record the probe-vs-other split in this spec
- [ ] State plainly which of the four prior hypotheses the trace refutes

### Phase 3: Fix, chosen from evidence

- [ ] Choose a fix from the Phase 2 attribution
- [ ] Implement it
- [ ] Update the candidate-fix list with what the trace refuted
- [ ] Re-run the suite and record the new number

### Phase 4: Integration test

- [ ] Extend `scripts/test-prompt-mode.sh` with a trace-enabled assertion that all turns are attributed
- [ ] Test: a failing intent produces a trace naming every consumed turn
- [ ] Test: tracing off produces no trace file (negative control)
- [ ] Test: `launch` intent starts a run that appears in `graph list`
- [ ] Test: named-gate approval releases the gate; unnamed "approve whatever is waiting" does not — and the negative control reports a validated pass, not a skip
- [ ] Run the script and confirm the result is no longer 26/2/1

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-115-prompt-agent-turn-budget-exhaustion | 35m | 2026-08-28 14:38 |

## Status

In Progress
