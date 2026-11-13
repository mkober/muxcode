# MUX-208: Durable Bus State Lives in /tmp, and macOS Deletes It After Three Idle Days

**Tracking:** [mkober/muxcode#176](https://github.com/mkober/muxcode/issues/176)

Every piece of long-lived muxcode state — graph runs and their approval markers, the spawn registry,
inboxes, receipts, role histories, agent-definition and sandbox hashes, the active-spec pointer — is
written under `/tmp/muxcode-bus-{session}/`. macOS runs `/usr/libexec/tmp_cleaner` at 00:00 every
day and deletes any file under `/tmp` whose atime, mtime **and** ctime are all more than three days
old. APFS does not advance atime on the daemon's reads, so a file muxcode writes once and only reads
afterwards is deleted on the first midnight it is at least four days old. A graph run parked on a
`wait_human` gate — which waits for a human by design — lost its frozen definition after three days,
and the daemon then logged an error every 2 s for hours, rotating every other row out of the
lifecycle log.

## Context

### Source and standard of evidence

Filed 2026-11-09 on the user's instruction relayed by edit (brief `/tmp/mux-defect-tmp-state.md`,
session `muxcode`). Every fact below was verified on the user's machine on 2026-11-09 by edit or plan
unless marked *inferred*. Code read at `3013363`.

### Mechanism — verified

| Fact | Where |
|------|-------|
| The bus directory is `BusDirBase()/muxcode-bus-{session}`; the base is the test override, else an absolute `MUXCODE_BUS_BASE`, else `/tmp` — chosen "directly (not `os.TempDir`) for compatibility with bash scripts that hardcode `/tmp/muxcode-bus-{SESSION}/`" | `BusDirBase`, `BusDir`, `bus/config.go:244-257` |
| `MUXCODE_BUS_BASE` is documented as **never for a real session**: "Bash hooks and pollers still hardcode /tmp" | `BusDirBaseEnv` doc comment, `bus/config.go:232-238` |
| 53 files under `scripts/`, 33 under `docs/` + `CLAUDE.md`, 5 non-test Go files and 17 Go test files name `muxcode-bus-` literally; the daemon monitor reads `/tmp/muxcode-bus-${SESSION}/daemon.keepalive` | `grep -rl muxcode-bus-`; `scripts/muxcode-daemon-monitor.sh:8` |
| Other `/tmp` writers: the analyze trigger (`/tmp/muxcode-analyze-{session}.trigger`), the preview temp file, the Codex package-cache root (`/tmp/muxcode-cache-<uid>`), the spawn scratch dir (`os.TempDir()`); the disk-pressure sweep measures `/tmp` | `bus/config.go:676`, `bus/hook.go:2105`, `bus/provider_codex.go:309`, `bus/spawn.go:1243`, `bus/cleanup.go:246,415,434,443` |
| macOS ships `/System/Library/LaunchDaemons/com.apple.tmp_cleaner.plist`, `StartCalendarInterval { Hour = 0 }`, running `/usr/libexec/tmp_cleaner` daily; it runs `find -dx /tmp -fstype local -type f -atime +3 -mtime +3 -ctime +3 -delete`, then removes empty directories with `-mtime +3`. Only X11 and quota names are exempt. This is the launchd job, not `periodic` (no `daily_clean_tmps`, no `/var/log/daily.out`). Darwin 27.0.0 | user's machine, 2026-11-09 |
| **Reads do not protect a file.** `stat` on `run.json` — which the daemon reads every tick — showed atime == mtime == 2026-11-05 10:43 four days later. APFS is not advancing atime on those reads | user's machine, 2026-11-09 |
| `find`'s `+3` is "more than three whole days", so a file goes on the first midnight it is at least four days old — roughly 3.5–4.5 days after its last write | `find(1)` |

### Incident — run `1793727228-50-spec-to-pr-5f982531` (MUX-177)

| Event | Evidence |
|-------|----------|
| The run parked at its Phase 4 `phase-gate` (`wait_human`) on 2026-11-05 10:43 | `approvals/phase-gate.pending` |
| `graph.json`, the frozen definition `CreateGraphRun` writes once (`bus/graph_run.go:262`), was written 2026-11-03 12:33 and deleted at 00:00 on 2026-11-08 | run directory mtime 2026-11-08 00:00; the directory now holds only `approvals/`, `nodes/`, `run.json`, `run.lock` |
| The `branch-check`, `branch-gate`, `create-branch` and `fix` node status files went at 00:00 on 2026-11-09 | `nodes/` mtime |
| Six older runs were emptied to bare `nodes/` and `approvals/` directories | `1793303392`, `1793637033`, `1793642571`, `1793662673`, `1793663044`, `1793664075` |
| From 2026-11-09 08:47:29 the daemon logged `graph-step-error … graph.json: no such file or directory` about every 2 s — 4,821 rows by 11:49 | `~/.config/muxcode/logs/muxcode.log`; `StepGraphRuns` logs each tick's error with no backoff and no state change, `bus/graph_exec.go:308-314` |
| **Lifecycle log loss**: the log now holds the old `graph-gate-approved` rows followed by nothing but these errors; every other row before 08:47 was rotated out (`MUXCODE_LIFECYCLE_LOG_MAX`, 1000) | same log |
| **The run vanished from the graph TUI**: the run list `continue`s past any run whose `ReadGraphRunGraph` fails, while `muxcode graph status` still lists it `[running]` | `tui/graph_ui.go:83-85` |
| **Cancel cannot complete** (*inferred from code*): `CancelGraphRun` sets `canceling`, then `runSendNodes` reads `graph.json` (`bus/graph_cancel.go:233`); that failure is a cleanup error, so `CancelIncompleteError` leaves the run `canceling` on every retry | `bus/graph_cancel.go:121,233` |
| Recovery: the run was abandoned; Phase 4 was committed by hand (`3013363`) and the spec closed out by hand | MUX-177 Status |

What started the error flood at 08:47 rather than at the 00:00 deletion is not determined; the
daemon had restarted at 06:59 (`daemon.version` mtime).

### Due at the next 00:00 pass

On 2026-11-09, 21 files under `/tmp/muxcode-bus-muxcode` were more than three days old (edit counted
26 earlier the same day). Among them:

| Class | Files |
|-------|-------|
| The rest of the run | `run.json`, 9 node status files, `approvals/phase-gate.pending` |
| Spawn state | `spawn.jsonl` (the registry), `inbox/spawn-5d9ee28e.jsonl`, its polling and waiting markers, notify lock and trigger |
| Commit inbox | `inbox/commit.jsonl`, `notified-commit.ids`, `trigger-commit.notify` |
| Histories — the evidence the hook road and graph nodes verify against | `build-history.jsonl`, `commit-history.jsonl`, `plan-history.jsonl` |
| Watchdog state | `agentdef-analyze.hash`, `agentdef-research.hash`, `trigger-daemon.notify` |

### Classes of state at risk

Anything under the bus that is written once and then only read, or that belongs to something idle
for more than three days. A session left running over a long weekend is the common case.

| Class | Consequence of loss |
|-------|---------------------|
| Graph runs parked on a gate, and their approval markers | The run cannot step, cannot be cancelled and disappears from the TUI; an approval already given is forgotten |
| The spawn registry | Workers become unknown to `spawn list`, the cap and adoption (MUX-195) |
| An idle role's inbox, receipts, notified ids and history | Pending requests vanish; the evidence rows a graph node or `verify-spec` checks against are gone |
| Agent-definition and sandbox-fingerprint hashes | The agent-defs watchdog re-reads "no stamp" and reloads a healthy agent |
| The active-spec pointer and `config/{role}.env` overrides | `verify-spec` stops firing; a provider override silently reverts on the next reload |

### Scope

The user asked for one defect: **durable state under `/tmp` being deleted by macOS.** The three
symptoms the incident exposed — the TUI silently skipping an unreadable run, cancel unable to complete
without `graph.json`, `graph-step-error` repeating every ~2 s with no backoff and flooding the
lifecycle log — are this incident's evidence, not separate scope. They are listed in
[Related](#related) for a spec of their own if a fix here does not naturally cover them.

## Requirements

### Acceptance criteria

- [ ] No durable bus state — the bus directory and everything under it — lives under `/tmp` or
  `os.TempDir()` by default on any platform; a test pins the resolved base
- [ ] One resolver answers "where is the bus directory" for the Go binary, the bash hooks, the
  daemon monitor and the integration scripts; no script hard-codes the base
- [ ] A graph run parked on a `wait_human` gate for more than four days — all its files backdated
  past the cleaner's threshold — steps, renders in the TUI and cancels normally after a simulated
  `tmp_cleaner` pass
- [ ] An existing session whose state is still in `/tmp/muxcode-bus-{session}` keeps that state
  across the upgrade: it is moved, not abandoned, and the daemon finds it without a restart loop
- [ ] Session discovery, stale cleanup and the disk-pressure sweep measure the new location (and
  stop reading `/tmp` as muxcode's footprint, except for the caches that stay there)
- [ ] Every `scripts/test-*.sh` still passes, and the test override (`SetBusDirBase`) keeps tests
  out of the real namespace
- [ ] `docs/configuration.md`, `docs/architecture.md` and `CLAUDE.md` name the new location and
  the migration

### Technical approach

Two shapes were considered:

| Option | Verdict |
|--------|---------|
| **Keep `/tmp`, refresh mtimes** — the daemon `touch`es every long-lived file each tick or each night | Rejected. It only works while a daemon runs; a stopped session's state (which is exactly the state someone comes back to) still ages out, and every new file class must remember to enrol |
| **Relocate durable state to a per-user state directory** | Chosen. `/tmp` is a scratch namespace the OS owns; muxcode's bus is session state. The precedent already exists: the lifecycle log lives in `~/.config/muxcode/logs/`, the upgrade cache under `$XDG_CACHE_HOME`/`~/.cache/muxcode/` |

The location is `$XDG_STATE_HOME/muxcode/bus/{session}`, defaulting to
`~/.local/state/muxcode/bus/{session}` (`MUXCODE_BUS_BASE` keeps its meaning as an absolute override
and becomes usable for a real session once nothing else hard-codes the base). Decide in Phase 1
whether `~/.config/muxcode/state/` is preferable for being beside the existing logs; either is fine,
`/tmp` is not.

The hard part is not the default — it is the 53 scripts, the hooks, the monitor and the docs that
spell `/tmp/muxcode-bus-`. The launcher already exports `BUS_SESSION` into every pane; it exports the
resolved directory beside it (`MUXCODE_BUS_DIR`), and a `muxcode bus-dir [session]` subcommand prints
the same answer for anything not launched by it. Scripts read the variable or call the subcommand;
none computes the path.

What stays in `/tmp`, deliberately: the Codex package caches (`/tmp/muxcode-cache-<uid>` — a cache,
and MUX-160's subject), the preview temp file and the analyze trigger (rewritten on every edit, so
never idle for three days). The disk-pressure sweep (`bus/cleanup.go`) keeps measuring `/tmp` for
those, and gains the state directory for the bus footprint it was really counting.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/config.go` | `BusDirBase`, `BusDir`, `BusDirBaseEnv`; the new default and resolver |
| `tools/muxcode/bus/launch.go`, `bus/launcher.go` | Export `MUXCODE_BUS_DIR` into panes; migrate an existing `/tmp` bus directory at session launch |
| `tools/muxcode/daemon/daemon.go` | Migrate on daemon start when the launcher did not (daemon relaunch, `upgrade-daemons`) |
| `tools/muxcode/bus/cleanup.go` | `DiscoverSessions`, `CleanupStale`, the disk-pressure footprint |
| `tools/muxcode/cmd/` | `muxcode bus-dir` |
| `scripts/muxcode-daemon-monitor.sh`, every hook script | Read the resolver, not the literal |
| `scripts/test-*.sh` (53) | Same; scratch buses keep their own base |
| `docs/configuration.md`, `docs/architecture.md`, `CLAUDE.md` | Location, env var, migration |

## Implementation

### Phase 1: Pin the defect and decide the location

- [ ] `TestBusDirNotUnderTmp` (`bus/config_test.go`): with no override and no env, `BusDirBase()` is
  not `/tmp`, not under `os.TempDir()`, and is absolute — red on the current code
- [ ] Enumerate every `/tmp` writer in the binary (the table above) and mark each **durable** (moves)
  or **ephemeral** (stays), in this spec
- [ ] Decide `$XDG_STATE_HOME/muxcode/bus` vs `~/.config/muxcode/state`, record the decision here
- [ ] Inventory the literal `muxcode-bus-` sites in `scripts/` and classify: hook, poller, monitor,
  integration fixture

### Phase 2: Relocate and resolve

- [ ] `BusDirBase()` defaults to the chosen state directory; `MUXCODE_BUS_BASE` still overrides
- [ ] The launcher exports `MUXCODE_BUS_DIR` into every pane and hook environment; `muxcode bus-dir
  [session]` prints it
- [ ] Hooks, pollers and the daemon monitor read the variable or the subcommand; `grep -rn
  '/tmp/muxcode-bus-' scripts/` finds nothing outside integration fixtures' own scratch bases
- [ ] `DiscoverSessions`, `CleanupStale` and the disk-pressure footprint read the new base; the sweep
  still measures `/tmp` for the caches
- [ ] Go tests: the test override keeps every test out of both namespaces; MUX-137's leak test, if
  present, asserts against the new base too

### Phase 3: Migrate existing sessions

- [ ] On session launch and on daemon start: if `/tmp/muxcode-bus-{session}` exists and the new
  directory does not, move it (`os.Rename` on the same device, copy-then-remove across devices) under
  the bus lock, logging one `bus-dir-migrated` row; if both exist, the new one wins and the old is
  left for the user with a `bus-dir-conflict` row
- [ ] A migrated session's daemon, agents and hooks resolve the same directory without a restart
  (`MUXCODE_BUS_DIR` re-exported, running daemons re-read via `upgrade-daemons`)
- [ ] `docs/configuration.md`, `docs/architecture.md` and `CLAUDE.md` updated: location, variable,
  subcommand, what stays in `/tmp`, the migration

### Phase 4: Integration test

- [ ] Create `scripts/test-durable-state-location.sh` (hermetic; builds muxcode from the tree,
  scratch HOME so the real state directory is untouched, scratch session)
- [ ] Every file written by a scratch daemon, a `muxcode send`, a `graph run` and a `spawn start` is
  under the scratch HOME's state directory — none under `/tmp` or `$TMPDIR`
- [ ] `muxcode bus-dir`, `$MUXCODE_BUS_DIR` in a launched pane, and `scripts/muxcode-daemon-monitor.sh`'s
  keepalive path agree byte for byte
- [ ] Simulated cleaner: a run parked on a `wait_human` gate has every file backdated five days with
  `touch -t`, then `find <state-dir> -type f -mtime +3 -delete` is run against **`/tmp`** (as the
  cleaner would) — the run still steps, `graph status` and the TUI (`--render-once`) list it, and
  `graph approve` then completes it
- [ ] Negative control: the same backdate-and-find against a bus directory placed under `/tmp` via
  `MUXCODE_BUS_BASE=/tmp` deletes `graph.json` and the daemon logs `graph-step-error` — proving the
  simulation reproduces the defect the fix removes
- [ ] Migration: a pre-seeded `/tmp/muxcode-bus-<scratch>` with an inbox, a history row and a parked
  run is moved on daemon start, one `bus-dir-migrated` row, the inbox still delivers and the run
  still steps; a second start moves nothing
- [ ] Floor: every assertion counted, the script exits 1 on any failure or a count below the floor

## Related

- [MUX-137](./MUX-137-test-bus-dir-leak.md) — tests leaking real bus directories into `/tmp`; the
  same namespace from the other side
- [MUX-160](./MUX-160-tmp-go-cache-leak-unclearable-pressure.md) — the `/tmp` caches the
  disk-pressure sweep counts; what stays in `/tmp` after this spec
- [MUX-168](./MUX-168-reinit-purges-active-spec-graph-run-survives.md) — the active-spec pointer
  lost by another road
- [MUX-002](../completed/MUX-002-disk-pressure-wrong-filesystem.md) — the sweep's filesystem
- Symptoms recorded here as evidence, each a candidate spec: the graph TUI hides a run whose
  definition is unreadable (`tui/graph_ui.go:83`); `graph cancel` cannot finish without `graph.json`
  (`bus/graph_cancel.go:233`); `StepGraphRuns` repeats an unchanged error every tick with no backoff
  and floods the lifecycle log (`bus/graph_exec.go:308`)

## Status

Backlog
