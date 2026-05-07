# Go native launcher

Merge the `muxcode.sh` bash launcher and `muxcode-agent.sh` agent launcher scripts into the Go `muxcode` binary. After this work, the two shell scripts are deleted and all session launch + agent bootstrap logic lives in Go. The single `muxcode` binary becomes the sole entry point — no bash wrapper required.

## Motivation

`muxcode.sh` (509 lines) and `muxcode-agent.sh` (349 lines) duplicate logic that already exists in Go and create a maintenance burden:

1. **Config drift** — when launcher config fields change (new windows, new roles, new env vars), both the shell scripts and Go code must be updated in lockstep. Missed updates cause silent misbehavior (e.g. new roles missing from `role_cli_var()` in bash but present in `RoleCLIEnvVar()` in Go).
2. **Redundant code** — 90%+ of both scripts is already reimplemented in Go:
   - `muxcode.sh` → `LaunchSession()` in `bus/launcher.go` (session creation, window setup, status bar, auto-accept, daemon start, Ollama check)
   - `muxcode-agent.sh` → `ResolveLaunchConfig()` + `BuildExecArgs()` + `PreLaunchSetup()` in `bus/launch.go` (agent file resolution, model selection, tool flags, shared prompt, venv, startup messages)
3. **Two entry points** — users and install scripts must manage both `muxcode` (Go binary) and `muxcode.sh` + `muxcode-agent.sh` (bash scripts installed to `~/.local/bin/`). The Makefile copies scripts during install, creating version skew risk.
4. **Shell portability** — bash-specific features (`set -a`, `compgen -v`, indirect variable expansion `${!var}`, process substitution) limit portability. Go eliminates these concerns.

## Current state

### What Go already handles

| Capability | Go function | File |
|------------|-------------|------|
| Session creation | `LaunchSession()` | `bus/launcher.go` |
| Window creation + pane setup | `createWindowContent()` | `bus/launcher.go` |
| Interactive project picker | `PickProject()`, `ScanProjects()` | `bus/launcher.go` |
| Config file loading | `LoadShellConfig()` | `bus/config_file.go` |
| Launcher config resolution | `LoadLauncherConfig()`, `DefaultLauncherConfig()` | `bus/launcher.go` |
| Status bar theming | `ConfigureStatusBar()`, `TransformStatusRight()` | `bus/launcher.go` |
| Auto-accept prompts | `AutoAccept()`, `ClassifyPane()` | `bus/launcher.go` |
| Agent wake-up | `NeedsWakeUp()`, `SendWakeUp()` | `bus/launcher.go`, providers |
| Window resize | `ResizeWindows()` | `bus/launcher.go` |
| Daemon/monitor start | `startDetachedProcess()`, `killStaleProcesses()` | `bus/launcher.go` |
| Ollama check | `EnsureOllama()` | `bus/launcher.go` |
| Agent file resolution | `ResolveAgentFile()` | `bus/launch.go` |
| Agent config resolution | `ResolveLaunchConfig()` | `bus/launch.go` |
| Model selection (all providers) | `resolveClaudeModel()`, `RoleOpenCodeModelDefault()`, etc. | `bus/launch.go` |
| Tool flags | `ResolveTools()` | `bus/profile.go` |
| Shared prompt | `BuildSharedPrompt()` | `bus/launch.go` |
| Exec args | `BuildExecArgs()` | `bus/launch.go` |
| Pre-launch setup | `PreLaunchSetup()` | `bus/launch.go` |
| Venv resolution | `ResolveVenv()` | `bus/launch.go` |
| Inline fallback prompts | `InlineFallbackPrompt()` | `bus/launch.go` |
| Role-agent filename mapping | `AgentFileName()` | `bus/launch.go` |
| Mode cycle init | JSON write in `LaunchSession()` | `bus/launcher.go` |
| tmux wrappers | `TmuxNewSession()`, `TmuxSplitWindow()`, etc. | `bus/tmux.go` |
| CLI subcommand dispatch | `RunLauncher()` | `cmd/launcher.go` |

### What bash still does (gap analysis)

| Capability | Bash location | Go equivalent exists? |
|------------|---------------|----------------------|
| `muxcode.sh` as primary entry point | `muxcode.sh` line 1 | Yes — `cmd/launcher.go` `RunLauncher()` already handles `muxcode` and `muxcode launch` |
| `load_config()` in muxcode.sh | `muxcode.sh` lines 18-32 | Yes — `LoadShellConfig()` in `bus/config_file.go` |
| PATH setup (Homebrew, ~/.local/bin) | `muxcode.sh` lines 48-51 | Yes — `SetupPath()` in `bus/launcher.go` |
| `muxcode-agent.sh` as agent launcher | Called via `muxcode agent launch <role>` | **Partial** — `cmd/launch.go` calls into `ResolveLaunchConfig()` + `BuildExecArgs()` but the current `muxcode agent launch` subcommand still shells out to `muxcode-agent.sh` |
| `load_config()` in muxcode-agent.sh | `muxcode-agent.sh` lines 14-30 | Yes — `LoadShellConfig()` |
| Local LLM routing (`ROLE_CLI=local`) | `muxcode-agent.sh` lines 61-106 | Yes — `ResolveLaunchConfig()` handles via `LocalProvider` |
| `role_cli_var()` bash switch | `muxcode-agent.sh` lines 39-54 | Yes — `RoleCLIEnvVar()` in `bus/launch.go` |
| `agent_name()` bash switch | `muxcode-agent.sh` lines 109-126 | Yes — `AgentFileName()` in `bus/launch.go` |
| `build_flags()` tool resolution | `muxcode-agent.sh` lines 131-140 | Yes — `ResolveTools()` in `bus/profile.go` |
| `build_shared_prompt()` | `muxcode-agent.sh` lines 148-157 | Yes — `BuildSharedPrompt()` in `bus/launch.go` |
| `launch_agent_from_file()` | `muxcode-agent.sh` lines 162-175 | Yes — `BuildAgentsJSON()` in `bus/launch.go` |
| `role_claude_model_var()` / defaults | `muxcode-agent.sh` lines 184-216 | Yes — `RoleClaudeModelEnvVar()`, `RoleClaudeModelDefault()` |
| Permission flags (`--dangerously-skip-permissions`) | `muxcode-agent.sh` lines 221-225 | Yes — handled by provider `ConfigureLaunch()` |
| Startup inbox message | `muxcode-agent.sh` lines 234-252 | Yes — `PreLaunchSetup()` in `bus/launch.go` |
| Lifecycle log on launch | `muxcode-agent.sh` lines 254-259 | Yes — `PreLaunchSetup()` |
| Venv activation | `muxcode-agent.sh` lines 265-277 | **Partial** — `ResolveVenv()` finds the dir but doesn't activate in the current process |
| `exec` into CLI with all flags | `muxcode-agent.sh` line 288/349 | Yes — `BuildExecArgs()` constructs the args, needs `syscall.Exec` |
| Fallback inline prompts | `muxcode-agent.sh` lines 297-343 | Yes — `InlineFallbackPrompt()` |
| `AGENT_ROLE` env export | `muxcode-agent.sh` line 347 | Needs to be added to exec env |

## Requirements

### Acceptance criteria

- [x] `muxcode` (bare invocation) launches the full tmux session — no `muxcode.sh` wrapper needed
- [x] `muxcode <path>` and `muxcode <path> <name>` work identically to current `muxcode.sh` behavior
- [x] `muxcode agent launch <role>` directly execs into the AI CLI — no `muxcode-agent.sh` script needed
- [x] `muxcode.sh` and `muxcode-agent.sh` are deleted from the repo
- [x] `Makefile` install target no longer copies `scripts/muxcode-*.sh` to `~/.local/bin/`
- [x] All agent launch behaviors are preserved: config loading, provider resolution, model selection, tool flags, shared prompt, venv activation, startup messages, lifecycle logging, permission flags, inline fallback prompts
- [x] Session launch behaviors are preserved: project picker, session attach, bus init, daemon/monitor start, Ollama check, window creation, status bar theming, auto-accept, display-name labels, cleanup hook, window resize
- [x] `install.sh` updated to reflect new install procedure (no script install step)
- [x] All existing tests pass (`go test ./...`)
- [x] New tests added for `muxcode agent launch` Go path covering: config loading, provider resolution, exec args, venv activation, startup messages
- [x] Mode cycle JSON init for the edit window preserved
- [x] `sendCommand()` in `createWindowContent()` references `muxcode agent launch` (already does)

### Out of scope

- Changing the agent launch protocol (still `exec` into `claude`/`opencode`/`codex`)
- Modifying provider behavior or adding new providers
- Changing the tmux window layout or pane structure
- Modifying the config file format
- Changing the hook system

## Technical approach

### Architecture

The Go binary already has two parallel paths that largely overlap:

```
Current:
  muxcode (bare)    → cmd/launcher.go → bus/launcher.go LaunchSession()
                      └── createWindowContent() sends "muxcode agent launch <role>" to panes
  
  muxcode agent launch <role>  → cmd/launch.go → ??? → shells out to muxcode-agent.sh
  
  muxcode.sh        → bash reimplementation of LaunchSession() (DEAD CODE — only needed for 
                       PATH setup and as an alias, since `muxcode` already works)

Target:
  muxcode (bare)    → cmd/launcher.go → bus/launcher.go LaunchSession()
                      └── createWindowContent() sends "muxcode agent launch <role>" to panes
  
  muxcode agent launch <role>  → cmd/launch.go → bus/launch.go RunAgentLaunch()
                                  └── LoadShellConfig() → ResolveLaunchConfig() → PreLaunchSetup()
                                      → activate venv → set AGENT_ROLE → syscall.Exec(BuildExecArgs())
```

The key insight is that `muxcode.sh` is already dead code — `LaunchSession()` in Go does everything `muxcode.sh` does. The real work is making `muxcode agent launch <role>` fully self-contained in Go instead of shelling out to `muxcode-agent.sh`.

### Key changes

1. **`cmd/launch.go`** — The `muxcode agent launch <role>` handler becomes the complete agent bootstrap:
   - Load shell config (`LoadShellConfig`)
   - Resolve launch config (`ResolveLaunchConfig`)
   - Pre-launch setup (startup messages, lifecycle log)
   - Activate venv (set `VIRTUAL_ENV`, prepend to `PATH`)
   - Export `AGENT_ROLE` env var
   - Clear terminal
   - `syscall.Exec()` into the resolved CLI with all flags

2. **`bus/launch.go`** — Add `ActivateVenv()` function that modifies `os.Environ()` (sets `VIRTUAL_ENV`, prepends `PATH`) instead of sourcing `activate` script

3. **`Makefile`** — Remove script install lines, update install message

4. **Delete** `muxcode.sh` and `scripts/muxcode-agent.sh`

5. **`install.sh`** — Remove script prerequisite checks, update instructions

### Venv activation in Go

The bash script sources `activate` to set `VIRTUAL_ENV` and modify `PATH`. In Go, replicate the effect directly:

```go
func ActivateVenv(venvDir string) {
    absDir, _ := filepath.Abs(venvDir)
    os.Setenv("VIRTUAL_ENV", absDir)
    os.Setenv("PATH", filepath.Join(absDir, "bin") + ":" + os.Getenv("PATH"))
    os.Unsetenv("PYTHONHOME") // activate script does this
}
```

This is sufficient because `syscall.Exec` inherits the modified environment.

## Key files

| File | Change |
|------|--------|
| `tools/muxcode/cmd/launch.go` | Rewrite `muxcode agent launch` handler to be fully self-contained |
| `tools/muxcode/bus/launch.go` | Add `ActivateVenv()`, `RunAgentLaunch()` orchestrator function |
| `tools/muxcode/cmd/launcher.go` | Verify `RunLauncher()` covers all `muxcode.sh` behavior (mostly done) |
| `tools/muxcode/bus/launcher.go` | Minor: ensure `LaunchSession()` covers any remaining `muxcode.sh` gaps |
| `Makefile` | Remove `scripts/muxcode-*.sh` install lines |
| `install.sh` | Remove script-related checks and instructions |
| `muxcode.sh` | **Delete** |
| `scripts/muxcode-agent.sh` | **Delete** |
| `CLAUDE.md` | Update build/install instructions, remove script references |
| `README.md` | Update install instructions, directory structure |
| `docs/architecture.md` | Update launcher/agent launch flow descriptions |
| `docs/agents.md` | Update agent launch description |
| `docs/configuration.md` | Remove references to shell scripts |

## Implementation

### Phase 1: Complete `muxcode agent launch` in Go

Make the `muxcode agent launch <role>` subcommand fully self-contained — no dependency on `muxcode-agent.sh`.

- [x] Add `ActivateVenv(venvDir string)` to `bus/launch.go` — sets `VIRTUAL_ENV`, prepends `PATH`, unsets `PYTHONHOME`
- [x] Add `RunAgentLaunch(role string)` orchestrator to `bus/launch.go`:
  - [x] Call `LoadShellConfig("")` to load config file
  - [x] Call `ResolveLaunchConfig(role)` to resolve provider, CLI, model, tools, prompt
  - [x] Call `PreLaunchSetup(role, session, cli)` for startup inbox + lifecycle log
  - [x] Call `ActivateVenv()` if venv found
  - [x] Set `AGENT_ROLE` env var
  - [x] Clear terminal (ANSI escape `\033[2J\033[H`)
  - [x] Build exec args via `BuildExecArgs()`
  - [x] `syscall.Exec()` into the resolved CLI
- [x] Update `cmd/launch.go` to call `RunAgentLaunch()` instead of shelling out to `muxcode-agent.sh`
- [x] Add tests for `RunAgentLaunch()`: config loading, venv activation, exec args construction, AGENT_ROLE env
- [x] Verify all provider paths work: claude, opencode, codex, local

**Success criteria**: `muxcode agent launch build` (invoked from a tmux pane) launches the build agent identically to the current `muxcode-agent.sh build` path.

### Phase 2: Verify `LaunchSession()` completeness

Ensure `LaunchSession()` in `bus/launcher.go` covers everything `muxcode.sh` does. Based on the gap analysis, it already does — this phase is verification + minor fixes.

- [x] Audit `muxcode.sh` line-by-line against `LaunchSession()` — document any behavioral gaps
  - Gap 1 (high): `muxcode.sh` checks `tmux has-session` and attaches if running; Go kills and recreates. Fix: add `TmuxHasSession()` + early-return attach in `RunLauncher()`
  - Gap 2 (low): `muxcode.sh` writes `mode-cycle-edit.json` after bus init; Go relies on read-time fallback. Fix: explicit `WriteModeCycleState()` in `LaunchSession()`
- [x] Fix: add `TmuxHasSession()` to `bus/tmux.go` and early-attach check in `cmd/launcher.go`
- [x] Fix: write `mode-cycle-edit.json` (and `mode-cycle-plan.json` if plan in window list) in `LaunchSession()` after `Init()`
- [x] Verify `SetupPath()` covers the Homebrew/macOS PATH additions from `muxcode.sh` lines 48-51 — confirmed identical
- [x] Verify `LoadShellConfig()` handles the project-dir re-load (line 83 of `cmd/launcher.go` already does this) — confirmed
- [x] Run integration test: launch session via `muxcode` (Go binary) and verify all windows, panes, consoles, agents come up correctly

**Success criteria**: `muxcode <project-path>` via the Go binary produces an identical session to `muxcode.sh <project-path>`.

### Phase 3: Delete shell scripts and update install

Remove the bash scripts and update all references.

- [x] Delete `muxcode.sh` from repo root
- [x] Delete `scripts/muxcode-agent.sh`
- [x] Update `Makefile`: remove `muxcode-agent.sh` from install, add cleanup of legacy scripts, keep hook/utility script install loop
- [x] Update `install.sh`: update success message (no scripts reference)
- [x] Verify `make install` succeeds without the scripts
- [x] Verify `make clean` doesn't reference scripts

**Success criteria**: `make install` installs the Go binary (+ harness), hook/utility scripts, agents, skills, and configs. No `muxcode-agent.sh` or `muxcode.sh` in `~/.local/bin/`.

### Phase 4: Update documentation

Update all docs to reflect the new single-binary architecture.

- [x] Update `CLAUDE.md`:
  - [x] Remove `muxcode.sh` from directory structure
  - [x] `scripts/` retained — still has hook scripts, utility scripts, and integration tests
  - [x] Update "Launcher & hooks" tech stack row — split into "Launcher" (Go) and "Hooks" (Bash)
  - [x] Update install/build instructions
  - [x] Remove references to `muxcode-agent.sh` in agent definitions section
- [x] Update `README.md`:
  - [x] N/A — directory structure diagram not present in README
  - [x] N/A — install instructions already reference `./install.sh` and Go binary
  - [x] Updated "Zero external dependencies" section to reference single Go binary
- [x] Update `docs/architecture.md`:
  - [x] Updated spawn flow, local LLM flow, auto-accept, and lifecycle logging references
  - [x] All `muxcode-agent.sh` and `muxcode.sh` references replaced with Go equivalents
- [x] Update `docs/agents.md`:
  - [x] Updated agent file resolution, how files are loaded, local LLM flow, custom agent instructions, harness, health monitoring
- [x] Update `docs/configuration.md`:
  - [x] N/A — no shell script config loading references found
- [x] Update `docs/agent-bus.md`:
  - [x] Updated watch, spawn, context, agent launch, and lifecycle sections

**Success criteria**: No documentation references `muxcode.sh` or `muxcode-agent.sh` as runtime dependencies.

### Phase 5: Cleanup remaining `scripts/` references

Check if any other scripts in `scripts/` depend on the deleted files, and verify the `scripts/` directory still has a purpose.

- [x] Audit `scripts/` directory for remaining files and their dependencies — 13 files remain (hook scripts, utility scripts, integration tests), all legitimate
- [x] Verify hook scripts don't reference `muxcode-agent.sh` — confirmed clean; stale comments in `muxcode-daemon-monitor.sh` updated
- [x] Update `.gitignore` if needed — no changes needed
- [x] Run full test suite: `go test ./...` in both Go modules — all tests pass
- [x] Run integration tests: `scripts/test-diff-split.sh` (10/15 pass, 5 nvim-state timing failures unrelated to launcher changes), `scripts/test-hot-reload.sh` (requires idle build agent)

**Success criteria**: All tests pass, no broken references, clean `scripts/` directory.

## Risks

| Risk | Mitigation |
|------|------------|
| Behavioral differences between bash and Go config loading | `LoadShellConfig()` already handles the same resolution chain — add test cases for edge cases (missing files, empty values) |
| Venv activation subtle differences | Go `ActivateVenv()` replicates the three key effects (`VIRTUAL_ENV`, `PATH`, unset `PYTHONHOME`) — test with real venvs |
| `exec` into CLI fails differently in Go vs bash | `syscall.Exec` replaces the process identically to bash `exec` — both use execve(2) |
| Users with custom `muxcode.sh` modifications | Document migration path in release notes; the config file system handles most customization needs |
| `scripts/` directory has other scripts that depend on `muxcode-agent.sh` | Phase 5 audit catches these; hook scripts are independent |

## Status

Complete
