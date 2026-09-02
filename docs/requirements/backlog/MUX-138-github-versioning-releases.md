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
  pane-scrape path, 17 High defects are open, and no compatibility contract exists anywhere. `0.x`
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
- [x] `.github/workflows/release.yml` runs on `v*` tag push, depends on `./test.sh` passing, builds darwin/arm64, darwin/amd64, linux/amd64 and linux/arm64 with version ldflags, attaches binaries plus `sha256sums.txt`, and publishes with generated notes
- [x] `.github/release.yml` categorises notes by label (`type:defect`, `type:feature`, `breaking`, `docs`)
- [x] The 10 integration scripts carrying a documented binary precondition assert it with `muxcode version --at-least`; `install.sh`'s smoke test uses `muxcode version`
- [x] `v0.1.0` tagged on the **release-workflow commit** (`6a05bc8`), not the Phase 1 landing commit, with a published GitHub release — same wording correction as [Phase 3](#phase-3-release-workflow-and-first-tag)
- [x] Docs updated: `CLAUDE.md` (build table, key constraints), `README.md`, `docs/agent-bus.md` (`version`, `upgrade-daemons`), `docs/configuration.md`

The acceptance criteria above had fallen behind their phases — Phases 3 and 4 were checked off while
the four criteria they satisfy stayed open. Reconciled 2026-09-02:

| Target | Content |
|--------|---------|
| `CLAUDE.md` | Build table rows for `version`, `upgrade-daemons` (with `--session`), the tag-push release flow and `release-labels.sh`, plus the precondition-helper bullet under Bash scripts |
| `docs/agent-bus.md` | `muxcode version` section — identity line, `--json` six-field contract, the `--at-least` tri-state, `routeFor()` interception, stamping and fallback — and `upgrade-daemons` extended with `--force`, `--session` and version-awareness |
| `README.md` | New **Versions and releases** subsection under Install: the verb, the deliberate `0.x` stance, tag-push releases, honest dev-build strings, `--at-least` with its three exits, and `upgrade-daemons` for live sessions |
| `docs/configuration.md` | New **Versioning and builds** subsection: the `VERSION` make variable and `MUXCODE_BIN`, cross-linked to the CLI reference rather than restating the verb |

**A scope note on `docs/configuration.md`.** The request named the same four topics for both files, but
versioning has **no runtime environment variables** — the identity is fixed at build time. Restating
the `version` verb in a file about env vars and directory structure would have duplicated
`agent-bus.md` rather than documenting configuration, so that section covers only what is genuinely
configurable and links out for the rest. That turned up a real gap worth having: **`MUXCODE_BIN` is
read by 14 integration scripts and was documented nowhere.**

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

- [x] `.github/workflows/release.yml` — test → matrix build → checksums → `gh release create --generate-notes`
- [x] `.github/release.yml` categories, and the matching labels created on the repo
- [x] Tag `v0.1.0` on the **release-workflow commit** (`6a05bc8`), not the Phase 1 landing commit (**user-approved**, via the commit agent) — see the correction below
- [x] Verify the release published with generated notes, 4 binaries and `sha256sums.txt`

**Correction to this phase's own wording.** Step 3 originally read *"Tag `v0.1.0` on the Phase 1
landing commit"*. That instruction was self-defeating and was correctly not followed:
`.github/workflows/release.yml` does not exist at the Phase 1 commit (`git cat-file -e
13bec9d:.github/workflows/release.yml` → absent), and the workflow triggers on `push: tags: ['v*']`.
Tagging there would have fired **no workflow and produced no release**. The tag belongs at or after
the commit that introduces the workflow; `6a05bc8` is that commit. Wording fixed to match what must
happen, rather than checking off an item against something that did not occur.

Verification 2026-09-02 (plan, `verify-spec`), with provenance stated because half of this phase is
only observable through `gh`, which this role must not run.

| Fact | How established |
|------|-----------------|
| Workflow shape: `test` → `build` on a 4-target matrix (`darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`), `sha256sum muxcode-* > sha256sums.txt` (:126), `gh release create "$TAG" --verify-tag --generate-notes` (:134-138) | **Directly verified** — read from the file |
| Tag `v0.1.0` exists, is **annotated** (`git cat-file -t` → `tag`), and points at `6a05bc8` | **Directly verified** — read-only git |
| `6a05bc8` = *"MUX-138 Add Phase 3 release workflow (tag-triggered) and label script"*; both `.github` files now tracked | **Directly verified** — the files were untracked at the previous pass |
| `scripts/release-labels.sh` creates **5** labels — `breaking`, `type:feature`, `type:defect`, `docs`, `skip-changelog` — covering every non-default name `.github/release.yml` references, idempotently via `--force` | **Directly verified** — read from the script |
| Labels actually created on the repo; workflow run `33667352101` green; release `v0.1.0` published with 4 binaries, `sha256sums.txt` and generated notes | **Relayed** by the commit agent (the role authorized for `gh`), user-approved. Not independently checked here |

**Gap closed 2026-09-02.** An earlier pass of this verification recorded that `.github/release.yml`
excluded PRs labelled `skip-changelog` while `scripts/release-labels.sh` never created it — leaving an
exclusion that could not match and a "suppress this PR from the notes" affordance that did not exist.
`skip-changelog` has since been added to the script (5 labels), so every non-default label the
config references is now created. Re-verified against the script.

### Phase 4: Enforce script preconditions

- [x] The 10 integration scripts assert `muxcode version --at-least <version that shipped their MUX>`
- [x] `install.sh` smoke test switched to `muxcode version`
- [x] CLAUDE.md build table: replace "requires the installed binary to include MUX-xxx" with the minimum version

Verification 2026-09-02 (plan, `verify-spec`), all three **directly verified**:

| Step | Evidence |
|------|----------|
| Script preconditions | `scripts/lib/muxcode-version.sh` defines `require_muxcode_version`, and **exactly 10** integration scripts invoke it — each with its own feature id (MUX-007/014/031/103/105/108/114/117/121/134), not merely sourcing the helper |
| `install.sh` | `:940` — `elif installed=$(muxcode version 2>/dev/null); then`, replacing the `config list` workaround the Context section cites |
| CLAUDE.md | **0** occurrences of "requires the installed binary to include" remain; **10** rows now read "requires installed muxcode >= v0.1.0" |

The helper's tri-state handling is worth recording, because it is the part that could have gone wrong
quietly: `muxcode version --at-least` exits 0 / 1 / 2, and exit **2** (uncomparable — an untagged dev
build has no semver rank) returns 0 with a note rather than failing. Treating 2 as failure would block
every developer running a tree-built binary between tags, i.e. the exact loop that produces the code
under test. A binary predating MUX-138 has no `version` verb at all, routes the word to the launcher
as a project path, and exits 1 — correctly read as "older".

All of this is **uncommitted** (`scripts/lib/` untracked; `CLAUDE.md`, `install.sh` and 10 scripts
modified).

**Flagged, not a blocker.** The 10 call sites split across two conventions for the same condition:
five `exit 1` with `FAIL  binary precondition not met`, five `exit 2` with `SKIP: binary precondition
not met`. A stale binary is therefore a *failure* in half the suite and a *skip* in the other half,
which a CI harness distinguishing the two exit codes would report inconsistently. It may be
deliberate — the SKIP scripts are largely those needing a live session — but the binary precondition
itself is identical in both, so the split is worth a deliberate ruling rather than being left to
whichever script was edited first.

### Phase 5: Integration test

- [x] Create `scripts/test-version.sh` covering the below, hermetic (scratch bus plus tmux session)
- [x] Stamped build reports itself; `--json` round-trips the same fields
- [x] `--version` and `-v` exit 0 and **never** create a tmux session or touch a project dir
- [x] `--at-least` truth table: lower, equal, higher, describe-suffix, pre-release
- [x] Scratch daemon launched from a binary stamped `v0.0.1-test` while installed reports higher → `upgrade-daemons --dry-run` names the mismatch, a real run cycles it, a second run skips it
- [x] `diagnose` shows the mismatch finding before, and none after
- [x] Coverage floor keeps a skipped section from reporting green
- [x] Run the script and verify all checks pass

**Run 2026-09-02 15:20 (run agent): `40 passed, 0 failed`, exit 0** — log `/tmp/test-version-phase5.log`,
verified here by reading it (40 `ok:` lines, zero `FAIL`), not taken from the report alone.

The count is the point: **40 is the coverage floor and also the achievable maximum** (28 single-fire
sites + 10 `row` cases + 2 loop iterations). A run landing exactly on the maximum proves every
assertion executed — no section was skipped, and the floor could not have been satisfied by a partial
run. Had the two figures differed, a green result would have been compatible with skipped work.

Verification 2026-09-02 (plan, `verify-spec`). Seven steps checked off by **reading the script**; the
eighth is deliberately left open — see below.

| Step | Evidence in `scripts/test-version.sh` |
|------|----------------------------------------|
| Hermetic | `mktemp -d` work dir (:35), private tmux server via `TMUX_TMPDIR` (:36), scratch `BUS_SESSION` (:39), redirected `MUXCODE_LIFECYCLE_LOG_DIR` (:41) — and a closing assertion that the real lifecycle log dir is untouched (:221) |
| Stamped identity + `--json` | :85 reports itself, :90 round-trips the same fields, :92 pins the field set as exactly the documented six |
| `--version` / `-v` | :102 (both flags, exit 0 and exact line), plus three negative assertions — cwd gained nothing (:105), no bus dir created (:106), no tmux server started (:111) |
| `--at-least` truth table | Ten `row` cases covering lower, equal, higher patch, higher major, describe-suffix on both sides of its tag, pre-release below its release and above the previous patch, and **both** uncomparable directions → exit 2; :133 pins that the older verdict names both versions on stderr |
| Daemon rollout | dry-run names both builds (:172) and leaves the pid unchanged (:175), real run cycles with a new pid (:195-199), old pid gone (:201), second run skips (:207) with the pid untouched (:208); `daemon-upgraded` (:202) and `daemon-current` (:209) lifecycle rows asserted |
| `diagnose` before/after | mismatch present (:185) with both builds in evidence (:188) and severity `warning` (:190); absent after (:216) — **with the daemon asserted alive in both reports** (:183, :214) so the absence is not vacuous |
| Coverage floor | `[ "$PASS" -ge 40 ]` (:227) |

**The floor is the achievable maximum, which is what makes it meaningful.** A first count of `ok "`
call sites reads 30 and looks short of 40 — but two sites multiply: `row()` (:120) is invoked **10**
times and the `--version`/`-v` site (:102) runs **twice**. So 28 single-fire sites + 10 + 2 = **40
exactly**. Floor equal to maximum means a green run proves no section was skipped; a floor even one
below that would let a skipped assertion still report green.

**Why the last step stays open.** Nothing here is evidence the script was *executed*: the file is
untracked, unmodified since 15:12, and no run output exists. Running `scripts/test-*.sh` is the **run**
agent's job, not plan's, so this step needs a real run reported back before it can be checked. The
seven above assert the script *contains* its coverage; only the eighth asserts it *passes*.

## Related

- [`MUX-012`](./MUX-012-remove-gated-pane-scrape-delivery.md) — remove gated pane-scrape delivery; 1.0 gate item
- [`MUX-071`](../completed/MUX-071-muxcode-go-launcher.md) — Go launcher; the `muxcode-agent-bus` symlink is a 1.0 gate item
- [`MUX-031`](../completed/MUX-031-graph-run-tui.md) — mentions the build/`upgrade-daemons` unblock that Phase 2 makes observable

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-138-github-versioning-releases | 1h 49m | 2026-09-02 14:48 |

The branch carries more than this spec: the `req-code-pr` → `spec-to-pr` rename, the
`story-lifecycle` template removal, and the branch-derived-intent work, alongside MUX-138 Phases 1-4
and the filing of MUX-139, MUX-140 and MUX-141.
The row records what the **branch** accrued, which is what the ledger measures — it is not a
figure for effort spent on this spec.

## Status

**Complete — 41/45 items.** All five phases and every acceptance criterion are done: the binary is
stamped and self-describing, the daemon records its build, `v0.1.0` is tagged (`6a05bc8`) and
released, the 10 integration scripts enforce their binary precondition, the docs are written, and
`scripts/test-version.sh` passes **40/40** — its floor and its achievable maximum, so the green run
proves nothing was skipped.

The 4 remaining items are the **`1.0 gate`**, which is deliberately future work rather than this
spec's scope: [`MUX-012`](./MUX-012-remove-gated-pane-scrape-delivery.md) landing, High-ranked defects
at zero or explicitly accepted, a written compatibility contract, and the
`muxcode-agent-bus` back-compat symlink. They gate a `v1.0.0`, not this spec.

Moving the file to `completed/` is a `git mv`, which is user-gated — flagged, not done.

The file still sits in `backlog/` while reading `In Progress`, the same deliberate exception the
index records for [`MUX-005`](./MUX-005-plan-diagrams.md). Moving it to `drafts/` is a `git mv`,
which is user-gated — flagged, not done.
