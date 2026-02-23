# Plan: Tool Profiles in Config (#5) + Configurable Event Chains (#10)

## Context

We just expanded the `allowed_tools()` bash function in `muxcode-agent.sh` to cover all 7 sub-agent roles. The function is already 80+ lines of hardcoded case statements with duplicated `cd * &&` variants — unwieldy and untestable. Similarly, the build->test->review chain in `muxcode-bash-hook.sh` is hardcoded (lines 94-119), and the auto-CC roles are hardcoded in `bus/inbox.go` (lines 11-16). This plan moves all three to a single JSON config file with Go binary subcommands, making them maintainable, testable, and per-project overridable.

## JSON Config Schema

Single file: `config/muxcode.json` (installed to `~/.config/muxcode/muxcode.json`, overridable at `.muxcode/muxcode.json`).

```json
{
  "shared_tools": {
    "bus": [
      "Bash(muxcode-agent-bus *)",
      "Bash(./bin/muxcode-agent-bus *)",
      "Bash(cd * && muxcode-agent-bus *)"
    ],
    "readonly": ["Read", "Glob", "Grep"],
    "common": [
      "Bash(ls*)", "Bash(cat*)", "Bash(which*)",
      "Bash(command -v*)", "Bash(pwd*)", "Bash(wc*)",
      "Bash(head*)", "Bash(tail*)"
    ]
  },
  "tool_profiles": {
    "build": {
      "include": ["bus", "readonly", "common"],
      "tools": ["Bash(./build.sh*)", "Bash(make*)", "..."],
      "cd_prefix": true
    }
  },
  "event_chains": {
    "build": {
      "on_success": { "send_to": "test", "action": "test", "message": "Build succeeded — run tests and report results", "type": "request" },
      "on_failure": { "send_to": "edit", "action": "notify", "message": "Build FAILED (exit ${exit_code}): ${command} — check build window", "type": "event" },
      "notify_analyst": true
    },
    "test": {
      "on_success": { "send_to": "review", "action": "review", "message": "Tests passed — review the changes and report results to edit", "type": "request" },
      "on_failure": { "send_to": "edit", "action": "notify", "message": "Tests FAILED (exit ${exit_code}): ${command} — check test window", "type": "event" },
      "notify_analyst": true
    }
  },
  "auto_cc": ["build", "test", "review"]
}
```

Key design decisions:
- `shared_tools` holds reusable groups referenced by name in `include`
- `cd_prefix: true` auto-generates `Bash(cd * && ...)` variants — eliminates manual duplication
- `${exit_code}` / `${command}` are template variables expanded at runtime by Go
- `auto_cc` replaces the hardcoded `ccRoles` map in Go
- Config resolution: `.muxcode/muxcode.json` (project) > `~/.config/muxcode/muxcode.json` (user) > compiled-in defaults
- If no config file exists, `DefaultConfig()` returns current behavior — fully backward compatible

## New Go Code

### `bus/profile.go` — config loading, tool resolution, chain resolution

Structs:
```go
type MuxcodeConfig struct {
    SharedTools  map[string][]string    `json:"shared_tools"`
    ToolProfiles map[string]ToolProfile `json:"tool_profiles"`
    EventChains  map[string]EventChain  `json:"event_chains"`
    AutoCC       []string               `json:"auto_cc"`
}
type ToolProfile struct {
    Include  []string `json:"include,omitempty"`
    Tools    []string `json:"tools,omitempty"`
    CdPrefix bool     `json:"cd_prefix,omitempty"`
}
type EventChain struct {
    OnSuccess     *ChainAction `json:"on_success,omitempty"`
    OnFailure     *ChainAction `json:"on_failure,omitempty"`
    OnUnknown     *ChainAction `json:"on_unknown,omitempty"`
    NotifyAnalyst bool         `json:"notify_analyst"`
}
type ChainAction struct {
    SendTo  string `json:"send_to"`
    Action  string `json:"action"`
    Message string `json:"message"`
    Type    string `json:"type"`
}
```

Key functions:
- `Config() *MuxcodeConfig` — lazy-loaded singleton (single-goroutine safe, no sync needed)
- `SetConfig(cfg)` — override for tests
- `LoadConfig() (*MuxcodeConfig, error)` — resolution chain: project > user > defaults
- `DefaultConfig() *MuxcodeConfig` — compiled-in defaults matching current bash/Go behavior
- `ResolveTools(role string) []string` — expand includes + generate cd-prefix variants
- `ResolveChain(eventType, outcome string) *ChainAction` — lookup chain action
- `ExpandMessage(template, exitCode, command string) string` — template variable substitution
- `GetAutoCC() map[string]bool` — config-driven auto-CC role set
- `expandCdPrefix(tool string) string` — `Bash(git *)` -> `Bash(cd * && git *)`; skips non-Bash and already-prefixed tools

### `bus/profile_test.go` — comprehensive tests

- `TestDefaultConfig_HasAllRoles` — all known roles except edit have profiles
- `TestResolveTools_Build` — shared tools included, cd-prefix variants generated
- `TestResolveTools_NoCdPrefix` — verify cd variants skipped when disabled
- `TestExpandCdPrefix` — correct expansion, skip non-Bash, skip already-prefixed
- `TestLoadConfig_FallbackToDefault` — no config files -> default config
- `TestMergeConfigs` — overlay replaces by role name, base roles preserved
- `TestResolveChain_BuildSuccess` — build success -> test
- `TestResolveChain_TestFailure` — test failure -> edit
- `TestExpandMessage` — template variables replaced
- `TestGetAutoCC` — default and custom auto_cc lists

### `cmd/tools.go` — new `tools` subcommand

Usage: `muxcode-agent-bus tools <role> [--json]`
- Outputs space-separated tool list (default) or JSON array (`--json`)
- Used by `muxcode-agent.sh` to replace the bash `allowed_tools()` function

### `cmd/chain.go` — new `chain` subcommand

Usage: `muxcode-agent-bus chain <event_type> <outcome> [--exit-code N] [--command CMD] [--no-notify] [--dry-run]`
- Looks up chain config, expands message template, sends bus message + analyst notification
- Exit 0 = sent, exit 1 = error, exit 2 = no chain configured

## Changes to Existing Files

### `tools/muxcode-agent-bus/main.go`
- Add `case "tools": cmd.Tools(args)` and `case "chain": cmd.Chain(args)` to switch
- Update usage string with new commands

### `tools/muxcode-agent-bus/bus/inbox.go`
- Remove hardcoded `ccRoles` map (lines 11-16)
- Change `IsAutoCCRole()` to use `Config().GetAutoCC()`
- Change `Send()` line 44 from `ccRoles[m.From]` to `IsAutoCCRole(m.From)`

### `scripts/muxcode-agent.sh`
- Replace `allowed_tools()` (80+ lines) and `build_flags()` with:
  ```bash
  build_flags() {
    local tools
    tools="$(muxcode-agent-bus tools "$1" 2>/dev/null)" || return
    [ -z "$tools" ] && return
    for tool in $tools; do
      printf -- '--allowedTools %s ' "$tool"
    done
  }
  ```
- ~80 lines removed, ~6 lines added

### `scripts/muxcode-bash-hook.sh`
- Replace lines 94-119 (hardcoded chain logic) with:
  ```bash
  if [ "$is_build" -eq 1 ]; then
    if [ -z "$EXIT_CODE" ]; then
      muxcode-agent-bus chain build unknown --command "$COMMAND"
    elif [ "$EXIT_CODE" = "0" ]; then
      muxcode-agent-bus chain build success --command "$COMMAND"
    else
      muxcode-agent-bus chain build failure --exit-code "$EXIT_CODE" --command "$COMMAND"
    fi
  fi
  # Same pattern for is_test
  ```
- ~25 lines replaced with ~14 lines

### `config/muxcode.json` — new file
- Full default config with all roles, chains, and auto_cc
- Installed to `~/.config/muxcode/` via existing `cp -n` in Makefile (no Makefile changes needed)

## Implementation Order

### Phase 1: Config infrastructure
1. Create `bus/profile.go` — structs, `DefaultConfig()`, `LoadConfig()`, `mergeConfigs()`, `Config()`, `SetConfig()`
2. Create `bus/profile_test.go` — loading, defaults, merging tests
3. Build + test to verify

### Phase 2: Tool profiles
4. Add `ResolveTools()`, `expandCdPrefix()`, `resolveIncludes()` to `bus/profile.go`
5. Add tool resolution tests
6. Create `cmd/tools.go` with `Tools` subcommand
7. Add `case "tools"` to `main.go`
8. Build + test
9. Update `scripts/muxcode-agent.sh` — replace bash functions with `muxcode-agent-bus tools` call
10. Build + test + review

### Phase 3: Event chains
11. Add `ResolveChain()`, `ChainNotifyAnalyst()`, `ExpandMessage()` to `bus/profile.go`
12. Add chain resolution tests
13. Create `cmd/chain.go` with `Chain` subcommand
14. Add `case "chain"` to `main.go`
15. Build + test
16. Update `scripts/muxcode-bash-hook.sh` — replace hardcoded chain with `muxcode-agent-bus chain` calls
17. Build + test + manual chain verification

### Phase 4: Auto-CC config
18. Add `GetAutoCC()` to `bus/profile.go`
19. Modify `bus/inbox.go` — remove `ccRoles`, use config-driven `IsAutoCCRole()`
20. Update/add auto-CC tests
21. Build + test

### Phase 5: Config file + final verification
22. Create `config/muxcode.json` with full defaults
23. Full build + test + review cycle
24. Test project-local override (`.muxcode/muxcode.json`)

## Verification

1. `go test ./...` passes — all existing 52 tests + new profile tests
2. `muxcode-agent-bus tools build` outputs same tools as current bash `allowed_tools build`
3. `muxcode-agent-bus chain build success --command "./build.sh"` sends message to test agent
4. Full muxcode session: edit a file -> delegate build -> chain auto-fires test -> auto-fires review
5. Project-local `.muxcode/muxcode.json` with custom chain overrides default behavior
