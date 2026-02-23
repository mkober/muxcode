# Memory Search & List — Implementation Plan

## Context

Feature #1 from `docs/openclaw-suggestions.md`: **Persistent memory with semantic search** (P0).

Today, memory is append-only markdown with no way to query it. Agents write learnings via `memory write` but have no way to find them later except reading the entire file. This adds `memory search` (keyword matching with scoring) and `memory list` (section inventory) — the two highest-value sub-features. Daily rotation and session summary are deferred to follow-up work.

## Files to modify

| File | What changes |
|------|-------------|
| `tools/muxcode-agent-bus/bus/memory.go` | New types, parser, search, formatting |
| `tools/muxcode-agent-bus/cmd/memory.go` | New `search` and `list` subcommands |
| `tools/muxcode-agent-bus/bus/memory_test.go` | ~11 new test functions |

No changes to `main.go`, `config.go`, or any other files.

## Step 1 — Types and parser in `bus/memory.go`

Add after existing functions:

```go
type MemoryEntry struct {
    Role      string // "shared", "build", "edit", etc.
    Section   string // from "## Title" line
    Timestamp string // from "_YYYY-MM-DD HH:MM_" line
    Content   string // body text after timestamp
}

type SearchResult struct {
    Entry MemoryEntry
    Score float64
}
```

**`ParseMemoryEntries(content, role string) []MemoryEntry`** — splits on `## ` headers, extracts timestamp from `_..._` line, collects body text. Relies on the rigid format produced by `AppendMemory()`.

**`ListMemoryFiles() ([]string, error)`** — scans `MemoryDir()` for `*.md` files via `os.ReadDir()`, returns role names (filename minus `.md`). Handles nonexistent dir gracefully.

**`AllMemoryEntries() ([]MemoryEntry, error)`** — calls `ListMemoryFiles()` + `ReadMemory()` + `ParseMemoryEntries()` for each file.

## Step 2 — Search scoring in `bus/memory.go`

**`SearchMemory(query, roleFilter string, limit int) ([]SearchResult, error)`**

- Tokenize query into lowercase words via `strings.Fields(strings.ToLower(query))`
- For each entry: score = `count(term in content) + count(term in header) * 2.0`
- Filter by role if `roleFilter != ""`
- Sort descending by score, exclude zero-score entries
- Truncate to `limit` if > 0

Uses `strings.Count()` for substring matching — intentionally loose so "build" matches "building", "rebuild", etc. No IDF (corpus too small to benefit).

**`scoreEntry(entry MemoryEntry, queryTerms []string) float64`** — unexported helper.

## Step 3 — Formatting in `bus/memory.go`

**`FormatSearchResults(results []SearchResult) string`** — block format matching `FormatMessage()` style:
```
--- [shared] Build Config (2026-02-21 14:27) score:4.0 ---
use pnpm for all builds
```

**`FormatMemoryList(entries []MemoryEntry) string`** — columnar output:
```
shared     Agent Permissions                    2026-02-21 14:27
edit       delegation rules                     2026-02-20 17:30
```

## Step 4 — CLI handlers in `cmd/memory.go`

Add `"search"` and `"list"` cases to the existing switch in `Memory()`. Update usage string to include them.

**`memorySearch(args)`** — parses positional query + `--role` and `--limit` flags using the manual loop pattern from `cmd/chain.go`. Calls `bus.SearchMemory()`, prints `bus.FormatSearchResults()`. Silent exit on no results (matches existing pattern).

**`memoryList(args)`** — parses optional `--role` flag. Calls `bus.AllMemoryEntries()`, filters, prints `bus.FormatMemoryList()`.

New import: `"strconv"` (for `--limit` parsing).

## Step 5 — Tests in `bus/memory_test.go`

| Test | What it validates |
|------|------------------|
| `TestParseMemoryEntries` | Two entries parsed, correct fields |
| `TestParseMemoryEntries_Empty` | Empty string → 0 entries |
| `TestParseMemoryEntries_MultilineContent` | Multi-line body preserved |
| `TestListMemoryFiles` | Scans dir, returns role names |
| `TestListMemoryFiles_EmptyDir` | Nonexistent dir → empty, no error |
| `TestSearchMemory_BasicMatch` | Single term matches correct entry |
| `TestSearchMemory_HeaderBoost` | Header match ranks above body match |
| `TestSearchMemory_RoleFilter` | `--role` limits scope |
| `TestSearchMemory_Limit` | `--limit` caps results |
| `TestSearchMemory_NoMatch` | Unmatched query → empty |
| `TestSearchMemory_CaseInsensitive` | "pnpm" matches "PNPM" |

## Usage examples

```bash
muxcode-agent-bus memory search "pnpm build"
muxcode-agent-bus memory search "permission" --role shared
muxcode-agent-bus memory search "deploy" --limit 5
muxcode-agent-bus memory list
muxcode-agent-bus memory list --role edit
```

## Verification

1. Delegate `go test ./bus/...` to the test agent — all new + existing tests pass
2. Delegate `./build.sh` to the build agent — binary compiles
3. Run `muxcode-agent-bus memory search "pnpm"` against real `.muxcode/memory/` — returns scored results
4. Run `muxcode-agent-bus memory list` — shows all sections across all memory files
5. Run `muxcode-agent-bus memory search "nonexistent"` — silent empty output
