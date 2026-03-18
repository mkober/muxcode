# Memory Search

## Purpose

Agents write learnings to persistent memory but have no way to query them later except reading the entire file. Memory search and list capabilities let agents find relevant past learnings quickly, making the memory system useful at scale.

## Requirements

- Agents must be able to search memory by keyword query across all memory files
- Search must use term-based scoring with header matches weighted higher than body matches
- Search must be case-insensitive and support substring matching (e.g., "build" matches "building", "rebuild")
- Results must be ranked by relevance score in descending order
- Search must support filtering by role (shared, build, edit, etc.)
- Search must support limiting the number of results returned
- Agents must be able to list all memory sections as an inventory across all roles
- List must support filtering by role
- No results must produce silent empty output (no error messages)
- Per-agent and shared memory scopes must be preserved

## Acceptance criteria

- `memory search "<query>"` returns scored, ranked results from all memory files
- `memory search "<query>" --role <role>` limits results to a specific role's memory
- `memory search "<query>" --limit N` caps results at N entries
- `memory list` shows all memory sections across all roles in columnar format
- `memory list --role <role>` filters to a single role's sections
- Header matches rank higher than body-only matches
- Searching for a nonexistent term produces no output and exits cleanly

## Status

Implemented
