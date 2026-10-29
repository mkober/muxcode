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

## Requirements

### Acceptance criteria

- [x] Experiment establishes, under the Codex sandbox as launched for build and test, what each of
      these needs: `swift build`, `swift test`, `swift run`, `xcodegen generate`,
      `xcodebuild … build` for a generic simulator destination, and `xcrun simctl list` — recorded
      here before any fix — _2026-10-29, see
      [Phase 1 findings](#phase-1-findings-2026-10-29); the only code in the tree is the probe
      script_
- [ ] Codex build and test agents can build and test a SwiftPM package nested below the repo root:
      tibo's `scripts/test.sh` and `scripts/build-docs.sh` pass on the Codex test and build agents
- [ ] Swift and Xcode build and test commands are classified: each writes an authoritative history
      row and fires its chain; a bare `scripts/test.sh` / `scripts/build-*.sh` is classified as well
      as the `bash` form; negative control — a non-build Swift call such as `swift --version` stays
      unclassified
- [ ] Detection finds a Swift or Xcode project whose `Package.swift`, `*.xcodeproj` or XcodeGen
      `project.yml` sits below the root, at a bounded depth that skips `.build/`, `DerivedData/`
      and `node_modules/`
- [ ] Session start creates missing `.muxcode/config`, `.claude/settings.json` and `CLAUDE.md` from
      detection: it never overwrites; it leaves the files untracked; it writes a lifecycle row
      naming what was created; it sends edit a notice listing the new files to review and commit;
      negative control — a project with all three present is untouched
- [ ] Tool profiles grant the Apple and Swift toolchain where each role needs it, and edit's
      `DenyTools` cover Swift and Xcode builds and tests — pinned by the profile test
- [ ] watch has a documented way to follow macOS, iOS and tvOS app logs

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
| `tools/muxcode/bus/provider_codex.go` | `codexWritableRoots` (`:141`), `codexPackageCacheEnv` (`:201`) — the sandbox grants and cache relocation |
| `tools/muxcode/bus/provider_codex_test.go` | the per-role sandbox pins, including `OtherRolesUnwidened` |
| `tools/muxcode/bus/hook.go` | `DefaultBuildPatterns` (`:359`), `DefaultTestPatterns` (`:371`), `matchPatterns` |
| `tools/muxcode/bus/detect.go` | `DetectProject` (`:107`), the Swift context text (`~310`) |
| `tools/muxcode/bus/profile.go` | `runWatchActions` (`:556`), `DefaultConfig().ToolProfiles` (`:580+`), edit's `DenyTools` |
| `tools/muxcode/bus/provider_claude.go` | `--dangerously-skip-permissions` for non-edit roles (`:46`) |
| `tools/muxcode/bus/launcher.go` | session launch — where session-start init hooks in |
| `scripts/probe-xcode-sandbox.sh` | Phase 1 probe — runs each command through `codex sandbox` under the role's real launch policy, verified against `muxcode agent sandbox-fingerprint` |
| `scripts/test-xcode-compat.sh` | the integration test (new) |

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
   `--disable-sandbox` themselves, a per-project script edit.
2. **Writable root `~/Library/Caches/org.swift.swiftpm` for build** (and test, if `xcodebuild test`
   runs there), narrowed in Phase 4 to the smallest set that keeps `xcodebuild` passing. No
   `~/Library/Developer` grant.
3. **Simulator and device control (`simctl`, `devicectl`) cannot run on Codex.** These steps belong
   to the Claude roles (serve, deploy, run). Rerouting a project script's simulator steps off the
   Codex build agent is the decision 3 fallback, so it **needs the user's approval** before Phase 4
   or 5 acts on it — recorded here as proposed, not decided.

### Phase 2: Detection

- [ ] `DetectProject` finds `Package.swift`, `*.xcodeproj` and XcodeGen `project.yml` below the
      root, to a bounded depth, skipping `.build/`, `DerivedData/` and `node_modules/`
- [ ] Richer Swift and Xcode context: `xcodebuild`, schemes, destinations, simulators, XcodeGen
- [ ] Tests: nested markers found, excluded directories skipped, root-only projects unchanged

### Phase 3: Classification

- [ ] Swift and Xcode build and test patterns — project-derived and/or defaults — for `swift build`,
      `swift test`, `xcodebuild … build|test`, `xcodegen generate`
- [ ] A bare `scripts/test.sh` / `scripts/build-*.sh` classifies as the `bash` form does
- [ ] The run chain's watch allowlist admits the `swift test` / `xcodebuild test` verification shapes
- [ ] Negative control pinned: `swift --version` and other non-build Swift calls stay unclassified

### Phase 4: Codex sandbox fix

- [ ] Apply the fix Phase 1 chose, in `codexWritableRoots` and/or `codexPackageCacheEnv`
- [ ] Pin it: the new roots or env present for build and test, absent for every other role
- [ ] tibo's `scripts/test.sh` and `scripts/build-docs.sh` pass on the Codex test and build agents

### Phase 5: Tool profiles

- [ ] Apple and Swift tool groups per role: build/test (`swift`, `xcodebuild`, `xcodegen`),
      watch (`log stream`/`log show`, `xcrun simctl spawn … log stream`), serve (`open`,
      `xcrun simctl launch`, `swift run`), deploy (`codesign`, `xcrun devicectl`, `notarytool`,
      `security`, `plutil`, `iconutil`)
- [ ] edit's `DenyTools` cover `swift build`, `swift test` and `xcodebuild`
- [ ] The watch app-log recipe documented for macOS, iOS simulator and tvOS
- [ ] Pinned by the profile test

### Phase 6: Session-start initialization

- [ ] On session launch, create a missing `.muxcode/config` with the detected build, test and
      precheck patterns; a missing `.claude/settings.json` with permissions for the detected
      toolchain; a missing `CLAUDE.md` stub naming the build and test commands (decision 1)
- [ ] Never overwrite an existing file; leave created files untracked (decision 2)
- [ ] Write a lifecycle row naming what was created, and send edit a notice listing the new files
- [ ] Negative control pinned: a project with all three present is left byte-identical

### Phase 7: Docs

- [ ] `CLAUDE.md`: Codex sandbox bullet (Swift and Xcode grants), hook patterns, session-start init
- [ ] [`docs/hooks.md`](../../hooks.md): the Swift and Xcode build and test patterns and the bare
      `scripts/*.sh` rule
- [ ] [`docs/configuration.md`](../../configuration.md): session-start init and what it writes
- [ ] [`docs/agents.md`](../../agents.md): the Apple and Swift tool groups per role, the watch
      app-log recipe

### Phase 8: Integration test

- [ ] `scripts/test-xcode-compat.sh`, hermetic on a scratch repo with a nested `Package.swift` and
      `project.yml`: detection finds both; classification writes rows and fires chains through the
      real `hook bash`, with `swift --version` as the negative control; session-init creates the
      three files, never overwrites, and leaves a fully configured project untouched
- [ ] Opt-in live part: a real Codex build and test agent runs `swift build` / `swift test` on the
      scratch package
- [ ] Coverage floor so a skipped section cannot read as green
- [ ] Run through the run agent and record the row here

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
| MUX-207-xcode-project-compatibility | 17m | 2026-10-29 16:05 |

## Status

In Progress — Phase 1 (experiment) complete 2026-10-29; Phase 2 next
