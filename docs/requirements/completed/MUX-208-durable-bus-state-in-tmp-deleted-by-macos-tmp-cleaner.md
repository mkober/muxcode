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

- [x] No durable bus state — the bus directory and everything under it — lives under `/tmp` or
  `os.TempDir()` by default on any platform; a test pins the resolved base
- [x] One resolver answers "where is the bus directory" for the Go binary, the bash hooks, the
  daemon monitor and the integration scripts; no script hard-codes the base
- [x] A graph run parked on a `wait_human` gate for more than four days — all its files backdated
  past the cleaner's threshold — steps, renders in the TUI and cancels normally after a simulated
  `tmp_cleaner` pass — `test-durable-state-location.sh`, run agent 2026-11-13 15:08: 53 passed,
  0 failed, floor 52 met, exit 0, including `graph cancel accepted after the pass` and
  `the run reaches canceled` on a second parked, backdated run
- [x] An existing session whose state is still in `/tmp/muxcode-bus-{session}` keeps that state
  across the upgrade: it is moved, not abandoned, and the daemon finds it without a restart loop —
  observed live 2026-11-13 13:09: the `muxcode` session's bus moved during Phase 2's build (one
  `bus-dir-migrated` row, no conflict) and the running graph carried on
- [x] Session discovery, stale cleanup and the disk-pressure sweep measure the new location (and
  stop reading `/tmp` as muxcode's footprint, except for the caches that stay there) —
  implemented in Phase 2; a review must-fix then added the default spawn root (`StateDir()/spawn`)
  to `MuxcodeTmpFootprint` (`bus/cleanup.go`), pinned by
  `TestTmpPressure_SpawnWorktreesCountOnDefaultBase` with an empty-tree negative control.
  `test-disk-pressure.sh` passed standalone 2026-11-13 15:08 (11 passed, 0 failed) and again
  inside the 15:11 `test-all.sh` run, on the binary installed 15:10 after that fix
- [x] Every `scripts/test-*.sh` still passes, and the test override (`SetBusDirBase`) keeps tests
  out of the real namespace — the override half is Phase 2 step 9. Ticked 2026-11-13 **with five
  failures that predate this spec or depend on the environment excluded**, on the user's
  instruction relayed by edit: pre-existing failures count as covered. All 65 scripts are accounted
  for, across four runs through the run agent rather than one complete `test-all.sh`:
  - **Pass — 51:** 47 in the two suite runs (26 in the 15:11 `bash scripts/test-all.sh`, proc
    `1794600689-proc-f90797b3`, which stopped at 15:35 inside `test-multi-phase-graph`; 21 in the
    15:38 rerun of its failures and the scripts it never reached, proc `1794602297-proc-b8c4f9fe`),
    plus four that failed there on fixture drift or leaked environment and pass after script-only
    fixes: `test-agent-resume` (104/0), `test-close-spec-guard` (21/0) and
    `test-node-outcome-attribution` (48/0) in the 15:53 run (proc `1794603193-proc-13e7baab`), and
    `test-pane-targeting` (22/0) at 15:58
  - **Excluded, filed as their own defects — 4:** `test-force-respond`
    ([MUX-209](../backlog/MUX-209-force-respond-script-non-forced-deliver-no-longer-surfaces-in-flight-skip.md),
    the non-forced deliver skip; `bus/deliver.go` and `bus/task.go` are unchanged by this spec),
    `test-graph-tui`
    ([MUX-210](../backlog/MUX-210-graph-tui-script-narrow-pane-flat-fallback-and-failed-first-order-fail.md),
    the narrow flat fallback and failed-before-done order; no `tui` source is changed by this spec),
    `test-opencode-edit`
    ([MUX-211](../backlog/MUX-211-opencode-edit-agent-told-a-pretooluse-hook-enforces-delegation.md),
    the definition rewrite at `bus/provider_opencode.go:619` no longer matches
    `agents/code-editor.md`) and `test-stall-redrive-busy`
    ([MUX-212](../backlog/MUX-212-stall-redrive-busy-script-stale-spinner-in-scrollback-pins-pane-busy.md),
    a stale spinner pins the pane busy; root cause unknown)
  - **Excluded, environment — 1:** `test-prompt-mode` — no `MUXCODE_OPENCODE_API_KEY` in the
    environment or config, so the surface renders its keyless state and the frame and live checks,
    which assume the interpret state, fail. Not filed
  - **Skipped by `test-all.sh` unless `MUXCODE_TEST_LIVE=1` — 9** (they drive the live session):
    `test-browser-monitor-e2e`, `test-context-pressure`, `test-diff-split`, `test-hot-reload`,
    `test-modal-size`, `test-multi-reload`, `test-notify-flood`, `test-resize-hook`,
    `test-stuck-agent`. Not run for this spec; `test-browser-monitor-e2e`, `test-context-pressure`
    and `test-modal-size` failed in the 15:11 run, before the skip list applied

  That each excluded failure predates this spec is inferred from the unchanged code under test; no
  script was run against `main`'s own binary
- [x] `docs/configuration.md`, `docs/architecture.md` and `CLAUDE.md` name the new location and
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

**Decided (Phase 1, 2026-11-13): `$XDG_STATE_HOME/muxcode/bus`**, with the directory keeping its
name beneath it — `~/.local/state/muxcode/bus/muxcode-bus-{session}/`. See
[Location decision](#location-decision).

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

- [x] `TestBusDirNotUnderTmp` (`bus/config_test.go`): with no override and no env, `BusDirBase()` is
  not `/tmp`, not under `os.TempDir()`, and is absolute — red on the current code — added; gated by
  `MUXCODE_PIN_MUX208=1` (skipped by default, red when opted in); Phase 2 removes the gate with the
  default change
- [x] Enumerate every `/tmp` writer in the binary (the table above) and mark each **durable** (moves)
  or **ephemeral** (stays), in this spec
- [x] Decide `$XDG_STATE_HOME/muxcode/bus` vs `~/.config/muxcode/state`, record the decision here
- [x] Inventory the literal `muxcode-bus-` sites in `scripts/` and classify: hook, poller, monitor,
  integration fixture

### Findings for Phase 1

Recorded 2026-11-13 from the Phase 1 implement node of run `1794591418-55-defect-backlog-loop`. Code
read at `b8bfcab`.

#### `/tmp` writers in the binary

| Writer | Path | Class | Phase 2 action |
|--------|------|-------|----------------|
| `BusDirBase` / `BusDir` (`bus/config.go:244-257`) | `/tmp/muxcode-bus-{session}/` — inboxes, receipts, histories, `graphs/`, `spawn.jsonl`, hashes, active spec, `config/{role}.env` | **durable** | Moves |
| `SpawnWorktreeBase` (`bus/spawn.go:1243`) | `os.TempDir()/muxcode-spawn-{session}` — git worktrees of `spawn start --worktree` workers, holding uncommitted work | **durable** | Moves to `<state>/muxcode/spawn/{session}`, beside the bus. macOS `$TMPDIR` is swept by `dirhelper`, a different cleaner with the same three-day loss |
| `TriggerFile` (`bus/config.go:676`) | `/tmp/muxcode-analyze-{session}.trigger` | ephemeral | Stays — rewritten on every edit |
| Preview temp (`bus/hook.go:2105`, `bus/launcher.go:198`, `scripts/muxcode-preview-hook.sh:23`, `scripts/muxcode-diff-cleanup.sh:11`) | `/tmp/muxcode-preview-{session}.tmp` | ephemeral | Stays |
| Codex package caches (`bus/provider_codex.go:309`) | `/tmp/muxcode-cache-<uid>` | ephemeral (cache, MUX-160) | Stays |
| `CleanupStale` (`bus/cleanup.go:52`) | Scans `BusDirBase()` for `muxcode-bus-*` **and** `muxcode-preview-*` / analyze triggers | reader | Must scan two roots once the base moves: the state base for bus dirs, `/tmp` for preview and trigger files |
| `DiscoverSessions` (`bus/remote.go:26`) | Scans `BusDirBase()` | reader | Follows the base unchanged |
| `CleanupClaudeTmp`, `MuxcodeTmpFootprint`, `Statfs("/tmp")` (`bus/cleanup.go:246,415,434,443`) | `/tmp` | reader | Footprint gains the state base for the bus; `/tmp` stays measured for caches and Claude dirs |
| `GoCacheRelievesTmp` (`bus/artifacts.go:159`) | Compares with `os.TempDir()` | reader | Unchanged |
| Claude permission profiles (`bus/profile.go:936,991-992,1046`) | `Read(/tmp/muxcode-bus-*)`, `Read(/private/tmp/muxcode-bus-*)`, `serve-state.json` | reference | Must name the new base |
| `muxcodeStateRoots` (`bus/provider_codex.go:282`) | Codex writable roots under the `workspace-write` opt-out: lifecycle log dir and global memory dir only | **gap** | Must add the bus base — `/tmp` is writable inside the Codex sandbox today, `~/.local/state` is not, so without it a sandboxed Codex agent cannot `muxcode send` |
| Daemon disk-pressure notice text (`daemon/daemon.go:4291,4301`) | Says "/tmp" | wording | Revisit with the footprint change |

#### Location decision

**`$XDG_STATE_HOME/muxcode/bus`** when `XDG_STATE_HOME` is absolute, else
**`~/.local/state/muxcode/bus`**. The bus directory keeps its name beneath it:
`~/.local/state/muxcode/bus/muxcode-bus-{session}/`. Not `~/.config/muxcode/state`.

| Reason | Detail |
|--------|--------|
| XDG defines `STATE_HOME` for this | Data that persists across restarts but is not configuration. `~/.config` is what users sync and version as dotfiles; high-churn inbox and receipt files do not belong there (the lifecycle log is there by precedent, not by fit) |
| One resolver pattern | Mirrors the upgrade cache's resolver (`bus/selfupgrade.go:412`: absolute `$XDG_CACHE_HOME`, else `~/.cache`) |
| Not `~/Library/Caches`, not `$TMPDIR` | macOS may purge Caches under disk pressure — the same failure class |
| The `muxcode-bus-{session}` name is kept under the base | `MUXCODE_BUS_BASE` keeps its meaning (a base holding `muxcode-bus-*` dirs), `BusDir` stays `base/muxcode-bus-{session}`, and the prefix scans in `DiscoverSessions`, `CleanupStale` and `MuxcodeTmpFootprint` are untouched. This spec's `.../bus/{session}` shorthand means `.../bus/muxcode-bus-{session}` |
| No silent fallback | An unresolvable home must fail loudly, never fall back to `/tmp` |
| No socket-path concern | No unix sockets live under the bus dir, so the longer path has no `sun_path` length limit to hit |

#### `muxcode-bus-` literals outside Go

53 files under `scripts/` (matching the count in [Mechanism](#mechanism--verified)), plus 5 outside
it.

| Class | Files | Phase 2 action |
|-------|-------|----------------|
| Monitor (production) | `scripts/muxcode-daemon-monitor.sh:8` — `KEEPALIVE="/tmp/muxcode-bus-${SESSION}/daemon.keepalive"` | Read `$MUXCODE_BUS_DIR` or `muxcode bus-dir` |
| Hooks and pollers (production) | **None** — no hook script names the bus dir; `muxcode-preview-hook.sh` and `muxcode-diff-cleanup.sh` name only the ephemeral preview file | None. The "Bash hooks and pollers still hardcode /tmp" claim in Mechanism, and `BusDirBaseEnv`'s doc comment, are out of date: the hooks moved into the Go binary |
| Agent definitions (production) | `agents/dev-server.md:39,70,82,157`, `agents/log-watcher.md:96`, `agents/autonomous-agent.md:228-230`, `agents/harness/autonomous-agent.md:59-61` | Use `$MUXCODE_BUS_DIR`. `agents/dev-server.md:93` already documents a `muxcode bus-dir` command and a `~/Library/Caches/muxcode/...` path — neither exists; stale |
| Neovim (production) | `config/nvim/plugin/startscreen.lua:51` — lock dir | Read `MUXCODE_BUS_DIR` from the environment |
| Settings merge | `scripts/merge-claude-settings.sh:109,189` — removes legacy `Write(/tmp/muxcode-*)` grants | None (removal of an old grant) |
| Integration fixture, already on its own scratch base | `test-agent-resume.sh` (uses `MUXCODE_BUS_BASE`; its `/tmp` check at `:237` is a negative assertion) | Keep; the `/tmp` assertion stays meaningful |
| Integration fixture, scratch session in `/tmp` | 48 scripts set `BD` / `BUSDIR` / `BUS_DIR="/tmp/muxcode-bus-$SESSION"` for a scratch session the binary resolves itself: `answered-row-receipt`, `auto-clear`, `auto-startup-gating`, `branch-time-recording`, `browser-monitor-e2e`, `cancel-provenance`, `chain-completion`, `close-spec-guard`, `codex-hooks`, `codex-idle-delivery`, `codex-trust-prompt`, `context-pressure`, `control-pane`, `delivery-ack`, `echo-as-result`, `edit-auto-resume`, `fix-loop-cap`, `fkey-labels`, `force-respond`, `gate-authority`, `graph-orchestrator`, `graph-request-echo`, `graph-tui`, `graph-worker-reuse`, `hook-bash-failure`, `hot-reload`, `multi-phase-graph`, `multi-reload`, `node-outcome-attribution`, `notify-flood`, `pane-targeting`, `pii-scrub-conversation`, `pii-scrub-roles`, `pr-merge-review-gate`, `prompt-mode`, `resize-hook`, `restart-definition`, `self-upgrade`, `send-keys-dash`, `spawn-no-cross-tree-delegation`, `stall-redrive-busy`, `startup-self-reply`, `status-line-task-close`, `stuck-agent`, `verify-spec-refire`, `version`, `watch-chain-gate`, `xcode-compat` | Each **breaks** when the default moves — compute the dir with `muxcode bus-dir "$SESSION"`, or pin `MUXCODE_BUS_BASE` to a scratch base. The live-session ones (`hot-reload`, `resize-hook`, `multi-reload`, `notify-flood`, `stuck-agent`, `context-pressure`, `browser-monitor-e2e`) read a real session's dir and must use `muxcode bus-dir` |
| Integration fixture, deliberate `/tmp` | `test-disk-pressure.sh:50,129,133` (snapshots `/tmp/muxcode-bus-*` to prove the sweep spares live buses), `test-lifecycle-log-leak.sh:73,110` (scratch bus in `/tmp`), `test-version.sh:106` (asserts no bus dir created) | `disk-pressure` and `version` must assert against the new base (or both); `lifecycle-log-leak`'s scratch can stay |
| Command-text fixture | `test-run-chain-scope.sh:47`, `test-watch-chain-gate.sh:109` — the literal is a classified command string, not a path read | None |

#### Test note

`TestBusDirNotUnderTmp` (new file `tools/muxcode/bus/config_test.go`) clears the test override and
`MUXCODE_BUS_BASE`, then asserts `BusDirBase()` is absolute and not within `/tmp`, `os.TempDir()`, or
either's symlink-resolved form (`/private/tmp`, `/private/var/folders/...`). It is red by
construction on the current code — the default returns the literal `"/tmp"` — and is **committed
gated**: it skips unless `MUXCODE_PIN_MUX208=1`, which runs it against `/tmp` to show the defect. A
red test would fail the graph's test node and send the fix loop to change the
default alone; the install that follows restarts live daemons onto an empty bus before the migration
exists. Phase 2 deletes the gate in the same change that moves the default.

#### Sequencing hazard — Phases 2 and 3

The default move and the migration must reach an installed binary **together**. A binary that moves
the default without the migration, once `./build.sh` runs `upgrade-daemons`, restarts every live
session's daemon onto an empty bus — including the run doing the work, whose own state is in
`/tmp/muxcode-bus-muxcode/graphs/`. In `55-defect-backlog-loop`, Phase 2's build node does exactly
that install. The migration step therefore moved from Phase 3 into Phase 2 (below).

### Phase 2: Relocate and resolve

**The default move and the migration ship in one installed binary** — see
[Sequencing hazard](#sequencing-hazard--phases-2-and-3). Do not build or install a tree that has the
first step below without the second.

- [x] `BusDirBase()` defaults to the chosen state directory; `MUXCODE_BUS_BASE` still overrides; an
  unresolvable home fails loudly rather than falling back to `/tmp`; the `MUXCODE_PIN_MUX208`
  gate in `TestBusDirNotUnderTmp` is removed in the same change — `StateDir()` (absolute
  `$XDG_STATE_HOME/muxcode`, else `~/.local/state/muxcode`; panics with no resolvable home),
  `BusDirBase()` = `StateDir()/bus`; test ungated (`bus/config.go`, `bus/config_test.go`)
- [x] Migration (moved from Phase 3): on session launch and on daemon start, if
  `/tmp/muxcode-bus-{session}` exists and the new directory does not, move it (`os.Rename` on the
  same device, copy-then-remove across devices) under the bus lock, logging one `bus-dir-migrated`
  row; if both exist, the new one wins and the old is left for the user with a `bus-dir-conflict` row
  — `bus/busdir_migrate.go`: `MigrateLegacyBusDir` (flock `<base>/.muxcode-bus-<s>.migrate.lock`,
  re-check under it, `bus-dir-migrate-failed` on error); `ReportLegacyBusDirConflict` called from
  `LaunchSession` and daemon `Run`. A third road was added: `BusDir()` itself migrates on a
  process's first resolution of a session (see [Findings for Phase 2](#findings-for-phase-2))
- [x] `SpawnWorktreeBase` moves out of `os.TempDir()` to `<state>/muxcode/spawn/{session}` —
  `StateDir()/spawn/{session}`, or `muxcode-spawn-{session}` beside the bus when the base is
  overridden; existing worktrees keep the path recorded in their spawn entry
- [x] Claude permission profiles (`bus/profile.go`) and the Codex writable roots
  (`muxcodeStateRoots`) name the new base, so a sandboxed Codex agent can still `muxcode send` —
  `busDirReadRule()` replaces the `/tmp` and `/private/tmp` `Read()` rules (Claude's `//`-absolute
  form of the resolved base); `muxcodeStateRoots` adds `BusDirBase()`
- [x] Agent definitions and `config/nvim/plugin/startscreen.lua` read `MUXCODE_BUS_DIR` instead of
  the literal; the stale `~/Library/Caches` path in `agents/dev-server.md` is corrected —
  `dev-server.md`, `log-watcher.md`, `autonomous-agent.md` and `harness/autonomous-agent.md` use
  `${MUXCODE_BUS_DIR:-$(muxcode bus-dir)}`; `startscreen.lua` falls back to `muxcode bus-dir`
- [x] The launcher exports `MUXCODE_BUS_DIR` into every pane and hook environment; `muxcode bus-dir
  [session]` prints it — `bus.BusDirEnv`, exported by `LaunchSession` (process env and
  `tmux set-environment`), the control pane (`-e`) and the daemon; `cmd/busdir.go` defaults to
  `$BUS_SESSION` and exits 2 with neither
- [x] Hooks, pollers and the daemon monitor read the variable or the subcommand; `grep -rn
  '/tmp/muxcode-bus-' scripts/` finds nothing outside integration fixtures' own scratch bases — the
  monitor reads `$MUXCODE_BUS_DIR`, else `muxcode bus-dir`, and exits 1 rather than fall back to
  `/tmp`; new `scripts/lib/muxcode-bus-dir.sh` (`muxcode_bus_dir <bin> <session>`, refusing a
  non-absolute answer so an old binary never yields an empty `rm -rf` target); 49 integration
  scripts converted. The grep leaves only `test-agent-resume` (negative assertion),
  `test-lifecycle-log-leak` (deliberate `/tmp` scratch), `test-disk-pressure` and `test-version`
  (a `/tmp` check kept beside the new one), and `test-watch-chain-gate` and `test-run-chain-scope`
  (command text)
- [x] `DiscoverSessions`, `CleanupStale` and the disk-pressure footprint read the new base; the sweep
  still measures `/tmp` for the caches — `DiscoverSessions` treats a missing base as no sessions;
  `CleanupStale` scans the bus base, plus `/tmp` on the default base (preview, triggers, unmigrated
  legacy buses) and the new spawn root; `MuxcodeTmpFootprint` adds the bus base
- [x] Go tests: the test override keeps every test out of both namespaces; MUX-137's leak test, if
  present, asserts against the new base too — tests that hard-coded `/tmp/muxcode-bus-` and wrote
  there now use `useTempBusDir` (`console_test`, `daemon_health_test` ×4, `health_test` ×2;
  `remote_test` asserts the new default); `codexTestHome` clears `XDG_STATE_HOME`,
  `MUXCODE_BUS_BASE` and the override, and `codexStateDirs` expects the bus base. Every package
  `TestMain` (`bus`, `cmd`, `daemon`, `tui`) pins `XDG_STATE_HOME` to its run's temp root, so a test
  resolving a bus dir without `SetBusDirBase` never reaches the real `~/.local/state/muxcode`;
  `TestDefaultBusBase_IsolatedFromRealState` fails if the pin is removed. No MUX-137 leak test
  exists; [MUX-137](../backlog/MUX-137-test-bus-dir-leak.md)'s dead `BUS_DIR_BASE` tests now land in
  the temp root, and their own fix stays MUX-137's. Verified via the run agent
  (`/tmp/mux208-step9-verify.sh`): full `go test` exit 0, the real state dir and
  `/tmp/muxcode-bus-*` unchanged across the run

### Findings for Phase 2

Recorded 2026-11-13 from the Phase 2 implement node of run `1794591418-55-defect-backlog-loop`.

| Finding | Detail |
|---------|--------|
| **Install-window race, closed by migrating on resolution** | `./build.sh` runs `make install` then `upgrade-daemons`; between them any new-binary process (a hook, `ReadDaemonVersion`) resolves the new base and could create an empty bus first, turning the daemon-start migration into a `bus-dir-conflict` that abandons the session's real state. So `BusDir()` runs `MigrateLegacyBusDir` once per process per session: whichever new-binary process resolves a session first moves it. Guards: default base only, never in a test binary (`testing.Testing()`), and the conflict is never logged from this road (every process would log it). `upgrade-daemons` finds daemons by `ps`, not bus dirs, so discovery is unaffected |
| **Migration hardened in review** | The per-process road blocks concurrent resolutions of a session until the move completes, and never memoizes a failure. Every conflict decision is taken under the flock. The cross-device copy goes to a hidden staging dir (`.muxcode-bus-<s>.migrating-<pid>`) that is renamed into place, so no destination ever exists half-copied. A failed migration panics in `BusDir` — no replacement bus is created — and `ReportLegacyBusDirConflict` returns the error, which stops `LaunchSession` and the daemon. Covered by `TestBusDir_ConcurrentResolutionWaitsForMigration`, `TestBusDir_FailedCopyStrandsNothing` and `TestBusDir_CrossDeviceCopyPublishesWhole` (`bus/config_test.go`) |
| **Missed by the Phase 1 inventory** | `tools/muxcode-llm-harness/harness/config.go:87` hard-coded `/tmp/muxcode-bus-`. Fixed: `resolveBusDir` prefers `$MUXCODE_BUS_DIR`, else mirrors the binary's base resolution (a stdlib-only module, it cannot import `bus`); `TestResolveBusDir` added |
| **Live-session effect of this phase's build** | The graph's build node installs this binary and `upgrade-daemons` restarts the `muxcode` session's daemon, so `/tmp/muxcode-bus-muxcode` — the run's own state — migrates then. Agents' already-running inbox listeners hold the old path until they next restart; the daemon's wake road re-drives them. Panes launched before the install lack `MUXCODE_BUS_DIR`; everything that reads it falls back to `muxcode bus-dir` |
| **Smoke** (not the phase's own script — Phase 4 creates it) | Via the run agent, `bash /tmp/mux208-phase2-smoke.sh`: `test-version` 40 passed, 0 failed; `test-watch-chain-gate` 18 passed, 0 failed (floor 18); both tree-built under a scratch HOME |

### Phase 3: Migrate existing sessions

The move itself is Phase 2's second step — it cannot ship after the default changes.

- [x] A migrated session's daemon, agents and hooks resolve the same directory without a restart
  (`MUXCODE_BUS_DIR` re-exported, running daemons re-read via `upgrade-daemons`) — the live
  migration (13:09:37) moved the bus but did **not** re-export the variable: the edit pane and the
  `muxcode` tmux session env both lacked it. Fixed: `RunAgentLaunchResume` (`bus/launch.go`) always
  sets `MUXCODE_BUS_DIR=BusDir(session)` before exec, overwriting a missing or stale `/tmp` value
  from a pre-install pane shell, and daemon `Run` also sets it on the tmux session, so
  `upgrade-daemons` re-exports into a session launched before the install.
  `TestRunAgentLaunch_ExportsResolvedBusDir` seeds the legacy value and fails without the change.
  Confirmed live after the Phase 3 build: the session env matches `muxcode bus-dir`
- [x] `docs/configuration.md`, `docs/architecture.md` and `CLAUDE.md` updated: location, variable,
  subcommand, what stays in `/tmp`, the migration — also `docs/agent-bus.md` (paths, a
  `muxcode bus-dir` entry), `docs/agents.md` and the README diagram

### Phase 4: Integration test

- [x] Create `scripts/test-durable-state-location.sh` (hermetic; builds muxcode from the tree,
  scratch HOME so the real state directory is untouched, scratch session)
- [x] Every file written by a scratch daemon, a `muxcode send`, a `graph run` and a `spawn start` is
  under the scratch HOME's state directory — none under `/tmp` or `$TMPDIR` — the search is proven
  on a planted probe
- [x] `muxcode bus-dir`, `$MUXCODE_BUS_DIR` in a launched pane, and `scripts/muxcode-daemon-monitor.sh`'s
  keepalive path agree byte for byte — also the daemon's tmux-env re-export, and a spawned pane's
  value over a stale `/tmp` one
- [x] Simulated cleaner: a run parked on a `wait_human` gate has every file backdated five days with
  `touch -t`, then `find <state-dir> -type f -mtime +3 -delete` is run against **`/tmp`** (as the
  cleaner would) — the run still steps, `graph status` and the TUI (`--render-once`) list it, and
  `graph approve` then completes it
- [x] Negative control: the same backdate-and-find against a bus directory placed under `/tmp` via
  `MUXCODE_BUS_BASE=/tmp` deletes `graph.json` and the daemon logs `graph-step-error` — proving the
  simulation reproduces the defect the fix removes
- [x] Migration: a pre-seeded `/tmp/muxcode-bus-<scratch>` with an inbox, a history row and a parked
  run is moved on daemon start, one `bus-dir-migrated` row, the inbox still delivers and the run
  still steps; a second start moves nothing
- [x] Floor: every assertion counted, the script exits 1 on any failure or a count below the floor —
  floor 48

Run through the run agent on 2026-11-13: the first run was 47 passed, 2 failed on two script-side
bugs (`find`'s exit status under `pipefail`; a `/tmp` check against a scratch HOME that itself sits
in `/tmp`); after the fix, **49 passed, 0 failed, floor met, exit 0**. The script then gained the
cancel section and floor 52; re-run 15:08 the same day: **53 passed, 0 failed, floor met, exit 0**.

## Related

- [MUX-137](../backlog/MUX-137-test-bus-dir-leak.md) — tests leaking real bus directories into `/tmp`; the
  same namespace from the other side
- [MUX-160](../backlog/MUX-160-tmp-go-cache-leak-unclearable-pressure.md) — the `/tmp` caches the
  disk-pressure sweep counts; what stays in `/tmp` after this spec
- [MUX-168](../completed/MUX-168-reinit-purges-active-spec-graph-run-survives.md) — the active-spec pointer
  lost by another road
- [MUX-002](../completed/MUX-002-disk-pressure-wrong-filesystem.md) — the sweep's filesystem
- Symptoms recorded here as evidence, each a candidate spec: the graph TUI hides a run whose
  definition is unreadable (`tui/graph_ui.go:83`); `graph cancel` cannot finish without `graph.json`
  (`bus/graph_cancel.go:233`); `StepGraphRuns` repeats an unchanged error every tick with no backoff
  and floods the lifecycle log (`bus/graph_exec.go:308`)

## Status

Complete

Closed out 2026-11-13 by `55-defect-backlog-loop` run `1794602128`. All four phases and seven
acceptance criteria are checked; the full-suite criterion carries five exclusions set by the user —
see its annotation and [MUX-209](../backlog/MUX-209-force-respond-script-non-forced-deliver-no-longer-surfaces-in-flight-skip.md),
[MUX-210](../backlog/MUX-210-graph-tui-script-narrow-pane-flat-fallback-and-failed-first-order-fail.md),
[MUX-211](../backlog/MUX-211-opencode-edit-agent-told-a-pretooluse-hook-enforces-delegation.md) and
[MUX-212](../backlog/MUX-212-stall-redrive-busy-script-stale-spinner-in-scrollback-pins-pane-busy.md).
