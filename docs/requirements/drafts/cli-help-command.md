# CLI help command

Add a `muxcode help [command]` system that provides structured, discoverable help for all 49+ subcommands. Today the only help is a flat usage dump when running `muxcode` with no args — no per-command help, no flag descriptions, no examples, and no way to browse related commands by category.

## Problem

### Observed behavior

Running `muxcode` with no args or an unknown command prints a flat list of ~49 subcommands with one-line descriptions. There is no way to:

1. Get detailed help for a specific command (`muxcode help send`)
2. See available flags and their descriptions (`muxcode send --help`)
3. Browse commands by category (messaging, monitoring, process management)
4. See usage examples for complex commands
5. Discover related commands ("you used `send`, you might also want `inbox`, `history`, `track`")

Individual commands print a bare `Usage:` line when called with wrong arguments, but these are inconsistent — some show flags, some don't, none show examples or descriptions.

### Impact

- New users don't know which commands to use for a task
- Experienced users can't remember flags for commands they use infrequently
- Agents waste tokens trying commands with wrong flag combinations
- The 49-command flat list is overwhelming — no structure or grouping

## Requirements

### Acceptance criteria

#### Core help command
- [ ] `muxcode help` displays all commands organized by category with short descriptions
- [ ] `muxcode help <command>` displays detailed help for a specific command: description, usage, flags, examples, related commands
- [ ] `muxcode <command> --help` and `muxcode <command> -h` are aliases for `muxcode help <command>`
- [ ] `muxcode help --all` displays every command with full details (reference mode)
- [ ] Help output uses Dracula-themed colors consistent with other muxcode commands

#### Command metadata
- [ ] Each command has: category, short description, long description, usage pattern, flags with descriptions, examples, related commands
- [ ] Command metadata is defined in a central registry (not scattered across cmd files)
- [ ] Categories group related commands: Launcher, Messaging, Agents, Monitoring, Processes, Configuration, Session, Development

#### Discoverability
- [ ] `muxcode help search <query>` searches command names, descriptions, and flag names for a keyword
- [ ] Category headers in `muxcode help` output are clickable-style (clearly labeled sections)
- [ ] Each command's help shows a "See also" section with related commands
- [ ] Unknown command errors suggest the closest match: `Unknown command: sned. Did you mean: send?`

#### Integration
- [ ] All existing `Usage:` strings in cmd files are replaced with calls to the help registry
- [ ] The top-level usage block in `main.go` is generated from the registry (single source of truth)
- [ ] `--json` flag outputs command metadata as JSON (for agent consumption)
- [ ] All existing tests pass (`go test ./...`)
- [ ] New tests cover help rendering, search, fuzzy matching, and category grouping

### Out of scope

- Interactive TUI browser (simple scrollable output is sufficient)
- Man page generation
- Shell completion scripts (may be a follow-up)
- Internationalization

## Technical approach

### Command registry

Centralized metadata for every subcommand, replacing the scattered usage strings:

```go
type CommandHelp struct {
    Name        string        `json:"name"`
    Category    string        `json:"category"`
    Short       string        `json:"short"`        // one-line description
    Long        string        `json:"long"`          // detailed paragraph
    Usage       string        `json:"usage"`         // usage pattern
    Flags       []FlagHelp    `json:"flags"`
    Examples    []ExampleHelp `json:"examples"`
    Related     []string      `json:"related"`       // related command names
    Subcommands []CommandHelp `json:"subcommands,omitempty"` // for compound commands (memory, cron, proc, etc.)
}

type FlagHelp struct {
    Name        string `json:"name"`         // "--json", "--all", "-v"
    Short       string `json:"short"`        // short alias ("-j" for "--json")
    Description string `json:"description"`
    Default     string `json:"default,omitempty"`
    Required    bool   `json:"required,omitempty"`
}

type ExampleHelp struct {
    Command     string `json:"command"`
    Description string `json:"description"`
}
```

### Command categories

```go
var commandCategories = []CategoryDef{
    {Name: "Launcher",      Description: "Start and manage muxcode sessions"},
    {Name: "Messaging",     Description: "Send and receive messages between agents"},
    {Name: "Agents",        Description: "Agent lifecycle, health, and configuration"},
    {Name: "Monitoring",    Description: "Status, logs, diagnostics, and workflow state"},
    {Name: "Processes",     Description: "Background processes, spawns, and cron jobs"},
    {Name: "Configuration", Description: "Settings, plugins, skills, and context"},
    {Name: "Session",       Description: "Session management, compaction, and cleanup"},
    {Name: "Development",   Description: "Hooks, testing, demos, and API tools"},
}
```

**Category assignments:**

| Category | Commands |
|----------|----------|
| Launcher | `muxcode`, `launch` |
| Messaging | `send`, `inbox`, `history`, `track`, `subscribe`, `notify` |
| Agents | `agent`, `agent-health`, `reload`, `mode`, `modal`, `spawn`, `prompt`, `tools`, `guard` |
| Monitoring | `status`, `diagnose`, `lifecycle`, `console`, `workflow`, `dashboard`, `tasks`, `stats` |
| Processes | `proc`, `spawn`, `cron`, `webhook` |
| Configuration | `config`, `plugin`, `skill`, `context`, `memory`, `init` |
| Session | `session`, `compact`, `cleanup`, `lock`, `unlock`, `is-locked` |
| Development | `hook`, `chain`, `log`, `demo`, `uitest`, `simulate`, `api`, `pii-scrub`, `provider-select`, `atlassian` |

### Fuzzy matching for unknown commands

When the user types an unknown command, suggest the closest match using Levenshtein distance:

```go
// suggestCommand returns the closest matching command name if the
// edit distance is <= 3. Returns "" if no close match.
func suggestCommand(input string, commands []string) string {
    // Levenshtein distance — find closest match
    // Also check prefix matches ("sub" → "subscribe")
}
```

### Help output format

```
$ muxcode help

  MuxCode CLI — multi-agent coding environment

  Launcher
    muxcode                   Interactive project picker → launch tmux session
    muxcode <path>            Launch session for project directory
    launch                    Explicit launch subcommand

  Messaging
    send                      Send a message to an agent
    inbox                     Read messages from your inbox
    history                   Show recent messages to/from an agent
    track                     Show delivery status for a message ID
    subscribe                 Manage event subscriptions
    notify                    Send tmux notification to an agent

  Agents
    agent                     Run local LLM agent loop or launch agent
    agent-health              Manage agent health monitoring
    reload                    Stop, reconfigure, and relaunch an agent
    ...

  Run 'muxcode help <command>' for detailed help on a specific command.
```

```
$ muxcode help send

  muxcode send — Send a message to an agent

  Usage:
    muxcode send <to> <action> "<payload>" [flags]

  Arguments:
    <to>        Target agent role (edit, build, test, commit, etc.)
    <action>    Message action type (build, test, commit, review, etc.)
    <payload>   Message content (quote if it contains spaces)

  Flags:
    --type TYPE       Message type: request (default), response, event
    --reply-to ID     Reply to a specific message ID
    --no-notify       Don't send tmux notification to the target agent
    --force           Bypass pre-commit agent-idle check
    --wait            Poll for a response (timeout: $MUXCODE_INBOX_POLL_TIMEOUT, default 600s)
    --no-cc           Don't auto-CC to the edit agent

  Examples:
    muxcode send build build "Run ./build.sh and report results" --wait
    muxcode send commit commit "Stage and commit changes" --force --wait
    muxcode send review review "Review the latest changes" --wait
    muxcode send edit implement "Fix the login bug" --type request

  See also: inbox, history, track, notify, subscribe
```

### `--help` / `-h` flag interception

Intercept `--help` and `-h` in the main dispatch before routing to the command handler:

```go
func main() {
    // ...
    subcmd := os.Args[1]
    args := os.Args[2:]

    // Intercept --help / -h on any command
    if hasFlag(args, "--help", "-h") {
        cmd.Help([]string{subcmd})
        return
    }

    // Existing switch dispatch...
}
```

## Key files

| File | Change |
|------|--------|
| `tools/muxcode/bus/help.go` | New file — `CommandHelp`, `FlagHelp`, `ExampleHelp` structs, command registry, `RegisterCommand()`, `GetCommandHelp()`, `SearchCommands()`, `FormatHelp()`, `FormatHelpJSON()`, `FormatCategoryList()`, `SuggestCommand()` |
| `tools/muxcode/bus/help_registry.go` | New file — `DefaultCommandHelps()` returning metadata for all ~49 commands organized by category |
| `tools/muxcode/cmd/help.go` | New file — `Help(args)` command handler, `--all`, `--json`, `search` subcommand |
| `tools/muxcode/main.go` | Add `"help"` to subcommands, intercept `--help`/`-h` flags, replace static `usage` string with registry-generated output, add `SuggestCommand()` to unknown-command error |
| `tools/muxcode/bus/help_test.go` | New file — tests for registry lookup, search, fuzzy matching, formatting, category grouping |

## Implementation

### Phase 1: Command registry and data structures

Build the centralized command metadata registry.

- [ ] Create `bus/help.go` with `CommandHelp`, `FlagHelp`, `ExampleHelp`, `CategoryDef` structs
- [ ] Add `commandRegistry` map and `RegisterCommand()`, `GetCommandHelp()`, `ListCommands()`, `ListCategories()`
- [ ] Add `SearchCommands(query string) []CommandHelp` — search names, descriptions, and flag names (case-insensitive substring)
- [ ] Add `SuggestCommand(input string, commands []string) string` — Levenshtein distance for fuzzy matching (threshold <= 3)
- [ ] Add `init()` to register all commands from `DefaultCommandHelps()`
- [ ] Add tests for registry operations, search, and fuzzy matching
- [ ] **Verify**: `cd tools/muxcode && go test ./...` — all tests pass
- [ ] **Verify**: `cd tools/muxcode && go vet ./...` — no issues

### Phase 2: Command metadata population

Define detailed help metadata for all ~49 subcommands.

- [ ] Create `bus/help_registry.go` with `DefaultCommandHelps() []CommandHelp`
- [ ] Populate metadata for Launcher commands: `launch`
- [ ] Populate metadata for Messaging commands: `send`, `inbox`, `history`, `track`, `subscribe`, `notify`
- [ ] Populate metadata for Agent commands: `agent`, `agent-health`, `reload`, `mode`, `modal`, `spawn`, `prompt`, `tools`, `guard`
- [ ] Populate metadata for Monitoring commands: `status`, `diagnose`, `lifecycle`, `console`, `workflow`, `dashboard`, `tasks`
- [ ] Populate metadata for Process commands: `proc`, `cron`, `webhook`
- [ ] Populate metadata for Configuration commands: `config`, `plugin`, `skill`, `context`, `memory`, `init`
- [ ] Populate metadata for Session commands: `session`, `compact`, `cleanup`, `lock`, `unlock`, `is-locked`
- [ ] Populate metadata for Development commands: `hook`, `chain`, `log`, `demo`, `uitest`, `simulate`, `api`, `pii-scrub`, `provider-select`, `atlassian`
- [ ] Include flags, examples, and related commands for each entry
- [ ] Add tests verifying all registered commands have category, short desc, usage, and at least one example
- [ ] **Verify**: `cd tools/muxcode && go test ./...` — all tests pass

### Phase 3: Output formatting

Build Dracula-themed human-readable and JSON formatters.

- [ ] Add `FormatCategoryList(commands []CommandHelp, categories []CategoryDef) string` — grouped command list with category headers
- [ ] Add `FormatCommandHelp(cmd CommandHelp) string` — detailed single-command help with flags table, examples, and see-also
- [ ] Add `FormatHelpJSON(cmd CommandHelp) string` — JSON output for agents
- [ ] Add `FormatAllJSON(commands []CommandHelp) string` — full registry as JSON
- [ ] Add `FormatSearchResults(results []CommandHelp, query string) string` — search result display
- [ ] Add tests for formatting output structure and content
- [ ] **Verify**: `cd tools/muxcode && go test ./...` — all tests pass

### Phase 4: CLI wiring and integration

Wire up the help command and integrate with existing dispatch.

- [ ] Create `cmd/help.go` with `Help(args)` — routes `help`, `help <command>`, `help --all`, `help --json`, `help search <query>`
- [ ] Add `"help"` to `knownSubcommands` in `main.go` and route to `cmd.Help()`
- [ ] Intercept `--help` / `-h` on any command in `main.go` dispatch — redirect to `cmd.Help([]string{subcmd})`
- [ ] Replace static `usage` string in `main.go` with registry-generated category list
- [ ] Add `SuggestCommand()` to the unknown-command error path: `Unknown command: sned. Did you mean: send?`
- [ ] Update individual `cmd/*.go` files to call registry-based usage instead of inline strings (incremental — start with top 10 most-used commands)
- [ ] Add tests for `--help` interception, unknown command suggestion, and help command routing
- [ ] **Verify**: `cd tools/muxcode && go test ./...` — all tests pass
- [ ] **Verify**: `cd tools/muxcode && go vet ./...` — no issues
- [ ] **Verify**: `make install` — binary builds and installs with help command available

## Status

Draft
