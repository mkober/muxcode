# The Daemon Notifies a Windowless Role Forever, and Its Inbox Grows Unbounded

**Tracking:** filed 2026-09-11 by plan on the user's request relayed by edit (`1789155218`). Every
figure below verified in the primary records before filing.

A role can be **configured without being launched**. `analyze` is set to `codex`/`gpt-5.6-sol` and has
no tmux window in this session. The daemon routes to it anyway: it rewrites `trigger-analyze.notify`
each cycle, attempts an injection, fails at pane capture, logs a warning, and repeats — **164 times
across 28 hours**. Meanwhile its inbox has reached **109 messages** that nothing can ever consume.

## Context

### Observed (session `muxcode`, 2026-09-10 → 2026-09-11)

| Fact | Value | Source |
|------|-------|--------|
| `analyze` window | **absent** — session holds `auto plan edit build test serve review deploy run watch commit` | `tmux list-windows` |
| `analyze` configured | `codex`, `gpt-5.6-sol` | `muxcode config list` |
| Queued inbox | **109 messages** | `AGENT_ROLE=analyze muxcode inbox --peek` |
| Refusals | **164** rows, `injection-refused  analyze: pane capture failed: exit status 1` | `~/.config/muxcode/logs/muxcode.log` |
| Window | first **2026-09-10 08:27:31**, last **2026-09-11 13:11:17** (~28 h) | same |
| Cadence | median **51 s**, min 2 s | same |
| Trigger file | `trigger-analyze.notify` rewritten each cycle (mtime 13:11) | filesystem |

### Mechanism

`WindowForRole(role)` resolves a target name whether or not that window exists
(`bus/notify.go:432`, `:565`, `:1001`); the notify path then captures the pane, and capture is where
it fails. Nothing upstream asks **"does this role have a window?"** — the failure is detected at the
last possible moment, per attempt, forever.

`analyze` is a legitimate role: `bus/launcher.go:113–116` lists it with `research` as included "for
opt-in/mode-cycled configurations". So **configured-but-windowless is a supported state**, and the
notify path does not account for it.

Two consequences, and the second is the one that persists:

| | |
|---|---|
| **Noise** | a `warn` row every ~51 s, indefinitely — it drowns the signal that `injection-refused` is meant to carry (a *dead agent's shell* about to receive a payload, [MUX-164](../completed/MUX-164-codex-trust-prompt-reads-idle-wakeup-into-shell.md)) |
| **Unbounded inbox** | 109 messages accumulate with no consumer and no cap. Delivery receipts never arrive, so anything keyed on receipt for this role never resolves |

### A third effect worth noting

The workflow state machine transitions **into** `analyzing` for this role each cycle —
`workflow analyzing from=editing trigger=daemon:analyze-route` at 13:09:56 and 13:11:17, each
immediately followed by the refusal. The state machine advances to a state whose agent cannot receive
anything, then returns on the next `hook:analyze:edit`. Whether that is a separate defect or the same
missing precondition is for Phase 1.

### Scope boundary

In scope: notifying a role with no window, and an inbox that grows without bound when nothing can
consume it. Not in scope: whether `analyze` *should* be launched in this session (a configuration
choice), and the `injection-refused` guard itself — it is working correctly, refusing an injection it
cannot verify.

## Requirements

### Acceptance criteria

- [ ] A role with no window is **not notified**, and the attempt is not retried on a timer
- [ ] The condition is reported **once** per role, not once per cycle — a configured-but-unlaunched role is a setup fact, not a recurring incident
- [ ] An undeliverable inbox does not grow without bound; the cap and its behaviour on overflow are explicit
- [ ] **Negative control:** a role that *gains* a window later starts receiving normally, and its queued messages are delivered — the fix must not permanently blacklist a role
- [ ] **Negative control:** a role with a window whose pane capture fails for a *real* reason still logs `injection-refused` — the signal is preserved, not suppressed
- [ ] Whether the workflow state machine should route to a windowless role is answered and recorded

### Technical approach

Ask the question before the attempt, not after: a window-existence check upstream of
`trigger-<role>.notify` being written, so a windowless role is skipped cheaply and silently after one
report. The lookup already exists wherever the launcher decides what to create.

The inbox cap is the part that needs judgement. Dropping messages silently trades one invisible
failure for another; the useful shape is a bounded queue **plus** a visible signal — an alert once a
role's undeliverable backlog crosses a threshold, so the operator learns the role is misconfigured
rather than discovering 109 messages later.

**The load-bearing control is the second negative one.** A fix that suppresses `injection-refused` for
"roles that look absent" would also suppress the real case the guard exists for: a *dead agent's
window still present*, whose shell would execute an injected payload. Those two must stay
distinguishable — absent window vs. present window that fails capture.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/notify.go` | `:432`, `:565`, `:1001` — `WindowForRole` target resolution; where the window check belongs |
| `tools/muxcode/bus/launcher.go` | `:113–116` — roles valid for opt-in/mode-cycled configs, i.e. legitimately windowless |
| `tools/muxcode/daemon/daemon.go` | the notify cycle and `analyze-route` workflow transition |
| `tools/muxcode/bus/inbox.go` | where an undeliverable backlog would be capped |

## Implementation

### Phase 1: Establish the precondition

- [ ] Add a window-existence check upstream of the notify attempt; record where it belongs
- [ ] Answer whether `analyze-route` should transition at all for a windowless role
- [ ] Unit tests: windowless role → no notify, no trigger file, one report; windowed role → unchanged

### Phase 2: Bound the inbox

- [ ] Cap an undeliverable backlog and define overflow behaviour explicitly
- [ ] Alert once when a role's backlog crosses the threshold
- [ ] **Negative control:** a healthy role's inbox is unaffected by the cap in normal operation

### Phase 3: Preserve the guard's signal

- [ ] `injection-refused` still fires for a **present** window whose capture fails
- [ ] Test distinguishing absent-window from present-but-failing-capture

### Phase 4: Docs

- [ ] [`docs/architecture.md`](../../architecture.md) notification section and [`docs/agents.md`](../../agents.md): configured-but-windowless is a supported state and is skipped, not retried

### Phase 5: Integration test

- [ ] `scripts/test-windowless-notify.sh`: configure a role with no window, run the daemon, assert **zero** repeat refusals and no trigger file
- [ ] **Negative control:** create the window; assert delivery resumes and the backlog drains
- [ ] Coverage floor pinned to the exact pass count
- [ ] Run through the run agent (**foreground**, per [MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md)) and record counts here

## Notes

**It ran for 28 hours without being noticed**, because each individual row is a plausible transient —
the cost is only visible in aggregate (164 refusals, 109 messages). A once-per-role report would have
made the same condition obvious on day one, which is why the second acceptance criterion is about
*reporting cadence* rather than volume.

**Related:** [MUX-156](./MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) (the other way a
role's inbox stops being consumed — there a rogue consumer, here no consumer at all);
[MUX-164](../completed/MUX-164-codex-trust-prompt-reads-idle-wakeup-into-shell.md) (the guard whose
signal this noise buries).

## Status

Backlog
