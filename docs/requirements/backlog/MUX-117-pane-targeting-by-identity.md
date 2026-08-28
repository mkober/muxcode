# Resolve Panes by Identity, Not by Index

**Tracking:** [#41](https://github.com/mkober/muxcode/issues/41) · blocks
[#42](https://github.com/mkober/muxcode/issues/42) ([MUX-116](./MUX-116-commit-window-lazygit-diff-pane.md))

Every path that reaches an agent resolves through a **hardcoded pane index**. `AgentPane()` returns
the literal `"1"` for every window, and a second set of call sites bypasses even that helper and
builds `session:window.0` / `.1` target strings by hand. Any change to a window's pane layout
therefore breaks delivery silently, which is why no feature has been allowed to add a pane.

Prerequisite for [MUX-116](./MUX-116-commit-window-lazygit-diff-pane.md), which adds a dedicated
lazygit pane to the commit window and cannot be built safely until this lands.

## Context

### The current contract, and why it is fragile

```go
// bus/config.go:62
// AgentPane returns the tmux pane number where the agent runs for a window.
// LaunchSession() always splits horizontally and launches the agent in pane 1
// (the right pane) for all windows, so this always returns "1".
func AgentPane(window string) string {
	return "1"
}
```

The comment is honest: the function is a constant justified by a launch-time convention. The
convention is real, but nothing enforces it, and tmux assigns pane indices **by position** — so any
split that lands before the agent renumbers it.

`control_pane.go:13` states the consequence in the code itself:

> It is ALWAYS created after panes 0 and 1 … creation order is the delivery contract — and a slip
> here breaks every agent's delivery at once, with messages **typing into an nvim buffer rather than
> crashing**.

**That failure mode is the reason this matters.** A wrong pane target does not error. It delivers
keystrokes to whatever is sitting at that index — an editor, a console poller, or a git TUI where
`c` opens a commit prompt and `P` pushes. The system reports success while typing into the wrong
window.

### Two classes of call site, and only one of them is the problem

| Class | Count | Status |
|-------|-------|--------|
| Resolves through `PaneTarget()` / `AgentPane()` | ~40 across `bus/`, `cmd/`, `daemon/`, `tui/` | **Fine as written** — these are the payoff of having the indirection; fixing the helper fixes all of them at once |
| Builds a target string by hand, bypassing the helper | See table below | **The actual work** |

Bypass sites found in the tree:

| Location | Literal | Note |
|----------|---------|------|
| `bus/launcher.go:301` | `session + ":edit.1"` | Select agent pane at launch |
| `bus/launcher.go:341`–`:373` | `target + ".1"`, `target + ".0"` | Per-window launch: agent send, left-pane select |
| `bus/reload.go:67` | `session + ":" + HoldWindow + ".1"` | Mode-cycled hold window |
| `bus/reload.go:478` | `session + ":" + window + ".0"` | Left pane |
| `bus/hook.go:1405` | `session + ":edit.0"` | Hook targeting the editor pane |
| `bus/prompt_inject.go:30` | `session + ":" + window + "." + AgentPane(window)` | Re-implements `PaneTarget()` inline |
| `scripts/muxcode-compact.sh:15` | `${session}:${role}.1` | Shell, outside the Go helper entirely |
| `scripts/muxcode-diff-cleanup.sh:14` | `$SESSION:edit.0` | Shell |
| `scripts/muxcode-preview-hook.sh:24` | `$SESSION:edit.0` | Shell |

The shell hooks matter as much as the Go: they are not reachable by any refactor of `AgentPane()`,
so a Go-only fix would leave three live paths still index-addressed and produce a false sense that
the problem is solved.

### The pattern already exists in this repo

**The control pane solved this problem correctly and can be generalized.** It does not trust an
index — it identifies its pane by matching the tmux start command:

```go
// controlPaneBaseCmd is the identity of a control pane: every surface
// variant starts with it, and scanControlPanes matches panes by their
// tmux start command against this prefix.
const controlPaneBaseCmd = "muxcode graph ui"
```

That is identity-based resolution, and it is why the control pane survives being killed and
respawned, and why a duplicate can be detected and converged. The gap is that it works only for
panes running a recognisable `muxcode` command. An agent pane runs `claude`, `opencode` or `codex`;
a left pane runs `nvim` or a console poller; a future lazygit pane runs `lazygit`. Start-command
matching gets fragile fast across that set.

**tmux offers something better: per-pane user options.** A pane can be tagged at creation
(`tmux set-option -p -t <pane> @muxcode_pane role`) and resolved by that tag afterwards. Unlike an
index it survives splits and reordering; unlike a start command it does not depend on what the pane
happens to be running. tmux pane **ids** (`%N`) are similarly stable and could back a registry.

### Related history

- `rotate-window` flips pane 1 from agent to nvim. Pane rotation was **deferred rather than
  shipped** specifically because of this coupling — a binding that would have been two lines was
  dropped because it would silently redirect every agent's delivery. `select-layout` is safe because
  it preserves indices; that distinction is recorded in `control_pane.go`.
- An earlier session recorded a false claim that both `select-layout` *and* `rotate-window` preserve
  indices, and it was copied into a code comment before being caught. Verify index behaviour
  empirically in Phase 1 rather than reasoning about it.

## Requirements

### Acceptance criteria

- [ ] A pane's role is resolved by **identity**, not by position — adding, removing, or reordering panes on a window does not change which pane an agent message reaches
- [ ] `AgentPane()` no longer returns a constant, or is removed in favour of an identity-based resolver
- [ ] Every hand-built pane target listed above resolves through the shared helper — no Go call site constructs `session:window.N` by hand
- [ ] The three shell hooks resolve panes through the same mechanism rather than hardcoded literals
- [ ] Resolution has an explicit, logged fallback when a pane carries no identity — sessions launched by an older binary must keep working
- [ ] **A failed resolution fails loudly** — it never silently falls back to an index that might host an editor or a git TUI
- [ ] **Negative control:** a test inserts a pane *before* the agent and proves delivery still reaches the agent — a fix that merely re-hardcodes a different index cannot pass
- [ ] Control-pane identification and its dedupe sweep still work, including for panes created by an older binary (retroactive identification is a property MUX-108 deliberately has)
- [ ] Mode-cycled windows (plan/research, edit/auto) still resolve the *active* agent correctly
- [ ] `scripts/test-pane-targeting.sh` passes

### Technical approach

**Tag at creation, resolve by tag, fall back deliberately.** The launch path already creates every
pane, so it is the natural place to stamp identity. A resolver then maps `(window, role)` to a pane
by tag, with a recorded fallback for untagged panes.

Points to settle:

- **Tag mechanism** — per-pane user option (`@muxcode_pane`) vs a registry keyed by tmux pane id
  (`%N`). The user option keeps state in tmux where the panes are; a registry keeps it where the rest
  of muxcode's state already lives, under `BusDir()`. The user option is likely simpler and survives
  daemon restarts for free.
- **Fallback semantics** — a session launched by an older binary has untagged panes. Falling back to
  today's index convention keeps those sessions working, but the fallback must be *logged* and must
  not mask a genuine resolution failure in a tagged session. These are different situations and
  should not share a code path.
- **Scope discipline** — this is a refactor of an existing contract, not a feature. It should change
  *how* a pane is found and nothing about what is delivered. Resist folding pane rotation, dynamic
  layouts, or the MUX-116 pane into this spec; they are what it unblocks, not what it is.

**Do not skip the shell hooks.** They are three lines each and are the most likely thing to be
declared out of scope and then forgotten, leaving the invariant half-true.

### Key files

| File | Change |
|------|--------|
| `tools/muxcode/bus/config.go` | `AgentPane()`, `PaneTarget()` — the resolver |
| `tools/muxcode/bus/launcher.go` | Tag panes at creation; remove `.0`/`.1` literals |
| `tools/muxcode/bus/control_pane.go` | Reconcile start-command identification with the new tagging |
| `tools/muxcode/bus/reload.go` | Hold-window and left-pane literals |
| `tools/muxcode/bus/hook.go` | `edit.0` literal |
| `tools/muxcode/bus/prompt_inject.go` | Inline re-implementation of `PaneTarget()` |
| `scripts/muxcode-compact.sh`, `muxcode-diff-cleanup.sh`, `muxcode-preview-hook.sh` | Shell-side resolution |
| `scripts/test-pane-targeting.sh` | New — identity, insertion, and fallback checks |
| `docs/architecture.md` | Document the pane identity contract, replacing the creation-order one |

## Implementation

### Phase 1: Establish the contract

- [ ] Empirically determine tmux index behaviour on split, kill, and `select-layout` for this repo's layouts — record results rather than reasoning from memory
- [ ] Choose the tag mechanism (pane user option vs pane-id registry) and record the reasoning
- [ ] Define fallback semantics for untagged panes, distinct from resolution failure
- [ ] Enumerate every pane-addressing site (Go and shell) and record the list in this spec

### Phase 2: Resolver and tagging

- [ ] Tag panes with their role at creation in the launch path
- [ ] Implement identity-based resolution behind `PaneTarget()`
- [ ] Logged fallback for untagged panes; loud failure for unresolvable ones in tagged sessions
- [ ] Unit tests: resolution by tag, fallback path, failure path — each pinned separately

### Phase 3: Retire the bypass sites

- [ ] Convert every hand-built Go target to the helper
- [ ] Convert the three shell hooks
- [ ] Grep gate: no `\.[012]"` pane literal remains outside tests and the resolver itself

### Phase 4: Preserve existing guarantees

- [ ] Control-pane identification and dedupe still work, including retroactively for old-binary panes
- [ ] Mode-cycled windows resolve the active agent
- [ ] Delivery-ack, notify, deliver, clear, compact, and diagnose all still reach agents

### Phase 5: Integration test

- [ ] Create `scripts/test-pane-targeting.sh` — hermetic: isolated tmux socket, scratch session
- [ ] Delivery reaches the agent on a normal three-pane window
- [ ] **Insert a pane before the agent; delivery still reaches the agent** (the negative control — an index-based fix fails here)
- [ ] Kill and respawn the control pane; it is re-identified and not duplicated
- [ ] An untagged (old-binary-style) session still resolves, and logs the fallback
- [ ] An unresolvable pane fails loudly rather than defaulting to an index
- [ ] Coverage floor so a skipped run cannot report green
- [ ] Run the script and confirm all checks pass

## Status

Backlog
