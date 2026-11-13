# MUX-212: `test-stall-redrive-busy.sh` Fails — a Stale Spinner in Scrollback Pins the Pane Busy

**Tracking:** [mkober/muxcode#183](https://github.com/mkober/muxcode/issues/183)

`scripts/test-stall-redrive-busy.sh` ends `14 passed, 1 failed (floor 15)`. Its "stale spinner in
scrollback" check renders a pane whose history mentions a spinner but which is at rest, sends it a
row and runs `muxcode deliver review --force`; the row never reaches the pane. The script's own
comment states what that means if the product is at fault: the busy check is reading history rather
than the pane's live tail, and force recovery is refused for as long as the line stays in the
capture window. Whether the product or the fixture is wrong is not determined.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's instruction relayed by edit, from the MUX-208 graph worker's report
(`/tmp/mux208-implement-report.md`), while closing out
[MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md), whose "every
`scripts/test-*.sh` still passes" criterion excludes this script. Observed by plan in the run agent's
logs; not re-run by plan. Code read at `b8bfcab` plus the uncommitted MUX-208 tree.

### Observation — verified

| Fact | Where |
|------|-------|
| `FAIL: stale spinner history pinned the pane busy — force recovery refused forever`; every other check passes, including the busy-pane refusals and their controls | `/tmp/mux208-rerun-logs/test-stall-redrive-busy.log`, `/tmp/mux208-rerun2-test-stall-redrive-busy.log` |
| Failed in both runs on 2026-11-13 (15:38 and 15:53) — "reproduced twice" in the worker's report | same logs |
| The failing sequence: `render_stale_spinner review`, `sleep 1`, `muxcode send review stale-row "a row after a stale spinner" --no-notify`, `muxcode deliver review --force`, `sleep 1`, then grep the pane for the row's text | `scripts/test-stall-redrive-busy.sh:192-206` |
| The check runs directly after the "provider awareness" section, which ran `deliver review --force` under `MUXCODE_REVIEW_CLI=codex` | `scripts/test-stall-redrive-busy.sh:180-190` |

### Why this is not MUX-208's — verified

`bus/timetrack.go`, `bus/stuck.go` and `bus/deliver.go` are unmodified in the MUX-208 working tree
(`git status --short`), and the script's MUX-208 change is its bus-directory line. The worker's
report adds that `AgentIsWorking` (`timetrack.go`, a 12-line tail) is unchanged since 2026-10-10 —
not checked by plan. The script was not run against `main`'s own binary.

### Cause — not determined

Candidates, none verified:

- **Product:** `AgentIsWorking`'s tail window still contains the stale spinner line, so the pane
  reads as working and `ForceDeliver` skips it. The 12-line tail against the fixture's rendered
  height decides this.
- **Fixture:** `render_stale_spinner` no longer pushes the spinner line out of the live tail — the
  pane the script draws is not the pane its comment describes.
- **Order dependence:** the preceding section's forced delivery left the pane, a task or a marker in
  a state that withholds the next one; the check may pass in isolation.
- **Timing:** the one-second `sleep` after the deliver is shorter than the injection's own retries.

### Blast radius

- `bash scripts/test-all.sh` exits 1 on every run.
- If the product candidate holds, [MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md)'s
  busy guard over-refuses: an agent at rest with a spinner in recent scrollback cannot be recovered
  by `deliver --force`, the stall watchdog or receipt-gap recovery until the line scrolls away — and
  `--force` deliberately never overrides the busy check.

## Requirements

### Acceptance criteria

- [ ] The cause is recorded here, with the captured pane and what `AgentIsWorking` returned for it
- [ ] A pane at rest whose scrollback mentions a spinner is delivered to by `deliver --force`
- [ ] A pane with a live spinner in its tail is still refused (the MUX-171 guarantee, unchanged)
- [ ] The check passes when run in isolation and in sequence — or the order dependence is removed
- [ ] `bash scripts/test-stall-redrive-busy.sh` exits 0, floor met

### Technical approach

Phase 1 is a capture: the pane as `AgentIsWorking` reads it, the tail it classifies, and the
`deliver --force` output the script currently discards (`>/dev/null 2>&1`). That output alone
distinguishes a busy skip from an injection that was attempted and lost.

### Key files

| File | Purpose |
|------|---------|
| `scripts/test-stall-redrive-busy.sh:192-206` | The failing check and `render_stale_spinner` |
| `tools/muxcode/bus/timetrack.go` | `AgentIsWorking` and its tail window |
| `tools/muxcode/bus/deliver.go` | `ForceDeliver`'s busy skip |
| `tools/muxcode/bus/stuck.go` | `PaneShowsRecoverableIdle` |

## Implementation

### Phase 1: Establish the cause

- [ ] Capture the fixture pane, the tail `AgentIsWorking` classifies, and the `deliver --force`
  output; record them here
- [ ] Run the check in isolation (without the provider-awareness section before it); record the
  result
- [ ] Run the script against a binary built from `main`; record pass/fail
- [ ] Decide: product, fixture, order or timing; record it here

### Phase 2: Fix

- [ ] Product: bound the busy read to the live tail, with a Go test over a pane whose history holds
  a spinner and whose tail does not, and a sibling whose tail does
- [ ] Fixture, order or timing: correct the script, and stop discarding the `deliver` output so the
  next failure names its cause

### Phase 3: Integration test

- [ ] `bash scripts/test-stall-redrive-busy.sh` through the run agent: every check passes, floor
  met, exit 0; record the counts here
- [ ] Negative control: a live spinner in the tail is still refused
- [ ] `bash scripts/test-all.sh` no longer lists `test-stall-redrive-busy` as failed

## Related

- [MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md) — the
  close-out that excluded this script
- [MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md) — the busy guard
  this script pins
- [MUX-123](./MUX-123-stall-watchdog-selective-misses.md) — the stall watchdog's misses

## Status

Backlog

Filed 2026-11-13 on the user's instruction relayed by edit. The failure is observed in two runs; the
cause is **not determined**. Not started.
