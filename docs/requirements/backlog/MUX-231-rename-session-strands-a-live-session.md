# MUX-231: `tmux rename-session` Strands a Live Session — Its Monitor and Listeners Exit

**Tracking:** [mkober/muxcode#225](https://github.com/mkober/muxcode/issues/225)

Everything muxcode runs for a session finds that session **by name**. `tmux rename-session` changes
the name and nothing else, so the processes that poll for "is my session still there?" read a live
session as gone: the daemon monitor exits, and every agent's inbox listener exits on its next check.
The session keeps running under its new name with no daemon supervision and no message delivery, and
nothing says so.

## Context

### Source

Found 2026-11-16 by the worker on
[MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md) while testing that spec's
rename guard, and filed on the user's standing instruction for the defect-loop run to file and rank
defects found along the way, relayed by edit (`1794875662-edit-4a835cd3`). Pre-existing: MUX-184 did
not cause it and did not change it. MUX-184 fixed only its own share — the orphan reaper judges a
session by its recorded tmux **session id** as well as its name, so a renamed session's processes are
no longer reaped.

**Standard of evidence.** Every call site below was read by plan at `00d7ed8` plus MUX-184's
uncommitted tree. **No rename was performed**: that the monitor and listeners exit is read from the
code, not observed. Phase 1 observes it.

### Mechanism — read in code

| Process | Check | On a rename |
|---------|-------|-------------|
| Daemon monitor | `tmux has-session -t <name>` about every 30 s (`cmd/watch.go:116`); any failure logs `session-gone` and returns | Exits. The daemon is then unsupervised: a stale keepalive is never noticed and the daemon is never relaunched |
| Inbox listener (`muxcode inbox --poll --loop`) | `bus.TmuxHasSession(session)` every `sessionCheckTicks` (`cmd/inbox.go:171`); false → returns without consuming | Exits. A Claude agent's Stop hook relaunches it after the next turn, and the relaunched listener exits again at its first check — so the agent stops receiving anything pushed to it |
| `bus.Notify` | `tmux has-session -t <name>` before any wake-up (`bus/notify.go:587`, `:1037`) | Skips the wake-up for every role |
| `muxcode deliver`, `bus.ForceDeliver` | `TmuxHasSession` (`bus/deliver.go:51`) | Refuses |
| `muxcode upgrade-daemons` | `sessionExists = TmuxHasSession` (`bus/upgrade.go:289`) | Reads the renamed session's daemon as an **orphan** and kills it without relaunch (`daemon-orphan-killed`) |
| The daemon's own session-gone check (MUX-184) | Recorded session id, from `list-sessions` | Not affected — the id survives a rename. A `tmux.socket` written before MUX-184 has no id and is judged on the name alone until that daemon restarts |

Behind the checks, the name is the session's identity everywhere: `BUS_SESSION` in every pane's
environment, the bus directory `muxcode-bus-<name>`, every pane target `<name>:<role>`, the lifecycle
log `<name>.log`. A rename leaves all of those pointing at a name tmux no longer has.

### What the user sees

Nothing at the moment of the rename. Afterwards: messages queue in inboxes and no agent is woken;
`muxcode status` and `diagnose` still resolve the bus directory by the old name the panes carry; the
next `./build.sh` kills the session's daemon as an orphan. The lifecycle log's one clue is a
`session-gone` row from the monitor, for a session that is plainly still there.

### A second thing seen in the same lines — not established

`has-session -t <name>` at `cmd/watch.go:116`, `bus/notify.go:587` and `:1037`, and inside
`bus.TmuxHasSession` (`bus/tmux.go:60`), passes the bare name. `cmd/clear.go:28` and
`cmd/resume.go:47` pass `=<name>`, tmux's exact-match form. Without the `=`, tmux resolves a target by
prefix as well, so a check for a closed session `mux` may be satisfied by a live `muxcode`. That is
the opposite failure — a dead session read as live — and is not this defect, but it sits in the same
call sites and the same fix would touch it. Not tested; recorded so Phase 1 settles it either way.

## Decisions owed

Not decided, and the spec is parked on it: **what should a rename do?**

| Option | Shape | Cost |
|--------|-------|------|
| A — survive it | Processes ask "is my session there?" by the recorded session id, as MUX-184's reaper now does | Stops the exits and the orphan kill. Does **not** make the session work: pane targets, `Notify` and delivery still address the old name. Half a fix unless the rest follows |
| B — follow it | A tmux `session-renamed` hook moves the session's identity: bus directory, `tmux.socket`, the daemon's and monitor's argv, `BUS_SESSION` in live panes | The only option under which a renamed session keeps working. `BUS_SESSION` in already-running processes cannot be rewritten, so every agent would need a relaunch — large |
| C — refuse it | The same hook renames the session straight back and tells the user why | Small and total. Takes away something tmux lets the user do |
| D — detect and say so | No behaviour change; a `session-renamed` lifecycle row and an alert to edit naming the remedy | Smallest. The session is still broken until the user renames it back |

Options are not exclusive — A plus D is a coherent minimum. The choice is the user's.

## Requirements

### Acceptance criteria

- [ ] A rename of a live session is never silent: a lifecycle row names the old and new names
- [ ] The behaviour chosen under Decisions owed is implemented, and the monitor does not exit with `session-gone` for a session that is still running
- [ ] An agent in a renamed session still receives a message sent after the rename, or the sender is told why not — per the decision
- [ ] `muxcode upgrade-daemons` does not kill a renamed session's daemon as an orphan
- [ ] Negative control: a session that is really closed is still detected as gone by the monitor and the listener, within the same interval as today
- [ ] The bare-name `has-session` question is settled: either the checks use the exact-match form, with a test that a closed `mux` beside a live `muxcode` reads as gone, or Phase 1 records why it cannot occur
- [ ] `docs/agent-bus.md` and `docs/architecture.md` say what a rename does

### Technical approach

Open until the decision. Whatever is chosen, one helper should answer "is this session alive?" for
every caller in the mechanism table — today there are four spellings of the question — and MUX-184
already has the pieces: the recorded session id in `<bus dir>/tmux.socket` and
`bus.SessionPositivelyGone`. The monitor's check is also not a positive-absence check: any failure of
`has-session`, a tmux blip included, ends it (noted in MUX-184's activation findings). That is a
neighbouring defect in the same line and should be fixed with the same helper or filed apart.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/cmd/watch.go` | The monitor's `has-session` check (`:116`) |
| `tools/muxcode/cmd/inbox.go` | The listener's `TmuxHasSession` check (`:171`) |
| `tools/muxcode/bus/tmux.go` | `TmuxHasSession` (`:59`) — the bare-name form |
| `tools/muxcode/bus/notify.go` | Two `has-session` guards (`:587`, `:1037`) |
| `tools/muxcode/bus/upgrade.go` | `sessionExists` in the orphan decision (`:289`) |
| `tools/muxcode/bus/tmux_socket.go`, `bus/reaper.go` | The recorded session id and `SessionPositivelyGone` (MUX-184) |

## Implementation

### Phase 1: Reproduce and pin

- [ ] On a scratch session on a private tmux server, rename it and record what exits and when: monitor, each listener, and what `Notify`, `deliver` and `upgrade-daemons --dry-run` then do
- [ ] Pin each observed behaviour with a test whose failure message names the phase that inverts it
- [ ] Settle the bare-name question: a closed session whose name is a prefix of a live one — does `has-session -t <name>` succeed?
- [ ] Record the findings here, and take the decision to the user with them

### Phase 2: The chosen behaviour

- [ ] Implement the decision; route every "is this session alive?" caller through one helper
- [ ] The rename is logged, old name and new
- [ ] Negative control: a really-closed session is still detected by the monitor and the listener

### Phase 3: Docs

- [ ] `docs/agent-bus.md` and `docs/architecture.md`: what a rename does, and the one liveness helper
- [ ] `CLAUDE.md`: one clause where session identity is described

### Phase 4: Integration test

- [ ] Create `scripts/test-rename-session.sh` — hermetic: a private tmux server, scratch state, stub agents
- [ ] Rename a live session → per the decision: the monitor is still running and a message sent afterwards is received, or the rename is refused or reported
- [ ] `upgrade-daemons --dry-run` after a rename does not plan an orphan kill
- [ ] Negative control: `kill-session` → the monitor and the listener both exit within their usual interval
- [ ] Coverage floor so a skipped section cannot read as green
- [ ] Run the script and record the counts here

## Notes

- **Parked at filing, 2026-11-16, by plan**, under the user's rule that a spec holding an unrecorded design decision is not worked unattended. Phase 1 needs no decision, but the loop works a spec through to close.
- Severity Medium: renaming a muxcode session is rare, and nothing is lost on disk — renaming it back restores the session, apart from a daemon `upgrade-daemons` has since killed.
- Related: [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md) (the reaper's rename guard; the monitor's non-positive liveness check); [MUX-156](../completed/MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) (the listener's other exit conditions); [MUX-008](./MUX-008-unverified-daemon-auto-restart.md) (a daemon left without a monitor).

## Status

**Backlog** — 0/22. Filed 2026-11-16. Parked at filing on the decision above.
