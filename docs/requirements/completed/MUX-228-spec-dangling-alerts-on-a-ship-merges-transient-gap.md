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

- [x] A pointer whose file is absent for less than the debounce and then present again raises no `spec-dangling` row and no event to edit
- [x] A pointer whose file stays absent past the debounce raises exactly one row and one event (negative control — the alert cannot go inert)
- [x] The debounce restarts if the file reappears and vanishes again, and when the pointer changes
- [x] `spec-repoint` and `spec-unset-live-run` behave exactly as today
- [x] `docs/architecture.md` or `docs/agent-bus.md`, wherever `spec-dangling` is described, states the debounce

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

- [x] `checkActiveSpec` alerts on `dangling` only after the pointer has stayed dangling for the debounce; a present file or a changed pointer clears the record
- [x] The debounce is configurable, with the default documented in `docs/configuration.md`
- [x] Unit tests: absent then present inside the window → no row, no event; absent past the window → one row, one event (negative control); absent, present, absent → the window restarts

### Results for Phase 1

Recorded 2026-11-17 from the worker's report; plan read the diff and the tests. Boxes are ticked at
`verify-spec`, not here.

**The change**, in `daemon/daemon.go` only — `bus/spec_pointer.go` is untouched, as planned:

- The daemon keeps the path last seen dangling and when that run of sightings began
  (`specDanglingPath`, `specDanglingSince`).
- `checkActiveSpec` clears that record on any outcome other than dangling, and alerts only when
  `specDanglingSettled(path)` holds — the same path dangling for `specDanglingDebounce()` — and the
  existing `shouldSendEvent` cooldown allows it.
- `specDanglingDebounce` reads `MUXCODE_SPEC_DANGLING_DEBOUNCE_SECS`: a non-negative integer is
  used (0 alerts on first sighting, as before); anything else is the 30 s default.

**The tests**, in `daemon/provenance_gate_test.go`, each reading both edit's inbox and the
lifecycle log:

| Test | Shows |
|------|-------|
| `TestCheckActiveSpec_DanglingAlertedOnceAfterDebounce` | The negative control: nothing on the first sighting; past the debounce, exactly one row and one event across two further checks; the pointer is not cleared |
| `TestCheckActiveSpec_TransientGapNotAlerted` | Absent, then present, then absent again: nothing — the second absence starts a new count even though the first sighting was older than the debounce |
| `TestCheckActiveSpec_DanglingDebounceRestartsOnPointerChange` | A pointer moved to another missing path is a new sighting: nothing |

The tests age a sighting by moving its recorded start back, not by sleeping.

**An existing script needed the variable.** `scripts/test-verify-spec-refire.sh` waits 15 s for a
`spec-dangling` row, which the 30 s default now outlasts; it sets the debounce to 2 s. On a tree
build through the run agent (task `1794957962-spawn-56d56314-f0761651`, reply at 18:26:50): 43
passed, 0 failed, its dangling checks included — "alert fired once, not per poll".

**Docs, written by plan from the code:** `docs/configuration.md` gains
`MUXCODE_SPEC_DANGLING_DEBOUNCE_SECS`; `docs/architecture.md`'s pointer table states the debounce,
what restarts it, and the two roads that used to raise a false alert.

Verified and ticked 2026-11-17, once all three had run after the last edit (18:25:49):
`./build.sh` exit 0 at 18:27:58, `./test.sh` exit 0 at 18:29:50, review at 18:31:23 with 0
must-fix, 0 should-fix, 0 nits. The three steps are ticked, and criteria 3, 4 and 5: the restart
cases on the two unit tests; `spec-repoint` and `spec-unset-live-run` on the diff — their branches
are unchanged — and their existing tests passing in `./test.sh`; the docs on plan's own edit.
**Criteria 1 and 2 are left for Phase 2**, though the unit tests cover both: they describe what a
running daemon does with a real file, which is what the integration script is for.

### Phase 2: Integration test

- [x] Create `scripts/test-spec-dangling-debounce.sh`: a scratch repository, session and daemon with a short debounce; point the active spec at a file, remove it for less than the debounce and restore it → no `spec-dangling` row and nothing in edit's inbox
- [x] Negative control: remove the file for good → exactly one row and one event after the debounce
- [x] A coverage floor keeps a skipped section from reporting green; run the script and record pass/fail counts in this spec
- [x] Update the docs named above and the script index in `CLAUDE.md`

### Results for Phase 2

Recorded 2026-11-17 from the worker's report; plan read the script's assertions and the run agent's
reply. Boxes are ticked at `verify-spec`, not here.

**The script.** `scripts/test-spec-dangling-debounce.sh`: a tree-built muxcode, a scratch
repository and session, and a real scratch daemon polling every second — on a private tmux server
(`TMUX_TMPDIR` under its work directory, `kill-server` on exit), the pattern
[MUX-226](../completed/MUX-226-graph-node-caps-script-not-isolated-from-real-tmux-and-binary.md)
set. Floor 14. Each case reads both the lifecycle log and edit's inbox.

| Section | Case | Result |
|---------|------|--------|
| 0 — control | Debounce 0, the spec file removed for 3 s and restored | **Reported** — the gap is seen and alerts, as before MUX-228. Without this, the "no row" cases below could pass on a daemon that never looked |
| 1 | Debounce 6 s, a 3 s gap | No row, no event |
| 2 | Debounce 6 s, two 4 s gaps one second apart — 8 s absent in all | No row, no event: the wait restarted when the file came back |
| 3 — negative control | Debounce 6 s, the file removed for good | Nothing 3 s in; then exactly one row and one event; the pointer is kept, not cleared; the row names the missing spec |

**The run, through the run agent** (task `1794958378-spawn-56d56314-3d6b5f46`, reply at 18:33:54):
exit 0 — 14 passed, 0 failed, floor 14.

The worker added the script to `CLAUDE.md`'s index. The docs the last step names were written in
Phase 1.

#### Review fix (2026-11-17, 18:35–18:38) — the new script could have killed the caller's tmux server

The review of the above returned **1 must-fix** and 1 should-fix, verdict `EXIT=1`.

**Must-fix.** The script armed its exit trap before it set up the private tmux server, and the
cleanup ran a bare `tmux kill-server`. An early exit — a failed build, say — would have run that
against whatever server the caller was on: the user's own, with every agent session in it. It did
not happen: the one run before the fix built cleanly and exited after isolation was in place.

| Change | What it is |
|--------|------------|
| Order | `unset TMUX`, `TMUX_TMPDIR` and the directory are set before any trap is armed |
| Cleanup | Kills only the private server, **by socket path** (`tmux -S "$PRIVATE_SOCK" kill-server`), and only if that socket exists — never a bare `kill-server` |
| Should-fix | A relative `MUXCODE_SPEC_DANGLING_BIN` is made absolute; the script runs muxcode from inside its scratch repository too |

**The run, through the run agent** (task `1794958574-spawn-56d56314-227b84c6`, reply at 18:37:58):
plain — 14 passed, 0 failed, exit 0; with a relative binary override — 14 passed, 0 failed, exit 0;
a forced early failure — exit 1, the caller's tmux server untouched, seven sessions before and
after.

**Whether any other script has the same ordering — checked by plan, and none does.** For each of
the 27 `scripts/test-*.sh` that call `kill-server`, plan compared the line of the first `trap` with
the line that sets `TMUX_TMPDIR`. In all 25 that isolate that way the isolation comes first,
`test-graph-node-caps.sh` (MUX-226, already merged) included; the other two
(`test-escape-absorber`, `test-send-keys-dash`) name their server with `-L` or `-S` on each call.
A line-number comparison, not a reading of each script. One thing it leaves: most of the 25 still
clean up with a bare `kill-server` that depends on `TMUX_TMPDIR` — safe while that directory
exists, and MUX-226's harness showed a `TMUX_TMPDIR` naming a removed directory falls back to the
real socket. The by-path form used here does not have that dependence;
[MUX-233](../completed/MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md)'s pattern
now asks for it.

#### Verification (2026-11-17, 18:41)

Ticked on the tree after the review fix; the report in the dispatch predates it. The script was
last edited at 18:35:59. After it: the three runs at 18:37:58 (14 of 14 twice, and the early
failure), `./build.sh` exit 0 at 18:39:16, `./test.sh` exit 0 at 18:39:36, review at 18:40:03 with
0 must-fix, 0 should-fix, 0 nits. The four steps are ticked, and criteria 1 and 2 — held over from
Phase 1 — on the script's sections 1–2 and 3, against a running daemon.

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-spec-dangling-merge.md` (edit), on the user's standing instruction to file defects found along the way.
- Not parked: the one design choice (debounce over git-operation detection) is recorded above as plan's, with its reason.

### Findings at activation (2026-11-17)

Activated against the tree at `b0d3b15`. Plan read the code and the session's lifecycle log;
nothing was run.

| Spec says | Tree and log today |
|-----------|--------------------|
| `checkActiveSpec` at `daemon/daemon.go:636–658` | At `:709–731`, the same logic: `SpecPointerDangling` logs and alerts on first sighting, gated only by `shouldSendEvent` |
| `ReconcileActiveSpec` at `bus/spec_pointer.go:42` | Still there |
| `spec-dangling` is described in `docs/architecture.md` or `docs/agent-bus.md` | In `docs/architecture.md:173` only |
| "Every defect the backlog loop ships can raise one false alert" | **It can; in the log that survives it has not.** The log holds 1,081 rows from 2026-11-16 and 3,924 from 2026-11-17 — two days in which this loop shipped more than a dozen defects — and exactly **one** `spec-dangling` row. The 2026-11-14 row this spec was filed on has since rotated out (11 rows remain from that day) |
| That one surviving row | 2026-11-17 16:40:34, naming the `drafts/` path of MUX-210. **Not a ship.** It is plan's own parking of MUX-210: the spec was moved back to `backlog/` — which has no `completed/` counterpart for the pointer to follow — a moment before the pointer was changed |

So the defect is a race that is rarely lost, not one alert per ship: the gap is about three seconds
and the check has to land inside it. The fix is unchanged and still worth its size — the alert is
wrong when it does fire, and the debounce also covers the parking case above, which is a second
road to the same false alert that this spec did not know about. Priority stays Low.

**No user decision is waiting.** The one design choice — a debounce rather than detecting a git
operation — is plan's, recorded above with its reason, and stays open to the user to overrule.

## Status

**Complete** — closed 2026-11-17, uncommitted at close-out (the run's ship step commits it). The
daemon now reports a dangling active-spec pointer only once the same path has stayed dangling for
`MUXCODE_SPEC_DANGLING_DEBOUNCE_SECS` (default 30 s); a present file or a changed pointer restarts
the count, and a file that is really gone still raises exactly one row and one event. That covers
the few-second gap during a ship merge and a spec moved back to `backlog/` before its pointer
changes. Three unit tests and `scripts/test-spec-dangling-debounce.sh` (14 of 14, against a running
daemon) pin it. The new script's first version could have killed the caller's tmux server on an
early exit; a review caught it before it happened. Activated 2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 34; every better-ranked row is parked). 12/12 — Phases 1–2 and all five criteria ticked 2026-11-17, after one review must-fix in Phase 2; awaiting close-out. Filed 2026-11-14.
