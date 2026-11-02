# MUX-207: Xcode and Swift Projects: Agents Lack the Patterns, Sandbox Access and Tools to Build, Test and Observe Them

**Tracking:** [mkober/muxcode#174](https://github.com/mkober/muxcode/issues/174)

**Provenance:** filed 2026-10-29 on the user's explicit request — "making sure muxcode is compatible to
work on xcode projects" — from edit's review of the `tibo` session that day. Filed as **MUX-207**: the
brief proposed MUX-205, but GitHub issues
[#161](https://github.com/mkober/muxcode/issues/161) (MUX-205, a fix loop spending its last fix
before discovering the cap) and [#162](https://github.com/mkober/muxcode/issues/162) (MUX-206, the
long-active watchdog and windowless roles) already carry those ids for unrelated defects whose specs
were never written, so the first id free on both the registry and the tracker was taken instead.

A Swift app for macOS, iOS and tvOS, running entirely on muxcode defaults, could not be built or
tested by its Codex agents, wrote no build or test history, was not detected as a Swift project,
and offered its Claude roles no Apple toolchain. None of this is one bug; it is five gaps in
defaults written for Go, Node and Python.

## Context

### The project (`~/Repos/mkober/tibo`)

| Piece | Where |
|-------|-------|
| SwiftPM package | `packages/TiboKit/Package.swift`; DocsGen at `tools/DocsGen/Package.swift` |
| XcodeGen projects | `apps/{macos,ios,tvos}/project.yml`, generating `Tibo.xcodeproj` |
| `scripts/test.sh` | `swift test` |
| `scripts/build-app.sh` | `swift build`, `plutil`, `iconutil`, `codesign`, `security` |
| `scripts/build-ios.sh`, `scripts/build-tvos.sh` | `xcodegen generate`, `xcodebuild … -derivedDataPath`, `xcrun simctl`, `xcrun devicectl` |
| `scripts/build-docs.sh` | `swift build` and `swift run` |
| CI | none; CONTRIBUTING says run `scripts/test.sh` before merging |
| Tests | run real `ffmpeg` conversions; brew deps `ffmpeg`, `transmission-cli`, `mpv`, `xcodegen` |

Providers in that session: build, test, review, analyze and research on **Codex**; plan, edit,
deploy, run, commit, watch, serve, api, auto, docs and pr-read on **Claude**. The repo has no
`.muxcode/config`, no `.claude/`, no root `CLAUDE.md` and no `AGENTS.md`, so it runs on muxcode
defaults alone; `.codex/AGENTS.md` and `.codex/{build,test,review}/AGENTS.md` are muxcode's
generated role instructions, not project context.

### Findings (2026-10-29 review)

**F1. SwiftPM cannot run under the Codex sandbox — the blocker.** On 2026-10-22 tibo's Codex build
agent ran `scripts/build-docs.sh` eight times and failed every time; the test agent's
`scripts/test.sh` failed the same way and ran no suite. Rollouts:
`~/.codex/sessions/2026/10/22/rollout-2026-10-22T11-03-46-01a16423-{bca3…,beab…}`. Verbatim:

```
error: 'tibokit': Invalid manifest (compiled with: [swiftc … packages/TiboKit/Package.swift]); sandbox-exec: sandbox_apply: Operation not permitted
error: 'docsgen': Invalid manifest …
Swift manifest compilation denied access to temporary Swift ModuleCache (Operation not permitted)
```

Two causes:

1. SwiftPM wraps manifest and plugin compilation in its own `sandbox-exec`; macOS refuses that
   nested inside Codex's Seatbelt sandbox.
2. The Swift/clang module cache write lands outside the writable roots.

`codexWritableRoots` (`bus/provider_codex.go:141`) grants build and test the Go caches, the
Makefile's install directories and `~/.claude/commands` — no SwiftPM or Xcode path:
`~/Library/Caches/org.swift.swiftpm`, `~/Library/org.swift.swiftpm`, `~/.swiftpm`,
`~/Library/Developer/Xcode/DerivedData` and its `ModuleCache.noindex`, the clang module cache /
`$TMPDIR`. `codexPackageCacheEnv` (`:201`) relocates npm, pip, uv, poetry and yarn caches, nothing
for Swift. **Unknown until measured:** whether `xcodebuild` (even a `generic/platform=iOS Simulator`
build) and `xcrun simctl` can reach the CoreSimulator service from inside the sandbox, and what
`xcodegen` needs.

**F2. Swift and Xcode commands are not classified as build or test.** `DefaultBuildPatterns`
(`bus/hook.go:359`) and `DefaultTestPatterns` (`:371`) contain no `swift build`, `swift test`,
`xcodebuild`, `xcodegen`, `scripts/build-*.sh` or `scripts/test.sh`. tibo's build and test history
files are empty — no row was ever written — so the build→test→review chain never fires, a graph
build or test node has no authoritative row and parks for approval, and the evidence guard never
applies. Wrapper matching (`matchPatterns`) does classify `bash scripts/test.sh` as a test by the
`test.sh` base name, but a bare `scripts/test.sh` — the form CONTRIBUTING documents — matches
nothing. The run chain's watch allowlist (`runWatchActions`, `bus/profile.go:556`) covers
`scripts/*.sh` and has no `xcodebuild test` or `swift test` verification shape.

**F3. Project detection only looks at the repo root.** `DetectProject` (`bus/detect.go:107`) knows
Swift (`Package.swift`, `*.xcodeproj`) but checks only the root; tibo's markers sit one and two
levels down. It has no XcodeGen (`project.yml`) or Tuist indicator, so tibo is not detected as
Swift and agents get no "Swift Project" context. The Swift context text (`detect.go:~310`) knows
only `swift build` and `swift test` — nothing about `xcodebuild`, schemes, destinations or
simulators.

**F4. Claude tool profiles have no Swift or Apple toolchain.** `DefaultConfig().ToolProfiles`
(`bus/profile.go:580+`) lists none of `swift`, `xcodebuild`, `xcodegen`, `xcrun` (`simctl`,
`devicectl`), `codesign`, `security`, `plutil`, `iconutil`, `log stream` / `log show`, `open -a`.
Today this does not block Claude non-edit roles, which launch with
`--dangerously-skip-permissions` (`bus/provider_claude.go:46`); it matters for edit, for any role
moved to OpenCode (governed by the profile's allow and deny rules), and as the documented intent
of each role. By role: **watch** cannot follow app logs (`log stream --predicate …`,
`xcrun simctl spawn booted log stream`); **serve** cannot launch the app (`open`,
`xcrun simctl launch`, `swift run`); **deploy** cannot sign or install (`codesign`,
`xcrun devicectl`, `notarytool`); **edit's `DenyTools`** do not deny `swift build`, `swift test`
or `xcodebuild`, so the delegation rule is not enforced for Swift.

**F5. Nothing initializes a project for muxcode — the user's request.** A new project gets no
`.muxcode/config` (so no project patterns), no `.claude/` and no `CLAUDE.md`, and everything above
falls back to defaults written for Go, Node and Python.

### Out of scope

tibo's analyze agent has no window in the session, yet notify keeps trying to inject into it —
`injection-refused: analyze: pane capture failed: exit status 1`, about ten times on 2026-10-29.
A separate defect; this spec only points at it.

### User decisions (2026-10-29)

1. **Session-start init: "Config + settings + CLAUDE.md".** When a session launches in a project
   that lacks any of these, muxcode creates it: `.muxcode/config` with the detected project's
   build, test and precheck patterns; `.claude/settings.json` with permissions for the detected
   toolchain; a `CLAUDE.md` stub naming the detected build and test commands. It **never
   overwrites** an existing file.
2. **Git: "Leave untracked".** Generated files are written and left uncommitted. Edit is told they
   are new, so the user reviews and commits them. Nothing is committed automatically.
3. **Codex and Swift: "Experiment first, keep Codex".** Phase 1 measures what works under the
   Codex sandbox. The fix makes Codex build and test work for Swift and Xcode projects. Routing
   build and test to Claude is a fallback, used only for what the experiment proves cannot work,
   and only with the user's approval.
4. **Codex sandbox (2026-11-02): "Run Codex's test agent with no sandbox, make this a default for
   all codex agents in muxcode."** Made after the Phase 4 live run in tibo: the suite crashed on a
   Seatbelt `system-info vfs.disk-space` deny that no grant reaches (see
   [Phase 4 findings](#phase-4-findings-2026-11-02)). Every Codex role now launches
   `-s danger-full-access`; `MUXCODE_CODEX_SANDBOX=workspace-write` (or the per-role variable) opts
   back into the sandbox, under which the Phase 4 shims and roots still apply.
5. **Config files (2026-11-02): "Layer the configs."** Made when Phase 6's session-start init
   could not write `.muxcode/config` without hiding `~/.config/muxcode/config`, because
   `GetShellConfig` read only the first file it found. Now the home config is the base and the
   project config overrides it key by key; `MUXCODE_CONFIG` is still read alone. Session-start init
   therefore writes the project config everywhere except under `MUXCODE_CONFIG` (see
   [Phase 6 findings](#phase-6-findings-2026-11-02)).

## Requirements

### Acceptance criteria

- [x] Experiment establishes, under the Codex sandbox as launched for build and test, what each of
      these needs: `swift build`, `swift test`, `swift run`, `xcodegen generate`,
      `xcodebuild … build` for a generic simulator destination, and `xcrun simctl list` — recorded
      here before any fix — _2026-10-29, see
      [Phase 1 findings](#phase-1-findings-2026-10-29); the only code in the tree is the probe
      script_
- [x] Codex build and test agents can build and test a SwiftPM package nested below the repo root:
      tibo's `scripts/test.sh` and `scripts/build-docs.sh` pass on the Codex test and build agents
      _(2026-11-02, on the no-sandbox default of user decision 4: tibo hook rows exit 0 —
      `scripts/test.sh` `ts 1793636350`, 270 tests / 55 suites; `scripts/build-docs.sh`
      `ts 1793636337`. Under the `workspace-write` opt-in the Phase 4 shims make `build-docs.sh`
      pass, while `test.sh` fails on a Seatbelt deny no grant reaches — see Phase 4 findings.)_
- [x] Swift and Xcode build and test commands are classified: each writes an authoritative history
      row and fires its chain; a bare `scripts/test.sh` / `scripts/build-*.sh` is classified as well
      as the `bash` form; negative control — a non-build Swift call such as `swift --version` stays
      unclassified — _Phase 3, 2026-10-29: patterns, `xcodebuildAction`, basename matching for the
      bare form, `swift --version` and the operand traps pinned in `hook_test.go` and
      `conditions_test.go`. Suite pass is the test agent's report (`./test.sh` EXIT=0,
      `ts 1793305737`); no hook row for the run_
- [x] Detection finds a Swift or Xcode project whose `Package.swift`, `*.xcodeproj` or XcodeGen
      `project.yml` sits below the root, at a bounded depth that skips `.build/`, `DerivedData/`
      and `node_modules/` — _Phase 2, 2026-10-29: depth 3, the skip set plus `Pods`, `Carthage` and
      hidden dirs, pinned by the five `TestDetectProject_Swift*` tests_
- [x] Session start creates missing `.muxcode/config`, `.claude/settings.json` and `CLAUDE.md` from
      detection: it never overwrites; it leaves the files untracked; it writes a lifecycle row
      naming what was created; it sends edit a notice listing the new files to review and commit;
      negative control — a project with all three present is untouched
      _(Phase 6, 2026-11-02: everything but the `.muxcode/config` write held at first — the config
      was withheld under any home config it would have shadowed; user decision 5 layers the configs
      so it is now written everywhere except under `MUXCODE_CONFIG`. Ticked on the re-verify, review
      `e76b2094` clean, `./test.sh` exit 0 `ts 1793643090` — see Phase 6 findings)_
- [x] Tool profiles grant the Apple and Swift toolchain where each role needs it, and edit's
      `DenyTools` cover Swift and Xcode builds and tests — pinned by the profile test
      _(Phase 5, 2026-11-02: `apple*Tools` per role, edit `DenyTools` + `editGuardRules`;
      `TestResolveTools_AppleToolchain`, `TestEditProfile_DenyTools`, `TestCheckEditGuard_*`)_
- [x] watch has a documented way to follow macOS, iOS and tvOS app logs
      _(Phase 5: `agents/log-watcher.md` unified-log recipe; `docs/agents.md` copy due in Phase 7)_

### Technical approach

Settle F1 by experiment before touching the sandbox (decision 3). The candidates, in the order
they are cheap to try: `swift build --disable-sandbox` / `swift test --disable-sandbox` (SwiftPM's
own nested sandbox is the first refusal); xcodebuild's
`-IDEPackageSupportDisableManifestSandbox=YES` and
`-IDEPackageSupportDisablePluginExecutionSandbox=YES`; extra `codexWritableRoots` for the SwiftPM
and Xcode cache paths; relocating caches through the environment (`CLANG_MODULE_CACHE_PATH`,
`SWIFTPM_*`) or into the repo (`-derivedDataPath`, `--cache-path`), the shape
`codexPackageCacheEnv` already uses for npm and pip. Whatever the experiment picks is pinned by
tests on `codexWritableRoots` and the env, with the existing per-role negative control
(`TestCodexBuildExecArgs_OtherRolesUnwidened`) extended.

Classification (F2) follows the precheck precedent: patterns are first-match and command-shaped,
so `swift --version` must stay out while `swift build` goes in; the bare `scripts/*.sh` gap is a
`matchPatterns` change, not a new pattern. Detection (F3) walks to a bounded depth with an
exclusion list rather than a full tree scan, because `.build/` and `DerivedData/` are deep and
large. Session-start init (F5) composes from detection and never overwrites — the negative control
is a fully configured project left byte-identical.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/provider_codex.go` | `codexWritableRoots` (`:141`), `codexPackageCacheEnv` (`:201`) — the sandbox grants and cache relocation; Phase 4 adds `codexShimEnv`, `swiftPMCacheRoots`, `codexHostOS` |
| `tools/muxcode/bus/provider_codex_test.go` | the per-role sandbox pins, including `OtherRolesUnwidened` |
| `config/codex-shim/` | Phase 4 — `swift` and `xcodebuild` shims plus `bash_env`, installed to `$(CONFIGDIR)/codex-shim/` by `make install` and reached through `BASH_ENV` on the Codex build and test agents |
| `tools/muxcode/bus/hook.go` | `DefaultBuildPatterns` (`:359`), `DefaultTestPatterns` (`:371`), `matchPatterns` |
| `tools/muxcode/bus/detect.go` | `DetectProject` (`:107`), the Swift context text (`~310`) |
| `tools/muxcode/bus/profile.go` | `runWatchActions` (`:556`), `DefaultConfig().ToolProfiles` (`:580+`), edit's `DenyTools` |
| `tools/muxcode/bus/provider_claude.go` | `--dangerously-skip-permissions` for non-edit roles (`:46`) |
| `tools/muxcode/bus/launcher.go` | session launch — where session-start init hooks in |
| `scripts/probe-xcode-sandbox.sh` | Phase 1 probe — runs each command through `codex sandbox` under the role's real launch policy, verified against `muxcode agent sandbox-fingerprint` |
| `scripts/test-xcode-compat.sh` | the integration test (new) |
| `cmd/context.go` | `muxcode context init [DIR]` — session-start init on demand, the script's road (Phase 8) |
| `bus/project_init.go` | `InitSessionProject`: detect → `InitProjectFiles` → `announceProjectInit`, the one call both the launcher and `context init` make (Phase 8) |

## Implementation

### Phase 1: Experiment

- [x] Write a PII-safe probe script under `scripts/`, read-only in the repository, that runs each
      command in a scratch Swift package and XcodeGen project through the real Codex build and test
      launch args — _`scripts/probe-xcode-sandbox.sh`: fixture laid out like tibo
      (`packages/ProbeKit`, `apps/ios`), each cell through `codex sandbox` with the role's real
      policy, rebuilt from `codexSandboxArgs`' inputs and hash-checked against
      `muxcode agent sandbox-fingerprint` (build `709f8f738ea4`, test `417232ec189c` matched);
      unsandboxed `host` column as control. Not read-only outside the repo: the `roots` and `host`
      cells touch the real home caches — see the Measurement limitation below_
- [x] Probe `swift build`, `swift test`, `swift run`, `xcodegen generate`, `xcodebuild … build`
      (generic simulator destination) and `xcrun simctl list`, through the run agent — _2026-10-29,
      run-agent task `1793303671-spawn-27a1f0ea-10fa3ea7`, exit 0, ~1.5 min; codex-cli 0.162.1,
      Xcode 27.1, Swift 6.4, XcodeGen 2.46.0, no simulator runtimes installed_
- [x] Try the candidates against each failure: `--disable-sandbox`, the xcodebuild
      `-IDEPackageSupportDisable*Sandbox=YES` flags, extra `codexWritableRoots`, cache relocation
      via env or in-repo paths — _variants `reloc`, `roots`, `nosbx`, `nosbx+reloc`, `nosbx+roots`,
      see the findings below_
- [x] Record the discriminating outcome here — what each command needs, and what nothing makes
      work — then choose the fix — _below, 2026-10-29_

### Phase 1 findings (2026-10-29)

Measured by `scripts/probe-xcode-sandbox.sh` through the run agent (task
`1793303671-spawn-27a1f0ea-10fa3ea7`, exit 0, ~1.5 min; log kept at
`/tmp/probe-xcode-sandbox-scrubbed.log`). Each cell runs one command in a fresh copy of a scratch
fixture laid out like tibo — a SwiftPM package nested at `packages/ProbeKit` (library, executable,
XCTest target) and an XcodeGen iOS app at `apps/ios` depending on it — through `codex sandbox`
(Codex's own Seatbelt runner, no model) under the build and test roles' real launch policy. The
policy is rebuilt from `codexSandboxArgs`' inputs and proven identical by hash: build
`709f8f738ea4`, test `417232ec189c`, both equal to the installed binary's
`muxcode agent sandbox-fingerprint`. `host` is the unsandboxed control. Environment: codex-cli
0.162.1, Xcode 27.1, Swift 6.4, XcodeGen 2.46.0, **no simulator runtimes installed**.

Variants, cheapest candidate first: `reloc` = `CLANG_MODULE_CACHE_PATH`,
`SWIFTPM_MODULECACHE_OVERRIDE`, `SWIFTPM_TESTS_MODULECACHE` into `/tmp` via
`shell_environment_policy.set` (the `codexPackageCacheEnv` shape); `roots` = extra writable roots
`DARWIN_USER_CACHE_DIR`, `~/Library/Caches/org.swift.swiftpm`, `~/Library/org.swift.swiftpm`,
`~/.swiftpm`, `~/Library/Developer/Xcode/DerivedData`; `nosbx` = SwiftPM `--disable-sandbox` /
xcodebuild `-IDEPackageSupportDisableManifestSandbox=YES
-IDEPackageSupportDisablePluginExecutionSandbox=YES`. `=` means not applicable: `xcodegen` and
`simctl` take no sandbox flag, so a `nosbx` cell is identical to the cell without the flag, whether
that passed (`xcodegen` base) or failed (`simctl` base).

**Measurement limitation.** The fresh fixture copy isolates only in-repo state. The `roots` cells
are granted, and the `host` cells run with, the real home caches — `~/Library/Caches/org.swift.swiftpm`,
`~/Library/org.swift.swiftpm`, `~/.swiftpm`, the `DARWIN_USER_CACHE_DIR` clang module cache, Xcode's
`DerivedData`, `~/Library/Logs/CoreSimulator` — and may populate or modify them; those caches carry
across cells in run order and from earlier runs. So the matrix measures **access** — whether a cell
may write where it must — not cold-cache behaviour, and no cache experiment here was isolated. The
probe is read-only only with respect to the repository.

| Role | Command | base | reloc | roots | nosbx | nosbx+reloc | nosbx+roots | host | Needs |
|------|---------|------|-------|-------|-------|-------------|-------------|------|-------|
| build | `swift build` | FAIL | FAIL | FAIL | OK | OK | OK | OK | `--disable-sandbox` |
| build | `swift run` | FAIL | FAIL | FAIL | OK | OK | OK | OK | `--disable-sandbox` |
| test | `swift test` | FAIL | FAIL | FAIL | OK | OK | OK | OK | `--disable-sandbox` |
| build | `xcodegen generate` | OK | OK | OK | = | = | = | OK | nothing |
| build | `xcodebuild … -destination 'generic/platform=iOS Simulator' build` | FAIL | FAIL | FAIL | FAIL | FAIL | OK | OK | both `-IDEPackageSupportDisable*Sandbox=YES` flags **and** a writable `~/Library/Caches/org.swift.swiftpm` |
| build, test | `xcrun simctl list devices` | FAIL | FAIL | FAIL | = | = | = | OK | nothing tried works |

**Discriminating outcome.**

- **SwiftPM (`swift build|test|run`): the nested sandbox is the only blocker.** Every cell without
  `--disable-sandbox` fails `Invalid manifest … sandbox-exec: sandbox_apply: Operation not
  permitted`, whatever is relocated or granted; `--disable-sandbox` alone passes. F1's second cause
  (the module cache) does **not** block on Swift 6.4: unwritable caches degrade to warnings
  (`… org.swift.swiftpm is not accessible or not writable, disabling user-level cache features`,
  `failed storing manifest … readonly database`). Relocation and extra roots changed nothing.
  **No environment variable disables SwiftPM's sandbox** — the toolchain binaries name only
  `SWIFTPM_MODULECACHE_OVERRIDE` and `SWIFTPM_TESTS_MODULECACHE`; it is a command-line flag only, so
  env alone cannot fix it.
- **`xcodegen generate` needs nothing.**
- **`xcodebuild` (generic simulator build, local package dependency) needs two changes.** `roots`
  alone fails on the same nested `sandbox_apply` (`Could not resolve package dependencies`); `nosbx`
  and `nosbx+reloc` then fail writing
  `~/Library/Caches/org.swift.swiftpm/manifests/ManifestLoading/probekit.dia … (Operation not
  permitted)`, a path env relocation does not move. `nosbx+roots` → `** BUILD SUCCEEDED **`. The
  only root a failure names is `~/Library/Caches/org.swift.swiftpm`; the narrowest sufficient set
  is not yet isolated, and `DerivedData` is not needed when `-derivedDataPath` is in-repo, as
  tibo's scripts pass. The `CoreSimulatorService` errors (`connection became invalid`, `Cannot talk
  to the service used to manage runtime disk images`) are noise for a generic destination — they
  print in the succeeding cell too.
- **`xcrun simctl` cannot work under the Codex sandbox.** Every sandboxed variant fails with
  `CoreSimulatorService connection became invalid` — an XPC/mach service the Seatbelt policy
  denies, not a file path, so no writable root or env can fix it; it passes on host. The same is
  expected of `simctl boot|install|launch|spawn` and `devicectl` (not measured).
- **Untested:** a destination needing an installed simulator runtime (none on this machine).

**Fix chosen (for Phase 4).**

1. **SwiftPM and xcodebuild sandbox flags via a muxcode shim on the Codex build/test `PATH`.**
   muxcode puts a shim directory first on `PATH` through `shell_environment_policy.set.PATH`, for
   build and test only; its `swift` adds `--disable-sandbox` to `build|test|run|package`
   subcommands and its `xcodebuild` adds the two `-IDEPackageSupportDisable*Sandbox=YES` user
   defaults. tibo's unmodified `scripts/test.sh` and `scripts/build-docs.sh` then pass, with no
   project edit and no host-wide `defaults write`; Codex's Seatbelt still confines the process, and
   SwiftPM's nested sandbox is redundant inside it. Pinned by tests: the `PATH` entry present for
   build and test, absent for every other role. Not chosen: telling projects to pass
   `--disable-sandbox` themselves, a per-project script edit. _(Superseded in part: the `PATH`
   road fails under Codex's `bash -lc` — see [Phase 4 findings](#phase-4-findings-2026-11-02); the
   shim reaches `PATH` through `BASH_ENV` instead.)_
2. **Writable root `~/Library/Caches/org.swift.swiftpm` for build** (and test, if `xcodebuild test`
   runs there), narrowed in Phase 4 to the smallest set that keeps `xcodebuild` passing. No
   `~/Library/Developer` grant.
3. **Simulator and device control (`simctl`, `devicectl`) cannot run on Codex.** These steps belong
   to the Claude roles (serve, deploy, run). Rerouting a project script's simulator steps off the
   Codex build agent is the decision 3 fallback, so it **needs the user's approval** before Phase 4
   or 5 acts on it — recorded here as proposed, not decided.

### Phase 2: Detection

- [x] `DetectProject` finds `Package.swift`, `*.xcodeproj` and XcodeGen `project.yml` below the
      root, to a bounded depth, skipping `.build/`, `DerivedData/` and `node_modules/` —
      _`bus/detect.go` 2026-10-29: `indicator` gains `Depth` and `Accept`; `nestedMatches` walks
      `swiftDetectDepth = 3` levels, records nested markers as root-relative paths, skips `.build`,
      `DerivedData`, `node_modules`, `Pods`, `Carthage` and hidden directories, and records an
      `.xcodeproj` bundle without entering it; `project.yml` counts only with a top-level
      `targets:` or `include:` key (`isXcodeGenSpec`, first 256 KiB)_
- [x] Richer Swift and Xcode context: `xcodebuild`, schemes, destinations, simulators, XcodeGen —
      _`swiftConventionText` built from what was found: nested package dirs, XcodeGen specs with
      `xcodegen generate`, `xcodebuild` build/test/`-list`/`-showdestinations`, `xcrun simctl`;
      lists capped at `maxListedMarkers = 6`; the root-only package text is byte-identical_
- [x] Tests: nested markers found, excluded directories skipped, root-only projects unchanged —
      _`detect_test.go`: `SwiftNestedMarkers` (tibo layout, exact indicators),
      `SwiftSkipsExcludedDirs`, `SwiftDepthBound` (3 found, 4 not), `ProjectYmlMustBeXcodeGen`,
      `SwiftRootOnlyUnchanged` (bundle not entered), `ConventionText_Swift`. Suite pass is the
      test agent's report (`./test.sh` EXIT=0, `ts 1793304975`); no hook row for the run exists_

### Phase 3: Classification

- [x] Swift and Xcode build and test patterns — project-derived and/or defaults — for `swift build`,
      `swift test`, `xcodebuild … build|test`, `xcodegen generate` — _`bus/hook.go` 2026-10-29:
      `swift build` and `./build-*.sh` in `DefaultBuildPatterns`, `swift test` in
      `DefaultTestPatterns`; `xcodebuild` classified by its **action words** (`xcodebuildAction`:
      test actions → `CmdTest`, build actions → `CmdBuild`, modes such as `-list`/`-showBuildSettings`
      → unknown; options consume their operand so `-archivePath test` or `-scheme "My test App"`
      cannot spell an action); `xcodegen generate` is the new `CmdBuildPrecheck`
      (`DefaultBuildPrecheckPatterns`), feeding build→test only on failure — the `go vet` hazard
      one stage up. The evidence guard and `graph_exec`'s stage map follow the same classification_
- [x] A bare `scripts/test.sh` / `scripts/build-*.sh` classifies as the `bash` form does —
      _`matchPatterns`: a first word containing `/` matches a path-carrying pattern by basename;
      pinned by `scripts/test.sh`, `./scripts/test.sh --verbose`, `scripts/build-ios.sh` classified
      and `scripts/test-all.sh`, `cat scripts/test.sh` not_
- [x] The run chain's watch allowlist admits the `swift test` / `xcodebuild test` verification shapes
      — _`appleTestRunShapes` (`bus/profile.go`): `swift test`, `swift test *`, each behind
      `cd * &&` for a nested package. Reachable only under a `MUXCODE_TEST_PATTERNS` override:
      with the defaults `ChainEvent` routes `swift test` to the test chain for every role, so the
      run chain never sees it (worker flag, 2026-10-29). `xcodebuild` deliberately **not** added: `xcodebuildAction` is
      not a pattern, so every real xcodebuild test action reaches the test chain whatever the
      overrides, and a glob would admit `xcodebuild test -showBuildSettings`, which runs no test
      (review should-fix, 2026-10-29)_
- [x] Negative control pinned: `swift --version` and other non-build Swift calls stay unclassified
      — _`TestClassifyCommand_SwiftAndXcode` and the conditions table: `swift --version`,
      `xcodebuild -list`, `-showBuildSettings … test`, a scheme or path named `test`, a quoted
      scheme — all unclassified or classified by their real action_

### Phase 4: Codex sandbox fix

- [x] Apply the fix Phase 1 chose, in `codexWritableRoots` and/or `codexPackageCacheEnv`
      _(2026-11-02 — `swiftPMCacheRoots` in `codexWritableRoots`, `codexShimEnv` beside
      `codexPackageCacheEnv`, and the `config/codex-shim/` shims; Seatbelt-verified 11/11 (see
      findings). Review `1793632359` found one must-fix — the `swift` shim's argument scan let an
      executable's own `--disable-sandbox` suppress SwiftPM's — fixed by inserting the flag
      unconditionally (`TestCodexShim_Flags` cases `run probe --disable-sandbox`,
      `--disable-sandbox-check`); re-review `1793632578` clean, `./build.sh` exit 0 (`ts 1793632523`).
      The post-fix `./test.sh` pass is the test agent's self-report with no hook row; the last
      row-backed suite pass is pre-fix, `ts 1793632253`.)_
- [x] Pin it: the new roots or env present for build and test, absent for every other role
      _(`TestCodexBuildExecArgs_SwiftShimAndCacheOnDarwin`; negative controls `NoSwiftGrantsOffDarwin`,
      `NoShimWithoutInstallDir`, `OtherRolesUnwidened`, `WatchGetsNetworkOnly`; `./test.sh` exit 0,
      hook row `ts 1793632253`, 2026-11-02)_
- [x] tibo's `scripts/test.sh` and `scripts/build-docs.sh` pass on the Codex test and build agents
      _(2026-11-02. Under workspace-write: `build-docs.sh` PASS, `test.sh` FAIL — AVAssetWriter
      refused by Seatbelt's `system-info` deny, see the findings; user decision 4 drops the sandbox
      by default. Re-run on tibo's reloaded no-sandbox agents — stamps `launchsandbox-{build,test}.hash`
      `68d8b216…` 10:43, equal to the installed `agent sandbox-fingerprint` and to each other, so no
      per-role grants — `scripts/test.sh` hook row exit 0 `ts 1793636350` (270 tests in 55 suites,
      1 skipped), `scripts/build-docs.sh` hook row exit 0 `ts 1793636337`.)_

### Phase 4 findings (2026-11-02)

**Deviation from Fix-chosen item 1, measured.** Phase 1 planned the shim directory on `PATH` via
`shell_environment_policy.set.PATH`. That cannot work: Codex runs every command as `/bin/bash -lc`
(the rollouts show `"command":["/bin/bash","-lc",…]`), a login profile may set `PATH` outright —
`~/.bash_profile` on the test host exports an absolute `PATH`, so a launch-time entry vanished
entirely — and bash reads `BASH_ENV` *after* the login profile, even for `-lc`. So the shim
directory reaches `PATH` through `BASH_ENV` → `<install dir>/codex-shim/bash_env`, which puts its
own directory first (once). Caveats: bash-only — zsh ignores `BASH_ENV`, and a zsh login shell gets
no shim; a `BASH_ENV` the user set is replaced for build's and test's commands; `xcrun swift`,
`xcrun xcodebuild` and absolute paths bypass the shim.

**What shipped (uncommitted at writing).**

| Piece | Behaviour |
|-------|-----------|
| `config/codex-shim/{swift,xcodebuild,lib.sh,bash_env}` | POSIX sh shims. `swift` inserts `--disable-sandbox` right after `build\|test\|run\|package` (before any operand — `swift run EXE ARGS` hands later words to EXE), unconditionally: SwiftPM accepts the flag repeated, and no scan can tell its own options from EXE's (the first cut skipped it when present, so `swift run probe --disable-sandbox` left SwiftPM sandboxed — review must-fix, fixed); `xcodebuild` prepends the two `-IDEPackageSupportDisable*Sandbox=YES` defaults unless either is set. Both act only when `CODEX_SANDBOX=seatbelt`, else pure pass-through to the first real binary on `PATH` outside the shim dir. `make install` copies them to `$(CONFIGDIR)/codex-shim/` |
| `codexShimEnv` (`bus/provider_codex.go`) | build and test on macOS get `-c shell_environment_policy.set.BASH_ENV="<install dir>/codex-shim/bash_env"`; nil for other roles, other OSes, or no resolvable install dir. The fingerprint hashes the path, not the content, so an install reaches running agents without a reload |
| `swiftPMCacheRoots` | build and test on macOS get `--add-dir ~/Library/Caches/org.swift.swiftpm` — the only root a failure named. No `~/Library/Developer` grant (item 2, narrowed) |
| `codexHostOS` | `runtime.GOOS` as a variable seam so the darwin-only grants test on any host |
| Tests | `SwiftShimAndCacheOnDarwin`; negative controls `NoSwiftGrantsOffDarwin`, `NoShimWithoutInstallDir`, plus `OtherRolesUnwidened` / `WatchGetsNetworkOnly` rerun with the macOS grants on; `TestCodexShim_Flags` runs the real shims against fake binaries in and out of the sandbox; `TestCodexShim_BashEnvPrependsShimDir` (prepend, no-`BASH_ENV` control, prepend-once) |
| `scripts/probe-xcode-sandbox.sh` | Launch-policy mirror gains the SwiftPM root and `BASH_ENV` so the fingerprint check still matches the new binary; new `--login` runs cells as `/bin/bash -lc` as Codex does, so `base` now measures the fixed policy |

**Verification under real Seatbelt, before install.** `/tmp/mux207-verify-shim.sh` through the run
agent (reply `1793632100-run-40f1c490`, exit 0; watch tail "ALL 11 cells as-expected"), each cell
under `codex sandbox` workspace-write with network — the build/test launch shape — on the probe's
tibo-shaped fixture:

| Cell | Result |
|------|--------|
| `-lc`, shim + SwiftPM root: `swift build`, `swift test` (1 XCTest passed), `swift run probe`, `xcodebuild … generic iOS Simulator build` | OK (`BUILD SUCCEEDED`); `command -v` resolved the shim each time |
| Control — `-lc`, no shim | `/usr/bin/swift`; `Invalid manifest … sandbox_apply: Operation not permitted`; xcodebuild rc=74 |
| Control — shim, no root | xcodebuild `cannot open file ~/Library/Caches/org.swift.swiftpm/manifests/ManifestLoading/probekit.dia … (Operation not permitted)` — the root is required |
| Hand-flag reference (Phase 1 shape, `-c`) | OK |

An earlier verifier run omitted network and failed every cell (xcodebuild rc=74, swift manifest) —
the verifier's error, not the shim's: build and test launch with network on.

**Box 3 measured (2026-11-02) — the sandbox fails tibo's suite for a reason no grant can reach.**
tibo's Codex build and test agents reloaded at 10:09:52 onto the Phase 4 sandbox (shim + SwiftPM
root) and ran the project's own scripts:

| Run | Agent | Result |
|-----|-------|--------|
| `scripts/build-docs.sh` | tibo build (Codex, workspace-write) | PASS — hook rows exit 0 (`ts 1793633074`, `1793633079`); warnings only, SwiftPM cache paths "not writable" |
| `scripts/test.sh` | tibo test (Codex, workspace-write) | FAIL — hook row exit 1 (`ts 1793633089`): Tibo Link 11/11 passed, then `TiboCoreTests` crashed at `PhotoSyncTests.swift:39` (`adaptor.pixelBufferPool!` nil) |
| `scripts/test.sh` control | muxcode run agent (Claude, no sandbox) | PASS — 11 tests in 2 suites + 270 in 55 suites, 0 failures (`/tmp/tibo-test.log`; backgrounded, so no hook row) |

Cause probe (run reply `1793633752-run-5219a0b2`): a compiled AVAssetWriter H.264 writer
reproducing tibo's `TestMedia.movie()`. `startWriting()` returns true on the host and false under
the test-role Seatbelt policy in both `$TMPDIR` and the workspace, the destination directory
writable in every cell — `AVFoundationErrorDomain -11800`, underlying `NSOSStatusErrorDomain -12903`.
The Seatbelt log names it: `deny(1) system-info vfs.disk-space` ×13 — AVAssetWriter's free-space
check is refused, so directory permissions are irrelevant. Codex 0.162.1's Seatbelt policy is
compiled into the binary with no `system-info` allow; no writable root, network switch, env or
config can extend it. Affected tibo tests: every `TestMedia.movie()` user
(`PhotoSyncTests.swift:177`, `FolderLibraryTests.swift:63`).

**User decision 4 (2026-11-02).** "Run Codex's test agent with no sandbox, make this a default for
all codex agents in muxcode."

**Code change (uncommitted at writing).**

| Piece | Behaviour |
|-------|-----------|
| `codexSandboxMode(role)` (`bus/provider_codex.go`) | `MUXCODE_<ROLE>_CODEX_SANDBOX` wins, then `MUXCODE_CODEX_SANDBOX`, then the default `danger-full-access`. Any set value other than `danger-full-access` selects `workspace-write` — a typo keeps the sandbox, never drops it |
| `codexSandboxArgs` | By default `-s danger-full-access` and **none** of the workspace-write grants (no `--add-dir`, no `network_access`, no package-cache env, no `BASH_ENV` shim). The opt-out goes through `codexWorkspaceWriteArgs`, byte-identical to the previous arguments. Approval policy unchanged (`-a never`; `-a on-request` for review and analyze, where nothing now escalates); research keeps `--search` |
| Fingerprint | `codexSandboxArgs` changed, so the agent-defs watchdog reloads every running Codex agent onto no sandbox after install |
| Tests | `TestCodexBuildExecArgs_DefaultIsFullAccess`; `TestCodexSandboxMode_OptOutAndPrecedence` (opt-out, precedence both ways, typo keeps the sandbox — the negative control); the existing grant tests run on the opt-out via `argsFor`; `TestLaunchSandboxFingerprint_FollowsSandboxArgs` also checks that dropping the sandbox changes the fingerprint |
| `scripts/probe-xcode-sandbox.sh` | Pins `MUXCODE_{BUILD,TEST}_CODEX_SANDBOX=workspace-write` — it measures the opt-out |
| Root `CLAUDE.md` | Codex CLI sandbox bullet rewritten by edit; [`docs/configuration.md`](../../configuration.md), [`docs/architecture.md`](../../architecture.md), [`docs/agents.md`](../../agents.md) by plan |

**What this does to the Fix-chosen items.** Items 1–2 (the shims, the SwiftPM cache root) now apply
only under the `workspace-write` opt-out: the default launch has no Seatbelt, so SwiftPM's nested
`sandbox-exec` is not refused and the shims — gated on `CODEX_SANDBOX=seatbelt` — pass through.
Item 3 (`simctl`/`devicectl` to Claude roles): the CoreSimulatorService failure was a Seatbelt
denial, so it is expected not to arise under the default — **unmeasured**; the rerouting stays
proposed for the opt-out only, still needing the user's approval there.

**Box 3 re-run on the no-sandbox default (2026-11-02 11:18).** tibo's build and test agents were
reloaded by the watchdog onto the installed binary at 10:43 (`launchsandbox-{build,test}.hash` both
`68d8b216…`, matching `muxcode agent sandbox-fingerprint` — one hash for both roles, so no per-role
grants: the `danger-full-access` launch). `scripts/test.sh` then passed — hook row exit 0
`ts 1793636350`, 270 tests in 55 suites, 1 skipped — and `scripts/build-docs.sh` passed, hook row
exit 0 `ts 1793636337`. Box 3 and AC2 ticked on those rows; Phase 4 complete.

**Two consequences of decision 4, found by the chain the same day.**

- *Review `1793636687` must-fix — the opt-in could lose its `-s`.* `codexSandboxArgs` at HEAD emitted
  `-s workspace-write` only when a role had a resolvable root or a network grant; with every state
  root unresolvable and no network (review, analyze, research, run, commit) it emitted no `-s`, and
  the sandbox fell to whatever `~/.codex/config.toml` says. The hole predates MUX-207 but the
  explicit opt-in made it a broken promise, so `codexWorkspaceWriteArgs` now opens with
  `-s workspace-write` unconditionally, pinned by
  `TestCodexBuildExecArgs_OptOutNamesPolicyWithNoGrants` (HOME under a regular file so no state root
  can be created, with a fixture self-check that the roots really were unresolvable).
- *`TestOpenCodeModelsExist` failed on the first unsandboxed run.* The Codex test agent could now
  reach `opencode models`, which on a machine with no opencode credentials lists only the free
  `opencode/*` models, so every `opencode-go` id read as missing (`./test.sh` exit 1,
  `ts 1793636450`). `provider_models_test.go` now judges only providers the catalog offers at least
  one model from (`missingOpenCodeModels`), skipping when it can judge none; `TestMissingOpenCodeModels`
  is the negative control — a phantom id from an offered provider is still caught. `./test.sh` is
  row-backed green again (`ts 1793636631`, `1793636696`).

### Phase 5: Tool profiles

- [x] Apple and Swift tool groups per role: build/test (`swift`, `xcodebuild`, `xcodegen`),
      watch (`log stream`/`log show`, `xcrun simctl spawn … log stream`), serve (`open`,
      `xcrun simctl launch`, `swift run`), deploy (`codesign`, `xcrun devicectl`, `notarytool`,
      `security`, `plutil`, `iconutil`)
      _(2026-11-02 — `appleBuildTools`, `appleTestTools`, `appleLogTools`, `appleServeTools`,
      `appleDeployTools` in `bus/profile.go`, appended to each role's `Tools` (not `SharedTools`) so
      `CdPrefix` yields the nested-package `cd packages/Kit && swift build` form. Deviation, accepted
      in review: `security` is narrowed to `find-identity`/`find-certificate`/`import` — a blanket
      grant would include `find-generic-password -w` and `dump-keychain`, which print keychain
      secrets; `xcrun stapler` and `xcrun simctl list` added beyond the list above.)_
- [x] edit's `DenyTools` cover `swift build`, `swift test` and `xcodebuild`
      _(`swift build*`, `swift test*`, `xcodebuild*`, `xcrun xcodebuild*`. `DenyTools` bite only on
      OpenCode, so `editGuardRules` in `bus/hook.go` gains the same prefixes with a reason routing
      builds to build and tests to test — the enforcement on Claude edit. Follow-up, not done:
      `planGuardRules` still lack the Swift/Xcode prefixes.)_
- [x] The watch app-log recipe documented for macOS, iOS simulator and tvOS
      _(`agents/log-watcher.md` "macOS, iOS and tvOS apps (unified log)": `log show --last` /
      `log stream --timeout`, `xcrun simctl spawn booted|<udid> log …`, every call bounded and piped
      to `muxcode pii-scrub`; simulator control named as serve's work, device install as deploy's.
      The `docs/agents.md` copy is Phase 7.)_
- [x] Pinned by the profile test
      _(`TestResolveTools_AppleToolchain` — per-role wants, cross-role negative controls, no blanket
      `security`/`simctl`/`xcrun`/`swift` grant, no keychain-secret verb for any role;
      `TestEditProfile_DenyTools` Swift entries and no blanket `swift` deny;
      `TestCheckEditGuard_Blocked` Swift/Xcode cases incl. cd-prefixed and `xcrun` forms;
      `TestCheckEditGuard_Allowed` negative controls `swift --version`, `which xcodebuild`.
      `./test.sh` exit 0 hook rows `ts 1793637381`, `1793637390`; reviews `6d007736`, `7f9dd1b0`
      clean after the guard-test should-fix of `3531309c` was addressed.)_

### Phase 6: Session-start initialization

- [x] On session launch, create a missing `.muxcode/config` with the detected build, test and
      precheck patterns; a missing `.claude/settings.json` with permissions for the detected
      toolchain; a missing `CLAUDE.md` stub naming the build and test commands (decision 1)
      _(2026-11-02 — `InitProjectFiles` (`bus/project_init.go`), wired into `LaunchSession` before
      any agent launches: `.claude/settings.json` (read-only toolchain queries only — edit is the one
      role that consults Claude permissions and its guard refuses builds) and the `CLAUDE.md` stub
      (skipped when `.claude/CLAUDE.md` exists) are created as specified. The `.muxcode/config`
      write was at first withheld under any home config, because `GetShellConfig` read only the
      first file it found and the project config would have hidden every role's `_CLI`/`_MODEL`;
      **user decision 5** layers the configs instead — home as base, project overriding per key,
      `MUXCODE_CONFIG` still alone — and the write is now withheld only under `MUXCODE_CONFIG`
      (`TestGetShellConfig_LayersProjectOverHome`, `TestInitProjectFiles_LayersOverHomeConfig`,
      `_WithholdsConfigUnderMuxcodeConfig`). Ticked 2026-11-02 13:12 on the re-verify: review
      `e76b2094` clean after two launcher bypasses were fixed, `./test.sh` exit 0 hook row
      `ts 1793643090` — see [Phase 6 findings](#phase-6-findings-2026-11-02).)_
- [x] Never overwrite an existing file; leave created files untracked (decision 2)
      _(`O_EXCL` on every write — an existing file or a dangling symlink at the path is left alone;
      no git command runs; `TestInitProjectFiles_LeavesExistingByteIdentical`,
      `_PartialFillsOnlyTheGap`)_
- [x] Write a lifecycle row naming what was created, and send edit a notice listing the new files
      _(`announceProjectInit`: `project-init` "created … (untracked)" or `project-init-skipped`, and
      a `project-init` request to edit listing the files; `TestAnnounceProjectInit` incl. the
      skip-only negative)_
- [x] Negative control pinned: a project with all three present is left byte-identical
      _(`TestInitProjectFiles_LeavesExistingByteIdentical` — files, dangling symlinks,
      `.claude/CLAUDE.md`; plus `_NothingDetectedWritesNothing`. `./test.sh` exit 0 hook rows
      `ts 1793638954`, `1793639143`; review `75eb7eba` clean after the must-fix of `4ad96595`)_

### Phase 6 findings (2026-11-02)

**What shipped (uncommitted at writing).** `bus/project_init.go`: `InitProjectFiles(projectDir,
types)` creates each missing file with `O_EXCL` — `.muxcode/config` carrying the detected build,
test and precheck patterns (round-tripped through `GetShellConfig`; after the review must-fix it
emits only detector-owned type names, never repository paths — `4ad96595` found a detected path
written verbatim into a shell-config comment, where a newline in a nested `Package.swift` path would
have injected a config line); `.claude/settings.json` with read-only toolchain queries only; a
`CLAUDE.md` stub from the detected types' convention text, skipped when `.claude/CLAUDE.md` exists.
Nothing is written when nothing is detected. `announceProjectInit` logs `project-init` /
`project-init-skipped` and sends edit a `project-init` request naming the new files. Wired into
`LaunchSession` (`bus/launcher.go:217`) ahead of every agent launch. Eight tests in
`project_init_test.go`, including `TestToolchainPermissions_PassEditGuard` with a blocked-command
control and `TestInitProjectFiles_WithholdsConfigThatWouldShadow`.

**The `.muxcode/config` write was withheld wherever it would shadow — resolved by user decision 5.**
`GetShellConfig` read the **first** config file it found (`MUXCODE_CONFIG`, then
`.muxcode/config`, then `~/.config/muxcode/config`) and no other. A freshly written project config
would therefore have hidden the home config entirely — on this machine that file holds every role's
`_CLI`/`_MODEL` and the Jira credentials, so tibo's Codex build and test agents would have
relaunched on the default provider. The first cut of `InitProjectFiles` withheld the config whenever
`MUXCODE_CONFIG` or a home config existed (reported as skipped and named in edit's notice), which
here was always. The options put to the user:

| Option | Effect |
|--------|--------|
| Keep the withhold | Decision 1's config file is created only on a machine with no home config; the other two files are always created. Box 1 stays partly open, or decision 1 is amended to say so |
| Layer `GetShellConfig` | Home config as the base, project config overriding per key; the project config can then be written safely everywhere. A behaviour change to config resolution, beyond this spec's scope as written |

**User decision 5 (2026-11-02): layer the configs.** `GetShellConfig` (`bus/config_file.go`) now
reads `~/.config/muxcode/config` as the base and `.muxcode/config` over it key by key
(`shellConfigFiles` lists them lowest precedence first, `readShellConfigInto` lets a later file's
value replace an earlier one's); a `MUXCODE_CONFIG` that names an existing file is still read alone.
`InitProjectFiles` withholds the project config only under `MUXCODE_CONFIG` — a home config is no
longer a reason, since the project file layers over it instead of hiding it. Pinned by
`TestGetShellConfig_LayersProjectOverHome` and `_MuxcodeConfigReadAlone` (`config_file_test.go`),
`TestInitProjectFiles_LayersOverHomeConfig` and `_WithholdsConfigUnderMuxcodeConfig`.
[`docs/configuration.md`](../../configuration.md) "Config File" and "Per-Project Config" and the
root `CLAUDE.md` describe the layering. Box 1 and its acceptance criterion ticked on the graph's
re-verify of this change (13:12).

**Two launcher bypasses the re-verify's reviews caught, both fixed.** `cmd/launcher.go` loaded the
shell config twice: once before the project picker, from the home and start-directory files, and
once after. Review `5d227062` found the first load *exported* the home values into the environment,
where they outranked the chosen project's layered file; review `bb260241` then found `runSession`
still received a `cfg` built before project selection, so a project's `MUXCODE_WINDOWS`,
`ROLE_MAP`, `EDITOR` and `SHELL_INIT` were ignored. Now `RunLauncher` keeps the preliminary load's
key list, `bus.ReloadShellConfig(projectDir, preliminary)` replaces those file-derived values with
the chosen project's layered config, and `projectLauncherConfig` rebuilds the launcher config from
them — pinned by `TestProjectLauncherConfig_ReflectsChosenProject` (`cmd/launcher_test.go`). Review
`e76b2094` clean; `./test.sh` exit 0 hook rows `ts 1793642697`, `1793642912`, `1793643090`.

**Housekeeping flag.** An untracked `config/codex-shim/.build/` (SwiftPM's `CACHEDIR.TAG`, 10:03)
was left by a `swift build` run inside the shim directory; it is not part of any phase and should
be removed before the next commit, not committed.

### Phase 7: Docs

- [x] `CLAUDE.md`: Codex sandbox bullet (Swift and Xcode grants), hook patterns, session-start init
      _(edit, 2026-11-02: chains bullet "Swift and Xcode", Codex sandbox bullet — no-sandbox default,
      SwiftPM root and `codex-shim` under the opt-in — and a new "Session-start init" bullet)_
- [x] [`docs/hooks.md`](../../hooks.md): the Swift and Xcode build and test patterns and the bare
      `scripts/*.sh` rule
      _(plan, 2026-11-02: "Swift and Xcode" paragraph in `### hook bash` — new default patterns,
      `xcodebuildAction` by action word, the `xcodegen generate` build precheck, the basename rule
      for bare project scripts, the negative control, the run-chain allowlist note;
      `MUXCODE_BUILD_PRECHECK_PATTERNS` in Customization)_
- [x] [`docs/configuration.md`](../../configuration.md): session-start init and what it writes
      _(plan, 2026-11-02: new "Session-Start Initialization" section — the three files, their
      content and withhold conditions, never-overwrite, untracked, lifecycle row and edit notice,
      the post-picker config reload; `MUXCODE_BUILD_PATTERNS`/`MUXCODE_TEST_PATTERNS` defaults
      updated and a `MUXCODE_BUILD_PRECHECK_PATTERNS` row added; config layering and the Codex
      sandbox default were documented earlier in Phases 4 and 6)_
- [x] [`docs/agents.md`](../../agents.md): the Apple and Swift tool groups per role, the watch
      app-log recipe
      _(plan, 2026-11-02: "Apple and Swift groups" table under Tool profiles with the `security`
      narrowing and the no-blanket-grant rule; the unified-log recipe under Observers (watch); a
      Swift/Xcode row in edit's enforcement table — `editGuardRules` on Claude, `DenyTools` on
      OpenCode)_

### Phase 8: Integration test

- [x] `scripts/test-xcode-compat.sh`, hermetic on a scratch repo with a nested `Package.swift` and
      `project.yml`: detection finds both; classification writes rows and fires chains through the
      real `hook bash`, with `swift --version` as the negative control; session-init creates the
      three files, never overwrites, and leaves a fully configured project untouched — 2026-11-02,
      hermetic run `23 passed, 0 failed (floor 23), 0 live` (run reply `1793661259-run-776fb561`);
      session init is driven through the new `muxcode context init [DIR]` (see findings below)
- [x] Opt-in live part: a real Codex build and test agent runs `swift build` / `swift test` on the
      scratch package — `--live` / `MUXCODE_XCODE_COMPAT_LIVE=1`: one real `codex exec` on the default
      no-sandbox policy; `23 passed, 0 failed (floor 23), 2 live`, both commands exit 0 and classify
      to build and test rows (run reply `1793661335-run-6c0d7b6b`)
- [x] Coverage floor so a skipped section cannot read as green — floor 23, the exact hermetic pass
      count; the live section is outside the floor and skipped with a printed reason
- [x] Run through the run agent and record the row here — hook row `run-history.jsonl ts 1793661252`
      (`bash scripts/test-xcode-compat.sh > /tmp/test-xcode-compat.log 2>&1; echo "EXIT_CODE:$?"`,
      output `EXIT_CODE:0`); the `--live` runs were log-only (no hook row — the call was backgrounded,
      so `chain-withheld`), their results read from `/tmp/test-xcode-compat-live-2.log`

### Phase 8 findings (2026-11-02)

| Item | What shipped |
|------|--------------|
| `muxcode context init [DIR]` | New subcommand (`cmd/context.go` `contextInit`). Calls `bus.InitSessionProject(session, dir)` — a new one-call wrapper in `bus/project_init.go` (`DetectProject` → `InitProjectFiles` → `announceProjectInit`) that `LaunchSession` now calls too (`bus/launcher.go:217`), so the script and the launcher run **the same road**, announcement included. Prints `created <path>` (project-relative) per file written, `skipped <path>: <reason>` only for a withheld file (`MUXCODE_CONFIG` or a write error — an existing file, or a stub left out for `.claude/CLAUDE.md`, is silent), or `nothing to create`; a non-directory or extra argument exits 1 |
| `muxcode agent sandbox-args <role>` | New subcommand (`cmd/agent.go` `agentSandboxArgs`): prints `CodexLaunchSandboxArgs(role)` one argument per line under the same `LoadShellConfig` resolution as `sandbox-fingerprint` — the exact slice the fingerprint hashes. `scripts/test-xcode-compat.sh --live` reads it to run `codex exec` under the build and test roles' real launch flags rather than a copy of them |
| First `--live` run | Failed before reaching Codex on bash 3.2: `line 395: model[@]: unbound variable` (an empty array expanded under `set -u`). Fixed in the script; the re-run is the recorded one |
| Hook-row gap | Only the hermetic run wrote a hook row; the `--live` runs were backgrounded, so `backgroundLaunch` withheld the row (MUX-176 by design). Evidence for them is the script's own `=== results:` line in the log |

Documented: `docs/agent-bus.md` (`muxcode context` — usage, table row, on-demand paragraph with examples), `docs/configuration.md` (Session-Start Initialization — on-demand sentence), `CLAUDE.md` `test-xcode-compat` entry (by edit).

## Notes

**Keep Codex.** The Codex sandbox has been widened before for exactly this shape of failure — the
Go caches for test (MUX-160 Decision 1), network for build and test, package caches relocated
rather than granted — and each time the grant was measured first. The brief's candidate list is
ordered so the cheapest, most contained change is tried before any root is widened; a grant on
`~/Library/Developer` is a wide one and needs the experiment's evidence behind it.

**Init is additive by construction.** Decision 1's "never overwrite" and decision 2's "leave
untracked" together mean session-start init can only ever add files the user then reviews — it
cannot change a configured project, and the negative control in Phase 6 and Phase 8 is what proves
that.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-207-xcode-project-compatibility | 3h 35m | 2026-11-02 13:56 |

## Status

Complete — closed 2026-11-02 after Phase 8 committed as `6f8965a`; moved to `completed/`. History: Phases 1–3 complete 2026-10-29; Phase 4 (Codex sandbox fix) complete 2026-11-02: the live tibo run under workspace-write passed build-docs but failed the suite on a Seatbelt `system-info` deny no grant reaches, so by user decision 4 every Codex agent now launches with no sandbox (`MUXCODE_CODEX_SANDBOX=workspace-write` opts back in), and the re-run on the reloaded no-sandbox agents passed both scripts (hook rows `ts 1793636350`, `1793636337`); the opt-in's missing-`-s` hole and the `TestOpenCodeModelsExist` fallout are fixed and pinned, suite green `ts 1793636696`. Phase 5 (Tool profiles) complete 2026-11-02 — Apple tool groups per role, edit deny + guard, watch recipe, pinned (Phase 4 committed as `16abf8d`, Phase 5 as `5727469`; the stray `config/codex-shim/.build/` was removed before the commit). Phase 6 (Session-start initialization) complete 2026-11-02 13:12: settings and `CLAUDE.md` stub created, never-overwrite, notice and negative control all pinned; the `.muxcode/config` write was withheld wherever it would shadow a home config until user decision 5 (12:57) layered `GetShellConfig` — home base, project overrides per key — and two launcher bypasses of that layering were fixed in review (committed `977231d`). Phase 7 (Docs) complete 2026-11-02 — `CLAUDE.md` by edit; `docs/hooks.md`, `docs/configuration.md`, `docs/agents.md` by plan (committed `8455f5e`). Phase 8 (Integration test) complete 2026-11-02 18:15: `scripts/test-xcode-compat.sh` hermetic `23 passed, 0 failed (floor 23)` (run reply `1793661259-run-776fb561`, hook row `ts 1793661252`) and `--live` `23 + 2 live`, both Codex commands exit 0 (run reply `1793661335-run-6c0d7b6b`); `muxcode context init` added as the script's road into session-start init and `muxcode agent sandbox-args` as its road to the roles' real launch flags; suite green on the Phase 8 tree (`./build.sh` ts 1793661843, `./test.sh` ts 1793661907), review `1793661945-review-f58d5d98` 0 must-fix / 0 should-fix (one docs nit, fixed). All phases complete — ready to move to `completed/` when the user says so
