# MuxCode Go Launcher

Migrate `muxcode.sh` (443 lines) into the `muxcode` Go binary as a `launch` subcommand, then rename the binary from `muxcode` to `muxcode`. After this, the project ships a single binary — `muxcode` — that handles everything: session launching, agent coordination, hooks, console views, and all CLI operations.

## Context

`muxcode` started as a message bus helper and grew into the central binary — 30+ subcommands covering agent launching, console views, hooks, PII scrubbing, Atlassian integration, lifecycle logging, and the watcher. The name no longer reflects what it does. Meanwhile, `muxcode.sh` is installed separately as `muxcode` — a shell script that creates the tmux session and delegates everything else to the Go binary.

After Phases 1-5 of the shell-to-Go migration eliminated 4,812 lines across 20 scripts, the launcher is the last significant shell script. Converting it and renaming the binary completes the consolidation into a single Go application.

### Why now

- All tmux interaction patterns are proven in Go (console, agent launch, lifecycle logging, watcher)
- Config loading already ported to Go in `cmd/launch.go` (`loadShellConfig()`)
- `bus/launch.go` handles agent file resolution, model selection, and prompt assembly
- The Makefile auto-builds from `tools/*/go.mod` — no new build modules needed

### What stays as shell

| Script | Lines | Reason |
|--------|-------|--------|
| `muxcode-preview-hook.sh` | 95 | Pure tmux/vim send-keys with timing-sensitive scrollbind |
| `muxcode-diff-cleanup.sh` | 22 | 5 lines of tmux send-keys |
| `muxcode-switch-session.sh` | 72 | Interactive fzf picker (shell is natural fit) |
| `muxcode-demo.sh` | 249 | Recording orchestrator (asciinema/ffmpeg/gifski) |
| `build.sh` | 7 | Makefile wrapper |
| `test.sh` | 13 | Go test runner |
| `muxcode-test-wrapper.sh` | 9 | Delegates to test.sh |
| `install.sh` | 205 | First-time setup (interactive, runs once) |
| `test-diff-split.sh` | 348 | Integration test for nvim diff preview |

---

## Phase 0: Rename Go module directory ✅

**Completed.** Renamed the Go module directory and updated Go import paths only. All runtime behavior, user-facing files, and installed binary names are unchanged.

### What changed

| Area | From | To |
|------|------|-----|
| Go source directory | `tools/muxcode/` | `tools/muxcode/` |
| Go module path (`go.mod`) | `github.com/mkober/muxcode/tools/muxcode` | `github.com/mkober/muxcode/tools/muxcode` |
| Go import statements | All `cmd/`, `bus/`, `tui/`, `watcher/` updated to new module path | |
| Build output artifact | `bin/muxcode` | `bin/muxcode` (directory name drives this) |
| Makefile install line | `install bin/muxcode` | `install bin/muxcode` (still installed **as** `muxcode`) |
| `.gitignore` | `tools/muxcode/muxcode` | `tools/muxcode/muxcode` |

### What did NOT change

Everything else stays exactly as before — this is a pure source-level refactor with no runtime impact:

- **Installed binary name**: still `~/.local/bin/muxcode` (the Go binary)
- **Installed launcher**: still `~/.local/bin/muxcode` (the `muxcode.sh` shell script)
- **Go runtime strings**: usage text, `exec.Command()` calls, prompts, tool profiles — all still say `muxcode`
- **LLM harness**: no changes at all (stays at `muxcode` everywhere)
- **Agent definitions** (`agents/*.md`) — all CLI examples still use `muxcode`
- **Skill definitions** (`skills/*.md`) — unchanged
- **Documentation** (`docs/*.md`, `CLAUDE.md`, `README.md`) — unchanged
- **Config files** (`config/settings.json`, `config/muxcode.json`, `config/tmux.conf`) — hook commands, permissions, menu items unchanged
- **Shell scripts** (`muxcode.sh`, `scripts/*.sh`) — binary invocations unchanged

### Verification

After `make install`, the installed files are identical to before:
- `~/.local/bin/muxcode` — the Go binary (one binary, not two)
- `~/.local/bin/muxcode` — `muxcode.sh` shell launcher (calls `muxcode` subcommands)
- Starting a new muxcode session should work exactly as before

### Why the split approach

The Go binary can't be installed as `muxcode` yet because:
1. `muxcode.sh` occupies that name — it's installed as `$(BINDIR)/muxcode`
2. `muxcode.sh` calls `muxcode init`, `muxcode watch`, etc. — if the Go binary were `muxcode`, these calls would recursively invoke the shell script
3. Agent definitions tell agents to run `muxcode send` — changing this before the binary name changes would break agent communication

Phase 1 resolves this atomically: add `launch` subcommand → install Go binary as `muxcode` → remove `muxcode.sh` install → rename all user-facing refs → add backward-compat `muxcode` symlink.

---

## Phase 1: Core launcher + full rename ✅

**Completed.** Converted the main session creation flow from `muxcode.sh` to Go and completed the binary rename. The Go binary is now installed as `muxcode` with a `muxcode-agent-bus` backward-compat symlink. `muxcode.sh` is no longer installed by the Makefile.

### What changed

| Area | Detail |
|------|--------|
| New: `bus/tmux.go` (184 lines) | Thin tmux wrapper — `TmuxRun`, `TmuxOutput`, `TmuxNewSession`, `TmuxNewWindow`, `TmuxSplitWindow`, `TmuxSendKeys`, `TmuxSendEnter`, `TmuxSetEnv`, `TmuxSetOption`, `TmuxSetHook`, `TmuxCapturePaneLines`, etc. |
| New: `bus/tmux_test.go` (192 lines) | Tests for tmux command building (no live tmux required) |
| New: `bus/launcher.go` (653 lines) | Core launcher: `LauncherConfig`, config loading, session creation, window loop, Ollama startup, process management, cleanup hooks |
| New: `bus/launcher_test.go` (259 lines) | Unit tests for config parsing, role mapping, split-left detection, window commands |
| New: `cmd/launcher.go` (93 lines) | CLI handler: arg parsing, routes to `bus.Launch()` |
| Updated: `main.go` | `knownSubcommands` map + invocation name detection — bare `muxcode` routes to launcher |
| Updated: Makefile | Go binary installed as `muxcode`, `muxcode-agent-bus` symlink for backward compat, `muxcode.sh` install removed |
| Global rename | ~900 replacements of `muxcode-agent-bus` → `muxcode` across agents, skills, docs, configs, scripts |
| `muxcode.sh` | Retained in repo as reference but no longer installed |

### Original spec (preserved for reference)

#### New files

| File | Purpose |
|------|---------|
| `bus/launcher.go` | Core launcher logic: config, tmux session creation, window loop, status bar, Ollama startup |
| `bus/launcher_test.go` | Unit tests for config parsing, role mapping, split-left detection, status bar formatting |
| `bus/tmux.go` | Thin tmux wrapper: `exec.Command("tmux", ...)` helpers for common operations |
| `bus/tmux_test.go` | Tests for tmux command building (no live tmux required) |
| `cmd/launcher.go` | Handler: parse CLI args, call `bus.Launch()` |

### 1.1 Entry point and invocation

When invoked as `muxcode` (detected via `os.Args[0]`), the binary routes to the launch handler. Explicit `muxcode launch` also works.

```go
// main.go
base := filepath.Base(os.Args[0])
if base == "muxcode" && (len(os.Args) < 2 || !isSubcommand(os.Args[1])) {
    // Route to launch handler, passing remaining args as project path / session name
    cmd.RunLauncher(os.Args[1:])
    return
}
```

Usage:
```
muxcode                          # interactive project picker
muxcode <path>                   # launch with project path
muxcode <path> <name>            # launch with project path and session name
muxcode launch                   # explicit subcommand (same behavior)
muxcode launch <path> [<name>]   # explicit with args
```

### 1.2 Config loading

Promote `loadShellConfig()` from `cmd/launch.go` to a shared location (e.g., `bus/config_file.go`). Add launcher-specific config struct:

```go
type LauncherConfig struct {
    ProjectsDir  string            // MUXCODE_PROJECTS_DIR (default: $HOME)
    ScanDepth    int               // MUXCODE_SCAN_DEPTH (default: 3)
    Windows      []string          // MUXCODE_WINDOWS
    RoleMap      map[string]string // MUXCODE_ROLE_MAP (run=runner commit=git analyze=analyst)
    SplitLeft    []string          // MUXCODE_SPLIT_LEFT
    ShellInit    string            // MUXCODE_SHELL_INIT
    Editor       string            // MUXCODE_EDITOR (default: nvim)
    NvimAppName  string            // MUXCODE_NVIM_APPNAME (default: muxcode/nvim)
    AgentCLI     string            // MUXCODE_AGENT_CLI (default: claude)
}
```

Defaults match current `muxcode.sh`:
```go
Windows:   []string{"edit", "api", "build", "test", "review", "deploy", "run", "watch", "commit", "analyze"}
RoleMap:   map[string]string{"run": "runner", "commit": "git", "analyze": "analyst"}
SplitLeft: []string{"edit", "api", "build", "test", "review", "deploy", "run", "analyze", "commit", "watch"}
```

Config resolution (3-tier, same as existing):
1. `$MUXCODE_CONFIG` env override
2. `.muxcode/config` relative to project dir
3. `~/.config/muxcode/config`

Environment variables override config file values.

### 1.3 Tmux helpers

Thin wrappers in `bus/tmux.go` that build and exec tmux commands:

```go
func TmuxRun(args ...string) error
func TmuxOutput(args ...string) (string, error)
func TmuxKillSession(session string) error
func TmuxNewSession(session, firstWindow, dir string, width, height int) error
func TmuxNewWindow(session, window, dir string) error
func TmuxSplitWindow(target, dir string) error
func TmuxSendKeys(target string, keys ...string) error
func TmuxSendEnter(target string) error
func TmuxSelectPane(target string) error
func TmuxSelectWindow(session, window string) error
func TmuxSetEnv(session, key, value string) error
func TmuxSetOption(target, key, value string) error
func TmuxSetGlobalOption(key, value string) error
func TmuxSetHook(session, hook, cmd string) error
func TmuxCapturePaneLines(target string, lines int) (string, error)
func TmuxClientDimensions() (width, height int, err error)
func TmuxShowOption(scope, key string) (string, error)
func IsInsideTmux() bool
```

These are intentionally simple — each is 3-5 lines wrapping `exec.Command("tmux", ...)`. No abstraction beyond what the launcher needs.

### 1.4 Session creation sequence

Mirrors `muxcode.sh` exactly:

1. Check `tmux` in PATH (`exec.LookPath`)
2. Load config
3. Resolve project directory (from arg or picker — picker in Phase 2)
4. Derive session name (from arg or project basename)
5. Print project/session info to stdout
6. Set up PATH (macOS: prepend `/opt/homebrew/bin`, `/opt/homebrew/sbin`, `~/.local/bin`)
7. Kill existing session with same name (ignore errors)
8. Clear stale `session-created` hook
9. Clean up stale preview temp files
10. Initialize bus: `bus.Init(session, projectDir)`
11. Log lifecycle: `session-start`
12. Kill stale watcher/monitor processes (`pkill -f` with `$`-anchored patterns)
13. Start watcher and monitor as detached processes (`SysProcAttr{Setsid: true}`)
14. Ensure Ollama if needed
15. Capture client dimensions if inside tmux
16. Create tmux session with first window
17. Set `BUS_SESSION` and `MUXCODE` environment variables on session
18. Create remaining windows in loop
19. Select edit window, agent pane
20. Configure status bar (Phase 3)
21. Register cleanup hook
22. Start auto-accept (Phase 4) as detached process
23. Start window resize (background process with 1s delay)
24. Attach or switch via `syscall.Exec()` — replaces Go process

### 1.5 Window creation loop

Three window types, matching current behavior:

**Edit window:**
```
Pane 0: [shell_init +] MUXCODE=1 NVIM_APPNAME=<name> <editor>
Pane 1: muxcode agent launch edit
Select pane 0 (editor focused)
```

**Split-left window** (build, test, review, deploy, run, watch, commit, analyze, api):
```
Pane 0: [shell_init +] muxcode console <window>
Pane 1: [shell_init +] muxcode agent launch <role>
Select pane 1 (agent focused)
```

**Standard window** (custom windows not in split-left list):
```
Pane 0: [shell_init]
Pane 1: [shell_init +] muxcode agent launch <role>
Select pane 1 (agent focused)
```

Role resolution: window name → role via `RoleMap`, then role → agent via existing `AgentFileName()`.

### 1.6 Process management

Watcher and monitor are started as detached background processes:

```go
cmd := exec.Command("muxcode", "watch", session)
cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
cmd.Stdout = nil
cmd.Stderr = nil
cmd.Start()
// Don't cmd.Wait() — process is detached
```

This replaces the bash `nohup ... &>/dev/null & disown` pattern. `Setsid: true` creates a new process group so the watcher survives the parent's exit.

### 1.7 Ollama startup

Port `ensure_ollama()`:
1. Scan env vars for any `MUXCODE_*_CLI=local` — skip if none found
2. HTTP GET `$MUXCODE_OLLAMA_URL/api/tags` with 2s timeout
3. If unreachable and `ollama` is in PATH, start `ollama serve` in background
4. Poll up to 10s (20 x 500ms)
5. Print warning to stderr if not ready (non-fatal)

### 1.8 Critical timing constraints

These timing-sensitive patterns from `muxcode.sh` must be preserved exactly:

| Pattern | Constraint |
|---------|-----------|
| `pkill` then start watcher | 100ms sleep between kill and start |
| Send-keys text + Enter | Two separate `tmux send-keys` calls with 100ms delay between |
| Window resize after attach | 1s delay, then resize all windows |
| Auto-accept poll interval | 2s between attempts (Phase 4) |
| Auto-accept wake-up stabilization | 1s delay before injecting wake-up text |

### 1.9 Full binary rename

Once the `launch` subcommand works, complete the rename atomically:

1. **Makefile**: install `bin/muxcode` as `$(BINDIR)/muxcode`, create `muxcode` → `muxcode` symlink
2. **Global replace** `muxcode` → `muxcode` across agents, skills, docs, configs, remaining scripts (~900 occurrences)
3. **Remove** `muxcode.sh` from Makefile install (no longer needed)
4. **Verify** with `grep -r "muxcode"` — should return 0 matches outside git history

---

## Phase 2: Project picker ✅

**Completed.** Interactive project selection when no path argument is provided. Implemented as part of Phase 1 (code was written alongside the launcher). Tests added separately.

### 2.1 Project scanning

```go
func ScanProjects(dirs []string, maxDepth int) ([]string, error)
```

Walk each directory up to `maxDepth` looking for `.git` directories. Return parent paths sorted. Uses `filepath.WalkDir` with depth tracking — more efficient than shelling out to `find`.

Multiple directories supported (comma-separated in `MUXCODE_PROJECTS_DIR`).

### 2.2 fzf integration

Shell out to `fzf` for the interactive picker:

```go
func PickProject(projects []string) (string, error)
```

- Check `fzf` in PATH — if missing, fall back to numbered list with stdin prompt
- Pipe project list to fzf's stdin
- Detect context: `TMUX_POPUP` → `--layout=reverse`, `TMUX` → `--tmux center,60%,50%`, neither → `--height=40%`
- fzf flags: `--prompt="  Project: " --reverse --border --header="Select a project · ESC to cancel" --bind="esc:abort"`
- Read selected project from stdout
- Handle ESC/cancel (empty output, non-zero exit) → clean exit

---

## Phase 3: Status bar customization ✅

**Completed.** Dracula-themed status bar configuration implemented as part of Phase 1. Refactored into testable pure functions (`TransformStatusRight`, `TransformStatusLeft`, `WindowStatusFormat`, `WindowStatusCurrentFormat`) with 8 unit tests added.

### 3.1 Status-right modifications

The current shell applies a series of `sed` transformations. Port as a Go function:

```go
func ConfigureStatusBar(session string) error
```

1. Get current `status-right` via `tmux show-options -gv status-right`
2. Apply transformations using `strings.ReplaceAll` and `strings.Replace`:
   - Strip powerline arrows (thin `\ue0b3` and filled `\ue0b2`)
   - Remove green arrow color block and unused music segment
   - Restyle date/time: tab-color bg for date, comment-color bg for time, with powerline arrows
   - Add padding around date and time text
3. Set result via `tmux set-option -t <session> status-right`

Embed powerline Unicode as Go constants:
```go
const (
    pwrLeft      = "\ue0b0" //
    pwrRight     = "\ue0b2" //
    pwrThinRight = "\ue0b3" //
)
```

### 3.2 Window status formats

Set `window-status-format` and `window-status-current-format` with Dracula colors and capitalized window names. The current shell uses `awk '{print toupper(substr($0,1,1)) substr($0,2)}'` (macOS bash 3.2 compat) — Go's `strings.ToUpper(s[:1]) + s[1:]` achieves the same.

Note: The formats use `#(echo #W | awk ...)` which tmux evaluates at render time. This must be preserved as-is — Go only sets the format string, tmux evaluates it.

### 3.3 Status-left hamburger

Replace `❐` with `☰` in `status-left` using `strings.Replace`.

---

## Phase 4: Auto-accept ✅

**Completed.** Ported auto-accept as a detached process that survives the parent's `syscall.Exec` into tmux. Also fixed window resize (same goroutine-vs-exec issue). Extracted `ClassifyPane()` and `NeedsWakeUp()` as testable pure functions with 10 unit tests.

### What changed

| Area | Detail |
|------|--------|
| `cmd/launcher.go` | Added `--auto-accept` and `--resize` internal flags — when passed, runs the corresponding function directly and exits |
| `bus/launcher.go` | `AutoAccept` refactored: `ClassifyPane()` (pure function, 4 states), `NeedsWakeUp()` (pure function), `ResizeWindows()` (extracted from goroutine). Both goroutines replaced with `startDetachedProcess()` calls. Lifecycle logging for auto-accept-start PID. |
| `bus/launcher_test.go` | 10 new tests: `ClassifyPane` (7 cases: trust, bypass, idle, not-ready, empty, trust-precedence, bypass-precedence), `NeedsWakeUp` (10 window roles) |

### Original spec (preserved for reference)

### 4.1 Approach

Run as a separate detached process via `muxcode launch --auto-accept <session> <window1> <window2> ...`. This keeps the auto-accept lifecycle separate from the main launcher (which `syscall.Exec`'s into tmux) and from the long-lived watcher.

### 4.2 Implementation

```go
func AutoAccept(session string, windows []string) error
```

Logic (mirrors shell exactly):

1. Loop up to 30 attempts, 2s sleep between each
2. For each window not yet accepted:
   a. Capture agent pane (`.1`) content
   b. If "trust this folder" → send Enter (trust prompt default is correct)
   c. If "Bypass Permissions" → send Down, 200ms sleep, send Enter
   d. If `❯` prompt → mark accepted; if edit or analyze, trigger wake-up (once per agent)
   e. Otherwise → not ready yet
3. Wake-up for edit/analyze:
   - 1s stabilization delay
   - If "You have new messages" already in pane → just send Enter
   - Otherwise → send text, poll up to 10x100ms for it to appear, then send Enter
4. Exit when all windows accepted or 30 attempts exhausted
5. Log lifecycle: `auto-accept complete`

### 4.3 Send-keys timing

Critical constraint: tmux send-keys text and Enter must be separate calls:
```go
TmuxSendKeys(pane, "You have new messages")
// Poll for text to appear in pane
time.Sleep(100 * time.Millisecond)
TmuxSendEnter(pane)
```

---

## Makefile changes

### Phase 0 (current)

```makefile
# Build produces bin/muxcode (from tools/muxcode/ directory name)
# Install as muxcode (user-facing name unchanged)
@install -m 755 bin/muxcode $(BINDIR)/muxcode
@install -m 755 muxcode.sh $(BINDIR)/muxcode
```

### After Phase 1

```makefile
# Go binary IS muxcode now — no more muxcode.sh
@install -m 755 bin/muxcode $(BINDIR)/muxcode
@ln -sf muxcode $(BINDIR)/muxcode   # backward compat symlink
# Remove: install -m 755 muxcode.sh $(BINDIR)/muxcode
```

---

## Testing strategy

### Unit tests (no tmux required)

| Test | What it validates |
|------|-------------------|
| `TestLoadLauncherConfig` | Config file parsing, env var overrides, defaults |
| `TestParseWindows` | Window list parsing from space-separated string |
| `TestParseRoleMap` | Role map parsing (`run=runner commit=git analyze=analyst`) |
| `TestAgentRole` | Window→role mapping with default and custom role maps |
| `TestIsSplitLeftLauncher` | Split-left detection for standard and custom windows |
| `TestScanProjects` | Project scanning with `.git` detection, depth limiting |
| `TestCapitalizeWindow` | Window name capitalization for status bar |
| `TestStatusBarTransforms` | String transformations on status-right |
| `TestBuildWindowCommands` | Correct tmux commands for each window type (edit, split-left, standard) |
| `TestDetectInvocationName` | `os.Args[0]` routing for `muxcode` vs `muxcode launch` |
| `TestOllamaNeeded` | Local LLM detection from env vars |
| `TestTmuxArgBuilding` | Each tmux helper builds correct argument lists |

### Manual test plan

1. `muxcode ~/Repos/project` — direct path, all 10 windows
2. `muxcode ~/Repos/project myname` — custom session name
3. `muxcode` — interactive picker (fzf)
4. `muxcode` from inside tmux — `switch-client` behavior
5. `muxcode` from outside tmux — `attach-session` behavior
6. Custom `MUXCODE_WINDOWS` — subset of windows
7. Custom `MUXCODE_ROLE_MAP` — non-standard role mapping
8. `MUXCODE_SHELL_INIT="source ~/.bashrc"` — verify init sent to panes
9. `MUXCODE_*_CLI=local` — Ollama startup
10. Auto-accept: trust prompt, bypass prompt, startup wake-up for edit/analyze
11. Status bar: Dracula theme, capitalized window names, hamburger icon
12. Session cleanup hook: verify bus directory cleaned on session close
13. Backward compat: `muxcode dashboard` still works via symlink
14. Stale watcher cleanup: start session, kill, start again — no duplicate watchers

---

## Implementation order

| Step | Phase | Scope | Est. lines |
|------|-------|-------|------------|
| ~~1~~ | ~~0~~ | ~~Rename Go module directory, update imports~~ | ~~done~~ |
| ~~2~~ | ~~0~~ | ~~Update Makefile, .gitignore~~ | ~~done~~ |
| ~~3~~ | ~~1~~ | ~~`bus/tmux.go` + `bus/tmux_test.go` — tmux helper functions~~ | ~~376~~ |
| ~~4~~ | ~~1~~ | ~~Promote `loadShellConfig()` to bus package~~ | ~~done~~ |
| ~~5~~ | ~~1~~ | ~~`bus/launcher.go` — config, session creation, window loop, Ollama~~ | ~~653~~ |
| ~~6~~ | ~~1~~ | ~~`cmd/launcher.go` — CLI handler, arg parsing~~ | ~~93~~ |
| ~~7~~ | ~~1~~ | ~~`main.go` — `launch` subcommand + invocation name detection~~ | ~~done~~ |
| ~~8~~ | ~~1~~ | ~~`bus/launcher_test.go` — unit tests~~ | ~~259~~ |
| ~~9~~ | ~~1~~ | ~~Full binary rename: agents, skills, docs, configs, scripts (~900 replacements)~~ | ~~done~~ |
| ~~10~~ | ~~2~~ | ~~Project scanner + fzf picker in `bus/launcher.go`~~ | ~~done~~ |
| ~~11~~ | ~~3~~ | ~~Status bar customization in `bus/launcher.go`~~ | ~~done~~ |
| ~~12~~ | ~~4~~ | ~~Auto-accept loop + `--auto-accept` flag + `--resize` flag~~ | ~~done~~ |
| 13 | — | Docs: update CLAUDE.md, architecture.md, MUX-089-shell-to-go-migration.md | ~100 |

**Phase 0:** ✅ Complete — Go module directory renamed, internal Go refs updated, Makefile + .gitignore updated
**Phase 1:** ✅ Complete — 1,381 lines of new Go code, ~900 string replacements, Go binary is `muxcode`, `muxcode.sh` retired
**Phase 2:** ✅ Complete — `ScanProjects()`, `PickProject()`, `pickProjectFallback()` implemented in launcher.go, 6 unit tests added
**Phase 4:** ✅ Complete — auto-accept + resize as detached processes, 10 new tests

### Verification cadence

After each phase is complete, rebuild and restart muxcode to verify before committing:

1. `make install` — rebuild and install
2. Exit the current muxcode session
3. Start a new muxcode session (`muxcode <project>`)
4. Verify the phase-specific behavior works correctly
5. Commit the phase once verified

---

## Risks and mitigations

| Risk | Mitigation |
|------|------------|
| Existing user `settings.json` has old binary name | Backward-compat symlink `muxcode` → `muxcode`. `install.sh` already merges hook config. |
| `syscall.Exec` for tmux attach must be last operation | Structure code: all setup completes, auto-accept/resize launched as detached processes, then `syscall.Exec`. |
| tmux send-keys timing regressions | Preserve exact same sleep durations; auto-accept is a separate process so timing is isolated. |
| fzf not installed for interactive picker | Fallback to numbered list on stdin. |
| Config files with shell constructs | `loadShellConfig()` handles `KEY=VALUE` and quoted values only. Shell expansion not supported (not used in practice). |
| Stale `muxcode.sh` in user PATH from manual installs | `make install` overwrites `$(BINDIR)/muxcode` with the Go binary. Old shell script is replaced. |

---

## Success criteria

- [x] Go module directory renamed to `tools/muxcode/`
- [x] All Go internal refs use `muxcode`
- [x] Build and tests pass with renamed module
- [x] `muxcode ~/Repos/project` launches identical session to current `muxcode.sh`
- [x] `grep -r "muxcode-agent-bus"` returns 0 matches outside backward-compat symlink
- [x] All 10 standard windows created with correct pane layout
- [x] Console views start in left panes for split-left windows
- [x] Agents launch in right panes with correct roles
- [x] Status bar shows Dracula theme with capitalized window names and hamburger icon
- [x] Auto-accept handles trust and bypass prompts
- [x] Edit and analyze agents receive startup wake-up
- [x] Watcher and monitor processes start in background and survive session attach
- [x] Cleanup hook registered and fires on session close
- [x] Interactive picker works from inside and outside tmux
- [x] `muxcode.sh` no longer installed by Makefile
- [x] `muxcode-agent-bus` symlink works for backward compatibility
- [x] New unit tests cover config, role mapping, tmux command building, project scanning
