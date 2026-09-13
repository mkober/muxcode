# The Watch Chain Fires on Every Bash Call and Pastes the Raw Command Into the Message

**Tracking:** filed 2026-09-10 by plan on the user's request relayed by edit (`1789070947`), after the
watch agent reported and then root-caused it itself at 15:58. Verified in code by plan before filing.

The `watch` role's event chain has **no conditions on any branch**. Every classified bash call in the
watch pane sends edit a `notify`, and the message interpolates `${command}` verbatim — so a
multi-line heredoc, a `ps` pipeline or a scratch `echo` block arrives at edit as its own raw text
wrapped in "logs look healthy after deploy (…)". Four such messages reached edit inside three minutes
while watch was investigating an unrelated question.

The same class was already fixed for the **run** chain, which carries a `command_match` allowlist and
a `command_not_match: "muxcode *"` exclusion. Watch never got either.

## Context

### Observed (2026-09-10, session `muxcode`)

| When | Evidence | Source | Provenance |
|------|----------|--------|------------|
| 15:47 | `watch → edit [event:notify]` "Watch completed — logs look healthy after deploy (`BUS_DIR=…` / `echo` / `ls -la` / `cat serve-state.json`)" — a four-line shell block as the message body | bus history | **machine-written (chain)** |
| 15:48 | same shape, body is a `tail` / `ps aux \| grep` block | bus history | **machine-written (chain)** |
| 15:49 | same shape, body is a `ps -o pid,ppid,stat,lstart,command` block | bus history | **machine-written (chain)** |
| 15:58 | watch → run, retracting an unrelated claim and naming this as the cause: the chain "fires on every successful watch-role bash call and interpolates the raw multi-line `${command}` verbatim into the notify — confirmed 4 such spam messages fired to edit" | bus history | agent self-report |

None of the four described a log finding. All fired because a bash call in the watch pane exited 0.

### Mechanism — verified in code, not inferred

`tools/muxcode/bus/profile.go:976–995`, the `"watch"` chain:

| Branch | Message | Conditions |
|--------|---------|------------|
| `OnSuccess` | `Watch completed — logs look healthy after deploy (${command})` | **none** |
| `OnFailure` | `Watch detected errors (exit ${exit_code}): ${command} — …` | **none** |
| `OnUnknown` | `Watch completed (exit code unknown): ${command}` | **none** |

Contrast `runWatchActions` (`profile.go:555–570`), which builds the run chain's `OnSuccess`:

```go
Conditions: map[string]any{
    "command_match":     p,              // an allowlist of verification commands
    "command_not_match": "muxcode *",    // never fire on a bus call
},
```

A comment at `profile.go:949` refers to a prior **`run-chain-watch-overfire`** fix — the precedent
that this exact overfiring was already recognised and gated for `run`. The watch chain was not
revisited.

Two distinct defects, separable and worth separating:

| # | Defect | Consequence |
|---|--------|-------------|
| 1 | **No condition gate** — any classified bash call in the watch pane notifies edit | Volume: routine investigation becomes a message storm; watch's own `muxcode send` calls also qualify, so the chain can amplify itself |
| 2 | **Raw `${command}` interpolation** | Shape: a multi-line body violates the delegation-message-hygiene rule, and per `CLAUDE.md` such a payload misses the `Bash(muxcode *)` glob and **prompts instead of delivering** — so the noise is not merely cosmetic |

Defect 2 is shared textually by the run and serve chains, which also interpolate `${command}`; there
it is largely masked because their allowlists keep the matched commands short and predictable. Any
fix should decide whether to bound the interpolation everywhere or only gate watch.

### Scope boundary

In scope: the watch chain's missing conditions, and the shape of `${command}` in chain messages. Not
in scope: the PID-ownership mistake watch made in the same window (watch retracted it at 15:58 and it
had no chain involvement), and the `run` chain's own allowlist, which is working as designed.

## Requirements

### Acceptance criteria

- [ ] A routine bash call in the watch pane that produces no log finding sends edit **no** `notify`
- [ ] A genuine watch finding still reaches edit — pinned by a test, so the fix does not buy quiet by disabling the chain
- [ ] No chain message carries a multi-line body; `${command}` is bounded (single line, truncated with an explicit marker) or omitted
- [ ] A chain message that would exceed the hygiene limits is truncated rather than sent raw, and the truncation is visible in the message
- [ ] Watch's own `muxcode *` calls never fire the watch chain (the exclusion the run chain already has)
- [ ] `docs/hooks.md` chain table and `CLAUDE.md` record the watch chain's gate alongside the run chain's

### Technical approach

The narrow fix is to give the watch chain the treatment the run chain already has: a `command_match`
allowlist of log-inspection commands plus `command_not_match: "muxcode *"`. That reuses
`EvaluateConditions()` and needs no new mechanism.

The interpolation is a separate decision. Bounding `${command}` at the point of substitution fixes it
for every chain at once and is the smaller change; gating watch alone leaves the same trap set for
the next role that gets a chain without an allowlist. Prefer the bounded interpolation, with the
allowlist as well — they address different halves.

**The load-bearing test is the negative control:** a real watch finding must still notify edit. A fix
that simply stops the chain firing would satisfy every "no spam" assertion and silently remove watch's
only route to report.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/profile.go` | the `"watch"` chain (976–995); `runWatchActions` (555–570) as the model; the `run-chain-watch-overfire` comment at 949 |
| `tools/muxcode/bus/conditions.go` | `EvaluateConditions()`, `command_match` / `command_not_match` |
| `tools/muxcode/bus/hook.go` | where chain messages are built and `${command}` is substituted |
| `tools/muxcode/bus/inbox.go` | `validatePayload()` — the hygiene limits the raw body violates |
| `docs/hooks.md`, `CLAUDE.md` | chain tables and the constraint bullet |

## Implementation

### Phase 1: Gate the watch chain

- [ ] Add `command_match` (log-inspection allowlist) and `command_not_match: "muxcode *"` to the watch chain's branches
- [ ] Unit tests: a `tail`/`grep` log command fires; a `ps`/`echo`/`ls` scratch command does not; a `muxcode send` never does
- [ ] **Negative control:** a genuine log-inspection command still fires the notify

### Phase 2: Bound the interpolation

- [ ] `${command}` substitution collapses newlines and truncates at a fixed width with an explicit marker
- [ ] Applies to every chain that interpolates `${command}` (run, watch, serve), not watch alone
- [ ] Unit test: a multi-line heredoc command yields a single-line bounded message that passes `validatePayload()`
- [ ] **Negative control:** a short single-line command is passed through unchanged

### Phase 3: Docs

- [ ] `docs/hooks.md` chain table: the watch chain's gate, beside the run chain's
- [ ] `CLAUDE.md`: extend the run-chain allowlist note to say every notifying chain is gated and `${command}` is bounded

### Phase 4: Integration test

- [ ] `scripts/test-watch-chain-gate.sh`: run a scratch non-log bash call in a watch-role pane and assert **no** notify reaches edit
- [ ] **Negative control:** a log-inspection call in the same pane **does** notify edit
- [ ] Assert no message body contains a newline
- [ ] Coverage floor pinned to the exact pass count so a skipped section cannot read green
- [ ] Run through the run agent (**foreground**, per MUX-171) and record the counts here

## Notes

**Found by the agent it was firing from.** Watch reported the noise, initially misattributed it to an
orphaned listener it had killed, and then retracted and root-caused it correctly. The retraction is
the reason this is filed against the chain and not against a process. Recorded because the first
explanation was wrong and the record should show why the second is better: the four messages are in
the bus history with chain-written bodies, and the missing `Conditions` are in the source.

**Related:** [MUX-176](./MUX-176-run-chain-fires-success-on-backgrounded-call.md) — the other chain
defect in the same session, where the run chain fired success on an unfinished call. Both are chain
edges firing on evidence that does not support them.

## Status

Backlog
