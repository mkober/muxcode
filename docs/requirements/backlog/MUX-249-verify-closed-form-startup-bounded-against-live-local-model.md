# Verify the Closed-Form Startup Is Bounded Against a Live Local Model

A task, not a defect; it has no GitHub issue. Filed 2026-11-28 on edit's call under the user's
standing autonomy, out of
[MUX-110](../completed/MUX-110-harness-startup-tool-loop-exhaustion.md) Phase 2.

MUX-110 gave local-model roles a closed-form startup message — one command, one deliverable, a
stated end. What it could prove without a model is that the launch seeds that text. What it could
not prove is the point of the change: that a small model given the text **stops** — a bounded
number of tool calls, an answer inside its turn budget. This spec carries that check to a machine
where a local model runs.

## Context

### Why a stub cannot show it

MUX-110 Phase 2 first pinned boundedness with a unit test against a scripted model. Review found
the test passed with the startup feature reverted: the stub ignored the message and always made
one tool call then answered, so it measured the stub. The test was removed. A stub that stops says
nothing about whether a message makes a model stop; only a model run does.

### What exists already

| Piece | Where | State |
|-------|-------|-------|
| The closed-form text | `bus/launch_reason.go` `localStartupRestorePayload`, seeded by `PreLaunchSetup` for a `LocalProvider` role | Shipped with MUX-110, pinned by `TestPreLaunchSetup_LocalRoleGetsClosedFormStartup` |
| The exhaustion event | `harness-tool-loop-exhausted` lifecycle row | Shipped with MUX-110 — the signal a model run reads for "did not stop" |
| The model-driven script section | `scripts/test-harness-startup.sh`, MUX-110 Phase 4 | Written by MUX-110; skipped with a reason where no Ollama endpoint answers |

No Ollama endpoint answered on the machine MUX-110 was worked on (`localhost:11434` refused,
2026-11-28), so that section has never run.

### Open from MUX-110, answerable only here

- Why `isSingleShotRole()` did not arm in the 2026-08-26 runs (no successful tool execution, or
  another cause). MUX-110 recorded it as not observed.
- What made the 2026-08-26 failure repeat every five minutes. MUX-110's re-check found no re-drive
  mechanism in today's code; the open-ended text on a live model is the one way to see whether it
  still repeats.

## Requirements

### Acceptance criteria

- [ ] On `qwen3:4b`, a harness role answers the closed-form startup message within its turn budget and the reply reaches the sender
- [ ] The tool-call count for that startup is bounded and recorded — the number, not only "passed"
- [ ] No `harness-tool-loop-exhausted` row is written for the closed-form startup
- [ ] **Negative control:** the same role given the open-ended `startupRestorePayload` text is run on the same model, and what it does is recorded — exhausts, or answers. If it also answers, the wording is not shown to be what bounds the model, and this spec says so
- [ ] The two questions left open by MUX-110 are answered from the runs, or recorded as still not observed with what was tried
- [ ] The run is repeated enough times to tell a result from luck — at least five per wording, each count recorded

### Technical approach

- Run MUX-110's `scripts/test-harness-startup.sh` with its model-driven section live. If that
  section does not record the tool-call count or run the open-ended control, extend it — the
  hermetic section and its coverage floor stay as they are.
- Read boundedness from the harness trace and the lifecycle log, not from the reply's text.
- If the closed-form wording does not bound the model, that is a finding, not a failure of this
  spec: record it and file the wording change as its own defect.

### Key files

| File | Change |
|------|--------|
| `scripts/test-harness-startup.sh` | Model-driven section: record the tool-call count, add the open-ended control, repeat runs |
| `docs/agents.md` | The measured result beside the harness startup contract |

## Implementation

### Phase 1: Run it

- [ ] Confirm an Ollama endpoint serves `qwen3:4b`; record the endpoint and model versions
- [ ] Run the model-driven section five times with the closed-form text; record per run: answered or not, tool calls, turns used, any exhaustion row
- [ ] Run it five times with the open-ended text as the control; record the same
- [ ] Record whether single-shot armed in each run and, where it did not, why

### Phase 2: Docs

- [ ] `docs/agents.md`: the measured result, and the wording's status — validated, or not shown

### Phase 3: Integration test

- [ ] `scripts/test-harness-startup.sh` records the tool-call count in its model-driven section and runs the open-ended control
- [ ] Run the script with the model section live and record the counts here, hermetic and model-driven separately
- [ ] Run it once with the endpoint unreachable: the model section is reported skipped with a reason and the hermetic floor still holds

## Status

**Backlog** — filed 2026-11-28. Not started. Needs a machine with Ollama and `qwen3:4b`; not
workable unattended where none answers.
