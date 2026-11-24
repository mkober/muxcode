# MUX-237: muxcode Blanks `XDG_STATE_HOME` in Every Pane, so Neovim Writes Its State Into the Working Directory

**Tracking:** [mkober/muxcode#249](https://github.com/mkober/muxcode/issues/249)

When the launcher's own `XDG_STATE_HOME` is unset, muxcode starts every pane of the session with
`XDG_STATE_HOME` set to the empty string. Neovim does not read an empty value as unset: it builds
its state directory from it as a relative path, so every `nvim` opened in a pane writes its shada
file, logs, plugin cache and persistent undo files into `./nvim` — or `./muxcode` for the managed
editor (`NVIM_APPNAME=muxcode`) — under whatever directory it was started in. On 2026-11-19 that
put an undo file for `~/.aws/credentials` into an untracked path at the root of a project
repository, one `git add .` from being committed.

## Context

### Source and standard of evidence

| Claim | Standing |
|-------|----------|
| Stray Neovim state at a project repository's root, including an undo file for `~/.aws/credentials` | **Reported** — edit's note of 2026-11-19, from another repository under a muxcode session (branch `PBP1-5243-reference-data-endpoints`): `nvim/lazy/pkg-cache.lua`, `nvim/lsp.log`, `nvim/luasnip.log`, `nvim/nvim.log`, `nvim/shada/main.shada`, `nvim/undo/%Users%<user>%.aws%credentials`. Plan did not see that tree |
| A pane carries an empty `XDG_STATE_HOME` | **Observed** 2026-11-19 in this repository's session: the plan pane has the variable set with length 0, and `tmux show-environment -t <session> XDG_STATE_HOME` prints `XDG_STATE_HOME=` |
| Neovim reads the empty value as a relative path | **Observed** 2026-11-19, NVIM v0.12.6, `NVIM_APPNAME=muxcode`: `stdpath("state")` is `muxcode` with `XDG_STATE_HOME=` and `~/.local/state/muxcode` with the variable unset |
| Starting Neovim is enough to write | **Observed** — two starts in an empty scratch directory with the empty value, both `-u NONE -i NONE` (no config, no shada), left `./muxcode` and `./nvim` behind |
| The same has happened in this repository | **Observed** — `./muxcode/nvim/` at the repository root holds `shada/`, `undo/`, `lazy/` and `nvim.log`, 31 files; hidden only by a local `/muxcode/` line in `.git/info/exclude` (line 7), which no other checkout has. Its undo files are for specs under `docs/requirements/` — the plan window's editor |
| The cause is the blank in `busBaseEnvPlan` | **Read**, and since 2026-11-24 **measured** on every road it feeds, with no other source found (Phase 1 results); not yet proven by a build without it — Phase 4's negative control |

Edit's evidence note is `/tmp/xdg-state-blank-nvim.md` (volatile; what it carries is recorded here).

### The cause — as read

| Where | What it does |
|-------|--------------|
| `busBaseEnvPlan`, `tools/muxcode/bus/config.go:262` | For each of `MUXCODE_BUS_BASE` and `XDG_STATE_HOME`: an absolute value in this process goes to `set`, anything else — unset, empty or relative — goes to `blank` |
| `busBaseEnvArgs`, `config.go:289` | Emits `KEY=` for each blank; `LaunchSession` passes the result to `tmux new-session -e` (`bus/launcher.go:280`), so the first window's shell starts with `XDG_STATE_HOME=` |
| `ExportBusBaseEnv`, `config.go:276` | Runs `TmuxSetEnv(session, key, "")` for each blank, so every later window and pane inherits the empty value. Called at launch (`launcher.go:287`) and again at every daemon start (`daemon/daemon.go:345`) |

The doc comment on `busBaseEnvPlan` gives the reasoning: a stale value already in the tmux
environment would override the launcher's own resolution, removing a session key exposes the
server's global environment, and `new-session -e` cannot remove a variable at all — so the key is
set to empty, on the grounds that "Empty reads as unset to BusDir (absolute values only) and, per
the XDG spec, to XDG_STATE_HOME". The first half holds: muxcode's own `stateDir` (`config.go:317`)
accepts absolute values only. The second half is true of the specification and false of Neovim.

Two unit tests pin the blank as intended behaviour and change with the fix:
`TestBusBaseEnvArgs` (`bus/tmux_test.go:95`, expects `XDG_STATE_HOME=`) and
`TestExportBusBaseEnv_UnsetsWhatTheLauncherLacks` (`tmux_test.go:77`).

The blank arrived in `608b5f0` (2026-11-13, PR #185), a follow-up to
[MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md) (`f0e5f14`), which
began exporting `XDG_STATE_HOME` so that later panes resolve the launcher's bus directory.

### Consequence

- **Secrets in project repositories.** `config/nvim/init.lua:28` sets `undofile`, so the managed
  editor writes an undo file for every file it changes; an undo file can hold the text of the file
  it belongs to. Opened from a pane, an edit to a credentials file leaves that under the project's
  working tree, untracked.
- **Every repository a session opens gains untracked `nvim/` or `muxcode/` directories.** Nothing
  ignores them except a line someone adds by hand.
- **Neovim's state is split per directory.** Shada history, undo history and the plugin cache
  written in one working directory are not found from another, or from outside muxcode.
- **Any other tool that reads `XDG_STATE_HOME` without the empty-as-unset rule is affected the same
  way.** None has been checked; Phase 1 records what it finds.

### Scope of the exposure

- A user whose shell profile exports an absolute `XDG_STATE_HOME` is not affected: the launcher has
  an absolute value and exports it. The default on macOS and most Linux setups is unset.
- A running pane keeps the environment it started with. A fix reaches new panes once the session's
  daemon restarts on the fixed binary; panes already open keep the empty value until relaunched.

### Workaround

`export XDG_STATE_HOME="$HOME/.local/state"` in the shell profile, before launching muxcode.

### Not this spec

- Deleting the stray `nvim/` and `muxcode/` directories already written into project repositories.
  That is the user's to do — an undo file of a secret among them first. muxcode removing files
  from a working tree it did not create them in on purpose is a separate decision.
- Whether the managed editor should keep persistent undo at all.
- `MUXCODE_BUS_BASE`, the other blanked key. Only muxcode reads it, and it accepts absolute values
  only; its blank stays.

## Requirements

### Acceptance criteria

- [x] Reproduced from a clean launch: a scratch session started with `XDG_STATE_HOME` unset in the launcher shows the empty value in its first pane and in a later window, and a headless Neovim edit there writes under the pane's working directory
- [x] No pane muxcode creates carries an empty `XDG_STATE_HOME` — the first window, a later window, a split, and a spawn worker's pane each hold an absolute value or no variable at all
- [x] In such a pane `stdpath("state")` is absolute, with `NVIM_APPNAME=muxcode` and without it
- [x] A headless Neovim edit with `undofile` on, made from a pane, creates nothing under the pane's working directory
- [x] The stale-override case the blank was written for still holds: with a different absolute `XDG_STATE_HOME` already in the tmux session or server environment and none in the launcher, a later pane's `muxcode bus-dir` equals the launcher's, byte for byte
- [x] A launcher with an absolute `XDG_STATE_HOME` exports it unchanged, as today; one with a relative value does not pass the relative value on
- [x] A session whose environment already holds the empty value is corrected when its daemon starts on the fixed binary, so panes created after `muxcode upgrade-daemons` are clean
- [x] Negative control: a build that still blanks the variable fails the pane-environment and the working-directory assertions
- [x] The doc comment on `busBaseEnvPlan` and the two unit tests that pin the blank state what the code now does

### Technical approach

Set the variable to what the launcher itself resolved, never to empty.

- When the launcher has no absolute `XDG_STATE_HOME`, export the default it already falls back to —
  the parent of `StateDir()`, `$HOME/.local/state` — through both roads (`new-session -e` and
  `set-environment`). It is an absolute value, so it overrides a stale session or server value
  exactly as the blank did, and every reader — muxcode, Neovim, anything else — resolves the same
  directory the specification's default names.
- The alternative in edit's note, leaving the variable untouched, does not meet the stale-override
  criterion: `new-session -e` cannot remove a key, and unsetting the session key exposes the
  server's global value. It is recorded here as considered and not taken.
- `busBaseEnvPlan` treats its two keys alike today. After the fix they differ: `MUXCODE_BUS_BASE`
  is still blanked, `XDG_STATE_HOME` is always set.
- The daemon's own `ExportBusBaseEnv` call at start is what carries the correction into a running
  session; no separate migration is needed. Document that open panes keep their environment.

Phase 1 may find the empty value arriving by another road (a spawn launch line, a `respawn-pane`, a
script that exports it); the scope is whatever the measurement shows.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/config.go` | `busBaseEnvPlan`, `busBaseEnvArgs`, `ExportBusBaseEnv`, `stateDir` |
| `tools/muxcode/bus/launcher.go` | Passes the plan to `new-session -e` and `set-environment` |
| `tools/muxcode/daemon/daemon.go` | Re-exports the plan at daemon start |
| `tools/muxcode/bus/tmux_test.go` | `TestBusBaseEnvArgs`, `TestExportBusBaseEnv_UnsetsWhatTheLauncherLacks` — pin the blank |
| `tools/muxcode/bus/busdir_env_test.go` | The bus-base pair tests |
| `config/nvim/init.lua` | The managed editor's config; `undofile` at line 28 — read only |
| `scripts/test-durable-state-location.sh` | MUX-208's script: checks panes agree on the bus directory, with an absolute `XDG_STATE_HOME` exported — the unset case is not covered |
| `scripts/test-pane-state-env.sh` | New — this spec's integration test |
| `docs/configuration.md`, `CLAUDE.md` | Where the bus directory path and the session environment are described |

## Implementation

### Phase 1: Measure

- [x] On a private tmux server with a scratch `HOME`, launch a session with `XDG_STATE_HOME` unset; record the variable in the first pane, a later window and a split, and the `tmux show-environment` line
- [x] In one of those panes run a headless Neovim edit with `undofile` on, from a scratch working directory; record what appears under it
- [x] Record every road by which a pane or worker receives the variable (session creation, `set-environment`, spawn launch, daemon start) and whether each carries the empty value
- [x] Record any other tool found writing under the working directory because of the empty value, or that none was found and what was tried

#### Phase 1 results (2026-11-24)

Measured by the implement worker of run 1795531653 with a one-off script, `/tmp/mux237-measure.sh`,
run three times by the run agent. The third run (`/tmp/mux237-measure3.log`, 09:52:50) is on the
script's final revision (09:52:10) and is the source of every line below unless marked. The files
are under `/tmp` and will not last; what they show is recorded here.

Setup: muxcode (`devel`) and the claude-stub built from the tree, a private tmux server (tmux 3.8),
a scratch `HOME`, `XDG_STATE_HOME` and `MUXCODE_BUS_BASE` unset in the launcher, a real
`muxcode launch` with `MUXCODE_WINDOWS="build edit"`, NVIM v0.12.6. The launch exits 1 at its final
attach (`open terminal failed: not a terminal`), after the session is built. Each reading is the
pane's own shell printing the variable — `ps -E` shows no environment on this macOS.

**The variable per pane**

| Where | Road | `XDG_STATE_HOME` | `MUXCODE_BUS_BASE` |
|-------|------|------------------|--------------------|
| First window, console pane (`build.0`) | `new-session -e` | set, empty | set, empty |
| A later window the launcher created (`build.0` with edit first) | session environment | set, empty | set, empty |
| A window created after launch, and its split | session environment | set, empty | set, empty |
| `tmux show-environment -t <session> XDG_STATE_HOME` | — | `XDG_STATE_HOME=` | — |
| `tmux show-environment -g XDG_STATE_HOME` | — | `unknown variable` | — |

Qualifications: the launcher-created later window was read in the second run only
(`/tmp/mux237-measure2.log`, 09:51:46 — a script revision before the final one); in the third the
window order was `build`, `edit` and `build.0` was the first window. The panes running the agent
stub and the edit console gave no reading in any run — they exit on the interrupt the probe sends.

**Neovim in an empty-value pane** — working directory a scratch dir, the edited file elsewhere,
`undofile` on:

| Reading | Result |
|---------|--------|
| `stdpath("state")` | `nvim`; `muxcode` with `NVIM_APPNAME=muxcode` — both relative |
| Created under the working directory | `muxcode/nvim.log`, `muxcode/shada/main.shada`, `muxcode/swap/`, `muxcode/undo/` with one undo file named for the edited file's full path, `nvim/nvim.log`, `nvim/shada/main.shada` |
| Undo files under the scratch `~/.local/state` | 0 |
| Control: an absolute `XDG_STATE_HOME` in the session environment, same road | Nothing under the working directory; 1 undo file under the absolute directory |

**Roads**

| Road | Carries the empty value |
|------|-------------------------|
| Session creation, `new-session -e` (`launcher.go:280`) | Yes — first window measured empty |
| `set-environment` at launch (`launcher.go:287`) | Yes — session environment `XDG_STATE_HOME=`; later windows and a split empty |
| Spawn worker (`launchSpawnWindow`, `bus/spawn.go:401` — plain `new-window` and `split-window`, no `-e`, no `XDG_STATE_HOME` on the launch line) | Yes, by inheritance — measured on that `new-window`/`split-window` road; `muxcode spawn start` itself was **not run** |
| Daemon start (`daemon.go:345`) | Yes — a stale absolute session value, `/stale/abs/state`, was replaced by `XDG_STATE_HOME=`, and a window created after it was empty |

No other road was found. With a value planted in the server's global environment and the session
key removed, a new window received the global value — the reason the doc comment gives for not
unsetting the key, and the stale-override criterion's case.

**Other tools.** A `strings` scan for `XDG_STATE_HOME` found it in `less` 668 and `gh` 2.103.3,
and not in `git`, `tmux`, `bash`, `zsh`, `python3`, `node` or `fzf`; `lazygit`, `tig`, `starship`
and `htop` are not installed here. `less` (a search, then quit) and `gh --version`, run in the
empty-value pane from a fresh scratch directory, created nothing under it. No tool other than
Neovim was found writing there; a `strings` scan and two commands are what was tried.

**What this settles.** The blank in `busBaseEnvPlan` is the only source found, by measurement on
every road it feeds. It is still not proven by a build without the blank — that is Phase 4's
negative control.

**Seen in this checkout during the run.** An untracked `nvim/nvim.log` (0 bytes, 09:48:40)
appeared at this repository's root, before the first measurement run (09:50) — a plain `nvim`
started from a pane of the live session, the defect itself. Which pane wrote it was not traced. It
is not part of this spec's change and must not be staged.

### Phase 2: Fix the export

- [x] `XDG_STATE_HOME` is exported as the launcher's resolved state base when the launcher has no absolute value; never as an empty string
- [x] `MUXCODE_BUS_BASE` keeps its blank; the two keys' handling is separated in `busBaseEnvPlan`
- [x] The doc comment states the rule and why empty is not used, naming Neovim
- [x] `TestBusBaseEnvArgs` and `TestExportBusBaseEnv_UnsetsWhatTheLauncherLacks` updated, and a unit test added for the unset, empty and relative launcher values

#### Phase 2 results (2026-11-24)

| What | As built |
|------|----------|
| `stateBase()`, new in `bus/config.go` | An absolute `XDG_STATE_HOME` as given, else `$HOME/.local/state`; empty and relative values ignored. `stateDir()` is now `stateBase()` plus `muxcode`, so the exported value is by construction the parent of `StateDir()` |
| `busBaseEnvPlan` | `MUXCODE_BUS_BASE`: set when absolute, else blanked, as before. `XDG_STATE_HOME`: always set to `stateBase()`, never blanked. It is left out — neither set nor blanked — only when no home resolves, where `StateDir` refuses too; a stale session value would stand in that case, which no test covers |
| `TestExportBusBaseEnv_UnsetsWhatTheLauncherLacks` | Renamed `TestExportBusBaseEnv_OverridesStaleSessionValues`; absolute and unset launcher values, each asserting the exact `set-environment` calls |
| `TestBusBaseEnvArgs` | Expects `XDG_STATE_HOME=<HOME>/.local/state` for an empty launcher value |
| `TestBusBaseEnvPlan_XDGStateHomeNeverEmpty`, new in `bus/busdir_env_test.go` | Unset, empty, relative and absolute launcher values; the exported base must be `StateDir()`'s parent and the only blank `MUXCODE_BUS_BASE` |
| `TestBusBaseEnvPairs` | Its negative control now reads a relative `MUXCODE_BUS_BASE` |

Evidence: the run's build, test and review nodes succeeded on this tree — `./test.sh` reported
`modules=2 ran=2 passed=2 failed=0 never-executed=0` at 10:03, on a request sent at 10:01, after
the last source edit (10:00:29). That the new test fails on the old blank is the worker's
statement, not re-run here. No pane has been measured on the fixed binary yet: criteria 2–5, 7
and 8 are Phase 4's.

### Phase 3: Running sessions and docs

- [x] A daemon starting on the fixed binary replaces an empty session value; covered by a unit test on the calls it makes
- [x] `docs/configuration.md` states what panes receive for `XDG_STATE_HOME`, that open panes keep their environment until relaunched, and the cleanup a user affected before the fix should do
- [x] `CLAUDE.md`'s bus directory path entry updated where it describes the exported environment

#### Phase 3 results (2026-11-24)

| What | As built |
|------|----------|
| `ExportSessionBusEnv`, new in `bus/config.go` | The daemon's start-time export: `MUXCODE_BUS_DIR`, then `ExportBusBaseEnv`. `daemon.Run` calls it in place of the two calls it made before — the same tmux calls, now under one name a test can drive |
| `TestExportSessionBusEnv_CorrectsEmptyXDGStateHome`, new in `bus/tmux_test.go` | With the daemon's own variable unset, asserts the three `set-environment` calls, `XDG_STATE_HOME` as `<HOME>/.local/state`. The test asserts the calls made, not a session seeded with the empty value — that is Phase 4's, and criterion 7 stays open until then |
| `docs/configuration.md` | The `XDG_STATE_HOME` row says panes always receive an absolute value. Under [Bus directory](../../configuration.md#bus-directory-per-session), a new "What panes receive" section: the per-key rule, why empty is not used, the three roads, that an open pane keeps its environment and how to check one, and the cleanup for a user affected before the fix. The sentence saying both keys were copied "only when set to an absolute path" is gone |
| `CLAUDE.md` | The bus directory path entry states the `XDG_STATE_HOME` rule, the open-pane limit and `ExportSessionBusEnv` (edit); one sentence after it reworded by plan |

Evidence: `./test.sh` reported `modules=2 ran=2 passed=2 failed=0 never-executed=0` at 10:09, on
a request sent at 10:07, after the last source edit (10:05:31). Review at 10:10: 0 must-fix,
0 should-fix, 1 nit — the comment at `tmux_test.go:107` says "the bus-base blank" where the test
sets an absolute `MUXCODE_BUS_BASE`. The nit is not fixed in this tree.

### Phase 4: Integration test

- [x] Create `scripts/test-pane-state-env.sh`: muxcode built from the tree, scratch `HOME`, private tmux server, coverage floor; skipped with a note where `nvim` is not installed, never reported green
- [x] Launcher with `XDG_STATE_HOME` unset → first pane, a later window and a split hold an absolute value; `nvim --headless` prints an absolute `stdpath("state")` with and without `NVIM_APPNAME=muxcode`
- [x] A headless Neovim edit with `undofile` on, from a scratch working directory in a pane → nothing created under it, and the undo file found under the scratch state directory
- [x] A stale absolute value planted in the tmux session and server environment → a later pane's `muxcode bus-dir` equals the launcher's
- [x] A session seeded with the empty value, then a daemon start → a new window holds the absolute value
- [x] Negative control: a build from a copy of the tree with the blank restored → the pane holds the empty value and the edit creates `./nvim` under the working directory
- [x] Run the script and record its counts here; run `scripts/test-durable-state-location.sh` and record that it still passes

#### Phase 4 results (2026-11-24)

| Run | Result |
|-----|--------|
| `bash scripts/test-pane-state-env.sh` | **23 passed, 0 failed (23 checks, floor 23)**, exit 0 — run task `1795533390-run-e3c4cb4a`, log written 10:16:27, after the script's last edit (10:16:06) and the last source edit (10:05:31) |
| Its earlier runs | First: 20 passed, 1 failed — a bug in the script, not the fix: `NVIM_APPNAME` was placed on the `cd`, not on `nvim`, so the undo file was looked for under the wrong app name. Second, corrected (task `1795533217-run-5fdf065b`): 21 passed, 0 failed at floor 21, before section E was added |
| `bash scripts/test-durable-state-location.sh` | **53 passed, 0 failed**, coverage floor met, exit 0 — run task `1795533253-run-3c8e071f`, log written 10:14:11 |

What the script's five sections assert, each read by the pane's own shell or process:

| Section | Checks | Assertion |
|---------|--------|-----------|
| Build | 2 | muxcode and the claude-stub from the tree; a second muxcode from a copy with the blank restored |
| A — a real `muxcode launch`, launcher's variable unset | 9 | The session environment, the first window's pane, a later window's pane and a split hold `$HOME/.local/state`, `MUXCODE_BUS_BASE` empty; `stdpath("state")` absolute with and without `NVIM_APPNAME=muxcode`; an `undofile` edit from a scratch working directory leaves it empty and its undo file is under the state directory |
| B — stale values | 4 | A different absolute value in the server's global environment, planted before the launch, and one in the session environment before a daemon restart: a later pane's `muxcode bus-dir` equals the launcher's both times |
| C — a session seeded with the empty value | 3 | A daemon start on this binary writes the absolute value; a new window holds it |
| E — a spawn worker | 2 | `muxcode spawn start research` launches a worker; its agent — a stub set as the research CLI that writes its own environment and idles at a `❯` — holds `$HOME/.local/state`, `MUXCODE_BUS_BASE` empty. Runs after C, on a session whose daemon is this binary |
| D — negative control | 3 | A daemon from the blank-restored build writes `XDG_STATE_HOME=`, a new window holds the empty value, and the same edit creates `./nvim` under the working directory |

Qualifications:

- **The spawn worker was added after the first record.** At 21 checks the script read only a plain
  `new-window` and a `split-window`, and criterion 2 — which names a spawn worker's pane — was left
  open. Section E starts a real worker; the reading is the worker's agent process, a stub, not a
  real agent CLI. The negative control does not start a spawn worker.
- **The negative control is on the daemon-start road**, not a launch: the blank-restored binary
  runs as the session's daemon. It shows the pane-environment and working-directory assertions
  fail on the old plan; it does not launch a session with that binary.
- **Without `nvim` the script exits 2** with a `SKIP` line; `scripts/test-all.sh` has no skip
  status of its own, so that reads as a failure there, never green.
- The undo-file check runs under `NVIM_APPNAME=muxcode` only.
- The logs are under `/tmp` (`mux237-pane-state-env3.log`, `mux237-durable.log`).
  `test-durable-state-location.sh` was not re-run after section E; no source file changed between.

## Related

- [MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md) — moved bus state under `XDG_STATE_HOME` and began exporting it to panes; the blank is from its follow-up commit
- [MUX-223](../completed/MUX-223-project-memory-resolves-against-the-callers-cwd.md) — the same shape of defect in muxcode's own code: state resolved relative to the working directory
- [MUX-236](./MUX-236-test-all-leaves-stray-and-modified-files-in-the-checkout.md) — the other defect about files left in a checkout, closed 2026-11-24

## Status

**Complete** — 27/27. Closed out 2026-11-24 by `55-defect-backlog-loop` run 1795531653, ahead of
its ship step. Filed 2026-11-19 on the user's request, relayed by edit, priority High.
Activated 2026-11-24 by `55-defect-backlog-loop` run 1795531653 (user-launched). Phases 1 and 2
done the same day: the measurement, then the export fix with its unit tests (criteria 1, 6 and 9;
criterion 6 at unit level). Phase 3 done too: the daemon-start export under one name with its
unit test, `docs/configuration.md` and `CLAUDE.md`. Phase 4 done: `scripts/test-pane-state-env.sh`
passes 23/23 — a real spawn worker's pane included, which closed criterion 2 — and
`test-durable-state-location.sh` 53/53. The run's test and review of Phase 4 followed: `./test.sh`
passed both modules at 10:18 and review at 10:19 returned 0 must-fix, 0 should-fix, 1 nit — the
`tmux_test.go:107` comment from Phase 3, still unfixed. Nothing changed after the 23/23 run.

Left for the user, outside this spec: deleting the stray `nvim/` and `muxcode/` directories
already written into project repositories (this one's root holds both), and panes opened before
the fixed daemon starts, which keep the empty value until relaunched.
