# MUX-202: Self-Upgrade From the Quick Menu — Check, Download, Rebuild, Restart Daemons

**Tracking:** _(no GitHub issue yet — a feature, filed on the user's request; an issue is the user's call)_

**Priority: High — requested 2026-10-06 to be worked on now.**

A new `Upgrade MuxCode` entry in the `prefix + b` quick menu (the bottom-left MuxCode menu) runs a
**hot fix / reload of muxcode itself**: check the latest release on GitHub, download it when it is newer
than the installed build, rebuild and install locally, then restart the session daemons — each step
shown as it runs in a modal of the same shape as the Provider/model reload. The user's words:

> *"This function will be a hot fix/reload to muxcode. It will check the latest version on github,
> download it if it is newer, re-build locally, then will restart the daemons. It should have a modal
> window that displays the status of each step similar to the Provider/model reload."*

## Context

### Why

Today an upgrade is four manual acts in two places: pull or fetch the muxcode checkout, run
`./build.sh`, run `muxcode upgrade-daemons` from a terminal that can see `ps` (the build agent's
sandbox cannot — MUX-161), and `tmux source-file` the reinstalled `tmux.conf`. On 2026-10-06 the
session daemon sat on a **pre-rebase build** (`v0.1.19-9-gfa1f56a`) while HEAD was `v0.1.20-9-g7d339be`
until the user asked for the daemons to be upgraded, and the answer needed a rebuild first. Every
MuxCode session on a machine that is not the muxcode checkout has no road to a newer release at all
short of cloning it.

### What exists to build on (verified at `7d339be`)

| Piece | Where | What it gives this feature |
|-------|-------|----------------------------|
| Stamped identity | `bus/version.go` — `BuildInfo()`, `Info.SameBuild`, `CompareSemver`, `splitDescribe`; `muxcode version --json`, `--at-least` | The installed version and a comparator that understands `git describe` output (`v0.1.20-9-g7d339be` is *past* `v0.1.20`) |
| Daemon re-exec | `bus/upgrade.go` — `UpgradeDaemons`, `UpgradePlan`, `UpgradeResult`, `EnsureSessionDaemonCurrent`; `cmd/upgrade.go` | The restart step, per session, with the daemon-vs-installed delta; needs `ps`; relaunches in each session's own dir |
| Install | `Makefile` — `install: build`; `VERSION ?= $(shell git describe …)`; installs binary to `BINDIR`, agents, skills, configs, `tmux.conf`, nvim config to `CONFIGDIR` | `make install VERSION=<tag>` works from a source tree with no `.git` |
| Releases | `.github/workflows/release.yml` + `auto-release.yml` — every merged PR cuts `vX.Y.Z`; assets `muxcode-<os>-<arch>` ×4 + `sha256sums.txt`; GitHub attaches the source tarball | `GET /repos/mkober/muxcode/releases/latest` names the tag; `archive/refs/tags/<tag>.tar.gz` is the source |
| Modal registry | `bus/modal.go` — `ModalConfig{Name, Title, Width, Height, Command, Sizes}`; `muxcode modal open <name>` | One more entry, same sizes as `provider`/`restart` |
| Quick menu | `config/tmux.conf` — `bind b display-menu … "Restart Agents" A "run-shell 'muxcode modal open restart'"` | One more line |
| Progress renderer | `tui/provider_select.go` — `renderBatchProgress(roles, results, total, done, verb, pendingFooter, width)`: ✓/✗/⟳/○ rows, a bar, done/pending footer — **pure, snapshot in, string out** | The shape the user asked for; generalise rows from "agent" to "step" |
| Restart modal | `tui/restart_select.go`, `cmd/restart_select.go` (MUX-139 Phase 5) | The precedent for a modal that runs a bus operation inside the TUI and reports per row |
| Control-pane recycle | `ControlPanesPredate()` + `control-panes-ready` marker | A daemon restarted under a live session recycles the panes that predate it — the existing contract this feature relies on, not changes |
| HTTP seams | `ollama.go` (`HTTPClient *http.Client`, nil in production), `atlassian.go`, `health.go`; tests use `newPipeServer`, never a socket | The pattern for a testable GitHub client |

Nothing records **where the muxcode source lives**: `findMuxcodeBinary` locates the binary only. A
session running in another repo has no checkout to rebuild from — which is why the download is a
source tarball into a cache, not a `git pull` ([Decision 1](#decision-1--source-tarball-into-a-cache-not-a-git-pull)).

## Requirements

### Acceptance criteria

**Menu and modal**

- [x] `prefix + b` shows **`Upgrade MuxCode`** (key `U`) beside `Restart Agents`; it opens a modal registered as `upgrade` in `bus/modal.go` with the `provider`/`restart` sizes (Phase 4: `config/tmux.conf`, `bus/modal.go`, `muxcode upgrade-ui`)
- [x] The modal shows **one row per step** — `Check`, `Download`, `Build`, `Install`, `Verify`, `Restart daemons`, `Reload tmux config` — as pending `○`, running `⟳`, done `✓` or failed `✗` with the failure's cause wrapped to the pane, a progress bar and a footer, rendered by a **pure** function (snapshot in, string out) that honours both width and height (Phase 4: `renderUpgradeProgress`/`renderUpgradeDone` over `upgradeProgressView`, through the shared `renderBatchRows`; `fitUpgrade` degrades by height; `TestRenderUpgradeProgress_DegradesToHeight`, `TestRenderUpgradeDone_FailureWrapsToNarrowWidth`)
- [x] Before anything mutates, the modal **stops at a confirm** stating the consequence — `installed vA → latest vB; rebuilds and installs to <BINDIR>/<CONFIGDIR>, then restarts N daemon(s): <sessions>` — and **re-checks the latest release and the daemon list at execution**, not at render (Phase 4: `renderUpgradeConfirm` + `upgradeConsequence`/`daemonClause`; `startRecheck` → `confirmChanged` on `⏎`/`f`; the confirmed tag and sessions then travel to the worker and are enforced there — `TestUpgradeUI_RecheckRefusesAStaleConfirm`, `TestSelfUpgradeRefusesAReleaseOtherThanConfirmed`, `TestSelfUpgradeRestartsOnlyConfirmedDaemons`)
- [x] An **up-to-date** result is an explicit state, not an empty body: `installed vA is current (latest vB)`; a dev build past the latest tag reads `installed v0.1.20-9-g7d339be is ahead of the latest release v0.1.20` ([Decision 2](#decision-2--newer-means-a-release-tag-past-the-installed-version-force-rebuilds-anyway)) (Phase 4: `verdictLines` renders `UpgradeCheck.Summary()` with header and footer; `TestRenderUpgradeConfirm_UpToDateAndAheadAreExplicit`)
- [x] The footer advertises every key: `⏎ Upgrade`, `f Force rebuild`, `q Quit`; while running, `q` closes the modal and the upgrade **continues in the background**, as the reload modal does (Phase 4: the run is a detached child; `TestUpgradeUI_KeysFollowTheFooter`, `TestUpgradeUI_PollFollowsTheEventsFile`)
- [x] State is readable **without colour** — the glyph carries it; no inline escapes outside `tui/styles.go` (Phase 4: screen escapes moved to `styles.go`; `TestRenderUpgradeProgress_StatesReadableWithoutColour` asserts over `StripAnsi`)

**Pipeline**

- [x] `Check` reads the installed identity (`BuildInfo()`) and the latest release from GitHub (`GET /repos/mkober/muxcode/releases/latest`, stdlib `net/http`, 10 s timeout, `GITHUB_TOKEN`/`GH_TOKEN` sent when set to lift the unauthenticated rate limit); a network or API failure fails `Check` with the HTTP status and body excerpt and runs nothing else (Phase 1: `ReleaseClient.LatestRelease`, `CheckUpgrade`; the token is sent only to `api.github.com`, never to an overridden URL)
- [x] `Download` fetches the release tag's **source tarball** into `~/.cache/muxcode/upgrade/<tag>/` (`XDG_CACHE_HOME` honoured), records its size and SHA-256 in the step row and the lifecycle log, and **skips the fetch when the extracted tree for that tag is already present and complete** ([Decision 1](#decision-1--source-tarball-into-a-cache-not-a-git-pull)) (Phase 2: `fetchSource`, `cachedSource`; the row reads `cache hit — <size>, sha256 <sum>` on reuse. `TestSelfUpgradeReusesCompleteCache`. Lifecycle rows arrive with the CLI wiring in Phase 3)
- [x] `Build` runs `make install VERSION=<tag>` in the extracted tree with stdout+stderr captured to `<cache>/<tag>/build.log`; on failure the row shows the last lines and names the log; prerequisites (`go`, `make`, `tar`) are checked **before** `Download` so a machine without a toolchain fails at `Check` with a named missing tool and downloads nothing (Phase 2: `runBuildStep` + `runInstallStep` via `runMake`; `runCheckStep` fails on `ToolsErr()` before `Download`. `TestSelfUpgradeMissingToolFailsAtCheck`, `TestSelfUpgradeBuildFailureKeepsInstalledBinary`)
- [x] `Verify` runs the **freshly installed** binary — `<BINDIR>/muxcode version --json` — and fails if its `version` is not the tag (a stale `PATH` entry or a different `BINDIR` must be caught here, not discovered later) (Phase 2: `runVerifyStep` — version by path, then `muxcode` on `PATH` must be the same file. `TestSelfUpgradeVerifyMismatchStopsPipeline`, `TestSelfUpgradeVerifyRefusesShadowedInstall`)
- [x] `Restart daemons` **executes the new binary's** `upgrade-daemons`, never the running process's `bus.UpgradeDaemons`: the modal is the old binary, whose `BuildInfo()` would read every daemon as current ([Decision 3](#decision-3--restart-every-sessions-daemon-through-the-new-binary)); each session is a sub-row with its `VersionDelta`; an unreadable `ps` fails the step with the exact error (Phase 3: `runDaemonsStep`, `parseDaemonLines`; `TestSelfUpgradeDaemonStepSurfacesFailures`, `TestSelfUpgradeDaemonRelaunchResolvesInstalledBinary`)
- [x] `Reload tmux config` sources the reinstalled `~/.config/muxcode/tmux.conf` into the running tmux server; the done footer names the version delta and the follow-up the upgrade does **not** do: *agents keep running until restarted — `Restart Agents` (prefix + b, A)* (Phase 3: `runTmuxStep`, `UpgradeState.DoneSummary`; the modal's footer in Phase 4 renders the same string)
- [x] Every step writes a lifecycle row (`upgrade-check`, `upgrade-download`, `upgrade-build`, `upgrade-verify`, `upgrade-daemons`, `upgrade-tmux`, `upgrade-done` / `upgrade-failed`) with the installed and target versions (Phase 3: `upgradeStep.Event`, `UpgradeState.logLifecycle` — plus `upgrade-install`, since Build and Install are separate steps; `TestSelfUpgradeWritesLifecycleRows`)
- [x] **Negative controls:** an up-to-date install runs no step past `Check` and touches no file; a failed `Build` leaves the installed binary untouched (`make install` fails before `install -m 755`, and `Verify` would catch a partial copy); a failed `Verify` **does not** restart daemons (Phases 2–3: `TestSelfUpgradeStopsAtCheckUnlessNewerOrForced`, `TestSelfUpgradeBuildFailureKeepsInstalledBinary`, `TestSelfUpgradeVerifyMismatchStopsPipeline` — the pipeline stops at the first failure, so the daemon step is unreachable after a failed `Verify`)

**CLI twin**

- [x] `muxcode upgrade [--check] [--force] [--json]` runs the same pipeline non-interactively, printing one line per step; `--check` stops after `Check` with exit 0 (current), 10 (newer available) or 1 (error) so a cron or a script can poll it; `--force` rebuilds and reinstalls the latest release even when current (Phases 1 + 3: `cmd/upgrade_self.go`; a full run exits 0 on success or an up-to-date install and 1 on a failed step; `--json` is `UpgradeCheck` with `--check`, else `upgradeReport`. `TestUpgradeCheckExit`, `TestWriteStepResult`, `TestWriteUpgradeReport`)
- [x] `muxcode upgrade-ui` is the modal's command (the `provider-select`/`restart-select` shape) (Phase 4: `cmd.UpgradeUI` → `tui.NewUpgradeUI().Run()`, wired in `main.go`; `bus/modal.go` `upgrade` entry's `Command` is `muxcode upgrade-ui` — overlooked when Phase 4 was ticked, corrected 2026-10-07 09:28)

**Docs and test**

- [x] Docs: [`docs/agent-bus.md`](../../agent-bus.md#muxcode-upgrade) (`muxcode upgrade`, `upgrade-ui`, the modal), [`docs/configuration.md`](../../configuration.md#self-upgrade) (`MUXCODE_UPGRADE_*` overrides, cache dir, token), [`docs/architecture.md`](../../architecture.md#self-upgrade-flow) (self-upgrade flow beside the daemon-upgrade contract), `CLAUDE.md` build/install table row, `README.md` quick-menu list (Phase 5, 2026-10-07)
- [x] `bash scripts/test-self-upgrade.sh` passes — hermetic, no network ([Phase 6](#phase-6-integration-test)) (run agent 2026-10-07: 46 passed, 0 failed, floor 46, exit 0)

### Technical approach

A new `bus/selfupgrade.go` owns the pipeline as a list of **steps** — `UpgradeStep{Name, Run func(*UpgradeState) error}` — driven by `RunSelfUpgrade(opts, progress)` where `progress func(i int, result StepResult)` is the same callback shape as `ReloadBatch`'s `ReloadProgress`, so the modal reuses the batch-progress renderer with `StepResult` rows (`Name`, `Success`, `Note`, `Error`, `Duration`). The GitHub client is `bus/release_client.go`: `LatestRelease(ctx) (Release, error)` over an optional `*http.Client` (nil in production), base URL overridable by `MUXCODE_UPGRADE_API_URL` and the tarball URL by `MUXCODE_UPGRADE_TARBALL_URL` so tests and the integration script point both at local files — `newPipeServer` in Go, a `file://` handler in the script. Downloads stream to a `.partial` file and rename on completion. The build step is `exec.Command("make", "install", "VERSION="+tag)` with `Dir` set to the extracted tree and `BINDIR`/`CONFIGDIR` passed through from the environment when set, so a user with a custom prefix is honoured. The daemon step execs `filepath.Join(bindir, "muxcode")` with `upgrade-daemons` and parses its per-session lines. The TUI (`tui/upgrade_ui.go`) has three screens — confirm, progress, done — and one renderer per screen, each pure; the confirm screen re-runs `Check` and `ListDaemonProcs` on Enter before it mutates. `cmd/upgrade_self.go` hosts `muxcode upgrade` and `upgrade-ui`; `bus/modal.go` and `config/tmux.conf` gain one entry each.

### Key files

| File | Role |
|------|------|
| `tools/muxcode/bus/selfupgrade.go` (new) | Step pipeline, state, cache paths, lifecycle rows |
| `tools/muxcode/bus/release_client.go` (new) | GitHub latest-release client with the `*http.Client` seam |
| `tools/muxcode/bus/version.go` | `CompareSemver` for newer-than; `BuildInfo` for installed |
| `tools/muxcode/bus/upgrade.go`, `cmd/upgrade.go` | The daemon restart the pipeline shells out to |
| `tools/muxcode/tui/upgrade_ui.go` (new), `tui/provider_select.go` | Modal screens; `renderBatchProgress` generalised to step rows |
| `tools/muxcode/cmd/upgrade_self.go` (new) | `muxcode upgrade`, `muxcode upgrade-ui` |
| `tools/muxcode/bus/modal.go`, `config/tmux.conf` | `upgrade` modal entry; `Upgrade MuxCode` menu line (key `U`) |
| `Makefile` | Already takes `VERSION=`; may need `BINDIR`/`CONFIGDIR` passthrough confirmed |
| `scripts/test-self-upgrade.sh` (new) | Integration test |

## Implementation

### Phase 1: Release check

- [x] `bus/release_client.go`: `LatestRelease` with the `*http.Client` seam, token header when set, 10 s timeout, status+body error; `Release{Tag, TarballURL, PublishedAt}` (`ReleaseClient{APIURL, TarballURL, Token, HTTP}`, `DefaultReleaseClient` from `MUXCODE_UPGRADE_API_URL` / `MUXCODE_UPGRADE_TARBALL_URL` / `GITHUB_TOKEN`→`GH_TOKEN`; body capped at 64 KiB, 200-rune excerpt; a 403/429 without a token names the token fix; the token travels **only** to `https://api.github.com`; the production client also serves `file://` for the Phase 6 script; the tag must match `vMAJOR.MINOR.PATCH[-pre]` — one safe path component, since it names the cache dir)
- [x] `bus/selfupgrade.go`: `CheckUpgrade() (Check, error)` — installed `BuildInfo()`, latest release, verdict `current` / `ahead` / `newer` via `CompareSemver` ([Decision 2](#decision-2--newer-means-a-release-tag-past-the-installed-version-force-rebuilds-anyway)); prerequisite probe for `go`, `make`, `tar` (`CheckUpgrade(ctx, rc)`, `ClassifyUpgrade`, `UpgradeCheck{Installed, Latest, Verdict, Reason, MissingTools}`, `Summary()`, `ToolsErr()`; a fourth verdict `unknown` for a build with no semver rank — `devel`, a bare commit — carrying the comparison error as its reason; a failed lookup returns the error with `Installed` still filled)
- [x] `muxcode upgrade --check [--json]` with exit codes 0 / 10 / 1 (`cmd/upgrade_self.go`, wired in `main.go`; `newer` with a build tool missing exits **1**, not 10 — a poller must not be told to run an upgrade that would fail at Check; `unknown` exits 1 rather than guess; `--json` on a failed lookup emits `{installed, error}` so a script cannot misread empty release fields; without `--check` the command refuses until the pipeline phases land)
- [x] Tests over `newPipeServer`: newer, current, ahead (dev build), 404, 403 rate-limited with body excerpt, timeout; **negative control:** a dev build past the tag is never `newer` (13 tests: `TestCheckUpgradeVerdicts`, `TestCheckUpgradeLookupFailureKeepsInstalled`, `TestCheckUpgradeProbesBuildTools`, `TestLatestReleaseStatusErrors`, `TestLatestReleaseTimeout`, `TestLatestReleaseTokenOnlyToGitHub`, `TestLatestReleaseRejectsTagThatIsNotARelease`, `TestLatestReleaseTarballURL`, `TestLatestReleaseReadsFileURL`, `TestReleaseClientTimeout`, `TestDefaultReleaseClientEnv`, `TestUpgradeCheckExit`, `TestWriteUpgradeCheckJSON`)

#### Phase 1 verification note

Verified 2026-10-06 15:55 by plan from the working tree (run `1791315389`, launched by the user). The
run's test node returned **success** on the full suite (`./test.sh`, 63 s) and review passed with 0
must-fix. Two refinements beyond the spec text, both kept: a fourth verdict `unknown` for unstamped
builds (the spec named three), and `--check` exiting 1 rather than 10 when a newer release exists but a
build tool is missing — the exit code is a promise that running the upgrade would work. The tag
validation (one safe path component) closes a path-traversal shape the spec had not named.

### Phase 2: Download, build, install, verify

- [x] Cache dir under `XDG_CACHE_HOME`/`~/.cache/muxcode/upgrade/<tag>/`; streamed download to `.partial`, SHA-256 and size recorded; skip when the tree is complete (`upgradePaths` — options, then `XDG_CACHE_HOME`/`BINDIR`/`PREFIX`/`CONFIGDIR`, then the Makefile's defaults, every path made absolute **before** anything mutates; `ReleaseClient.Download` streams to `.partial`, hashes as it writes, 5 min timeout, 256 MiB cap, no token; `fetchSource`/`extractSource` unpack through `src.partial` with `--strip-components=1` and refuse a tree with no top-level `Makefile`; `download.json` is removed first and written **last**, so a cache hit is exactly a completed extraction)
- [x] `make install VERSION=<tag>` in the extracted tree, log captured; `BINDIR`/`CONFIGDIR` passthrough (`runMake`: `make build` then `make install`, each with `VERSION=`, `DATE=` (stamped once so install links what build compiled), `BINDIR=`, `CONFIGDIR=`; `build.log` truncated by build, appended by install, last 5 lines quoted on failure; `GIT_CEILING_DIRECTORIES` + `-buildvcs=false` so an enclosing repository cannot lend the binary its commit; 15 min timeout)
- [x] `Verify` via the installed binary's `version --json`; mismatch fails with both versions named (`runVerifyStep` runs `<BINDIR>/muxcode` by path, 10 s; then requires `muxcode` on `PATH` to be that **same file** — a shadowing `PATH` entry fails here rather than as a daemon restart onto stale code; `BINDIR` absent from `PATH` is a note)
- [x] Tests: tarball from a local file URL, a fake `make` on `PATH` that records its args and writes a stub binary whose `version --json` the test controls; **negative control:** a build that fails leaves the previous binary bytes in place; a verify mismatch stops the pipeline (`bus/selfupgrade_pipeline_test.go`, 10 tests: `TestSelfUpgradeInstallsNewerRelease`, `…ReusesCompleteCache`, `…StopsAtCheckUnlessNewerOrForced`, `…MissingToolFailsAtCheck`, `…BuildFailureKeepsInstalledBinary`, `…VerifyMismatchStopsPipeline`, `…VerifyRefusesShadowedInstall`, `…RefusesOverlappingRun`, `…RelativePathsResolveAgainstCaller`, `TestUpgradePathsFollowMakefileDefaults`)

#### Phase 2 verification note

Verified 2026-10-06 16:40 by plan from the working tree (run `1791315389`, retried from `build` by the
user at 15:36; Phase 1 committed as `b143299`). The test node returned **success** on the full
repository suite and review passed with 0 must-fix after one round that asked for a stable per-user
lock, an overlap test and path normalisation — all in the tree: `lockSelfUpgrade` is one non-blocking
`flock` at `~/.config/muxcode/upgrade.lock`, held from the first mutating step to the end, refused
rather than waited on. Two additions beyond the spec, both kept: `Build` and `Install` are **separate
steps** (`make build` then `make install`), so a tree that does not compile never reaches `install -m
755`; and `Verify` checks `PATH` as well as the installed file. A side fix rode along: the Codex
**test** role's sandbox now gets the Go caches (`codexWritableRoots`, `provider_codex.go`,
`CLAUDE.md` bullet), because this phase's `archive/tar`-shaped imports made `go vet` fail "package
… is not in std" on a read-only `GOCACHE` — MUX-160 Decision 1's shape. `muxcode upgrade` still
refuses without `--check`; the full pipeline is wired in Phase 3.

### Phase 3: Daemon restart, tmux reload, CLI

- [x] `Restart daemons` execs `<BINDIR>/muxcode upgrade-daemons`, parses per-session lines into sub-rows; `ps` failure surfaces verbatim ([Decision 3](#decision-3--restart-every-sessions-daemon-through-the-new-binary)) (`runDaemonsStep`, 2 min timeout, **`BINDIR` first on the helper's `PATH`** — `upgrade-daemons` relaunches each daemon as bare `muxcode`, so with `BINDIR` off `PATH`, which `Verify` allows, it would relaunch the old build or nothing (review must-fix, fixed with a regression test); `parseDaemonLines` turns each indented `<session>: <detail>` line into a `StepResult.Sub` row, `FAILED — ` marking failure; any other output line is a message, and a non-zero exit fails the step with those lines verbatim)
- [x] `Reload tmux config` via `tmux source-file`; skipped with a note when no tmux server is running (`runTmuxStep` sources `<CONFIGDIR>/tmux.conf`; "no server running" / "error connecting to" → `skipped — no tmux server is running`; no `tmux` binary → `skipped — tmux is not installed`; any other failure fails the step with tmux's message)
- [x] `muxcode upgrade [--force] [--json]` runs the full pipeline with one line per step; lifecycle rows per step (`cmd/upgrade_self.go`: `RunSelfUpgrade` with a progress callback printing `<Step>: <note>` and indented sub-rows; `--json` emits an `upgradeReport{installed, latest, verdict, upgraded, steps, error}`; exit 0 on success **and** on an up-to-date install, 1 on a failed step; `DoneSummary` names the version delta and `Restart Agents (prefix + b, A)`. Every step has an `Event` — `upgrade-check` … `upgrade-tmux` — written by `runUpgradeSteps` with `installed=<v> target=<tag>`, then `upgrade-done` or `upgrade-failed` naming the step)
- [x] Tests: a fake `muxcode` on `PATH` whose `upgrade-daemons` output the test scripts; **negative control:** a failed `Verify` never reaches the daemon step (`TestSelfUpgradeDaemonStepSurfacesFailures`, `TestSelfUpgradeDaemonRelaunchResolvesInstalledBinary` — removes `muxcode` from the caller's `PATH` and checks the helper still resolves the installed binary — `TestSelfUpgradeTmuxReloadSkipsWithoutServer`, `TestSelfUpgradeWritesLifecycleRows`, `TestWriteStepResult`, `TestWriteUpgradeReport`; the pipeline stops at the first failed step, so `TestSelfUpgradeVerifyMismatchStopsPipeline` now also proves the daemon step is never reached)

#### Phase 3 verification note

Verified 2026-10-06 16:58 by plan from the working tree (run `1791315389`; Phase 2 committed as
`2b74024`). The test node returned **success** on the full suite and review passed with 0 must-fix after
one round — the daemon helper's `PATH` must begin with the verified `BINDIR` — which is in the tree with
its regression test. With this phase the pipeline is complete end to end on the CLI: the only roads left
are the modal (Phase 4), the docs (Phase 5) and the integration script (Phase 6). The CLI's exit code is
0 for an up-to-date install, not 10 — only `--check` uses 10, because `--check` answers "should I run
the upgrade?" while a full run answers "did it succeed?".

### Phase 4: Modal and menu

- [x] `tui/upgrade_ui.go`: confirm / progress / done screens, each a pure renderer taking width and height; confirm re-runs `Check` and the daemon list on Enter; `f` forces; `q` detaches while the pipeline continues (`renderUpgradeConfirm` / `renderUpgradeProgress` / `renderUpgradeDone` over view structs, `fitUpgrade` picks the fullest form that fits the height — session sub-rows dropped first, then the step list folded to one status line, the footer always kept. `⏎`/`f` → `startRecheck` re-reads release and daemons; `confirmChanged` refuses a changed release, verdict, install path or daemon set and says which. The run is a detached `muxcode upgrade --events <file> --expect-tag <tag> --expect-session <s>…` child (`StartSelfUpgradeDetached`, `bus/selfupgrade_run.go`) — the **confirmed snapshot is bound to execution** (review must-fix): the worker's `Check` refuses any release but the confirmed tag before mutating ("nothing was changed; confirm again"), and `runConfirmedDaemons` restarts exactly the confirmed sessions, naming any daemon started since as not restarted. The modal polls the events file so `q` closes it while the run continues; a child that dies without writing its end is finished as failed)
- [x] Generalise `renderBatchProgress` row input from `ReloadResult` to a small interface or a `StepResult` adapter — one renderer, two callers, no copy (`tui/provider_select.go`: rows become `batchRow`; `renderFailureRow` is now `renderFailureRowIn(indent, name, cause, nameWidth, width)` — stacks the cause under the name when the columns leave it under 20 cells, and `wrapCause` cuts a word wider than its line (a log path) rather than let it break the modal border; `stepRows` adapts `StepResult`, sub-rows included. Three callers — provider reload, restart, upgrade — one renderer)
- [x] `bus/modal.go` `upgrade` entry; `config/tmux.conf` `Upgrade MuxCode` (key `U`); `muxcode upgrade-ui` (modal `upgrade`, title ` Upgrade MuxCode `, the `provider`/`restart` sizes; menu line after `Restart Agents`; `cmd.UpgradeUI` → `tui.NewUpgradeUI().Run()`, wired in `main.go`)
- [x] Tests per the [TUI checklist](../../tui-style.md): up-to-date and ahead states render an explicit body with header and footer; a failure row wraps to a narrow width without overflow; height is read and the step list degrades when it does not fit; **negative control:** at a comfortable size nothing degrades; `StripAnsi` output carries every state (`TestRenderUpgradeConfirm_UpToDateAndAheadAreExplicit`, `TestRenderUpgradeDone_FailureWrapsToNarrowWidth`, `TestRenderUpgradeProgress_DegradesToHeight` — comfortable 80×40 undegraded, 80×16 drops sub-rows, 80×8 folds, footer at every height — `TestRenderUpgradeProgress_StatesReadableWithoutColour`, `TestUpgradeUI_RecheckRefusesAStaleConfirm`, `TestUpgradeUI_KeysFollowTheFooter`, `TestUpgradeUI_PollFollowsTheEventsFile`; from the fix round: `TestSelfUpgradeRefusesAReleaseOtherThanConfirmed`, `TestSelfUpgradeRestartsOnlyConfirmedDaemons`, and short- and full-height failure rendering keeping the cause and log path. `TestUpgradeEventsRoundTripAndPartialLine`, `TestPruneUpgradeEventsKeepsFreshFiles` cover the events file)

#### Phase 4 verification note

Verified 2026-10-06 17:25 by plan from the working tree (run `1791315389`; Phase 3 committed as
`f3d2d12`). First pass: review **failed** with one must-fix — the modal re-checked the release and
daemons on Enter but then launched the detached worker with only the force flag, so the worker ran its
own `Check` and found its own daemons, and a release published or a daemon started in between became an
unconfirmed target — and one should-fix, the compact done screen dropping a failure's cause and log path.
Second pass, after the `fix` worker: the confirm travels as `--expect-tag` plus one `--expect-session`
per daemon (`UpgradeConfirmation`, `SelfUpgradeOptions.Confirmed`), the worker's `Check` refuses any other
release before anything mutates, `runConfirmedDaemons` restarts exactly the confirmed sessions and names
any started since, and the compact outcome reuses `progressSummary`'s failure row. Test node **success**
on the full suite including the upgrade-UI tests; review 0 must-fix. One consequence for Decision 3 is
recorded there. Screen escape sequences moved into `tui/styles.go`, per the TUI style rule.

### Phase 5: Docs

- [x] `docs/agent-bus.md` (`muxcode upgrade`, `upgrade-ui`, modal), `docs/configuration.md` (`MUXCODE_UPGRADE_API_URL`, `MUXCODE_UPGRADE_TARBALL_URL`, cache dir, `GITHUB_TOKEN`), `docs/architecture.md` (self-upgrade flow next to the daemon-upgrade contract), `CLAUDE.md` table row, `README.md` quick-menu list (2026-10-07: plan wrote `agent-bus.md` §§ `muxcode upgrade` + `muxcode upgrade-ui` after `upgrade-daemons`, `configuration.md` § *Self-upgrade* under *Versioning and builds* (variables, cache layout, lock, limits, lifecycle rows), `architecture.md` § *Self-upgrade flow* after the attach-time freshness check; the worker had already done the `CLAUDE.md` row and the `README.md` quick-menu line and upgrade walkthrough. A separate user-requested stale-doc fix rode along: `architecture.md` and `agents.md` now say the Codex **test** role gets the Go caches)

### Phase 6: Integration test

- [x] Create `scripts/test-self-upgrade.sh` — hermetic: scratch `HOME`, `XDG_CACHE_HOME`, `BINDIR`, `CONFIGDIR` and `BUS_SESSION`; a local tarball and a local `releases/latest` JSON served through the URL overrides (no network); a scratch daemon on the current binary (281 lines. Two binaries are built from this checkout with explicit `-X` stamps — `v0.0.1-test` as the "installed" build that runs the CLI and the scratch daemon, `v0.0.2-test` as the "release" — so no tag need exist and the real installed muxcode is never run; a fake `make` on `PATH` records each call and, for `install`, copies the chosen binary into `BINDIR` with a marker `tmux.conf`; the tmux server is private (`TMUX_TMPDIR`); every run that can reach `Restart daemons` is bound with `--expect-tag`/`--expect-session` so live daemons are never touched, and a last section proves it. Skips exit 2, not 0; listed in `CLAUDE.md`)
- [x] Test: `muxcode upgrade --check` exits 10 against a newer fake tag, 0 against the installed version, and 1 when the API file is missing (section 1)
- [x] Test: full `muxcode upgrade` against the fake tag → tarball cached with recorded SHA-256, `make install VERSION=<tag>` ran in the extracted tree (fake `make` log), `Verify` passed against the stub binary, the scratch daemon was restarted onto it, lifecycle rows `upgrade-check … upgrade-done` present (section 6: the run is a cache hit after the earlier sections' downloads, `make` ran in the extracted tree, `Verify` passed, the scratch daemon's command now names the `BINDIR` binary, tmux reloaded, `upgrade-check … upgrade-done` in order)
- [x] **Negative control:** an up-to-date check runs no download and leaves `BINDIR` untouched (mtime unchanged) (section 2)
- [x] **Negative control:** a tarball whose `make install` exits 1 leaves the installed binary byte-identical and the daemon unrestarted; `upgrade-failed` names the build step and the log path (sections 3 **and** 4 — a failed `make build` and a failed `make install` each leave the binary byte-identical and the daemon on its old pid, and `upgrade-failed` names the step and the log)
- [x] **Negative control:** a stub binary reporting the wrong version fails `Verify` and the daemon is not restarted (section 5)
- [x] Test: a second run against the same tag skips the download (cache hit noted in the row) (section 6's run reports `cache hit` for `v0.0.2-test`, already extracted by the earlier sections)
- [x] Coverage floor set to the maximum achievable count; run the script and record counts here (floor **46** = every check. Run agent, 2026-10-07: first run **45 passed, 1 failed, exit 1** — the other-daemons snapshot compared against the wrong session name, a bug in the script itself, caught by its own last section; fixed, re-run task `1791379078-spawn-c59824d4-753f6a55`: **46 passed, 0 failed, exit 0**)

#### Phase 6 verification note

Verified 2026-10-07 09:25 by plan from the working tree (run `1791315389`; Phase 5 committed as
`2e70186`). The run agent executed the script twice — the first run's single failure was in the
script's own "no other daemon was touched" check, which is the right kind of failure for that check to
have had — and the second passed every check at the floor. Review 0 must-fix; test node success on the
full suite. With this, every acceptance criterion (22) and every phase step in the spec is ticked.

## Decisions

### Decision 1 — source tarball into a cache, not a `git pull`

**Proposed.** The upgrade downloads the release tag's source tarball to `~/.cache/muxcode/upgrade/<tag>/`
and runs `make install VERSION=<tag>` there. A `git pull` would need a checkout muxcode does not know
about and would be hostage to that checkout's branch and dirty state — on 2026-10-06 the checkout was
on a feature branch with uncommitted work. The tarball is what the release *is*; the version is passed
explicitly because `git describe` has no `.git` to read. The prebuilt release binaries were rejected as
the primary road because `make install` is what also installs agents, skills, configs, `tmux.conf` and
the nvim config — the binary alone would upgrade half of muxcode. Integrity: GitHub publishes no checksum
for source tarballs; the recorded SHA-256 is for the log and the cache-hit check, and `Verify` confirms
the installed binary reports the tag. A `--from <dir>` override for building the local checkout is a
possible later addition, not part of this spec.

### Decision 2 — "newer" means a release tag past the installed version; force rebuilds anyway

**Proposed.** `CompareSemver(latest, installed) > 0` is `newer`. A dev build such as
`v0.1.20-9-g7d339be` compares *past* `v0.1.20`, so on the developer's own machine the check reads
`ahead` and the upgrade does nothing unless forced — a hot fix must not downgrade a tree that is ahead of
the release. `f` in the modal and `--force` on the CLI rebuild and reinstall the latest release regardless.

### Decision 3 — restart every session's daemon, through the new binary

**Proposed.** The restart step runs `<BINDIR>/muxcode upgrade-daemons` as a child process. The modal
and the CLI are the *old* binary; calling `bus.UpgradeDaemons` in-process would compare each daemon
against the old `BuildInfo()` and skip them all as current. Scope is every session on the machine — the
same as `build.sh`'s call — because a hot fix that left other sessions on the old build would recreate
the drift this feature exists to remove; `EnsureSessionDaemonCurrent`'s single-session scope stays for
the attach road. The modal runs in the user's tmux popup, not an agent sandbox, so `ps` is available
(the MUX-161 constraint applies to the build agent, not here).

*Amended by the Phase 4 review fix (2026-10-06):* a run the modal **confirmed** restarts exactly the
sessions the confirm listed (`runConfirmedDaemons`) and names any daemon started since as not restarted,
so the user is never shown one set and handed another; the unconfirmed CLI run (`muxcode upgrade` with
no `--expect-tag`) still restarts every session on the machine, as above.

## Related

| Spec | Relationship |
|------|--------------|
| [MUX-139](../completed/MUX-139-claude-agent-auto-resume.md) Phase 5 | The `Restart Agents` modal — the precedent for a menu-launched TUI that runs a bus operation and reports per row; also the follow-up this upgrade points the user at |
| [MUX-161](../backlog/MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md) | Why `build.sh`'s own `upgrade-daemons` fails from the build agent; this feature runs it from the user's popup instead |
| [MUX-108](../completed/MUX-108-control-pane.md) | The control-pane recycle on daemon restart, relied on unchanged |
| [MUX-020](../backlog/MUX-020-cli-help-command.md) | `muxcode upgrade --check` is the kind of command a help listing should surface |

## Out of scope

- Restarting agents after the upgrade — `Restart Agents` exists for that and the done footer names it
- Upgrading the Neovim plugins or Claude Code itself
- Downgrading to an older release, or upgrading to a pre-release
- Building from a local checkout (`--from <dir>`) — see Decision 1

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-202-self-upgrade-from-the-quick-menu | 1h 42m | 2026-10-07 09:25 |

## Status

In Progress — **all six phases implemented and verified 2026-10-07**; awaiting the Phase 6 commit and
the run's close-out (Status → `Complete`, move to `completed/`). Filed 2026-10-06 on the user's request,
active spec the same day; phases on `MUX-202-self-upgrade-from-the-quick-menu`: `b143299`, `2b74024`,
`f3d2d12`, `1c13460`, `2e70186`.
