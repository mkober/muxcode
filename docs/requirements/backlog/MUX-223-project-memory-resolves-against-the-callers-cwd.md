# MUX-223: Project Memory Resolves Against the Caller's Working Directory, Stranding Session Summaries

**Tracking:** [mkober/muxcode#207](https://github.com/mkober/muxcode/issues/207)

`MemoryDir()` returns the relative path `.muxcode/memory` unless `BUS_MEMORY_DIR` is set, and no
production code sets it. So `muxcode session compact` and `muxcode memory write` write wherever the
calling shell happens to stand. Run from a subdirectory, they create a second memory tree there, and
the summary is never restored: session restore and `memory context` read the project root's.

## Context

### Source and standard of evidence

Reported by edit during [MUX-137](../completed/MUX-137-test-bus-dir-leak.md)'s sweep-boundary check
(main `f9e13f2`), on the user's standing instruction to file defects found along the way. Plan
confirmed the code and the files in the tree; the claim about what restore reads is edit's and was
not re-traced here.

| Evidence | Detail |
|----------|--------|
| `bus/config.go:385–390` `MemoryDir()` | `BUS_MEMORY_DIR` if set, else `filepath.Join(".muxcode", "memory")` — relative to the process's working directory |
| Who sets `BUS_MEMORY_DIR` | Outside `*_test.go`, nothing: the only non-test mention is the read in `config.go` |
| `tools/muxcode/.muxcode/memory/edit.md` | 5524 bytes, last written 2026-11-13 15:03, with an `edit/` archive directory beside it |
| `tools/muxcode/bus/.muxcode/memory/edit.md` | 1161 bytes, 2026-11-13 17:03 — edit's summary of PR #185 |

Both are real session summaries written by an agent whose shell stood in `tools/muxcode` or
`tools/muxcode/bus`. Both directories are gitignored, so nothing surfaced them.

### Scope boundary

Global memory (`GlobalMemoryDir`, `~/.config/muxcode/memory`) is absolute already and is not part of
this defect. Test isolation of the two directories is
[MUX-137](../completed/MUX-137-test-bus-dir-leak.md) and
[MUX-224](../completed/MUX-224-tests-use-the-real-global-memory-dir.md).

### Decisions — the user's, recorded 2026-11-14

Both were owed at filing. The user decided them on 2026-11-14; edit relayed the decisions
(`/tmp/decisions-223-225.md`) and plan recorded them here.

| # | Decision | Options offered | Decided |
|---|----------|-----------------|---------|
| 1 | How project memory finds its root | (a) export an absolute `BUS_MEMORY_DIR` at launch, as `MUXCODE_BUS_DIR` is; (b) resolve from the session's project directory or the git toplevel inside `MemoryDir()`; (c) both, the export winning | **(c)**, in the order below |
| 2 | What happens to memory already stranded | Report only; move into the root; or merge entry by entry | **Merge entry by entry**, as below. The stranded files are the user's own session history — **never deleted** |

**Decision 1 — resolution order.**

1. An absolute `BUS_MEMORY_DIR`, exported at launch as `MUXCODE_BUS_DIR` is, wins.
2. Else the **main checkout's** root found through git: the parent of `git rev-parse --git-common-dir`, not `--show-toplevel`, so a worktree worker shares the main checkout's memory.
3. Else an error. Writes (`memory write`, `session compact`) fail naming `BUS_MEMORY_DIR` as the fix; reads return global memory only. Never a cwd-relative path.

Implementation note from the decision: do **not** resolve by walking up to the nearest `.muxcode/`
— from `tools/muxcode/bus` that walk would stop at the stranded `tools/muxcode/.muxcode`.

**Decision 2 — merge.** Stranded memory is merged entry by entry into the root's memory:
timestamped sections in time order, exact duplicates skipped, and the root file backed up before the
merge. The stranded originals are kept after merging, not deleted — for example under
`.muxcode/memory/stranded/<original-relative-path>/`. The two files named above
(`tools/muxcode/.muxcode/memory/edit.md` with its `edit/` archive, and
`tools/muxcode/bus/.muxcode/memory/edit.md`) are merged this way.

## Requirements

### Acceptance criteria

- [ ] `muxcode session compact` and `muxcode memory write`, run from any subdirectory of a project, write to the project root's `.muxcode/memory`
- [ ] `muxcode memory context` and session restore, run from any subdirectory, read the same directory the writes went to
- [ ] An explicit `BUS_MEMORY_DIR` still wins (negative control: behaviour with it set is unchanged)
- [ ] A process outside any project resolves somewhere defined and documented, never to an arbitrary relative path — per Decision 1: writes fail naming `BUS_MEMORY_DIR`, reads return global memory only
- [ ] Stranded memory directories under a project are found and reported, and handled as Decision 2 says; no summary is lost
- [ ] The two stranded `edit.md` files in this repository are accounted for, by name, in this spec
- [ ] `docs/configuration.md` and `docs/agent-bus.md` state how the memory directory is resolved

### Technical approach

Follows Decision 1's order: the launch export, then the main checkout's root through
`git rev-parse --git-common-dir`, then an error. One function owns the answer (`MemoryDir`), every
caller goes through it, and a spawned worker and a hosted role resolve the same root as their
session. `MemoryDir` returns a bare string today, so the error branch needs a seam its callers can
read. Stranded directories are merged as Decision 2 says, originals kept.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/config.go` | `MemoryDir`, `MemoryPath` |
| `tools/muxcode/bus/memory.go`, `bus/rotation.go` | Readers and the archive rotation |
| `tools/muxcode/bus/launch.go`, `bus/launcher.go` | Where a launch-time export would be set |
| `tools/muxcode/cmd/session.go` | `session compact` |

## Implementation

### Phase 1: One root for project memory

- [x] Record Decisions 1 and 2 in this spec before any code — recorded 2026-11-14, see "Decisions — the user's"
- [ ] `MemoryDir` resolves the project root as decided; an explicit `BUS_MEMORY_DIR` still wins
- [ ] Unit tests: a write from a subdirectory lands at the root; a read from a subdirectory finds it; outside a project the documented fallback applies

### Phase 2: Stranded memory

- [ ] A command or startup check lists `.muxcode/memory` directories below the project root
- [ ] Apply Decision 2 to them; record in this spec what happened to the two `edit.md` files named above
- [ ] Negative control: the project root's own memory directory is never reported as stranded

### Phase 3: Integration test

- [ ] Create `scripts/test-memory-dir-root.sh`: a scratch project and session; `session compact` and `memory write` from two subdirectories → one memory directory, at the root; `memory context` from a third subdirectory returns both entries
- [ ] Control: with `BUS_MEMORY_DIR` set, writes go there and nowhere else
- [ ] A seeded stranded directory is reported and handled as decided, its content intact
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-memory-dirs.md` (edit).
- Parked at filing: Decisions 1 and 2 were unrecorded, and Decision 2 acts on the user's own session history.
- Unparked 2026-11-14: the user decided both (relayed by edit); no other decision is open.

## Status

**Backlog** — 1/17. Filed 2026-11-14. Unparked 2026-11-14 with Decisions 1 and 2 recorded; eligible for the defect loop.
