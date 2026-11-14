# MUX-228: `spec-dangling` Alerts on the Transient Gap During a Ship Merge

**Tracking:** [mkober/muxcode#216](https://github.com/mkober/muxcode/issues/216)

The daemon tells edit the active spec pointer names a missing file the first time it sees the file
absent. During a ship — branch, commit, merge, checkout main, pull — a closed-out spec is absent from
the working tree for a few seconds, between the checkout of main and the pull that brings the merge
in. Every defect the backlog loop ships can raise one false alert.

## Context

### Observed (2026-11-14, session `muxcode`, run `1794626267-55-defect-backlog-loop-15547e2e`)

Reported by edit on the user's standing instruction to file defects found along the way. Plan re-read
the lifecycle row and the code; the file's presence after the merge is edit's observation.

| Time | Event |
|------|-------|
| 16:51:29 | Plan's close-out moves MUX-224 to `docs/requirements/completed/` (uncommitted) and repoints the active spec there |
| ~17:01 | The ship commits it on its branch; the commit agent merges PR #215 |
| 17:01:16 | Lifecycle row `spec-dangling` (warn, daemon) naming `docs/requirements/completed/MUX-224-tests-use-the-real-global-memory-dir.md`, and an event to edit: "Active spec pointer names a missing file" |
| 17:01:19 | Commit reports the merge done and main pulled. Edit checks at once: the file exists, rewritten by the checkout at 17:01 |

On main before the pull the file is not yet present. The alert described a state that lasted about
three seconds.

### Mechanism — verified in code

- `daemon/daemon.go:636–658` `checkActiveSpec` — calls `bus.ReconcileActiveSpec`; on
  `SpecPointerDangling` it writes the warn row and sends edit the event, gated only by
  `shouldSendEvent("spec-dangling", path)`.
- `shouldSendEvent` is a cooldown — it stops a repeat — not a debounce. The first sighting alerts.
- `bus/spec_pointer.go:42–64` `ReconcileActiveSpec` — returns `dangling` when the file is gone and
  no `completed/` counterpart exists. It reads the working tree at one instant.

### Consequence

Cosmetic. The pointer is not cleared and no wrong action was taken. The cost is one false warning
to edit per shipped defect, and an alert that is wrong whenever a ship is under way trains its reader
to ignore it.

## Requirements

### Acceptance criteria

- [ ] A pointer whose file is absent for less than the debounce and then present again raises no `spec-dangling` row and no event to edit
- [ ] A pointer whose file stays absent past the debounce raises exactly one row and one event (negative control — the alert cannot go inert)
- [ ] The debounce restarts if the file reappears and vanishes again, and when the pointer changes
- [ ] `spec-repoint` and `spec-unset-live-run` behave exactly as today
- [ ] `docs/architecture.md` or `docs/agent-bus.md`, wherever `spec-dangling` is described, states the debounce

### Technical approach

Debounce in the daemon: record when the pointer was first seen dangling, and alert only once it has
stayed dangling for a set time across consecutive checks. The default is 30 s — ten times the
observed gap, short against a file that is really gone — and is overridable by an environment
variable, as the other watchdog timings are. Any sighting of the file present, or a different
pointer, clears the record.

Chosen by plan at filing over the alternative edit offered — suppressing while a git operation is in
progress (`.git/index.lock`, or a commit node running). The debounce needs no knowledge of what
caused the gap, so it also covers a manual checkout or rebase, and a lock file is not held across the
whole checkout-then-pull window. The user may overrule this.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/daemon/daemon.go` | `checkActiveSpec`, `shouldSendEvent` |
| `tools/muxcode/bus/spec_pointer.go` | `ReconcileActiveSpec` — unchanged; it reports what it sees |
| `docs/architecture.md`, `docs/agent-bus.md` | Where the pointer reconciliation is described |

## Implementation

### Phase 1: Debounce the alert

- [ ] `checkActiveSpec` alerts on `dangling` only after the pointer has stayed dangling for the debounce; a present file or a changed pointer clears the record
- [ ] The debounce is configurable, with the default documented in `docs/configuration.md`
- [ ] Unit tests: absent then present inside the window → no row, no event; absent past the window → one row, one event (negative control); absent, present, absent → the window restarts

### Phase 2: Integration test

- [ ] Create `scripts/test-spec-dangling-debounce.sh`: a scratch repository, session and daemon with a short debounce; point the active spec at a file, remove it for less than the debounce and restore it → no `spec-dangling` row and nothing in edit's inbox
- [ ] Negative control: remove the file for good → exactly one row and one event after the debounce
- [ ] A coverage floor keeps a skipped section from reporting green; run the script and record pass/fail counts in this spec
- [ ] Update the docs named above and the script index in `CLAUDE.md`

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-spec-dangling-merge.md` (edit), on the user's standing instruction to file defects found along the way.
- Not parked: the one design choice (debounce over git-operation detection) is recorded above as plan's, with its reason.

## Status

**Backlog** — 0/12. Filed 2026-11-14.
