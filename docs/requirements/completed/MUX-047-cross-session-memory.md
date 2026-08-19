# Cross-Session Memory

## Context

The current memory system is project-scoped — each project has its own `.muxcode/memory/` directory. Learnings about personal conventions, tool quirks, debugging patterns, and workflow preferences are trapped in individual project memories. When starting a new project or switching between projects, agents have no knowledge of patterns the user has established elsewhere.

Cross-session memory adds a user-level memory layer at `~/.config/muxcode/memory/` that persists across all projects and sessions. It stores universal patterns — things that are true regardless of which project is active.

## Problem statement

| Scenario | Current behavior | Desired behavior |
|----------|-----------------|-----------------|
| User always uses 2-space indentation in TypeScript | Written to project memory, lost in new projects | Global convention available in every session |
| Build agent learns a Homebrew quirk on macOS | Stored per-project, rediscovered in every repo | Global tool quirk available everywhere |
| User prefers concise commit messages | Re-established per project via corrections | Global preference injected into all commit agents |
| Debugging pattern for flaky tests | Trapped in one project's memory | Available when same issue appears in another project |
| Agent learns user's preferred review style | Per-project only | Consistent across all repos |

## Design

### Storage layout

```
~/.config/muxcode/memory/           # User-level (cross-session)
  shared.md                         # Global shared learnings
  {role}.md                         # Global per-role learnings
  {role}/                           # Daily archives (same rotation as project)
    YYYY-MM-DD.md

.muxcode/memory/                    # Project-level (existing, unchanged)
  shared.md
  {role}.md
  {role}/
    YYYY-MM-DD.md
```

### Resolution order

When reading context, both layers are included. Project memory takes precedence conceptually (listed second, closer to the agent's working context):

```
## Global Memory

(contents of ~/.config/muxcode/memory/shared.md + {role}.md)

## Shared Memory

(contents of .muxcode/memory/shared.md)

## {Role} Memory

(contents of .muxcode/memory/{role}.md)
```

Global memory appears first so project-specific learnings can override or refine global patterns. This matches the existing pattern where shared memory appears before role memory.

### CLI changes

#### New subcommands

```bash
# Write to global memory (any agent can call this)
muxcode memory write-global "<section>" "<text>"

# Read global memory
muxcode memory read-global [role]

# Read global context (shared + role)
muxcode memory context-global [--days N]
```

#### Modified subcommands

```bash
# context — now includes global memory as the first section
muxcode memory context [--days N] [--no-global]

# search — now includes global memory entries (tagged with source: "global")
muxcode memory search <query> [--role R] [--limit N] [--scope project|global|all]

# list — now includes global memory entries (tagged with source: "global")
muxcode memory list [--role R] [--scope project|global|all]
```

- `--no-global` on `context` skips global memory (useful for reducing context size)
- `--scope` defaults to `all` for search/list
- Search results include a `Source` field ("project" or "global") in the output

### Prompt injection

The `ReadContext()` / `ReadContextWithDays()` functions are modified to prepend global memory:

```markdown
# Global Memory

## Commit conventions
_2026-03-01 10:00_

Use imperative mood, keep subject under 72 chars

## TypeScript patterns
_2026-03-05 14:30_

Always use 2-space indentation

# Shared Memory

## Project-specific config
_2026-03-10 09:00_

Uses pnpm, build with ./build.sh

# Edit Memory

## Delegation rules
_2026-03-10 09:15_

Never run build commands directly
```

### Write heuristic

Agents write to global memory explicitly via `write-global`. There is no automatic promotion from project to global — the user or agent makes a conscious decision that a learning is universal.

**When to use `write-global`:**
- Personal conventions (indentation, naming, commit style)
- Tool/environment quirks (macOS, Homebrew, tmux, neovim)
- Workflow preferences (review style, delegation patterns)
- Debugging techniques that apply broadly

**When to use `write` (project):**
- Build commands specific to this project
- Architecture decisions for this codebase
- Test patterns unique to this project's stack

### Rotation

Global memory uses the same lazy daily rotation as project memory:
- Archive on first write of each day
- 30-day retention, 7-day context window (configurable)
- Uses `RotateMemory()` with the global memory directory

### Search

`SearchMemoryWithOptions()` and `AllMemoryEntriesWithArchives()` are extended to optionally include global memory entries. Each entry gets a `Source` field:

```go
type MemoryEntry struct {
    Role      string
    Section   string
    Timestamp string
    Content   string
    Source    string  // "project" or "global"
}
```

Search results display the source:

```
--- [global:shared] Commit conventions (2026-03-01 10:00) score:4.2 ---
Use imperative mood, keep subject under 72 chars

--- [project:edit] Delegation rules (2026-03-10 09:15) score:3.8 ---
Never run build commands directly
```

## Changes

### 1. Modify: `bus/config.go`

New path helpers:

```go
func GlobalMemoryDir() string           // ~/.config/muxcode/memory
func GlobalMemoryPath(role string) string    // ~/.config/muxcode/memory/{role}.md
func GlobalMemoryArchiveDir(role string) string  // ~/.config/muxcode/memory/{role}/
```

### 2. Modify: `bus/memory.go`

- Add `Source` field to `MemoryEntry` struct
- New `ReadGlobalMemory(role) (string, error)` — reads from `GlobalMemoryDir()`
- New `AppendGlobalMemory(section, content, role) error` — writes to `GlobalMemoryDir()`
- Modify `ReadContext()` / `ReadContextWithDays()` — prepend global memory section
- New `ReadContextWithOptions(role, days, includeGlobal) (string, error)` — supports `--no-global`

### 3. Modify: `bus/rotation.go`

- `NeedsRotation()` and `RotateMemory()` accept a `memDir` parameter (or use a helper that resolves the correct directory)
- `ReadMemoryWithHistory()` extended to work with global memory directory

### 4. Modify: `bus/search.go`

- `AllMemoryEntries()` extended with `includeGlobal` option
- `AllMemoryEntriesWithArchives()` extended similarly
- Entries from global memory tagged with `Source: "global"`
- `FormatSearchResults()` includes source prefix in output

### 5. Modify: `cmd/memory.go`

- Add `write-global` subcommand
- Add `read-global` subcommand
- Add `context-global` subcommand
- Add `--no-global` flag to `context`
- Add `--scope` flag to `search` and `list`

### 6. Modify: `bus/setup.go`

- `Init()` creates `GlobalMemoryDir()` if it doesn't exist (alongside project memory dir)

### 7. Tests

New test file: `bus/global_memory_test.go`

- Global path resolution (uses `~/.config/muxcode/memory/`)
- Write + read round-trip for global memory
- Context output includes global section header
- Search includes global entries with correct source tag
- `--no-global` excludes global entries from context
- `--scope project` excludes global entries from search/list
- Rotation works independently for global and project memory

### 8. Documentation updates

- `CLAUDE.md` — add `GlobalMemoryDir()`, `GlobalMemoryPath()` to code reference; update memory system description
- `docs/agent-bus.md` — add `write-global`, `read-global`, `context-global` subcommands; document `--no-global` and `--scope` flags
- `docs/agents.md` — update memory section with global memory description
- `docs/architecture.md` — update memory system diagram with global layer
- `docs/configuration.md` — add `~/.config/muxcode/memory/` to user config directory listing
- `docs/requirements/backlog.md` — move from Planned to Implemented

## Data flow

```
Agent writes global memory:
  muxcode memory write-global "conventions" "2-space indentation"
    ↓
  AppendGlobalMemory() → ~/.config/muxcode/memory/shared.md
    ↓
  NeedsRotation() → lazy archive if new day

Agent reads context (every prompt injection):
  muxcode memory context
    ↓
  ReadContextWithOptions(role, 7, includeGlobal=true)
    ↓
  1. ReadGlobalMemoryWithHistory("shared", 7) → global shared
  2. ReadGlobalMemoryWithHistory(role, 7)     → global role
  3. ReadMemoryWithHistory("shared", 7)       → project shared
  4. ReadMemoryWithHistory(role, 7)           → project role
    ↓
  Output: "# Global Memory\n...\n# Shared Memory\n...\n# {Role} Memory\n..."

Agent searches memory:
  muxcode memory search "indentation" --scope all
    ↓
  AllMemoryEntriesWithArchives(includeGlobal=true)
    ↓
  Entries tagged: Source="global" or Source="project"
    ↓
  BM25 ranking across combined corpus
    ↓
  Output: "[global:shared] Conventions (score:4.2)\n..."
```

## Verification plan

| Test | Steps | Expected result |
|------|-------|-----------------|
| Global write | `muxcode memory write-global "test" "hello"` | `~/.config/muxcode/memory/shared.md` contains entry |
| Global read | `muxcode memory read-global` | Shows content of global shared memory |
| Context includes global | `muxcode memory context` | Output starts with `# Global Memory` section |
| No-global flag | `muxcode memory context --no-global` | Output has no `# Global Memory` section |
| Search all scopes | `muxcode memory search "test"` | Results include both global and project entries with source tags |
| Search project only | `muxcode memory search "test" --scope project` | Only project entries |
| Search global only | `muxcode memory search "test" --scope global` | Only global entries |
| List with source | `muxcode memory list` | Entries show `[global]` or `[project]` prefix |
| Global rotation | Write on two different days | Previous day's file archived to `~/.config/muxcode/memory/{role}/YYYY-MM-DD.md` |
| New project session | Start muxcode in a fresh project | Agent prompt includes global memory from `~/.config/muxcode/memory/` |
| Init creates dir | `muxcode init` | `~/.config/muxcode/memory/` directory exists |
| Unit tests | `go test ./...` | All existing + new tests pass |

## Token budget considerations

Global memory adds a new section to every agent's system prompt. To manage token growth:

- Global memory respects the same 7-day context window as project memory
- Compaction alerts (`compact-recommended`) include global memory size in the total
- `--no-global` provides an escape hatch if context is too large
- Agents should be selective about what they write to global memory — universal patterns only
