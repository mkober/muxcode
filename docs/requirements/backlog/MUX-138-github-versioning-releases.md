# MUX-138: GitHub versioning & releases

**Tracking:** (GitHub issue not yet created)

## Context

MuxCode has **612 commits** and 92 delivered specs but no version identity beyond the MUX id:
**0 git tags, 0 GitHub releases**, no tracked `CHANGELOG` or `VERSION` file. The binary cannot
report what it is, `muxcode --version` routes to the launcher as a project path, `upgrade-daemons`
restarts every daemon blind because it cannot tell stale from current, and ten integration scripts
document a binary precondition that nothing enforces. This spec introduces SemVer tagging, a stamped
binary with a `version` subcommand, daemon version awareness, and a GitHub release workflow.

### Evidence (verified 2026-09-02 against the tree at `2f55e13`)

| Claim | Where |
|-------|-------|
| 0 tags, 0 releases, 612 commits, no `CHANGELOG`/`VERSION` tracked | `git tag`, `git rev-list --count HEAD`, `git ls-files` |
| No `version` in `knownSubcommands`; an unknown arg routes to the launcher **as a project path** | `tools/muxcode/main.go:108` — `if !knownSubcommands[os.Args[1]] { cmd.RunLauncher(os.Args[1:]) }` |
| Build stamps nothing — `-ldflags="-s -w"` only, no `-X` | `Makefile:21` |
| `install.sh` smoke-tests with `muxcode config list` because no version verb exists | `install.sh:942` |
| `UpgradeDaemons` cycles every discovered daemon unconditionally — no staleness test exists | `tools/muxcode/bus/upgrade.go:125` |
| Daemon keepalive is a bare Unix timestamp | `tools/muxcode/bus/daemon_health.go:14` — `DaemonKeepalivePath()` → `daemon.keepalive` |
| `.github/workflows/test.yml` is the only workflow (PR + push-to-main, `./test.sh`, deliberately not `build.sh`); no `.github/release.yml` | `.github/` |
| Nested module `github.com/mkober/muxcode/tools/muxcode` — `go install …@vX.Y.Z` would need a `tools/muxcode/vX.Y.Z` tag prefix; harness module is `muxcode-llm-harness`, no host path, not go-installable | both `go.mod` |
| "requires the installed binary to include MUX-xxx" appears **10×** in CLAUDE.md, **0×** in `scripts/` — documentation-only, unenforced | `CLAUDE.md`, `scripts/` |

**Naming correction folded in from the handoff brief.** The brief specified `bus/watcher_health.go`
and `watcher.keepalive`; neither exists. The daemon-identity rename (`watcher` → `daemon`, which
would otherwise collide with the `watch` agent) moved this to **`bus/daemon_health.go`** writing
**`daemon.keepalive`**, with `DaemonKeepalivePath()` / `TouchKeepaliveDaemon()` / `IsDaemonAlive()`.
This spec uses the real names. The stale names still sit in live docs — `CLAUDE.md:155` and its code
reference table at `:224`, plus `docs/architecture.md:30` — which is drift to fix separately, not
here. (`completed/MUX-030` also carries them, correctly, as a historical record.)

### Decisions (defaults chosen 2026-09-02; each is flippable before Phase 1 starts)

- **D1 — Start at `v0.1.0`, not `v1.0.0`.** Delivery-ack is still a soak with three rollback valves,
  [`MUX-012`](./MUX-012-remove-gated-pane-scrape-delivery.md) has not removed the bypassed
  pane-scrape path, 14 High defects are open, and no compatibility contract exists anywhere. `0.x`
  lets MINOR bumps carry breaking changes honestly. The written 1.0 gate below replaces a feeling
  with a checklist.
- **D2 — GitHub-generated release notes from PR titles; no hand-maintained `CHANGELOG.md`.** PR
  titles already carry `MUX-NNN: summary`, so notes are MUX-keyed for free. A `.github/release.yml`
  buckets by label. The [completed-specs index](./backlog.md#completed-id-registry) remains the
  long-form changelog.
- **D3 — Hand-rolled release workflow, not GoReleaser.** Stdlib-only ethos, and a bare binary is not
  a full install (agents, skills, configs and nvim ship via `make install`), so the release publishes
  binaries plus a source tarball and `install.sh` stays the install path.
- **D4 — Root `vX.Y.Z` tags only.** Nested-module `tools/muxcode/vX.Y.Z` tags for `go install` are
  deferred: nobody go-installs today and the harness module could not be anyway.
- **D5 — No retro-tagging.** The first tag is `v0.1.0` on the commit that lands Phase 1, so the first
  tagged binary can report itself.

### 1.0 gate

- [ ] [`MUX-012`](./MUX-012-remove-gated-pane-scrape-delivery.md) landed — pane-scrape delivery removed, one delivery model
- [ ] High-ranked defects at 0, or explicitly accepted in the backlog index
- [ ] Written compatibility contract covering: bus JSONL message shape, CLI verbs and flags, `MUXCODE_*` env names, config file paths, agent-definition frontmatter
- [ ] `muxcode-agent-bus` back-compat symlink ([`MUX-071`](../completed/MUX-071-muxcode-go-launcher.md)) dropped

### Bump rules

| Bump | When |
|------|------|
| PATCH | defect fixes (ranked-defect table), docs-only, tests-only |
| MINOR | a MUX feature spec completes; new CLI verb, flag or env var; deprecations; in `0.x` also breaking changes (labelled `breaking`) |
| MAJOR | the 1.0 gate; afterwards, compatibility-contract breaks |

Cadence: tag when a spec cluster closes (roughly every 3–6 merged PRs), never on a timer.

## Requirements

### Acceptance criteria

- [x] `muxcode version` prints `muxcode vX.Y.Z (<commit>, <date>, go<ver> <os>/<arch>)`; `--json` emits the same fields as an object
- [x] `muxcode --version` and `muxcode -v` print the same line and exit 0 — they never route to the launcher as a project path
- [x] Dev builds self-describe via `git describe --tags --always --dirty` (e.g. `v0.1.0-3-gabc1234-dirty`)
- [x] A source build with no ldflags falls back to `debug.ReadBuildInfo()` (`vcs.revision`, `vcs.modified`) and never prints an empty version
- [x] `muxcode version --at-least vX.Y.Z` exits 0/1 by stdlib semver comparison, handling pre-release and `-N-g<sha>` describe suffixes
- [x] The daemon records its version at startup; `muxcode status` shows a version column per session
- [x] `muxcode upgrade-daemons` skips daemons already on the installed version, cycles stale ones, and `--dry-run` reports `session X: daemon vA → installed vB` per session
- [x] `muxcode diagnose` reports a `binary-daemon-version-mismatch` finding when daemon and installed binary differ
- [ ] `.github/workflows/release.yml` runs on `v*` tag push, depends on `./test.sh` passing, builds darwin/arm64, darwin/amd64, linux/amd64 and linux/arm64 with version ldflags, attaches binaries plus `sha256sums.txt`, and publishes with generated notes
- [ ] `.github/release.yml` categorises notes by label (`type:defect`, `type:feature`, `breaking`, `docs`)
- [ ] The 10 integration scripts carrying a documented binary precondition assert it with `muxcode version --at-least`; `install.sh`'s smoke test uses `muxcode version`
- [ ] `v0.1.0` tagged on the Phase 1 landing commit, with a published GitHub release
- [ ] Docs updated: `CLAUDE.md` (build table, key constraints), `README.md`, `docs/agent-bus.md` (`version`, `upgrade-daemons`), `docs/configuration.md`

### Technical approach

- **`bus/version.go`** (new): `var Version, Commit, BuildDate string` set via `-X`; `BuildVersion()`
  resolving ldflags → `debug.ReadBuildInfo()` → `"devel"`; `CompareSemver(a, b string) int` — stdlib
  only, ~30 lines, strips a leading `v`, splits on `-` for the pre-release/describe suffix, and sorts
  a describe suffix **after** its base tag.
- **`cmd/version.go`** (new): `Version(args)` with `--json` and `--at-least <v>`.
- **`main.go`**: add `"version"` to `knownSubcommands`; intercept `--version`/`-v` as `argv[1]`
  **before** the path routing at `:108`, which is what currently swallows them.
- **`Makefile`**: `VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)`
  plus `COMMIT` and `DATE`; `LDFLAGS = -s -w -X github.com/mkober/muxcode/tools/muxcode/bus.Version=$(VERSION) …`.
  The harness gets the same treatment against its own module path (`muxcode-llm-harness/harness`) —
  optional in Phase 1.
- **Daemon**: on startup write `daemon.version` beside `daemon.keepalive`, with
  `WriteDaemonVersion()` / `ReadDaemonVersion()` / `DaemonVersionPath()` added to
  **`bus/daemon_health.go`** alongside the existing `DaemonKeepalivePath()`. An `upgrade-daemons`
  relaunch rewrites it automatically, since the new process writes it.
- **`bus/upgrade.go`**: the plan gains `DaemonVersion`, `Installed string`, `Current bool`; it skips
  `Current` unless `--force`, and `--dry-run` output names both versions. Orphan handling unchanged.
- **`bus/diagnose.go`**: new `binary-daemon-version-mismatch` pattern — **warning, not critical**,
  remediation `./build.sh` or `muxcode upgrade-daemons` — registered **before** `checkUnexplainedEvidence`,
  which must stay last as the verdict-consistency backstop.
- **`bus/inspect.go`**: status output gains a version column.
- **`release.yml`**: `actions/checkout@v4` with `fetch-depth: 0` (tags are needed for `describe`),
  `setup-go@v5` on go 1.22 with `cache: false`, then test job → build matrix job →
  `gh release create "$TAG" --generate-notes --verify-tag dist/*`. No third-party release actions.
- **Follow-up, not in this spec's phases**: a `release` graph template — main-clean check → test →
  `wait_human` gate → tag+push (commit node) → watch CI.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/version.go` | new — version vars, build-info fallback, semver compare |
| `tools/muxcode/cmd/version.go` | new — `version` subcommand |
| `tools/muxcode/main.go` | register `version`; intercept `--version`/`-v` before the `:108` path routing |
| `Makefile` | VERSION/COMMIT/DATE plus `-X` ldflags (today `:21` is `-s -w` only) |
| `tools/muxcode/bus/daemon_health.go` | daemon version file helpers beside `DaemonKeepalivePath()` (`:14`) |
| `tools/muxcode/daemon/daemon.go` | write version at startup |
| `tools/muxcode/bus/upgrade.go`, `tools/muxcode/cmd/upgrade.go` | staleness compare, skip/report, `--force` (`UpgradeDaemons` at `:125`) |
| `tools/muxcode/bus/diagnose.go` | mismatch finding |
| `tools/muxcode/bus/inspect.go` | status version column |
| `.github/workflows/release.yml` | new — tag-triggered release |
| `.github/release.yml` | new — notes categories |
| `install.sh` | smoke test via `muxcode version` (today `muxcode config list` at `:942`) |
| `scripts/test-*.sh` (the 10 with preconditions) | `--at-least` preconditions |
| `scripts/test-version.sh` | new — integration test |
| `CLAUDE.md`, `README.md`, `docs/agent-bus.md`, `docs/configuration.md` | docs |

## Implementation

### Phase 1: Stamp the binary and add the `version` subcommand

- [x] `bus/version.go` with ldflags vars, the `BuildVersion()` fallback chain, and `CompareSemver()`
- [x] Unit tests for `CompareSemver`: describe suffix, pre-release, `v` prefix, malformed input → error
- [x] `cmd/version.go`: plain output, `--json`, `--at-least`
- [x] `main.go`: `version` in `knownSubcommands`; `--version`/`-v` intercepted before path routing
- [x] Test pinning that `--version` never reaches the launcher
- [x] Makefile ldflags; `make build` output shows the stamped version
- [x] (optional) harness binary stamped the same way

### Phase 2: Daemon version awareness

- [x] Daemon writes `daemon.version` at startup; helpers in `bus/daemon_health.go` plus tests
- [x] `muxcode status` version column
- [x] `upgrade-daemons`: skip current, cycle stale, `--force`, `--dry-run` naming both versions
- [x] Unit tests on the plan builder (current vs stale vs orphan)
- [x] `diagnose`: `binary-daemon-version-mismatch` pattern plus test
- [x] Negative control: matching versions produce **no** finding

### Phase 3: Release workflow and first tag

- [ ] `.github/workflows/release.yml` — test → matrix build → checksums → `gh release create --generate-notes`
- [ ] `.github/release.yml` categories, and the matching labels created on the repo
- [ ] Tag `v0.1.0` on the Phase 1 landing commit (**user-approved**, via the commit agent)
- [ ] Verify the release published with generated notes, 4 binaries and `sha256sums.txt`

### Phase 4: Enforce script preconditions

- [ ] The 10 integration scripts assert `muxcode version --at-least <version that shipped their MUX>`
- [ ] `install.sh` smoke test switched to `muxcode version`
- [ ] CLAUDE.md build table: replace "requires the installed binary to include MUX-xxx" with the minimum version

### Phase 5: Integration test

- [ ] Create `scripts/test-version.sh` covering the below, hermetic (scratch bus plus tmux session)
- [ ] Stamped build reports itself; `--json` round-trips the same fields
- [ ] `--version` and `-v` exit 0 and **never** create a tmux session or touch a project dir
- [ ] `--at-least` truth table: lower, equal, higher, describe-suffix, pre-release
- [ ] Scratch daemon launched from a binary stamped `v0.0.1-test` while installed reports higher → `upgrade-daemons --dry-run` names the mismatch, a real run cycles it, a second run skips it
- [ ] `diagnose` shows the mismatch finding before, and none after
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and verify all checks pass

## Related

- [`MUX-012`](./MUX-012-remove-gated-pane-scrape-delivery.md) — remove gated pane-scrape delivery; 1.0 gate item
- [`MUX-071`](../completed/MUX-071-muxcode-go-launcher.md) — Go launcher; the `muxcode-agent-bus` symlink is a 1.0 gate item
- [`MUX-031`](../completed/MUX-031-graph-run-tui.md) — mentions the build/`upgrade-daemons` unblock that Phase 2 makes observable

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-138-github-versioning-releases | 1h 3m | 2026-09-02 13:29 |

The branch carries more than this spec: the `req-code-pr` → `spec-to-pr` rename, the
`story-lifecycle` template removal, and the branch-derived-intent work, alongside MUX-138 Phase 1.
The row records what the **branch** accrued, which is what the ledger measures — it is not a
figure for effort spent on this spec.

## Status

In Progress — Phases 1 and 2 complete (13/13 steps, 8 acceptance criteria), 21/45 items overall.

The file still sits in `backlog/` while reading `In Progress`, the same deliberate exception the
index records for [`MUX-005`](./MUX-005-plan-diagrams.md). Moving it to `drafts/` is a `git mv`,
which is user-gated — flagged, not done.
