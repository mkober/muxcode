# Spawn worktree isolation

## Purpose

Spawned agents currently share the main working directory with all other agents. This means a spawn running `git diff`, reading files, or making edits can see (and be affected by) the edit agent's uncommitted changes. Worktree isolation gives each spawn its own checkout of the repo at the current commit, so it works on a clean snapshot without interfering with the main working tree.

## Context

### Current state

The spawn system (`bus/spawn.go`) creates temporary agent sessions:

1. `StartSpawn()` generates a unique spawn role (`spawn-{8hex}`), creates a tmux window with horizontal split
2. Seeds the spawn's inbox with the task message
3. Launches the agent via `muxcode-agent.sh` in pane 1
4. Daemon's `checkSpawns()` detects window death, marks the spawn completed, sends `spawn-complete` to the owner

Spawns share the same working directory as all other agents. The agent runs with `AGENT_ROLE=spawn-{id}` but no filesystem isolation. The launch command is:

```bash
AGENT_ROLE=spawn-a1b2c3d4 muxcode-agent.sh research
```

### What this changes

- `StartSpawn()` creates a git worktree before launching the agent
- The spawned agent's tmux pane starts in the worktree directory
- On spawn completion (or stop), the worktree is removed
- New `--no-worktree` flag for cases where shared working directory is desired
- `SpawnEntry` tracks the worktree path for cleanup

### Why spawns are the right target

| Candidate | Benefit | Risk |
|-----------|---------|------|
| **Spawn agents** | Already ephemeral with lifecycle management; filesystem isolation matches the session isolation model | Low — spawns are independent by design, no shared state expectations |
| Story isolation (agent-mode) | Would isolate autonomous multi-step work | High — entire feature is unbuilt; worktrees would be one piece of a much larger system |
| Parallel builds | Build on one branch while editing another | Medium — build agent hooks, history logging, and console rendering all assume shared CWD |
| Checkpoints | Snapshot before risky operations | Medium — new UX concept with edge cases around uncommitted state |

## Requirements

### Worktree lifecycle

- By default, `StartSpawn()` creates a git worktree in a temporary directory before launching the agent
- The worktree is created at the current `HEAD` commit (detached HEAD) — the spawn sees committed state only
- Worktree path: `/tmp/muxcode-spawn-{session}/{spawn-role}/`
- The spawned agent's shell starts with `cd` into the worktree directory
- When the spawn completes (window death detected by daemon) or is stopped (`StopSpawn`), the worktree is removed via `git worktree remove --force`
- Orphaned worktrees (spawn crashed without cleanup) are cleaned by `CleanFinishedSpawns()`

### Opt-out

- `muxcode spawn start <role> "<task>" --no-worktree` — launch without worktree isolation (shares main working directory, current behavior)
- Use case: spawns that need to see uncommitted changes (e.g. reviewing current edits, running tests on dirty state)
- `SpawnEntry` gains a `Worktree string` field — empty string means no worktree (either opted out or pre-feature spawns)

### Spawn entry tracking

`SpawnEntry` gains two fields:

```go
type SpawnEntry struct {
    // ... existing fields ...
    Worktree   string `json:"worktree,omitempty"`   // worktree directory path
    WorktreeRef string `json:"worktree_ref,omitempty"` // git ref used (commit SHA)
}
```

### Bus messaging from worktrees

- The message bus is path-independent — inbox files live in `/tmp/muxcode-bus-{session}/`, not in the repo. Bus messaging works unchanged from worktrees.
- `AGENT_ROLE` environment variable is already set for spawns. No change needed.
- Memory files (`.muxcode/memory/`) are in the main repo — worktree agents can read shared memory but writes go to the worktree copy. This is acceptable since spawn memory is ephemeral.

### Git operations in worktrees

- Spawns that create commits in the worktree create them on the detached HEAD — they don't affect any branch
- If a spawn needs to push changes, it must create a branch first (this is a spawn task design choice, not a system constraint)
- `git worktree list` shows all active spawn worktrees — useful for debugging

### Error handling

- If `git worktree add` fails (not a git repo, disk full, etc.), fall back to shared working directory with a warning in the spawn result
- If worktree cleanup fails (directory locked, manual changes), log a warning and continue — don't block spawn completion
- The cleanup path must handle: worktree already removed, directory doesn't exist, git worktree remove fails

### CLI changes

```bash
# Default: worktree isolation
muxcode spawn start research "What does guard.go do?"

# Opt out: shared working directory
muxcode spawn start research "Review my uncommitted changes" --no-worktree

# Show worktree path in status
muxcode spawn list
# ID        Role      Status   Owner  Worktree
# spawn-... research  running  edit   /tmp/muxcode-spawn-muxcode/spawn-a1b2c3d4/
```

## Acceptance criteria

- Spawns launch in a git worktree by default — `git worktree list` shows the new worktree
- The spawned agent's working directory is the worktree, not the main repo
- The worktree is at detached HEAD matching the current commit when `StartSpawn()` runs
- Bus messaging works unchanged — spawn can send/receive messages from the worktree
- Worktree is removed when the spawn completes (daemon detects window death)
- Worktree is removed when the spawn is stopped via `muxcode spawn stop`
- `CleanFinishedSpawns()` removes orphaned worktrees for completed/stopped spawns
- `--no-worktree` flag bypasses worktree creation (shared CWD, current behavior)
- If `git worktree add` fails, spawn falls back to shared CWD with a warning
- `muxcode spawn list` shows the worktree path (or "shared" for non-worktree spawns)
- `SpawnEntry.Worktree` field is populated and persisted in `spawn.jsonl`
- Existing spawns (no `Worktree` field) continue to work — backward compatible JSON parsing

## Key files

| File | Changes |
|------|---------|
| `bus/spawn.go` | Add worktree create/remove to `StartSpawn()`/`StopSpawn()`/`CleanFinishedSpawns()`, add `Worktree`/`WorktreeRef` to `SpawnEntry`, add `--no-worktree` parameter |
| `cmd/spawn.go` | Parse `--no-worktree` flag, pass to `StartSpawn()`, show worktree in `list` output |
| `watcher/watcher.go` | `checkSpawns()` calls worktree cleanup on completed spawns (already calls `RefreshSpawnStatus`) |
| `bus/spawn_test.go` | Tests for worktree lifecycle, fallback on failure, cleanup, `--no-worktree` |

## Non-goals

- **Branch creation** in worktrees — spawns work on detached HEAD. If a spawn needs a branch, the task prompt instructs it to create one. This is a task-level concern, not a system feature.
- **Worktree persistence** across session restarts — worktrees are ephemeral like the spawns themselves. Session re-init (`bus/setup.go`) can clean `/tmp/muxcode-spawn-{session}/` as part of stale data purge.
- **Worktree for non-spawn agents** — build, test, deploy, etc. share the main working directory. Worktree isolation for permanent agents is a separate, larger effort.
- **Uncommitted changes in worktrees** — worktrees start at `HEAD` (committed state only). Copying uncommitted changes into the worktree adds complexity (stash/patch mechanics) and defeats the purpose of isolation.
- **Worktree pooling** — each spawn gets its own worktree. Reusing worktrees across spawns adds lifecycle complexity for minimal benefit (worktree creation is fast).

## Implementation phases

### Phase 1: Worktree create and launch

Modify `StartSpawn()` in `bus/spawn.go`:

```go
func StartSpawn(session, role, task, owner string, useWorktree bool) (SpawnEntry, error) {
    // ... existing ID generation, entry creation ...

    var worktreePath string
    var worktreeRef string

    if useWorktree {
        worktreePath, worktreeRef, err = createSpawnWorktree(session, spawnRole)
        if err != nil {
            // Fall back to shared CWD with warning
            fmt.Fprintf(os.Stderr, "Warning: worktree creation failed (%v), using shared CWD\n", err)
            worktreePath = ""
        }
    }

    entry.Worktree = worktreePath
    entry.WorktreeRef = worktreeRef

    // ... existing tmux window creation ...

    // Launch agent — cd into worktree if set
    var launchStr string
    if worktreePath != "" {
        launchStr = fmt.Sprintf("cd %s && AGENT_ROLE=%s %s %s", worktreePath, spawnRole, launcher, role)
    } else {
        launchStr = fmt.Sprintf("AGENT_ROLE=%s %s %s", spawnRole, launcher, role)
    }
    // ... existing send-keys ...
}
```

New helper:

```go
// createSpawnWorktree creates a git worktree for a spawn agent.
// Returns (worktree path, commit SHA, error).
func createSpawnWorktree(session, spawnRole string) (string, string, error) {
    // Get current HEAD commit
    ref, err := exec.Command("git", "rev-parse", "HEAD").Output()
    // ...

    // Create worktree directory
    base := filepath.Join(os.TempDir(), "muxcode-spawn-"+session)
    wtPath := filepath.Join(base, spawnRole)
    os.MkdirAll(base, 0755)

    // git worktree add --detach <path> HEAD
    cmd := exec.Command("git", "worktree", "add", "--detach", wtPath, "HEAD")
    // ...

    return wtPath, strings.TrimSpace(string(ref)), nil
}
```

Implementation steps:

- Add `Worktree` and `WorktreeRef` fields to `SpawnEntry`
- Add `useWorktree bool` parameter to `StartSpawn()`
- Implement `createSpawnWorktree()` — `git worktree add --detach`
- Modify launch command to `cd` into worktree when set
- Fallback: if `git worktree add` fails, continue without worktree

### Phase 2: Worktree cleanup

Modify `StopSpawn()` and `RefreshSpawnStatus()`:

```go
// removeSpawnWorktree cleans up a spawn's worktree.
func removeSpawnWorktree(worktreePath string) error {
    if worktreePath == "" {
        return nil
    }
    // git worktree remove --force <path>
    cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
    if err := cmd.Run(); err != nil {
        // Fallback: rm -rf if git worktree remove fails
        return os.RemoveAll(worktreePath)
    }
    return nil
}
```

Implementation steps:

- Add `removeSpawnWorktree()` helper
- Call from `StopSpawn()` after killing tmux window
- Call from `RefreshSpawnStatus()` when marking spawn as completed
- Call from `CleanFinishedSpawns()` for any entry with a non-empty `Worktree` field
- Handle cleanup errors gracefully — log and continue

### Phase 3: CLI and display

Modify `cmd/spawn.go`:

- Parse `--no-worktree` flag in `spawn start` subcommand
- Pass `useWorktree` (default `true`) to `StartSpawn()`
- Add worktree column to `spawn list` output
- Show worktree path in `spawn result` output

Implementation steps:

- Add `--no-worktree` flag parsing
- Update `formatSpawnList()` to show worktree path or "shared"
- Update `formatSpawnResult()` to include worktree info

### Phase 4: Session cleanup integration

- Add `/tmp/muxcode-spawn-{session}/` cleanup to `bus/setup.go` `Init()` re-init path
- Run `git worktree prune` during re-init to clean stale worktree references
- Add to `CleanFinishedSpawns()`: after removing entries, prune any orphaned worktree directories under `/tmp/muxcode-spawn-{session}/`

## Status

Complete — status corrected 2026-08-31 during a backlog/completed sync audit. The spec was moved
into `completed/` without its Status field being updated, so a shipped feature read as unstarted.
Verified against the tree rather than inferred from the directory: `StartSpawn(session, role, task,
owner string, useWorktree bool)` and `createSpawnWorktree()` (`bus/spawn.go:112`, `:132`), the
`Worktree`/`WorktreeRef` entry fields (`:27-28`), `removeSpawnWorktree()` on completion detection
(`:300`), and the `--no-worktree` opt-out in `cmd/spawn.go:41`.

Phase steps below are recorded as plain bullets, predating the checkbox convention; they are left
as written rather than retroactively ticked, since no evidence was gathered per step.
