# Modal: Memory Browser

Interactive modal for browsing, searching, and editing agent memory. Provides a unified view of shared and per-role memory with BM25 search, section navigation, and inline editing.

**Depends on:** [Modal Window Manager](modal-window-manager.md)

## Use case

Memory is currently accessed via CLI commands (`muxcode memory context`, `muxcode memory search`). These work but are hard to browse — output is long, unsearchable in the terminal without piping to a pager, and editing requires knowing the exact section name. A modal provides a navigable, searchable interface.

## Layout

Vertical split: memory content on top (80%), search/command bar on bottom (20%).

```
+--------------------------------------------------+
|  ' Memory Browser '                              |
|                                                   |
|  +----------------------------------------------+ |
|  | # Shared Memory                              | |
|  |                                              | |
|  | ## Console.go Shell-to-Go Migration          | |
|  | _2026-03-24 16:32_                           | |
|  | Phase 1 of shell-to-go migration complete... | |
|  |                                              | |
|  | ## Session Summary                           | |
|  | _2026-03-25 15:50_                           | |
|  | Phase 4 (auto-accept) of muxcode-go...       | |
|  +----------------------------------------------+ |
|  | > search: _                    [role: all]   | |
|  +----------------------------------------------+ |
+--------------------------------------------------+
```

## Modal config

```go
RegisterModal(ModalConfig{
  Name:    "memory",
  Title:   " Memory Browser ",
  Width:   "62%",
  Height:  "62%",
  Command: "muxcode modal memory-browser",
  Sizes: map[string][2]string{
    "compact": {"50%", "40%"},
    "full":    {"95%", "95%"},
  },
})
```

## Features

### Role filtering

Browse memory by role:
- All roles (default) — shows shared + all role-specific memory
- Single role — `muxcode modal open memory --arg edit` shows shared + edit memory
- Shared only — `muxcode modal open memory --arg shared`

### BM25 search

Uses the existing `SearchMemoryBM25()` from `bus/search.go`. Search results are ranked by relevance and highlighted in the content view. Search is interactive — results update as the user types.

### Section navigation

- Arrow keys or `j/k` to scroll
- `/` to focus the search bar
- `n/N` to jump between search matches
- `Tab` to cycle through roles
- `Enter` on a section to expand/collapse

### Archive access

Uses `ReadMemoryWithHistory()` from `bus/rotation.go` to include archived memory entries. Toggle archive visibility with `a` key.

### Inline editing

- `e` on a section opens it in `$EDITOR` (nvim) for editing
- `d` on a section deletes it (with confirmation)
- `w` writes a new memory entry (prompts for section name and content)

### Display

- Section headers in `ColorPurple`
- Timestamps in `ColorDim`
- Search matches highlighted in `ColorYellow`
- Role labels in `ColorCyan`

## Menu entry

```
"Memory Browser"          M "run-shell 'muxcode modal open memory'"
```

## Keybinding

```
bind M run-shell 'muxcode modal open memory'
```

## Success criteria

- [ ] Modal displays shared + role memory with section headers
- [ ] BM25 search with interactive results and highlighting
- [ ] Role filtering via `--arg` and `Tab` cycling
- [ ] Section navigation with `j/k`, `n/N` for search matches
- [ ] Archive toggle with `a` key
- [ ] Inline edit (`e`), delete (`d`), write (`w`) operations
- [ ] Dracula-themed color coding
- [ ] Menu entry and keybinding registered
