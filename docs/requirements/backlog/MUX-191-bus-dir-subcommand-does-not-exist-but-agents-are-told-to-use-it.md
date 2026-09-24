# MUX-191: `muxcode bus-dir` Does Not Exist, but Agent Definitions Tell Agents to Use It

Two agent definitions instruct their agents to resolve the bus directory with `muxcode bus-dir`.
There is no such subcommand: `bus-dir` is absent from `knownSubcommands` (`main.go:24-30`), so
`routeFor` (`main.go:142-154`) treats it as a project name — the launcher road, or the near-miss
road if it resembles a real subcommand — never as a command that prints a path. Run from
`tools/muxcode` it launched a session instead of answering. `BUS_DIR` in those scripts is then empty
and the serve-state lookup silently does nothing.

## Context

### Source and standard of evidence

Verified by plan on 2026-09-24: no `bus-dir` case in `main.go` or `cmd/`; the two references below
are the only ones in the repo. The launch-instead-of-answer behaviour is from the session notes and
was not re-run (running it would launch a session). Filed on the user's instruction relayed by edit.

### Where it is referenced

| File | Text |
|---|---|
| `agents/log-watcher.md:77-79` | "resolved via `muxcode bus-dir`" … `BUS_DIR="${BUS_DIR:-$(muxcode bus-dir 2>/dev/null)}"` |
| `agents/dev-server.md:37,93` | `BUS_DIR="${BUS_SESSION:+$(muxcode bus-dir)}"`; "determined by `muxcode bus-dir` (typically `~/Library/Caches/muxcode/muxcode-bus-{session}/serve-state.json` on macOS)" |

The `dev-server.md` prose also names a path that contradicts the code: `BusDir()` in `bus/config.go`
is `/tmp/muxcode-bus-{session}/` (CLAUDE.md agrees). Two errors in one paragraph.

### Mechanism

| Step | Code | Behaviour |
|---|---|---|
| Not a subcommand | `knownSubcommands` map (`main.go:24-30`) has no `bus-dir` | `routeFor` falls past `routeSubcommand` |
| Not a flag | `main.go:145` | not `routeUsage` |
| Near-miss or launcher | `main.go:148-154`: `nearestSubcommand("bus-dir")` may name a close subcommand → `routeNearMiss`; otherwise `routeLauncher` with `bus-dir` as the project path | Either an error message or a session launch; never a path on stdout |
| Callers swallow it | `2>/dev/null` and `${BUS_SESSION:+…}` | `BUS_DIR` is empty; the serve-state read fails silently |

### Blast radius

- The watch agent's dev-server URL discovery and the serve agent's state file both depend on it;
  both degrade silently.
- A watch agent that runs it without `2>/dev/null` from a repo path could launch a nested session.
- Low severity; a documentation-vs-binary contract broken at the cheapest layer to fix.

## Requirements

### Acceptance criteria

- [ ] `muxcode bus-dir` exists: prints `BusDir()` for `BUS_SESSION` (or `--session <name>`), exit 0; with no session resolvable, a one-line error and exit 1 — `TestRouteFor` covers it as a known subcommand, unit test for both outcomes
- [ ] `agents/log-watcher.md` and `agents/dev-server.md` call it correctly, and `dev-server.md`'s stated path matches `BusDir()`
- [ ] `docs/agent-bus.md` documents the subcommand
- [ ] **Negative control:** `muxcode bus-dir` from `tools/muxcode` with `BUS_SESSION` unset exits 1 and launches nothing
- [ ] `bash scripts/test-bus-dir.sh` passes

### Technical approach

Add `bus-dir` to `knownSubcommands` and a `cmd/busdir.go` handler that prints `bus.BusDir(session)`.
Alternatively, change the two definitions to use `/tmp/muxcode-bus-$BUS_SESSION` directly and drop
the promise — smaller, but every future definition would repeat the path. The subcommand is the
better contract.

### Key files

| File | Role |
|---|---|
| `tools/muxcode/main.go:24-30,140-155` | subcommand registry and routing |
| `tools/muxcode/bus/config.go` (`BusDir`) | the path |
| `tools/muxcode/cmd/busdir.go` | new |
| `agents/log-watcher.md:77-79`, `agents/dev-server.md:37,93` | callers |
| `docs/agent-bus.md` | CLI reference |
| `scripts/test-bus-dir.sh` | new |

## Implementation

### Phase 1: Subcommand

- [ ] `bus-dir` handler, registry entry, `TestRouteFor` case, unit tests for session-set and session-unset
- [ ] `docs/agent-bus.md` entry

### Phase 2: Definitions

- [ ] Fix both definitions' invocation and `dev-server.md`'s path prose; grep the repo for any other `bus-dir` mention

### Phase 3: Integration test

- [ ] Create `scripts/test-bus-dir.sh` — hermetic
- [ ] Test: `BUS_SESSION=x muxcode bus-dir` prints `/tmp/muxcode-bus-x`
- [ ] **Negative control:** unset session → exit 1, no tmux session created (assert `tmux ls` unchanged)
- [ ] Coverage floor; run and record counts here

## Out of scope

- Changing `BusDir()`'s location.

## Status

Backlog

Filed 2026-09-24 on the user's instruction relayed by edit, from the session notes; absence of the
subcommand and the two references verified the same day. Not started.
