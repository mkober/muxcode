# MUX-230: Integration Scripts Run With the Consumer Guard Turned Off

**Tracking:** _(no GitHub issue yet — filed as a follow-up, not a defect; say the word and plan files one)_

Twenty-eight integration scripts set `MUXCODE_CONSUMER_GUARD=0`, which turns off the no-consumer
refusal [MUX-145](../completed/MUX-145-messages-routed-to-windowless-role.md) added to `bus.Send`. They
do it because their fixtures play an agent by reading its inbox from the script — a reader that is
no window, no inbox listener and no headless agent, so the guard would refuse every send to it. The
cost is that those scripts exercise every send with the guard disabled: none of them would notice
the refusal misfiring on a real role. This spec gives each fake agent a real consumer and removes
the opt-out.

Filed 2026-11-16 on the user's request, relayed by edit (`1794854617-edit-48e5e641`), with the
decision that produced it: **the switch stays** as an emergency off switch, and the scripts keep
setting it **for now**.

## Context

### What MUX-145 shipped

| Piece | Where |
|-------|-------|
| The refusal | `refuseWithoutConsumer`, called from `sendMessage` (`tools/muxcode/bus/consumer.go`, `bus/inbox.go`) |
| What counts as a consumer | `RoleHasConsumer`: a window for the role, a live `muxcode inbox --poll`/`--wait` listener, or a running headless agent; an unreadable or empty window list counts; a spawn worker role always counts |
| The switch | `MUXCODE_CONSUMER_GUARD=0`, read from the sending process's environment on every send (`consumerGuardEnv`) |

### The scripts

Each carries one added line — `export MUXCODE_CONSUMER_GUARD=0`, with a comment naming MUX-145:

`test-agent-resume`, `test-auto-clear`, `test-auto-startup-gating`, `test-batch-reply-correlation`,
`test-cancel-provenance`, `test-close-spec-guard`, `test-codex-hooks`, `test-codex-trust-prompt`,
`test-control-pane`, `test-diagnose-dead-agent`, `test-durable-state-location`, `test-force-respond`,
`test-gate-authority`, `test-graph-foreign-dispatch`, `test-graph-node-caps`,
`test-graph-orchestrator`, `test-graph-worker-reuse`, `test-multi-phase-graph`,
`test-node-outcome-attribution`, `test-pane-targeting`, `test-prompt-mode`,
`test-reload-defers-dispatch`, `test-response-echo`, `test-root-go-vet`, `test-send-keys-dash`,
`test-spawn-no-cross-tree-delegation`, `test-unrequested-test-cycles`, `test-verify-spec-refire`.

`scripts/test-windowless-routing.sh` is not one of them: it runs with the guard on, and sets the
variable on a single line, deliberately, to plant stranded messages for its diagnose section.

The list is from `grep -l MUXCODE_CONSUMER_GUARD scripts/*.sh` on 2026-11-16, MUX-145 uncommitted.
Phase 1 re-takes it, since a script added in between may have copied the line.

### Why the opt-out is not harmless

- The guard sits on every send road. A change that made it refuse a role with a real consumer — a
  window list parsed wrongly, a listener marker read wrongly — would break delivery in a live
  session, and 28 of the scripts that send messages would stay green.
- The opt-out is an environment variable exported at the top of the script, so it also reaches the
  daemon and every `muxcode` call the script makes. A script cannot test anything about the refusal
  while it is set.
- The line is easy to copy into a new script, where it hides the same class of fault.

### Not the same for every script

A fake agent that only needs to *receive* can be given a consumer cheaply. Others may send to a role
that the test means to be absent, or read an inbox with `--raw` precisely so that nothing is
consumed. Which script is which is Phase 1's finding, not assumed here.

## Requirements

### Acceptance criteria

- [ ] No integration script sets `MUXCODE_CONSUMER_GUARD=0` for its whole run; any that still sets
      it does so on the single command that needs it, with a comment saying why
- [ ] Every role a script sends to has a real consumer for the duration of that send — a window, a
      live `muxcode inbox --poll` listener, or a running headless agent — or the send is one the test
      expects to be refused
- [ ] Each converted script passes at its existing coverage floor, with the guard on
- [ ] Negative control: with the guard on, a send from one converted script to a role it has given no
      consumer is refused — the script would now notice the refusal
- [ ] `MUXCODE_CONSUMER_GUARD=0` itself still works as the emergency off switch, and is still
      documented as one (`docs/configuration.md`)

### Technical approach

Not chosen here; Phase 1 chooses after reading the scripts. The options as they look today:

| Option | Shape | Cost |
|--------|-------|------|
| A shared helper | `scripts/lib/` gains a function that starts `muxcode inbox --poll --loop` for a role in the background and hands its output to the script — the fake agent becomes a real listener, the same consumer a Claude agent is | Scripts that read an inbox themselves must read the listener's output instead; a listener consumes, so a script that inspects the inbox after the fact changes shape |
| A window per fake role | The script's private tmux session gains a window named for the role | Cheapest where a private tmux server already exists; makes `RoleHasWindow` true without anything reading — which satisfies the guard and still exercises it, but is a consumer in name only |
| Per-command opt-out | Keep the variable, but only on the commands that need it | Smallest change; leaves those commands unguarded, which is the thing being removed |

The helper is the only option that gives the fake agent what the title asks for. A window is
honest for scripts that already drive a pane. The two can mix.

### Key files

| File | Purpose |
|------|---------|
| `scripts/test-*.sh` (the 28 above) | The opt-out lines |
| `scripts/lib/` | Home for a shared listener helper |
| `tools/muxcode/bus/consumer.go` | `RoleHasConsumer`, `consumerGuardEnv` — unchanged by this spec |
| `scripts/test-all.sh` | Runs them serially; the check that all still pass |
| `docs/configuration.md`, `CLAUDE.md` | The variable's description; the integration-test list |

## Implementation

### Phase 1: Survey

- [ ] Re-take the list of scripts that set the variable
- [ ] For each, record which roles it sends to, how the fake agent reads (the script reading the
      inbox, a pane, a stub agent), and whether a listener or a window would serve it
- [ ] Record any script that depends on a message **not** being consumed, or on a send to an absent
      role succeeding
- [ ] Choose the approach per script and record it here before changing any

### Phase 2: The helper

- [ ] Add the shared listener helper (or the window helper) the survey chose, with cleanup on exit —
      no listener left behind, the leak [MUX-156](../completed/MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) is about
- [ ] Convert two scripts of different shapes as the pattern, guard on, floors unchanged

### Phase 3: Convert the rest

- [ ] Convert the remaining scripts; remove each whole-run opt-out
- [ ] Any command that keeps the variable carries it inline with a comment
- [ ] Update each script's header where it describes how its agents are played

### Phase 4: Integration test

- [ ] Run `bash scripts/test-all.sh` with the guard on and record the pass/fail summary here
- [ ] Negative control: in one converted script, remove a fake agent's consumer and confirm the
      script fails on the refusal rather than passing
- [ ] A check — in `test-all.sh` or a script of its own — that fails when a `scripts/test-*.sh`
      exports `MUXCODE_CONSUMER_GUARD=0` for its whole run, so the line cannot be copied back in
- [ ] Update `CLAUDE.md`'s integration-test notes and `docs/configuration.md` if the variable's
      description of who sets it changes

## Notes

- **Decision recorded with this filing (user, 2026-11-16):** keep `MUXCODE_CONSUMER_GUARD=0` as an
  emergency off switch; the 28 scripts keep setting it for now; this spec is the follow-up that
  removes the need.
- The live-session scripts (`test-all.sh` skips them unless `MUXCODE_TEST_LIVE=1`) run inside a real
  session whose roles have real windows; whether any of them is among the 28 is part of the survey.
- Depends on MUX-145 shipping: the guard and the variable do not exist on `main` until it does.

## Status

**Backlog** — 0/18. Filed 2026-11-16 by plan on the user's request relayed by edit. Not a defect
and not in the Defects table, so `55-defect-backlog-loop` will not pick it up; it is worked when the
user asks for it.
