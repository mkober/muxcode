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

- [x] `muxcode session compact` and `muxcode memory write`, run from any subdirectory of a project, write to the project root's `.muxcode/memory`
- [x] `muxcode memory context` and session restore, run from any subdirectory, read the same directory the writes went to
- [x] An explicit `BUS_MEMORY_DIR` still wins (negative control: behaviour with it set is unchanged) — for an absolute value, which is what Decision 1 names; a relative one is now refused (qualified 2026-11-17 by plan; see [Results for Phase 3](#results-for-phase-3))
- [x] A process outside any project resolves somewhere defined and documented, never to an arbitrary relative path — per Decision 1: writes fail naming `BUS_MEMORY_DIR`, reads return global memory only
- [x] Stranded memory directories under a project are found and reported, and handled as Decision 2 says; no summary is lost
- [x] The two stranded `edit.md` files in this repository are accounted for, by name, in this spec
- [x] `docs/configuration.md` and `docs/agent-bus.md` state how the memory directory is resolved

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
- [x] `MemoryDir` resolves the project root as decided; an explicit `BUS_MEMORY_DIR` still wins
- [x] Unit tests: a write from a subdirectory lands at the root; a read from a subdirectory finds it; outside a project the documented fallback applies

### Results for Phase 1

Recorded 2026-11-17 from the worker's handoff (`/tmp/mux223-phase1-reply.md`), which predates the
review's two must-fixes; where they differ, this is the tree. Plan read the diffs of `bus/config.go`,
`launcher.go`, `memory.go`, `setup.go` and `provider_codex.go`, and the test file.

| What | Where |
|------|-------|
| One resolver, in Decision 1's order: an absolute `BUS_MEMORY_DIR`; else `.muxcode/memory` at the main checkout's root; else `ErrNoMemoryRoot`, whose text names `BUS_MEMORY_DIR` | `ProjectMemoryDir` (`bus/config.go`) |
| The root is the parent of `git rev-parse --git-common-dir`, so a worktree shares it; no walk up to a nearest `.muxcode/` | `MainCheckoutRoot`, cached per directory |
| `MemoryDir`, `MemoryPath` and the archive helpers keep their signatures and return `""` when nothing resolves, so a reader finds no project memory and `context` falls back to global alone; a writer asks `ProjectMemoryDir` for the error | `bus/config.go`, `AppendMemory` (`bus/memory.go`) |
| `Init` creates no memory directory when nothing resolves | `bus/setup.go` |
| `LaunchSession` exports an absolute `BUS_MEMORY_DIR` into its own environment, the first window and the tmux session | `LaunchMemoryDir`, `bus/launcher.go` |

**Beyond what Decision 1 spells out — the worker's and the review's, not the user's.**

| Addition | Why | From |
|----------|-----|------|
| A **relative** `BUS_MEMORY_DIR` is refused (`ErrRelativeMemoryDir`), at launch too | The first cut made it absolute against the caller's directory, which split memory again — two subdirectories, two trees. Decision 1 names an absolute override only | Review must-fix |
| The Codex `workspace-write` sandbox grants the project memory directory, and it alone | A spawn worker in a worktree now writes the main checkout's memory, outside its workspace, where `memory write` and `session compact` would fail | Review must-fix (`muxcodeStateRoots`) |
| A launch on a project **outside git** exports the project directory's own `.muxcode/memory` | Decision 1's third step is an error; the launcher knows the project directory, so a session always has a defined root. A bare command outside git, with nothing exported, still errors | Worker |
| A `BUS_MEMORY_DIR` inherited inside another muxcode pane (`MUXCODE=1`) is ignored at launch | It is the other session's export; honouring it would write into that project's memory | Worker |
| A git common dir not named `.git` (a submodule's) falls back to `--show-toplevel` | The parent of `.git/modules/<name>` is not a checkout root | Worker |

**Tests** (`bus/memory_root_test.go`): `TestProjectMemory_SubdirectoryWritesAtRoot` (a write from a
subdirectory lands at the root, a sibling subdirectory's `context` reads it, no tree beside the
caller); `_WorktreeSharesMainRoot`; `_OutsideProjectFailsWrites` (the write fails naming
`BUS_MEMORY_DIR`, creates nothing, `context` returns global memory only); `_ExplicitEnvWins`
(negative control, the root untouched); `_RelativeOverrideRefused` (from every directory, inside a
checkout and out; control — the same value made absolute is honoured); `TestLaunchMemoryDir`;
`TestCodexMuxcodeStateRoots_GrantProjectMemory`.

**A script outside this phase changed.** `scripts/test-bus-dir-leak.sh` (MUX-137): its control leak
can no longer land relative to the working directory, so its throwaway tree is `git init`ed and the
expected leak moved. Its run — 22 passed, 0 failed, floor 22 (run task
`1794934869-spawn-26088880-c9e10526`) — predates the review fixes and has not been repeated since.

**For the third criterion.** It reads "an explicit `BUS_MEMORY_DIR` still wins (negative control:
behaviour with it set is unchanged)". That holds for an absolute value. A relative one used to be
taken as written and is now an error. Left open for Phase 3, which drives it.

Verified and ticked 2026-11-17 once all three had run after the last edit (12:14:49): `./build.sh`
exit 0 at 12:18:32, `./test.sh` exit 0 at 12:20:07 (it had failed at 12:14:33 on
`TestCodexBuildExecArgs_OptOutNamesPolicyWithNoGrants`, which the sandbox grant changed and the
worker then updated), review at 12:20:39 with 0 must-fix, 0 should-fix, 0 nits. Criterion 4 is
ticked on `_OutsideProjectFailsWrites` and the docs below; criterion 7 on the docs.

Docs (plan): `docs/configuration.md` — the `BUS_MEMORY_DIR` row; `docs/agent-bus.md` — the
`muxcode memory` paragraph and the environment row.

### Phase 2: Stranded memory

- [x] A command or startup check lists `.muxcode/memory` directories below the project root
- [x] Apply Decision 2 to them; record in this spec what happened to the two `edit.md` files named above
- [x] Negative control: the project root's own memory directory is never reported as stranded

### Results for Phase 2

Recorded 2026-11-17 from the worker's results (`/tmp/mux223-phase2-results.md`). Plan checked the
files on disk itself, against the worker's pre-merge snapshot; what plan checked is marked. Boxes are
ticked at `verify-spec`, not here.

**The command.** `muxcode memory stranded` lists `.muxcode/memory` directories below the checkout
root, never the project memory's own `.muxcode`, skipping `.git` and `node_modules` and following no
symlink. A command only — no startup check; the phase allows either.

**The merge rule** (`muxcode memory stranded --merge`, `MergeStrandedMemory` in
`bus/memory_stranded.go`), Decision 2 as built:

| Step | What |
|------|------|
| Back up | The root's existing file is copied to `.muxcode/stranded-memory/backup-<stamp>-<random>/<file>` before it is touched. Each invocation has its own backup directory (`os.MkdirTemp`, created on its first backup), so two runs in the same second cannot share one, and within a run the first copy of a file wins (`writeBackupOnce`, `O_EXCL`). Before a review fix the directory was a shared `backup-<stamp>/` — the form the 2026-11-17 run below used |
| Merge | Each stranded file is merged entry by entry into the same path under the project memory directory: sections in timestamp order, exact duplicates skipped, a `## ` heading inside an entry's body kept with its entry. A file that adds nothing is not rewritten |
| Keep | The stranded directory is then moved intact to `.muxcode/stranded-memory/<its path relative to the root>`. Nothing is deleted |

The keep directory is `.muxcode/stranded-memory/`, beside `.muxcode/memory`, not the
`.muxcode/memory/stranded/` Decision 2 gave as an example: a subdirectory of the memory directory
reads as a role's archive.

**What happened to the two files** — the sixth criterion. The merge was run on them on 2026-11-17 at
12:25, with the tree-built binary, after a snapshot to `/tmp/mux223-merge-oNDxaK/snapshot`.

| Stranded directory | Merged | Kept at | Plan checked |
|--------------------|--------|---------|--------------|
| `tools/muxcode/.muxcode/memory` — `edit.md`, `edit/2026-10-22.md`, `edit/2026-10-29.md` | `edit.md` +6 entries, each archive +2, no duplicates | `.muxcode/stranded-memory/tools/muxcode/.muxcode/memory` | Byte-identical to the snapshot (`diff -r`); all 6 entries' timestamps present in the root `edit.md` |
| `tools/muxcode/bus/.muxcode/memory` — `edit.md`, the PR #185 summary of 2026-11-13 17:03 | +1 entry, no duplicate | `.muxcode/stranded-memory/tools/muxcode/bus/.muxcode/memory` | Byte-identical to the snapshot; its entry present in the root `edit.md` |

The root `.muxcode/memory/edit.md` went from 1014 bytes and 3 entries to 7699 bytes and 10
(plan counted both), and all 3 of its own earlier entries are still in it. Neither stranded
directory remains at its old path; a second scan reports none. Everything under `.muxcode/` is
gitignored, so none of this is in any commit.

**It ran on real session history before the code had been built, tested or reviewed by the graph, and
a bug showed.** Both directories shared the run's backup directory, so the second merge overwrote
the backup of `edit.md` with the file as the first merge had left it. The worker's snapshot is what
made that recoverable: the backup was repaired from it — `backup-20261117-122502/edit.md` is the
1014-byte pre-merge file, which plan confirmed identical to the snapshot (`cmp`) — and the
overwritten copy is kept beside it as `edit.md.after-first-merge`. The code now refuses to overwrite
a backup (`writeBackupOnce`), pinned by `TestMergeStrandedMemory_BackupIsThePreRunFile`. No entry was
lost. The snapshot itself sits under `/tmp`, which macOS clears after three days; the kept originals
and the backup under `.muxcode/stranded-memory/` are the lasting copies.

**Tests** (worker's list; plan has not read them): `TestFindStrandedMemory` — also the negative
control, the root's own memory directory is never reported; `TestMergeStrandedMemory` (order,
duplicate, body heading, archive merge, byte-identical backup and kept original, an empty rescan,
the keep directory not read as a role); `TestMergeStrandedMemory_BackupIsThePreRunFile`;
`TestMergeStrandedMemory_AllDuplicatesWritesNothing`.

**Three review fixes after the run on the real files** (reviews of 12:36 and 12:45; plan read the
findings and the code's own account of each, not the tests).

| Finding | Fix |
|---------|-----|
| **Must-fix: the merge could lose a summary appended while it ran.** It read the root file, backed it up and replaced it with no coordination, so a `session compact` landing between the read and the rename vanished from both the file and the backup; two merges could also collide | The whole merge holds the project memory lock (`withMemoryLock`, an exclusive flock on `<memory dir>/.memory.lock`), and `AppendMemory` with its rotation now takes the same lock. A directory a concurrent merge already moved is skipped. `TestMergeStrandedMemory_ConcurrentAppendSurvives`, `_ConcurrentMerges` |
| **Must-fix: the scanner skipped the memory directory's parent.** With `BUS_MEMORY_DIR` outside the repo it would have reported and moved the root's own `.muxcode/memory`; with `BUS_MEMORY_DIR=<repo>/memory` it skipped the whole scan | `FindStrandedMemory` skips exactly the memory directory and its keep directory, plus the checkout root's own `.muxcode/memory` and `.muxcode/stranded-memory` under any override, and no parent of them. `TestFindStrandedMemory_Overrides` |
| **Should-fix: two merges in the same second shared a backup directory** | One backup directory per invocation — the Back up row above. `TestMergeStrandedMemory_SameSecondRunsBackUpTheirOwnStart` |

**The 12:25 run on the real files predates all three.** It ran without the lock. Plan's check that
no entry was lost was made after it, against the pre-merge snapshot: the root `edit.md` holds its 3
earlier entries and the 7 merged ones. An append to `edit.md` landing inside that run's
read-to-rename window would not appear in the snapshot either, so that check cannot rule it out;
nothing suggests one happened — the file's newest entry before the merge was 11:53.

Verified and ticked 2026-11-17 once all three had run after the last edit (12:45:45): `./build.sh`
exit 0 at 12:49:58, `./test.sh` exit 0 at 12:51:36, review at 12:53:12 with 0 must-fix, 0 should-fix,
0 nits (a static review). Criterion 5 is ticked on the tests and the real run; criterion 6 on the
table above.

Docs (plan): `docs/agent-bus.md` — `muxcode memory stranded` under `muxcode memory`.

### Phase 3: Integration test

- [x] Create `scripts/test-memory-dir-root.sh`: a scratch project and session; `session compact` and `memory write` from two subdirectories → one memory directory, at the root; `memory context` from a third subdirectory returns both entries
- [x] Control: with `BUS_MEMORY_DIR` set, writes go there and nowhere else
- [x] A seeded stranded directory is reported and handled as decided, its content intact
- [x] Run the script and record pass/fail counts in this spec

### Results for Phase 3

Recorded 2026-11-17 from the worker's request; plan read the script's header and the run's log.
Boxes are ticked at `verify-spec`, not here.

`scripts/test-memory-dir-root.sh` — hermetic: muxcode built from the tree, scratch `HOME`,
`XDG_STATE_HOME`, global memory and git checkout, no daemon and no tmux. Its pre-fix build is the
tree with `MemoryDir`'s relative default restored in a scratch copy.

**Run: 25 passed, 0 failed, floor 25, exit 0** (run task `1794938153-spawn-26088880-02322619`,
requested 12:55:53, after the script's last edit at 12:55:47; the run agent's history row confirms
it, and plan read the log: 25 `ok`, no `FAIL`, sections A–G all present).

| Section | Shows |
|---------|-------|
| A | `session compact` from `sub/a` and `memory write` from `sub/b/c` land in one memory directory, at the checkout root; `memory context` and `session resume` from `sub/d` read both |
| B | A git worktree's write lands in the main checkout's memory |
| C | Control: an absolute `BUS_MEMORY_DIR` receives the write and nothing in the checkout changes |
| D | A relative `BUS_MEMORY_DIR` is refused naming the variable, from two subdirectories, creating nothing |
| E | Outside any checkout a write fails naming `BUS_MEMORY_DIR` and creates nothing; `memory context` returns global memory only |
| F | A seeded stranded directory is listed — the root's own memory never is — merged entry by entry with the root backed up, kept byte for byte under `.muxcode/stranded-memory`, and a rescan finds nothing |
| G | Control: the pre-fix build strands a subdirectory's write beside it |

**What it does not drive.** The launch export: the script runs no tmux and launches no session, so
`LaunchSession` exporting `BUS_MEMORY_DIR` into a session's panes is covered by the unit test
`TestLaunchMemoryDir` alone. Every command in the script resolves through git, or through a
`BUS_MEMORY_DIR` the script sets itself.

The docs the worker asks for under criterion 7 were written at Phase 1 and Phase 2's verification
(`docs/configuration.md`, `docs/agent-bus.md`); `CLAUDE.md` lists the script.

**Verified and ticked 2026-11-17**, every piece of evidence after the last edit (the script at
12:55:47, `CLAUDE.md` at 12:56:20):

| Evidence | When | Result |
|----------|------|--------|
| `bash scripts/test-memory-dir-root.sh` | requested 12:55:53 | 25 passed, 0 failed, floor 25, exit 0 |
| `./build.sh` | 12:57:20 | exit 0 |
| `./test.sh` (hook row) | 12:57:41 | exit 0, `modules=2 ran=2 passed=2 failed=0 never-executed=0` |
| Review | 12:58:34 | 0 must-fix, 0 should-fix, 0 nits |

What the last three criteria rest on:

| # | Criterion | Evidence |
|---|-----------|----------|
| 1 | `session compact` and `memory write` from any subdirectory write to the root | Section A; `TestProjectMemory_SubdirectoryWritesAtRoot` |
| 2 | `memory context` and session restore from any subdirectory read the same directory | Section A — `memory context` and `session resume` from a third subdirectory read both entries |
| 3 | An explicit `BUS_MEMORY_DIR` still wins | Section C and `TestProjectMemory_ExplicitEnvWins`, for an absolute value. **Qualified by plan:** the criterion's "behaviour with it set is unchanged" is not true of a relative value, which was taken as written before and is refused now (section D). Decision 1 names an absolute override; the refusal was a review must-fix, not a user decision |

Two things this close does not show, both recorded above: the launch export is covered by a unit
test only, and `scripts/test-bus-dir-leak.sh` — changed in Phase 1 — was last run by the worker
during Phase 2 (22 passed, 0 failed, run task `1794936631-spawn-26088880-892c5214`), before Phase
2's last review fixes.

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-memory-dirs.md` (edit).
- Parked at filing: Decisions 1 and 2 were unrecorded, and Decision 2 acts on the user's own session history.
- Unparked 2026-11-14: the user decided both (relayed by edit); no other decision is open.

### Findings at activation (2026-11-17)

Checked against the tree at `dbc8e20`. The defect stands as filed.

| Spec says | Tree today |
|-----------|------------|
| `MemoryDir()` at `bus/config.go:385–390` returns the relative `.muxcode/memory` unless `BUS_MEMORY_DIR` is set | Unchanged, same lines |
| Nothing outside `*_test.go` sets `BUS_MEMORY_DIR` | Still so: the only non-test mentions are the read and its comment in `config.go` |
| `tools/muxcode/.muxcode/memory/edit.md`, 5524 bytes, 2026-11-13 15:03, with an `edit/` archive | Present, same size and time |
| `tools/muxcode/bus/.muxcode/memory/edit.md`, 1161 bytes, 2026-11-13 17:03 | Present, same size and time |
| Other stranded memory directories | None found below the project root besides those two |
| `scripts/test-memory-dir-root.sh` | Does not exist |

For the worker: Phase 2 acts on the user's own session history. Decision 2 is the rule — merge entry
by entry, back the root file up first, keep the stranded originals, delete nothing.

## Status

**Complete** — activated and closed 2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 14 then; ranks 1–13 were parked). Uncommitted at close-out; the run's ship step commits it. 17/17 — Phases 1–3 complete and all seven criteria met, criterion 3 as qualified by plan. Filed 2026-11-14; unparked 2026-11-14 with Decisions 1 and 2 recorded.

Project memory now has one root (`ProjectMemoryDir`), in the order the user decided: an absolute
`BUS_MEMORY_DIR`, which the launcher exports into every session; else `.muxcode/memory` at the main
checkout's root, found through `git rev-parse --git-common-dir`, so a worktree shares it; else an
error — a write fails naming `BUS_MEMORY_DIR` and `memory context` returns global memory only. It is
never relative to the working directory. `muxcode memory stranded [--merge]` finds memory stranded
by the old default and merges it entry by entry, backing the root file up and keeping every
original. The two stranded `edit.md` directories in this repository were merged that way on
2026-11-17 and are kept under `.muxcode/stranded-memory/`.

What a reader should not take for more than it is, each recorded in the results above:

- **Not the user's decisions:** a relative `BUS_MEMORY_DIR` being refused, a launch outside git
  exporting the project directory, an inherited value being ignored inside another muxcode pane, the
  submodule fallback, and the keep directory's name. Criterion 3 was qualified by plan accordingly.
- **The merge of the real files ran before the code was reviewed** and before the memory lock
  existed. Plan verified afterwards, against a pre-merge snapshot, that no entry was lost.
- **Covered by unit test only:** the launch export.
- **Not re-run on the final tree:** `scripts/test-bus-dir-leak.sh`, changed in Phase 1.
