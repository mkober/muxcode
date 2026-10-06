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

- [ ] `prefix + b` shows **`Upgrade MuxCode`** (key `U`) beside `Restart Agents`; it opens a modal registered as `upgrade` in `bus/modal.go` with the `provider`/`restart` sizes
- [ ] The modal shows **one row per step** — `Check`, `Download`, `Build`, `Install`, `Verify`, `Restart daemons`, `Reload tmux config` — as pending `○`, running `⟳`, done `✓` or failed `✗` with the failure's cause wrapped to the pane, a progress bar and a footer, rendered by a **pure** function (snapshot in, string out) that honours both width and height
- [ ] Before anything mutates, the modal **stops at a confirm** stating the consequence — `installed vA → latest vB; rebuilds and installs to <BINDIR>/<CONFIGDIR>, then restarts N daemon(s): <sessions>` — and **re-checks the latest release and the daemon list at execution**, not at render
- [ ] An **up-to-date** result is an explicit state, not an empty body: `installed vA is current (latest vB)`; a dev build past the latest tag reads `installed v0.1.20-9-g7d339be is ahead of the latest release v0.1.20` ([Decision 2](#decision-2--newer-means-a-release-tag-past-the-installed-version-force-rebuilds-anyway))
- [ ] The footer advertises every key: `⏎ Upgrade`, `f Force rebuild`, `q Quit`; while running, `q` closes the modal and the upgrade **continues in the background**, as the reload modal does
- [ ] State is readable **without colour** — the glyph carries it; no inline escapes outside `tui/styles.go`

**Pipeline**

- [x] `Check` reads the installed identity (`BuildInfo()`) and the latest release from GitHub (`GET /repos/mkober/muxcode/releases/latest`, stdlib `net/http`, 10 s timeout, `GITHUB_TOKEN`/`GH_TOKEN` sent when set to lift the unauthenticated rate limit); a network or API failure fails `Check` with the HTTP status and body excerpt and runs nothing else (Phase 1: `ReleaseClient.LatestRelease`, `CheckUpgrade`; the token is sent only to `api.github.com`, never to an overridden URL)
- [ ] `Download` fetches the release tag's **source tarball** into `~/.cache/muxcode/upgrade/<tag>/` (`XDG_CACHE_HOME` honoured), records its size and SHA-256 in the step row and the lifecycle log, and **skips the fetch when the extracted tree for that tag is already present and complete** ([Decision 1](#decision-1--source-tarball-into-a-cache-not-a-git-pull))
- [ ] `Build` runs `make install VERSION=<tag>` in the extracted tree with stdout+stderr captured to `<cache>/<tag>/build.log`; on failure the row shows the last lines and names the log; prerequisites (`go`, `make`, `tar`) are checked **before** `Download` so a machine without a toolchain fails at `Check` with a named missing tool and downloads nothing
- [ ] `Verify` runs the **freshly installed** binary — `<BINDIR>/muxcode version --json` — and fails if its `version` is not the tag (a stale `PATH` entry or a different `BINDIR` must be caught here, not discovered later)
- [ ] `Restart daemons` **executes the new binary's** `upgrade-daemons`, never the running process's `bus.UpgradeDaemons`: the modal is the old binary, whose `BuildInfo()` would read every daemon as current ([Decision 3](#decision-3--restart-every-sessions-daemon-through-the-new-binary)); each session is a sub-row with its `VersionDelta`; an unreadable `ps` fails the step with the exact error
- [ ] `Reload tmux config` sources the reinstalled `~/.config/muxcode/tmux.conf` into the running tmux server; the done footer names the version delta and the follow-up the upgrade does **not** do: *agents keep running until restarted — `Restart Agents` (prefix + b, A)*
- [ ] Every step writes a lifecycle row (`upgrade-check`, `upgrade-download`, `upgrade-build`, `upgrade-verify`, `upgrade-daemons`, `upgrade-tmux`, `upgrade-done` / `upgrade-failed`) with the installed and target versions
- [ ] **Negative controls:** an up-to-date install runs no step past `Check` and touches no file; a failed `Build` leaves the installed binary untouched (`make install` fails before `install -m 755`, and `Verify` would catch a partial copy); a failed `Verify` **does not** restart daemons

**CLI twin**

- [ ] `muxcode upgrade [--check] [--force] [--json]` runs the same pipeline non-interactively, printing one line per step; `--check` stops after `Check` with exit 0 (current), 10 (newer available) or 1 (error) so a cron or a script can poll it; `--force` rebuilds and reinstalls the latest release even when current
- [ ] `muxcode upgrade-ui` is the modal's command (the `provider-select`/`restart-select` shape)

**Docs and test**

- [ ] Docs: [`docs/agent-bus.md`](../../agent-bus.md) (`muxcode upgrade`, `upgrade-ui`, the modal), [`docs/configuration.md`](../../configuration.md) (`MUXCODE_UPGRADE_*` overrides, cache dir, token), [`docs/architecture.md`](../../architecture.md) (self-upgrade flow beside the daemon-upgrade contract), `CLAUDE.md` build/install table row, `README.md` quick-menu list
- [ ] `bash scripts/test-self-upgrade.sh` passes — hermetic, no network ([Phase 6](#phase-6-integration-test))

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

- [ ] Cache dir under `XDG_CACHE_HOME`/`~/.cache/muxcode/upgrade/<tag>/`; streamed download to `.partial`, SHA-256 and size recorded; skip when the tree is complete
- [ ] `make install VERSION=<tag>` in the extracted tree, log captured; `BINDIR`/`CONFIGDIR` passthrough
- [ ] `Verify` via the installed binary's `version --json`; mismatch fails with both versions named
- [ ] Tests: tarball from a local file URL, a fake `make` on `PATH` that records its args and writes a stub binary whose `version --json` the test controls; **negative control:** a build that fails leaves the previous binary bytes in place; a verify mismatch stops the pipeline

### Phase 3: Daemon restart, tmux reload, CLI

- [ ] `Restart daemons` execs `<BINDIR>/muxcode upgrade-daemons`, parses per-session lines into sub-rows; `ps` failure surfaces verbatim ([Decision 3](#decision-3--restart-every-sessions-daemon-through-the-new-binary))
- [ ] `Reload tmux config` via `tmux source-file`; skipped with a note when no tmux server is running
- [ ] `muxcode upgrade [--force] [--json]` runs the full pipeline with one line per step; lifecycle rows per step
- [ ] Tests: a fake `muxcode` on `PATH` whose `upgrade-daemons` output the test scripts; **negative control:** a failed `Verify` never reaches the daemon step

### Phase 4: Modal and menu

- [ ] `tui/upgrade_ui.go`: confirm / progress / done screens, each a pure renderer taking width and height; confirm re-runs `Check` and the daemon list on Enter; `f` forces; `q` detaches while the pipeline continues
- [ ] Generalise `renderBatchProgress` row input from `ReloadResult` to a small interface or a `StepResult` adapter — one renderer, two callers, no copy
- [ ] `bus/modal.go` `upgrade` entry; `config/tmux.conf` `Upgrade MuxCode` (key `U`); `muxcode upgrade-ui`
- [ ] Tests per the [TUI checklist](../../tui-style.md): up-to-date and ahead states render an explicit body with header and footer; a failure row wraps to a narrow width without overflow; height is read and the step list degrades when it does not fit; **negative control:** at a comfortable size nothing degrades; `StripAnsi` output carries every state

### Phase 5: Docs

- [ ] `docs/agent-bus.md` (`muxcode upgrade`, `upgrade-ui`, modal), `docs/configuration.md` (`MUXCODE_UPGRADE_API_URL`, `MUXCODE_UPGRADE_TARBALL_URL`, cache dir, `GITHUB_TOKEN`), `docs/architecture.md` (self-upgrade flow next to the daemon-upgrade contract), `CLAUDE.md` table row, `README.md` quick-menu list

### Phase 6: Integration test

- [ ] Create `scripts/test-self-upgrade.sh` — hermetic: scratch `HOME`, `XDG_CACHE_HOME`, `BINDIR`, `CONFIGDIR` and `BUS_SESSION`; a local tarball and a local `releases/latest` JSON served through the URL overrides (no network); a scratch daemon on the current binary
- [ ] Test: `muxcode upgrade --check` exits 10 against a newer fake tag, 0 against the installed version, and 1 when the API file is missing
- [ ] Test: full `muxcode upgrade` against the fake tag → tarball cached with recorded SHA-256, `make install VERSION=<tag>` ran in the extracted tree (fake `make` log), `Verify` passed against the stub binary, the scratch daemon was restarted onto it, lifecycle rows `upgrade-check … upgrade-done` present
- [ ] **Negative control:** an up-to-date check runs no download and leaves `BINDIR` untouched (mtime unchanged)
- [ ] **Negative control:** a tarball whose `make install` exits 1 leaves the installed binary byte-identical and the daemon unrestarted; `upgrade-failed` names the build step and the log path
- [ ] **Negative control:** a stub binary reporting the wrong version fails `Verify` and the daemon is not restarted
- [ ] Test: a second run against the same tag skips the download (cache hit noted in the row)
- [ ] Coverage floor set to the maximum achievable count; run the script and record counts here

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
| MUX-202-self-upgrade-from-the-quick-menu | 8m | 2026-10-06 15:55 |

## Status

In Progress — filed 2026-10-06 on the user's request, to be worked on now; active spec set the same
day. Phase 1 (release check) implemented and verified 2026-10-06 on
`MUX-202-self-upgrade-from-the-quick-menu`.
