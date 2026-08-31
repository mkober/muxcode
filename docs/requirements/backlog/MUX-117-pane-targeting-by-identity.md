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

This table was the first pass and is **superseded** — it missed live production sites in `spawn.go`,
`mode.go`, `launcher.go`, and `daemon/daemon.go`. The authoritative, verified enumeration is
[Phase 1 findings](#pane-addressing-site-enumeration-verified-against-the-tree-2026-08-31) below;
use that one. Kept here only so the shape of the original claim stays readable.

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

### Phase 1 findings

#### Empirical tmux index behaviour (tmux 3.6a, clean config, pane-base-index 0)

Measured on an isolated socket with the repo's launch layout (left=idx0, agent=idx1), 2026-08-31.
**Independently reproduced by the plan agent on a second isolated socket the same day** — every row
below was observed twice, by two separately-constructed probes:

| Operation | index<->pane binding | pane id (%N) | @muxcode_pane tag |
|-----------|----------------------|--------------|-------------------|
| `split-window -h` (launch convention) | new pane appended at next index; existing unchanged | stable | new pane untagged (empty) |
| `split-window -hb` before the agent | **agent renumbered 1 -> 2** — the failure this spec exists for | stable | follows pane |
| `kill-pane` | surviving indices compact/heal | stable | follows pane |
| `select-layout` (main-vertical, even-horizontal) | **preserved** — geometry changes only | stable | follows pane |
| `rotate-window` | **broken** — pane ids move across indices | stable | follows pane |
| `swap-pane` | **broken** — same as rotate | stable | follows pane |
| `respawn-pane -k` | unchanged | **same id kept** | **survives** |

Reproduction detail for the two rows the design leans on hardest: inserting a pane before the agent
moved it from index 1 to index 2 while pane id `%1` retained its `agent` tag; `select-layout` left
the index->id mapping at `0:%2 1:%0 2:%1`, and `rotate-window` then changed it to `0:%0 1:%1 2:%2`.

- Confirms `control_pane.go`'s recorded distinction: `select-layout` is safe, `rotate-window` is not.
  The earlier false claim that rotate-window preserves indices is now empirically refuted.
- Pane ids are stable for the pane's lifetime and directly addressable
  (`send-keys -t %N`, `display-message -p -t %N '#{@muxcode_pane}'`).
- An unset pane user option renders as the empty string in formats — clean untagged detection.

#### Decision: tag mechanism = per-pane user option `@muxcode_pane`

Chosen over a `BusDir()` registry keyed by pane id, because:

- The tag rides the pane entity itself — verified surviving rotate-window, swap-pane,
  insert-before, and `respawn-pane -k`. It cannot dangle: state lives where the panes live,
  so there is no registry<->tmux reconciliation problem (a registry row keyed by `%N` goes
  stale the moment a pane is killed and recreated with a new id).
- Survives daemon restarts for free — the tmux server holds it.
- Readable from the shell hooks with the same one-liner as Go
  (`tmux list-panes -t <win> -F '#{pane_id} #{@muxcode_pane}'` + match), which a JSON
  registry under `BusDir()` would not give shell without a parser.
- Complements, not replaces, control-pane start-command matching: retroactive identification
  of old-binary panes (the MUX-108 property) still needs the command-prefix scan; the sweep
  can then stamp `@muxcode_pane` on panes it identifies (self-healing migration).

> **Open, and not closed by either probe:** both measurements ran on **tmux 3.6a**. `set-option -p`
> is documented since the repo's declared 3.0 floor, but neither run verified it there. Phase 2 must
> either probe the version at runtime or verify on 3.0 before relying on the mechanism — a second
> measurement on the same version is not evidence about a different one.

Canonical tag values: `agent`, `left`, `control` (Phase 2 may extend, e.g. `lazygit` for MUX-116).

#### Fallback semantics: untagged legacy vs resolution failure

Resolution for `(window, role-pane)` is a three-way outcome, decided mechanically by how many
panes on the window carry any `@muxcode_pane` tag:

1. **Tag match** — a pane on the window carries the requested tag: resolve to its pane id. Normal path.
2. **Untagged window (legacy fallback)** — zero panes on the window carry any tag: the window was
   created by an older binary. Fall back to today's index convention (`.0`/`.1`) and log a
   `pane-fallback` lifecycle event, once per window per session (not per message — no log flood).
   Only old binaries produce this state, so the creation-order contract is a safe assumption there.
3. **Resolution failure (loud)** — the window has at least one tagged pane but the requested tag is
   absent, or two panes claim the same tag: return an error. Never fall back to an index in a tagged
   session — the index may host an editor or a git TUI, and silent misdelivery is the exact failure
   this spec removes. Duplicate-tag convergence is the control-pane sweep's job, not the resolver's.

The two non-normal outcomes must not share a code path: (2) is expected compatibility, (3) is a
broken contract.

#### Pane-addressing site enumeration (verified against the tree, 2026-08-31)

Class A — resolves through `PaneTarget()`/`AgentPane()` (fine as written; fixed wholesale by the
resolver): 39 non-test call sites across `bus/` (clear, deliver, diagnose, health, notify,
prompt_inject via `AgentPane`, providers claude/codex/opencode, reload, remote, timetrack),
`cmd/` (compact, simulate), `daemon/daemon.go` (10 sites), `tui/` (model.go via the
`tui/agents.go` wrapper).

Class B — hand-built pane targets in non-test Go (the actual work):

| Location | Literal | Purpose |
|----------|---------|---------|
| `bus/launcher.go:301` | `session + ":edit.1"` | select agent pane at launch |
| `bus/launcher.go:341-343` | `target+".1"`, `target+".0"` | edit window launch: init, agent launch, left select |
| `bus/launcher.go:349-351` | `target+".1"`, `target+".0"` | plan window launch |
| `bus/launcher.go:372-374` | `target+".1"` | serve window launch |
| `bus/launcher.go:379-381` | `target+".1"` | remaining windows launch |
| `bus/launcher.go:394` | `target+".0"` | left-pane title |
| `bus/launcher.go:688` | `session+":"+win+".1"` | startup acceptance capture loop |
| `bus/reload.go:67` | `HoldWindow+".1"` | mode-cycled hold window |
| `bus/reload.go:478` | `window+".0"` | left pane during reload |
| `bus/hook.go:1405` | `session+":edit.0"` | preview hook -> editor pane |
| `bus/prompt_inject.go:30` | inline `PaneTarget()` re-implementation | prompt injection |
| `bus/mode.go:332` | `HoldWindow+".0"` | mode-cycle left pane |
| `bus/mode.go:343` | `HoldWindow+".1"` | mode-cycle agent pane |
| `bus/mode.go:362` | `session+":edit.0"` | `pane_current_path` query |
| `bus/mode.go:447` | `HoldWindow+".1"` | mode-cycle target |
| `bus/spawn.go:183-184` | `spawnRole+".0"` | spawn console pane |
| `bus/spawn.go:195` | `spawnRole+".1"` | spawn agent pane |
| `bus/uitest_mode.go:115,121,145,149` | `":edit.1"`, `":auto.1"` | uitest capture (test infra living in bus/) |
| `daemon/daemon.go:2910` | `session+":edit.0"` | `showEditInNeovim` (non-hook edit review) |
| `bus/control_pane.go:102` | `target+".2"` | deliberate legacy-compat dedupe match — keep, with comment |

Class C — shell hooks (unreachable by any Go refactor):

| Location | Literal |
|----------|---------|
| `scripts/muxcode-compact.sh:15` | `${session}:${role}.1` |
| `scripts/muxcode-diff-cleanup.sh:14` | `$SESSION:edit.0` |
| `scripts/muxcode-preview-hook.sh:24` | `$SESSION:edit.0` |

> **Line numbers are a snapshot; the expressions are authoritative.** They already drifted once —
> unrelated MUX-120 work in `spawn.go` shifted its three sites by two lines within a day of this
> table being written. Locate sites by the literal expression, and treat a number that no longer
> matches as drift rather than as a site that disappeared.

No `fmt.Sprintf`-style pane construction exists (grep for `:%s.`/`.%d` variants came back clean).
`scripts/test-*.sh` fixtures build their own scratch sessions and are out of scope per the Phase 3
grep gate ("outside tests").

**How this list was closed to the standard the Phase 1 step demands.** A pattern count was explicitly
not sufficient, so three independent methods were made to agree: two separately-constructed literal
sweeps converged on the same 31 index literals; `prompt_inject.go:30`, which no literal pattern can
find because it builds its target from `AgentPane(window)`, is included; and a call-site trace of all
98 target-taking tmux invocations in non-test Go accounts for every one as Class A (42
`PaneTarget`/`AgentPane` references), Class B, or a session/window-scoped target
(`has-session`, `list-windows`, `select-window`, `set-hook`) that addresses no pane at all.

### Key files

| File | Change |
|------|--------|
| `tools/muxcode/bus/config.go` | `AgentPane()`, `PaneTarget()` — the resolver |
| `tools/muxcode/bus/launcher.go` | Tag panes at creation; remove `.0`/`.1` literals |
| `tools/muxcode/bus/control_pane.go` | Reconcile start-command identification with the new tagging |
| `tools/muxcode/bus/reload.go` | Hold-window and left-pane literals |
| `tools/muxcode/bus/hook.go` | `edit.0` literal |
| `tools/muxcode/bus/prompt_inject.go` | Inline re-implementation of `PaneTarget()` |
| `tools/muxcode/bus/spawn.go` | Spawn worker console/agent pane literals |
| `tools/muxcode/bus/mode.go` | Hold-window and `edit.0` literals across mode cycling |
| `tools/muxcode/bus/uitest_mode.go` | Test-scope `:edit.1` / `:auto.1` captures — update in lockstep |
| `scripts/muxcode-compact.sh`, `muxcode-diff-cleanup.sh`, `muxcode-preview-hook.sh` | Shell-side resolution |
| `scripts/test-pane-targeting.sh` | New — identity, insertion, and fallback checks |
| `docs/architecture.md` | Document the pane identity contract, replacing the creation-order one |

## Implementation

### Phase 1: Establish the contract

- [x] Empirically determine tmux index behaviour on split, kill, and `select-layout` for this repo's layouts — record results rather than reasoning from memory — *measured on an isolated socket and independently re-measured by a second probe; both runs on tmux 3.6a, so the 3.0-floor question stays open and is recorded as such in the findings*
- [x] Choose the tag mechanism (pane user option vs pane-id registry) and record the reasoning — *`@muxcode_pane` per-pane user option; the survival properties the choice rests on (tag follows the pane across renumbering, survives `respawn-pane -k`, untagged reads empty) were each observed directly*
- [x] Define fallback semantics for untagged panes, distinct from resolution failure — *three-way outcome: tag match / untagged-window legacy fallback with a once-per-window `pane-fallback` event / loud error, with the requirement that the two non-normal outcomes never share a code path*
- [x] Enumerate every pane-addressing site (Go and shell) and record the list in this spec — closed
  against a resolution-based check, not a pattern count. This bar was set because the first sweep was
  **grep-derived and provably incomplete**: it missed `prompt_inject.go:30`, which builds its target as
  `... + AgentPane(window)` and matches no literal `.0`/`.1` pattern. It is met by three agreeing
  methods — two independently-constructed literal sweeps converging on the same 31 sites, explicit
  inclusion of the non-literal site, and a trace of all 98 target-taking tmux calls in non-test Go
  showing every one is Class A, Class B, or session/window-scoped and addresses no pane

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

In Progress — Phase 1 complete (contract established); Phases 2-5 open. Spec file still lives in
`backlog/`; moving it to `drafts/` is a separate user-decided step.
