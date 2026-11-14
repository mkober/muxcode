# `upgrade-daemons` Cannot See the Daemons From a Codex Build Agent

**Tracking:** [mkober/muxcode#124](https://github.com/mkober/muxcode/issues/124)

`./build.sh` ends with `muxcode upgrade-daemons`, the rollout that makes a long-lived daemon re-exec
the binary just installed. Daemon discovery is `ps -axo pid=,command=`, and the Codex build sandbox
refuses to exec `/bin/ps`. So when the build role runs on codex — the configuration this project has
used since 2026-09-08 — every build prints `ps: fork/exec /bin/ps: operation not permitted`, the
rollout is skipped, `build.sh` exits 0 by design, and the live daemon keeps the code it was launched
with. Nothing in the session says so except a stderr line in the build agent's pane.

## Context

### Observed (2026-09-09, session `muxcode`)

| Measurement | Value |
|-------------|-------|
| Build pane | after `Go binary: Built 2 modules → bin/ (v0.1.0-61-g3d3fd9b-dirty)` and the install, `upgrade-daemons: ps: fork/exec /bin/ps: operation not permitted` — on the 00:28, 00:45 and 00:54 builds, every one reported to edit as `./build.sh completed successfully (exit 0)`; a fourth at 01:15:57 inside graph run `1788930816-spec-to-pr-f7fb2610`, after the 01:12 session relaunch, the same line in the build-history row |
| Live daemon | `muxcode upgrade-daemons --dry-run` from an unsandboxed pane at 00:56: `daemon v0.1.0-61-g3d3fd9b-dirty (built 2026-09-09T04:18:06Z) → installed v0.1.0-61-g3d3fd9b-dirty (built 2026-09-09T04:54:40Z) — would restart` — the daemon was still the session-launch build after three installs |
| What that stale daemon was running | none of the branch's daemon-side changes (`daemon.go` seams for `checkNonHookTasks`/`checkStuckProviders`); the hook subprocesses (`muxcode hook guard|bash|stop`) were current, because a hook runs whatever `muxcode` is on `PATH` at call time |
| What the daemon stamps | `BusDir()/daemon.pid` (`bus/config.go:443`), `daemon.version` (`bus/daemon_health.go:45` — `{"version","commit","date","go","os","arch"}`) and `daemon.keepalive` (touched every poll) — everything discovery needs except the monitor's pid |

### Mechanism — verified in code

1. **Discovery is `ps`.** `ListDaemonProcs` (`bus/upgrade.go:68`) is `exec.Command("ps", "-axo",
   "pid=,command=")`; `parseDaemonProcs` (`:80`) keeps rows whose binary basename is `muxcode` and
   whose first argument is `watch`, reading the session and the `--monitor` flag from the command
   line. Its sole caller is `UpgradeDaemons` (`:183`). No error path tries anything else.
2. **The build sandbox refuses the exec.** `BuildExecArgs` gives the build role `-s workspace-write`
   plus one `--add-dir` per external root (`codexWritableRoots`) — write roots, not executables. The
   seatbelt profile denies `fork/exec /bin/ps`; the Go error surfaces verbatim as `ps: fork/exec
   /bin/ps: operation not permitted`.
3. **`build.sh` treats the rollout as best-effort.** `build.sh:20` is `muxcode upgrade-daemons ||
   echo "Warning: daemon upgrade failed — running daemons remain on the old binary" >&2` — a
   deliberate choice (a build must not fail over a daemon it cannot reach), which on this road turns
   into a rollout that *never* happens and a build that always reads green.
4. **Nothing downstream notices.** `muxcode diagnose` reports a daemon/installed version mismatch as
   a warning fixed by `muxcode upgrade-daemons`, but only when someone runs it; no lifecycle row is
   written when the rollout could not run, and `muxcode session status` does not compare builds.

The instrument problem is the point: a daemon-side fix built and installed from a codex build agent
is verified — by the suite, by review, by `verify-spec` — against a daemon that does not contain it.

### Scope boundary

**In:** discovery without `ps`; a visible failure when the rollout cannot run; the monitor's pid
stamp; an integration test. **Out:** widening the codex sandbox to permit `/bin/ps` (the sandbox
policy is [MUX-153](../backlog/MUX-153-codex-test-agent-cannot-run-the-suite.md)'s decision and a process
list is more than a build needs); changing `build.sh`'s best-effort contract.

## Requirements

### Acceptance criteria

- [x] `muxcode upgrade-daemons` discovers every live daemon and monitor from the bus directories' pid stamps when `ps` cannot run, and restarts them exactly as the `ps` road does — proven with `ps` unavailable on `PATH`. Met with two recorded deviations: discovery reads kernel-owned owner locks, not pid stamps, and the restart is **not** the `ps` road's — it is a re-exec in place, because kill-and-relaunch from a sandbox left the session with no daemon. Script sections B and D
- [x] When both roads are available they agree: the pid-file set and the `ps` set name the same daemons (negative control: a stale pid file whose process is gone is dropped, not restarted) — unit level, with lock holders in place of pid files; an unheld lock naming a live unrelated pid is no target
- [x] A rollout that cannot run writes a lifecycle row (`daemon-upgrade-failed`, naming the reason) and `muxcode diagnose`/`session status` surface the daemon-vs-installed build mismatch without being asked to look for it — unit level; the row through the real CLI is the integration script's to show
- [x] `./build.sh` from a codex build agent cycles the session daemon — `daemon.version` after the build carries the installed binary's date — met by the live run of 2026-11-14 (proc `1794680719-proc-f83bd89b`, 26 passed, 0 failed, 6 live), with one deviation: the live section runs `upgrade-daemons --session <scratch>`, the call `./build.sh` ends with, inside a real `codex exec` under the build role's `-s workspace-write` flags, against a scratch session — not `./build.sh` itself, and it asserts `daemon.version`'s version (v0.0.2-test), the date being asserted on the same re-exec road in section D
- [x] The monitor (`watch --monitor`) stamps a pid file the discovery reads, so it is cycled on the same road as the daemon — as a held owner lock (`lock/monitor.owner`), not a pid file; discovery names it beside the daemon in the no-`ps` test
- [x] `scripts/test-upgrade-daemons-no-ps.sh` passes with a coverage floor — 26 passed, 0 failed, floor 26, 0 live

### Technical approach

- **Pid files first, `ps` for orphans.** `ListDaemonProcs` walks `/tmp/muxcode-bus-*/daemon.pid`
  (and a new `monitor.pid`), checks each pid is alive (`syscall.Kill(pid, 0)`) and that its
  `daemon.version` parses, and returns the same `DaemonProc` shape. `ps` runs afterwards when it
  can, adding only processes the walk did not find — the orphan case whose bus directory
  `CleanupStale` already removed — and its failure is logged, not fatal.
- **Say it when it cannot.** `UpgradeDaemons` logs `daemon-upgrade-failed` with the reason and the
  installed build when neither road produced a daemon for the live session; `diagnose` and
  `session status` compare `daemon.version` with `muxcode version` unconditionally.
- **No sandbox change.** The fix must work inside `-s workspace-write` with today's roots; reading
  `/tmp/muxcode-bus-*` is already permitted (the bus lives there).

### Key files

| File | Change |
|------|--------|
| `tools/muxcode/bus/upgrade.go` | `ListDaemonProcs` pid-file walk + liveness check; `ps` demoted to additive with a logged failure; `daemon-upgrade-failed` |
| `tools/muxcode/bus/config.go`, `daemon/daemon.go` | `MonitorPidFile`; the monitor writes it at startup |
| `tools/muxcode/bus/diagnose.go`, `cmd/session.go` | unconditional daemon-vs-installed build comparison |
| `scripts/test-upgrade-daemons-no-ps.sh` | **New** — Phase 3 |
| `docs/architecture.md`, `docs/agent-bus.md`, `CLAUDE.md` | `upgrade-daemons` discovery road and the new lifecycle event |

## Implementation

### Findings at activation (2026-11-14)

Read from the tree and `CLAUDE.md` at activation. The spec was filed on 2026-09-09; three things it rests on have moved. Nothing is ticked.

| Then | Now | Consequence for the work |
|------|-----|--------------------------|
| The bus lives at `/tmp/muxcode-bus-*`, and the approach walks that glob | It lives under `$XDG_STATE_HOME/muxcode/bus/muxcode-bus-{session}/` (MUX-208; `BusDirBase`, `MUXCODE_BUS_BASE`) | The pid-file walk reads `BusDirBase()`, never a `/tmp` glob. "Reading `/tmp/muxcode-bus-*` is already permitted" no longer describes the path |
| Every Codex role runs under `-s workspace-write` | No sandbox by default since 2026-11-02 (`codexSandboxMode`); `MUXCODE_CODEX_SANDBOX=workspace-write` restores it | The failure does not reproduce on a default session — today's build rows read "daemons restarted". It is live only under the opt-out, which `CLAUDE.md` still lists as refusing `ps`. Whether a sandboxed build can read the new state directory and signal a daemon is unverified; Phase 1 should establish it before the walk is built on it |
| `build.sh` is the only road that cycles daemons | `muxcode <dir>` attaching to a running session also runs `upgrade-daemons`, scoped to that session, from the user's own terminal | A second road already covers the drift this spec describes; the fix still matters for a build made under the opt-out and for the silent failure |
| — | `ListDaemonProcs` (`bus/upgrade.go:69`) is still `ps` alone; there is no `monitor.pid` and no `daemon-upgrade-failed` row | Not started |

The live check in the integration phase needs a Codex build agent **under the sandbox opt-out**; on a default session it would pass without exercising the fix.

### Phase 1: Discovery without `ps`

- [x] `ListDaemonProcs` walks the bus directories' `daemon.pid` files, drops dead pids, and merges `ps` results only when `ps` runs; a `ps` failure is a lifecycle `info`, not an error — met by a different mechanism: kernel-owned locks on `lock/daemon.owner`, read only when `ps` fails; `ps` alone decides when it runs (deviations below)
- [x] The monitor stamps `monitor.pid`; the walk reads it — it holds `lock/monitor.owner` instead, which the walk reads
- [x] Tests: pid-file discovery with a stubbed `ps` that fails; the two roads agree when both run; a dead pid file is dropped (negative control) — `TestListDaemonProcs_LockHoldersWhenPsCannotRun`, `_RoadsAgree`, `_PsIsAuthoritative`, `TestAcquirePidLock_HolderIsTheKernelsOwner`, `TestAcquireInstanceLock_ExcludesAcrossVersions`; the dead-pid control is an unheld lock

### Results for Phase 1 (2026-11-14)

Reported by the phase worker over four rounds and verified the same day against the tree: build (exit 0), `./test.sh` (exit 0, 2 of 2 modules, row 1794668933) and review (0 must-fix, 0 should-fix, `/tmp/muxcode-review-1794668939.txt`) all ran after the last source edit. The graph's verify request carried the worker's first report, which describes the pid-stamp design below as superseded; the ticks are against what the tree holds.

| Item | Result |
|------|--------|
| Finding — `daemon.pid` was never written | The "What the daemon stamps" row above is wrong on this point: `DaemonPidPath` had no caller, and the pid sat only in `lock/daemon.lock` |
| Two locks, two jobs | `bus.AcquireInstanceLock(lockPath, ownerPath)` takes both; either failing unwinds both. The legacy `flock` on `lock/daemon.lock` (`lock/monitor.lock` for the monitor) stays the single-instance guard that every version takes. A POSIX record lock (`fcntl` `F_SETLK`) on a separate identity file, `lock/daemon.owner` (`lock/monitor.owner`), is what discovery reads |
| Identity is the kernel's record of who holds the owner lock | `bus.LockHolderPid` asks the kernel for the owner with `F_GETLK`, on the `.owner` files only. A crash releases the lock, so a reused pid is never named. No `daemon.pid` or `monitor.pid` is written |
| Mixed-version exclusion | `TestAcquireInstanceLock_ExcludesAcrossVersions`: a child holding only the legacy `flock` refuses this build's acquisition, leaving no owner lock held, until it exits; a child holding this build's locks refuses a legacy-style `flock` while its owner stays discoverable. Platform-neutral, so it runs on Linux CI as well as macOS |
| The lock file's contents are never read by discovery | A holder that has not yet published its pid, or whose publish failed, is still named correctly; a leftover pid in the file never is. A pid write failure is now returned to the caller |
| Discovery when `ps` runs | `ps` is authoritative: a lock can neither add a process nor contradict what `ps` says about one |
| Discovery when `ps` fails | `ListDaemonProcs` names the holders of **held** locks only, under `BusDirBase()`, and logs `daemon-discovery-ps-failed` as info — never an error |
| The probe takes no lock | So it cannot fail a starting daemon, and the earlier retry is gone |
| A process never probes its own held lock | Closing that descriptor would release the lock. Guarded by a held-path set; the daemon and monitor must not open their lock file anywhere else (documented in the code) |
| Behaviour change | A second monitor for a session now exits — the monitor is single-instance |
| Tests | Use the test binary as a child holder: a held lock whose file still names an unrelated live pid (pre-publication), a held lock with an empty file (unpublished), an unheld lock naming a live unrelated pid; the `ps`-authoritative and roads-agree cases; holder refusal, exit and self-probe |
| Trade-off | With `ps` unavailable, an orphan whose bus directory is gone is missed |
| **Transitional gap** | A daemon still running the old binary holds only the `flock` and no owner lock, so `F_GETLK` finds nothing. Without `ps` it is not found until it restarts some other way; with `ps` it is found as before. So the first install of this fix made from a sandboxed build does not cycle the daemon — the very case the spec describes — and the fix takes effect from the second |
| ~~Still unverified~~ — answered by the live run | Whether a build under `MUXCODE_CODEX_SANDBOX=workspace-write` may signal the daemon it discovers. It may, and that was the problem: see the redesign below |

**Redesign after the live run (2026-11-14, recorded before verification; Phase 1 was ticked on the design above and has had no verification since).** A live run from inside a Codex sandbox showed that a daemon relaunched there dies with the sandbox: the rollout killed the daemon and monitor, and the session was left with neither. The worker reports the change as edit's decision plus a review must-fix; it is not a recorded user decision.

| Item | Now |
|------|-----|
| Without `ps` | Nothing is killed or orphan-killed, and tmux is not consulted. The rollout writes restart requests — `lock/daemon.restart`, `lock/monitor.restart` (`bus/restart_request.go`) — and the daemon (each poll) and monitor (each second) re-exec their own executable in place, keeping their pid. Rows: `daemon-reexec`, `monitor-reexec` (and `-failed`) |
| What a request may name | **Nothing.** First version: an absolute path to an executable named `muxcode`. A security must-fix removed it — the bus directory is writable from a sandbox and the daemon runs outside one, so a path in the request let a sandboxed writer choose code for the unsandboxed daemon. A request now carries only a human note; the consumer re-execs its own executable (`os.Executable`, the path it was started from, which `make install` overwrites). `TakeRestartRequest` and `installedBinary` are replaced by `TakeRestartTarget` |
| A daemon started from elsewhere | One started from a path the install did not reach re-execs its old build, fails the build-stamp proof, and the rollout reports `daemon-upgrade-failed` |
| With `ps` | Kill-and-relaunch as before; the children's output is now kept in `<bus dir>/upgrade-relaunch.log` |
| Proof, on both roads | Within 20 s the owner lock's holder must be the expected pid **and** a stamp of the installed build must have been written since the rollout began — `daemon.version`, and a new `monitor.version`. Otherwise `daemon-upgrade-failed`, exit 1, an unhonoured request is withdrawn and the old processes keep running |
| Tests | `bus/restart_request_test.go`, including `TestTakeRestartTarget_IsAlwaysTheConsumersOwnExecutable`: a request planted with an executable `/tmp/…/muxcode` path still yields the consumer's own executable |
| Consequence for the transitional gap | A daemon on a binary older than this change neither holds an owner lock nor reads restart requests, so without `ps` it still cannot be cycled |

**Three superseded designs.** (3, the latest) Replacing the daemon's `flock` outright with a record lock passed on macOS but lost mixed-version exclusion on Linux, where `flock` and record locks are independent: an old daemon and a new one could both start. Review's third must-fix; the fix keeps the `flock` and adds the owner file. (1) The first implementation wrote `daemon.pid` and `monitor.pid` and kept a pid while its process existed and its stamp was fresh within 120 s. Review returned a must-fix — a fresh stamp is not process identity — and it was removed. (2) The second held an `flock` and read the pid written in the lock file. Review returned a second must-fix — lock contention plus file contents is not owner identity, since a new holder locks before it writes its pid — and it was replaced by the record lock above.

Deviations from the steps as written, for verification to weigh:

- Step 1 says the walk reads `daemon.pid` files and **merges** `ps` results. The tree reads lock holders, and only when `ps` fails; when `ps` runs, it alone decides.
- Step 2 says the monitor stamps `monitor.pid`. It holds `lock/monitor.lock` instead.
- The acceptance criterion "the pid-file set and the `ps` set name the same daemons (negative control: a stale pid file … is dropped)" is met by locks: the stale case is an unheld lock.

### Phase 2: A rollout that cannot run is visible

- [x] `daemon-upgrade-failed` lifecycle row with the reason when no daemon was cycled for the live session — `unrolledReason`/`failRollout`, `bus/rollout_visibility_test.go`
- [x] `muxcode diagnose` and `session status` report a daemon-vs-installed build mismatch unconditionally — `DaemonBuildStatus` for `session status`; `diagnose` already did
- [x] Docs: `docs/architecture.md`, `docs/agent-bus.md` (`upgrade-daemons`), `CLAUDE.md` build table row

### Results for Phase 2 (2026-11-14)

Reported by the phase worker and verified the same day: build (exit 0), `./test.sh` (exit 0, 2 of 2 modules, row 1794669459) and review (0 must-fix, 0 should-fix, `/tmp/muxcode-review-1794669464.txt`) all ran after the must-fix below.

| Item | Result |
|------|--------|
| `daemon-upgrade-failed` | `unrolledReason` and `failRollout` (`bus/upgrade.go`): a live daemon in the rollout's own session (`--session`, else `BUS_SESSION`) that is on an older build and was not found, or whose relaunch failed, writes a warn row naming the reason. The reason says whether `ps` listed no daemon, or `ps` could not run and no process holds the owner lock |
| Exit code (added) | `upgrade-daemons` then exits 1, so `build.sh`'s existing warning reaches the build output. `build.sh`'s best-effort contract is unchanged — the build still exits 0 |
| `session status` | `DaemonBuildStatus` (`bus/upgrade.go`, called from `cmd/session.go`) ends the output with `Daemon: <v> (current)`, `Daemon: STALE — daemon X → installed Y; run muxcode upgrade-daemons --session S`, or `Daemon: not running` |
| `diagnose` | No change: `checkDaemonVersionMismatch` already runs in every report, as a warning. The step's "unconditionally" was already true there |
| Review must-fix (applied) | A session counted as accounted for whenever any plan named it, so a plan that found only its monitor hid a missed older daemon. `unrolledReason` now requires a plan that found the daemon itself (`DaemonPID != 0`) |
| Tests | `bus/rollout_visibility_test.go`, including a monitor-only control added for the must-fix; the "planned" negative control now names a real daemon pid |
| Docs | `docs/agent-bus.md` (`muxcode upgrade-daemons`: discovery table, the failed-rollout row, the `session status` line) and `docs/architecture.md` (attach-refresh table: Discovery and missed-session rows) by plan; the `CLAUDE.md` `upgrade-daemons` row by the worker |

### Phase 3: Integration test

- [x] Create `scripts/test-upgrade-daemons-no-ps.sh` — a scratch session with a daemon; `upgrade-daemons --dry-run` with a `PATH` that has no `ps` still names the daemon from its pid file; with `ps` restored the two listings agree; a stale pid file is ignored; the lifecycle row appears when the rollout cannot run
- [x] Live check (skipped with a reason without a codex build agent): `./build.sh` from the codex build role leaves `daemon.version` at the installed build's date — **skipped, as the step allows**: the script carries an opt-in live section (`--live` or `MUXCODE_UPGRADE_NO_PS_LIVE=1`) and prints the skip; no live run on the final design exists. The step is done; the acceptance criterion it would have proven is not, and stays open
- [x] Run the script and record pass/fail counts in this spec — 26 passed, 0 failed, floor 26, 0 live (proc `1794670583-proc-9c547694`)

### Results for the integration phase (2026-11-14, recorded before verification)

Reported by the phase worker; the boxes above stay open until this run's build, test and review pass.

| Item | Result |
|------|--------|
| Script | New `scripts/test-upgrade-daemons-no-ps.sh`, with an OLD and a NEW binary built from the tree and a scratch session |
| Run | 26 passed, 0 failed, floor 26 — run agent, task `1794669654-spawn-0616aad2-fad9480d`. The real session's daemon was unaffected (its dry run read the same before and after) |
| A | An OLD daemon and monitor run |
| B | A dry run with no `ps` on `PATH` plans the session and writes the info row; the plan lines with and without `ps` are identical |
| C | A ghost lock — unheld, freshly naming a live unrelated pid — is not planned |
| D | The no-`ps` rollout restarts daemon and monitor; `daemon.version` shows NEW's version and date; the unrelated pid survives |
| E | The transitional gap, shown: a LEGACY flock-only daemon beside a NEW monitor refuses a NEW daemon; the no-`ps` rollout exits 1 with `daemon-upgrade-failed`, leaves the daemon running rather than killing blind, and `session status` shows STALE |
| F (control) | With `ps`, the same legacy daemon is cycled and no new failure row is written |
| **Live check — skipped, not counted** | `./build.sh` from a Codex build agent under `MUXCODE_CODEX_SANDBOX=workspace-write` was not run; the script prints the skip and its reason. Whether that sandbox lets the rollout signal the daemon stays **unverified** |

**First verification round — not ticked.** Build (exit 0), `./test.sh` (exit 0, 2 of 2 modules, row 1794669762) and review (0 must-fix, 0 should-fix, `/tmp/muxcode-review-1794669768.txt`) passed, but the script on disk is not the one they covered:

| Time | Event |
|------|-------|
| 1794669687 | Run agent reports 26 passed, 0 failed |
| 1794669768 | Review requested |
| 1794669791 | The script is edited (the daemon's analyze event names it) |
| 1794669802 | Review replies clean |
| 1794669808 | The script's modification time — edited again, after the review's reply |

The 26/26 count belongs to an earlier version of the file, and the last edit was not reviewed. The phase's steps stay open until the script is run again as it now stands and a review covers that version.

**After the redesign (recorded before verification).** Section D now runs with no `ps` **and** an unreachable tmux socket, and asserts the re-exec road: the same pids before and after, `daemon-reexec` and `monitor-reexec` rows, and both build stamps on NEW. Hermetic rerun: 26 passed, 0 failed, floor 26 — task `1794670242-spawn-0616aad2-fe748ddd`. After the security must-fix the script runs its daemons from an install directory and moves NEW over it before the rollout, as `make install` does; rerun again at 26 passed, 0 failed — task `1794670577-spawn-0616aad2-178aaf96`. The sections table above describes the kill-and-relaunch version of D and is superseded on that row.

**Second verification round — ticked.** The last source edit (1794670575, the script) precedes the script's run (26 passed, 0 failed, 0 live; 1794670603), the build (exit 0), `./test.sh` (exit 0, 2 of 2 modules, row 1794670729) and the review (0 must-fix, 0 should-fix, `/tmp/muxcode-review-1794670733.txt`). That round covers the whole tree, so it also re-verifies Phases 1 and 2 after the redesign and the security fix. The review states it does not certify live-rollout acceptance.

**One box stays open: the criterion "`./build.sh` from a codex build agent cycles the session daemon".** Nothing shows it on the design that ships. The reports disagree on why the live rerun did not happen — the worker's phase report says the user declined it, its later messages say it awaits the user's approval — and plan has seen neither from the user. Either way it is the user's call: run the script with `--live` under `MUXCODE_CODEX_SANDBOX=workspace-write`, or defer the criterion to another spec.

| Live check | State |
|------------|-------|
| First live run (2026-11-14) | Done, from inside a Codex sandbox, on the kill-and-relaunch design. It left the session with no daemon — the finding that caused the redesign |
| Rerun on the re-exec design | **Done** after the user's approval (relayed by edit) — see the third round below |

What this leaves for verification to weigh: the acceptance criterion "`./build.sh` from a codex build agent cycles the session daemon" has no evidence behind it. The phase step allows the live check to be skipped with a reason; the criterion does not.

**Third round — the live run, criterion ticked (15/15).** Edit relayed that the user approved the live run. The run agent ran `MUXCODE_UPGRADE_NO_PS_LIVE=1 bash scripts/test-upgrade-daemons-no-ps.sh` (proc `1794680719-proc-f83bd89b`, 1794680719, exit 0): 26 passed, 0 failed, floor 26, 6 live. No source or script file changed since the second round (last edit 1794670575), so that round's build, suite and review still describe the tree the live run exercised.

| Live assertion | Result |
|----------------|--------|
| Sandbox | A real `codex exec` under the build role's opt-out flags (`-s workspace-write` plus its `--add-dir` roots and network grant) |
| `ps` | Refused inside the sandbox — a new `daemon-discovery-ps-failed` row |
| Rollout | `daemon v0.0.1-test → installed v0.0.2-test — daemon + monitor restarted`, `upgrade-daemons` exit 0, codex exit 0 |
| After codex exited | Daemon (pid 50115, unchanged — re-exec'd in place) and monitor alive on NEW; `daemon.version` v0.0.2-test |

Not shown by it: `./build.sh` itself run by a live Codex build agent in a real session — the scratch session stands in for it; and old-binary daemons, which still cannot be cycled without `ps` (the transitional gap above).

## Notes

- Filed 2026-09-09 from edit's side finding during [MUX-159](./MUX-159-codex-hooks-provider.md)'s
  evidence-guard work; the three builds that showed it and the 00:56 `--dry-run` are recorded there
  (Notes, side finding a).
- [MUX-153](../backlog/MUX-153-codex-test-agent-cannot-run-the-suite.md) and
  [MUX-160](../backlog/MUX-160-tmp-go-cache-leak-unclearable-pressure.md) are the same family — a codex
  sandbox refusing something a validation role's job requires — and this is the smallest member.
- `docs/upgrade-daemons` semantics otherwise unchanged: version-aware skip (`Info.SameBuild`),
  `--force`, `--session`, orphan kill.

## Status

Complete — closed 2026-11-14 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e`, 15/15 (activated the same day from Defects rank 14; ranks 1–13 were parked). Filed 2026-09-09 01:00.
