# Lazygit Diff Pane on the Commit Window

**Tracking:** [#42](https://github.com/mkober/muxcode/issues/42) · blocked by
[#41](https://github.com/mkober/muxcode/issues/41) ([MUX-117](./MUX-117-pane-targeting-by-identity.md))

The commit console lists changed files but cannot show what changed in one. Add a **lazygit** view
to the commit window's left column so the user can cycle through modified and untracked files and
read the diff of the selected one, without leaving the window or asking the commit agent to print a
diff into its pane.

> **Depends on [MUX-117](./MUX-117-pane-targeting-by-identity.md).** The dedicated pane cannot be
> added while pane targeting is index-based — see
> [the hazard](#the-hazard-that-governs-the-design-pane-indices-are-load-bearing).

## Context

### What the commit window looks like today

Three panes, and the left column is entirely console:

| Pane | Contents | Size (observed) |
|------|----------|-----------------|
| 0 | Commit console poller — `renderCommit()`, re-renders on an interval | 158×59 |
| 1 | The commit agent (git-manager) | 158×59 |
| 2 | Control pane — graph surfaces (MUX-108) | 317×18, full width |

`renderGitStatus()` (`bus/console.go:1257`) already lists **staged**, **modified**, and **untracked**
files, each capped at 10 with a `… +N more` overflow line. What it cannot do is show a diff: it is a
poller that re-renders a string on a timer, with no input handling and no selection.

### Why lazygit rather than a custom selection mechanism

The obvious build — a selection index, key bindings to cycle it, a marker file to share the
selection, and a driver that opens the right diff in nvim — reimplements a tool that already exists
and already handles the cases that make it fiddly:

| Case | Custom build | lazygit |
|------|-------------|---------|
| Untracked file has no diff base | Needs `git diff --no-index /dev/null <file>` special-casing | Shows new-file contents natively |
| File list changes under the cursor (staged mid-review, new file appears) | Selection by row index silently points at a different file — the [tui-style](../../tui-style.md) rule that selection must key on **id**, not row | Tracks its own list |
| Staged vs unstaged vs untracked in one list | Three `git` invocations to merge and de-duplicate | Native panels |
| Renames, mode changes, submodules | Each a separate special case | Handled |

The cost of lazygit is a **new external dependency** (see below), which is the thing to weigh.

### The hazard that governs the design: pane indices are load-bearing

**This is the constraint most likely to break the feature, and it is not obvious.**
`AgentPane()` (`bus/config.go:65`) is **hardcoded to return `"1"`** for every window, and
`control_pane.go:13` states the control pane "is ALWAYS created after panes 0 and 1". Everything
that reaches an agent — `PaneTarget()`, `Notify()`, `ClearAgent()`, `muxcode deliver`,
`prompt_inject.go:30` — resolves through that hardcoded `1`.

tmux assigns pane indices by position. **Splitting pane 0 inserts the new pane at index 1 and shifts
the agent to index 2**, which would simultaneously break every delivery path *and* collide with the
control pane's index. A closely-related version of this was already caught once: `rotate-window`
flips pane 1 from agent to nvim, which is why tmux pane rotation was deferred rather than shipped.

So the new pane must be created **without displacing index 1**, or `AgentPane()` must stop being a
constant. Phase 1 exists to settle that before any lazygit work happens.

### The second hazard: lazygit is a git *mutation* surface

lazygit can stage, unstage, commit, amend, discard hunks, push, and force-push. Putting it in a pane
puts a full git-write UI inside a session whose central rule is that **git mutations are
user-initiated**, enforced by `CheckCommitAuthority` gating commit requests to the edit agent alone.

A human driving lazygit by hand *is* user-initiated, so that is fine and is the point of the
feature. What is not fine is an agent reaching it. The daemon and several bus paths send keys to
panes by target string, and a pane running lazygit will execute whatever keystrokes arrive —
`c` opens a commit prompt, `P` pushes. **There is no permission hook between a `tmux send-keys` and
lazygit**; the authority checks live in the bus, which this path bypasses entirely.

The pane must therefore be excluded from every agent-facing send-keys path, and that exclusion needs
a test, not a convention.

### Dependency precedent worth heeding

MUX-109 promoted Ollama from optional to **required** because one feature needed a local model, then
demoted it again when the default backend changed and the required-tier prereq was demanding a
multi-GB download most installs never used. lazygit is much smaller, but the shape of the mistake is
the same: **do not make a whole-install prereq out of a single window's convenience.** lazygit is
absent from this machine and from `install.sh` today, so the degradation path is the common case at
first, not an edge case.

## Requirements

### Acceptance criteria

- [ ] The commit window shows a lazygit view in its left column, below the commit console
- [ ] The user can cycle through **modified** and **untracked** files and see the diff of the selected one
- [ ] Untracked files show their contents as a new-file diff rather than an empty pane
- [ ] The commit agent stays reachable at pane index 1 — `PaneTarget()`, `Notify()`, `deliver`, and `ClearAgent()` all still resolve to the agent after the new pane exists
- [ ] The control pane (MUX-108) still resolves to its own pane and its supervision sweep does not create a duplicate or adopt the lazygit pane
- [ ] **Negative control:** a test proves the agent pane target is unchanged by the new pane — a fix that quietly renumbers panes cannot pass
- [ ] No agent-facing send-keys path can reach the lazygit pane — pinned by a test, not by convention
- [ ] With lazygit **not installed**, the commit window launches normally and the pane states plainly that lazygit is missing, with the install hint — it never appears as an empty or broken pane
- [ ] lazygit is an **optional** prereq: `install.sh` reports it as missing-but-optional and the install succeeds without it
- [ ] The view is reachable and dismissible by a documented key, and the key does not collide with an existing tmux binding
- [ ] Launching and dismissing the view leaves the window layout as it found it — pane sizes and indices unchanged
- [ ] `scripts/test-commit-diff-pane.sh` passes

### Technical approach

**Decided: a dedicated pane, gated behind [MUX-117](./MUX-117-pane-targeting-by-identity.md)**
(user decision, 2026-08-28). The left column splits — console on top, lazygit below — so the diff is
glanceable rather than summoned.

| Shape | Trade | Outcome |
|-------|-------|---------|
| **Dedicated pane** in the left column | Persistent and glanceable, matches the request. Requires pane-index displacement to be solved first, and permanently costs vertical space | **Chosen** |
| On-demand overlay (`display-popup` / nvim float) | Sidesteps both hazards and costs no permanent space; mutation surface exists only while open. Not glanceable — the diff is invisible until asked for | Rejected: the point of the feature is seeing the diff while working, not summoning it |

The rejected option was the cheaper one, and choosing against it is what creates the prerequisite:
an overlay needs no new pane, so it would never have renumbered anything. **A dedicated pane makes
the pane-index hazard unavoidable, so it is being fixed properly rather than worked around** — pane
targeting moves from index-based to identity-based in MUX-117, which is a prerequisite for this
spec, not a parallel nice-to-have.

That ordering is deliberate: the alternative — adding the pane and patching whatever delivery
breaks — would spread index assumptions further instead of retiring them, and the failure mode is
silent (keystrokes land in the wrong pane; nothing errors).

**On "in nvim".** The request is for lazygit as the way to see changes *in nvim*. Two routes:

- `lazygit.nvim`-style plugin in a floating terminal, added through the existing user-extension hook
  (`config/nvim/init.lua:122` already loads `lua/user/plugins.lua` through lazy.nvim) — keeps it
  inside the editor the user is already in.
- Bare `lazygit` in the pane — simpler, no plugin, but it is not "in nvim" in any meaningful sense
  and loses editor integration (opening a file from the diff into a buffer).

The plugin route is the one that matches the request. Note the commit window's pane 0 currently runs
the **console**, not nvim — unlike the plan window where pane 0 *is* nvim — so "in nvim" on this
window means introducing an nvim instance that is not there today.

**Do not delete the commit console for this.** It renders workflow state, the agent's background
procs, recent branches, and last-commit info — none of which lazygit shows. They overlap only on the
file list. If space is tight, shrink the console's file list rather than removing the console.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/config.go` | `AgentPane()` — the hardcoded `"1"` this feature must not invalidate |
| `tools/muxcode/bus/control_pane.go` | Control-pane creation and supervision sweep; its index assumptions |
| `tools/muxcode/bus/console.go` | `renderCommit()` / `renderGitStatus()` — the console that stays |
| `tools/muxcode/bus/launch.go` | Window and pane construction at session launch |
| `config/nvim/init.lua` | lazy.nvim setup and the `lua/user/plugins.lua` extension hook |
| `config/tmux.conf` | Any new binding — see the quoting hazard below |
| `install.sh` | Optional-prereq check for lazygit |
| `scripts/test-commit-diff-pane.sh` | New — end-to-end pane, degradation, and index-stability checks |
| `docs/agents.md`, `docs/configuration.md` | Document the view, its key, and the optional prereq |

**tmux binding hazard.** A previous unquoted `bind }` was parsed as a command-block terminator, a
syntax error that aborted the rest of `tmux.conf` and silently killed 25 later directives. Quote any
non-letter key, and verify the **installed** `~/.config/muxcode/tmux.conf`, not just the repo copy —
the installed path is what live and new sessions load.

## Implementation

### Phase 0: Prerequisite — [MUX-117](./MUX-117-pane-targeting-by-identity.md)

**Unblocked 2026-08-31 — MUX-117 is complete (33/33).** Pane targeting now resolves by identity, so
this window can gain a fourth pane. Phase 1 may start.

- [x] MUX-117 complete: panes resolve by identity, `AgentPane()` is no longer a constant, and the shell hooks no longer hardcode indices — *each of the three conditions verified directly against the tree, not inherited from MUX-117's checkboxes: `AgentPane()` has zero non-test references (the surviving `grep` hits are `testModeVerifyAgentPanes`, a different identifier that merely contains the substring); the three shell hooks carry no pane-index literals; and `bus/pane.go` is committed and clean, not merely present in the working tree*

> **Note for Phase 1.** MUX-117's resolver is committed, but its integration test
> (`scripts/test-pane-targeting.sh`) is **not yet committed**. Phase 1's negative control below is
> the same shape as that script's — when writing it, extend the existing script rather than starting
> a second one.

### Phase 1: Add the pane

- [ ] Split the commit window's left column — console on top, lazygit below
- [ ] Tag the new pane with its role through MUX-117's mechanism
- [ ] Test: agent pane target unchanged with the new pane present (**negative control** — a renumbering fix must fail this)
- [ ] Test: control-pane sweep neither duplicates nor adopts the new pane
- [ ] Verify the console remains readable at its reduced height — it must degrade, not overflow

### Phase 2: lazygit view

- [ ] Add the lazygit view by the chosen shape
- [ ] Verify modified and untracked files both cycle, and untracked shows new-file contents
- [ ] Bind and document the open/dismiss key; verify no collision against the installed tmux config
- [ ] Verify layout is restored on dismiss — indices and sizes unchanged

### Phase 3: Degradation and prereq

- [ ] Missing lazygit renders an explicit "not installed" state with the install hint
- [ ] `install.sh` checks lazygit as **optional**; install succeeds without it
- [ ] Negative control: a run with lazygit absent still launches the commit window normally

### Phase 4: Mutation-surface containment

- [ ] Exclude the lazygit pane from every agent-facing send-keys path (`Notify`, `deliver`, `ClearAgent`, control-pane sweep, prompt injection)
- [ ] Test: each of those paths, given the commit window, resolves to the agent pane and never the lazygit pane
- [ ] Document that the view is human-only and why — it is a git-write surface with no permission hook in front of it

### Phase 5: Integration test

- [ ] Create `scripts/test-commit-diff-pane.sh` — hermetic: scratch tmux session, scratch repo fixture
- [ ] Fixture repo with a modified file and an untracked file; both appear and both show a diff
- [ ] Agent pane target resolves correctly with the view open **and** closed
- [ ] Control pane still converges to exactly one pane (the MUX-108 dedupe case)
- [ ] lazygit-absent path renders the explicit missing state (**negative control**)
- [ ] Send-keys containment: an agent-facing delivery reaches the agent, not the lazygit pane
- [ ] Coverage floor so a skipped run cannot report green
- [ ] Run the script and confirm all checks pass

## Open questions

| Question | Why it matters |
|----------|----------------|
| Should **staged** files be cyclable too, or only modified + untracked? | The annotation names modified and untracked; the console also lists staged. lazygit shows all three natively, so excluding staged would mean configuring it *against* its default |
| Read-only lazygit, or full capability? | A restricted config would shrink the mutation surface, at the cost of the staging workflow that makes lazygit worth having |

## Status

Backlog
