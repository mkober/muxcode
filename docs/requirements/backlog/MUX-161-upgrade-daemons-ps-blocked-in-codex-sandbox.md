# `upgrade-daemons` Cannot See the Daemons From a Codex Build Agent

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
policy is [MUX-153](./MUX-153-codex-test-agent-cannot-run-the-suite.md)'s decision and a process
list is more than a build needs); changing `build.sh`'s best-effort contract.

## Requirements

### Acceptance criteria

- [ ] `muxcode upgrade-daemons` discovers every live daemon and monitor from the bus directories' pid stamps when `ps` cannot run, and restarts them exactly as the `ps` road does — proven with `ps` unavailable on `PATH`
- [ ] When both roads are available they agree: the pid-file set and the `ps` set name the same daemons (negative control: a stale pid file whose process is gone is dropped, not restarted)
- [ ] A rollout that cannot run writes a lifecycle row (`daemon-upgrade-failed`, naming the reason) and `muxcode diagnose`/`session status` surface the daemon-vs-installed build mismatch without being asked to look for it
- [ ] `./build.sh` from a codex build agent cycles the session daemon — `daemon.version` after the build carries the installed binary's date
- [ ] The monitor (`watch --monitor`) stamps a pid file the discovery reads, so it is cycled on the same road as the daemon
- [ ] `scripts/test-upgrade-daemons-no-ps.sh` passes with a coverage floor

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

### Phase 1: Discovery without `ps`

- [ ] `ListDaemonProcs` walks the bus directories' `daemon.pid` files, drops dead pids, and merges `ps` results only when `ps` runs; a `ps` failure is a lifecycle `info`, not an error
- [ ] The monitor stamps `monitor.pid`; the walk reads it
- [ ] Tests: pid-file discovery with a stubbed `ps` that fails; the two roads agree when both run; a dead pid file is dropped (negative control)

### Phase 2: A rollout that cannot run is visible

- [ ] `daemon-upgrade-failed` lifecycle row with the reason when no daemon was cycled for the live session
- [ ] `muxcode diagnose` and `session status` report a daemon-vs-installed build mismatch unconditionally
- [ ] Docs: `docs/architecture.md`, `docs/agent-bus.md` (`upgrade-daemons`), `CLAUDE.md` build table row

### Phase 3: Integration test

- [ ] Create `scripts/test-upgrade-daemons-no-ps.sh` — a scratch session with a daemon; `upgrade-daemons --dry-run` with a `PATH` that has no `ps` still names the daemon from its pid file; with `ps` restored the two listings agree; a stale pid file is ignored; the lifecycle row appears when the rollout cannot run
- [ ] Live check (skipped with a reason without a codex build agent): `./build.sh` from the codex build role leaves `daemon.version` at the installed build's date
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- Filed 2026-09-09 from edit's side finding during [MUX-159](../drafts/MUX-159-codex-hooks-provider.md)'s
  evidence-guard work; the three builds that showed it and the 00:56 `--dry-run` are recorded there
  (Notes, side finding a).
- [MUX-153](./MUX-153-codex-test-agent-cannot-run-the-suite.md) and
  [MUX-160](./MUX-160-tmp-go-cache-leak-unclearable-pressure.md) are the same family — a codex
  sandbox refusing something a validation role's job requires — and this is the smallest member.
- `docs/upgrade-daemons` semantics otherwise unchanged: version-aware skip (`Info.SameBuild`),
  `--force`, `--session`, orphan kill.

## Status

**Backlog** — 0/15. Filed 2026-09-09 01:00.
