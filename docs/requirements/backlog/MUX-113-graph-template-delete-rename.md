# Graph Template Delete and Rename

The graph command surface can create a template and export one, but never remove or move one. Every
supersede operation therefore leaves the old definition resolvable — and because resolution is
`project > user > builtin`, the superseded name can keep **winning** over its replacement.

Found by the prompt-agent hitting the wall live (2026-08-27) while doing exactly the work the
[Prompt surface](../completed/MUX-109-prompt-mode-graph-control-pane.md) exists for.

## Context

### What happened

Asked to rename a template, the agent did the only thing the surface allows — created the new name
and left the old one — then reported the shortfall precisely:

> **Partially succeeded.** … Could not move it to builtin: `muxcode graph create --scope builtin` is
> rejected — `--scope` only accepts `project|user`. … The original `review-spec-docs` project
> template still exists — **there is no delete/rename command in the graph surface**, so it was not
> removed.

Resulting state, confirmed with `graph list`:

```
review-spec-docs     project  Verify requirements-spec alignment, update spec/architecture docs …
update-spec-docs     user     Verify requirements-spec alignment, update spec/architecture docs …
```

Two entries, identical descriptions, different tiers — and the **superseded** one at the higher
precedence. A user or agent naming `review-spec-docs` still gets the old definition.

**This particular duplicate is already gone** — the rename was redone in code (the builtin is now
`update-spec-docs`) and both file-tier copies were deleted by hand. That resolves the incident and
leaves the gap exactly where it was: the fix required editing Go source and deleting files manually,
which is the thing a CLI surface is supposed to make unnecessary.

### Why the surface is asymmetric

| Operation | Exists | Notes |
|-----------|--------|-------|
| Create | `graph create --json\|<file> [--scope project\|user]` | Validates before writing |
| Read | `graph export <template>` | Prints resolved JSON — the modify-and-shadow path |
| List | `graph list` | Shows the resolved set with tiers |
| **Delete** | **—** | No way to remove a project or user template |
| **Rename / move** | **—** | Achievable only as create-new + orphan-old |

`--scope builtin` being rejected is **correct** — builtins are compiled into `graph_templates.go`
and a CLI write there would be meaningless. The gap is that the two writable tiers have a write
but no unwrite.

### Why it matters beyond tidiness

- **Shadowing makes the stale copy authoritative.** This is not a leftover file in a directory
  nobody reads; project tier wins resolution, so the superseded definition is what actually runs.
- **The Prompt surface's create intent is the main producer of templates.** A capability whose
  natural workflow accumulates unremovable duplicates will accumulate them steadily.
- **Agents cannot clean up after themselves.** The `prompt` tool profile grants no `Write`/`Edit`
  deliberately — graph writes go only through the validating CLI. With no `graph delete`, a
  correctly-sandboxed agent has *no* path to remove what it created, so every mistake is permanent
  until a human deletes the file by hand.

## Requirements

### Acceptance criteria

- [ ] `muxcode graph delete <name> [--scope project|user]` removes a writable template
- [ ] Deleting a **builtin** is refused with a message explaining they are compiled in, not files
- [ ] Deleting a name that does not exist at the target scope fails clearly rather than silently succeeding
- [ ] With no `--scope`, the command resolves the same way `graph list` does and **reports which tier it will remove from before acting** — never guesses between a project and user copy of the same name
- [ ] `muxcode graph rename <old> <new>` moves within a scope: the new name resolves, **the old name no longer does**
- [ ] Rename validates the definition under its new name before removing the old — a failed rename leaves the original intact
- [ ] Renaming onto an existing name is refused unless explicitly forced
- [ ] The `prompt` tool profile reaches both commands, so an agent can undo its own create
- [ ] **Negative control:** a test proves delete refuses a builtin and refuses a missing name — a command that removed nothing would otherwise pass the positive cases
- [ ] `scripts/test-graph-template-lifecycle.sh` passes

### Technical approach

- **Mirror `WriteGraphDefinition`'s shape.** It already resolves a scope directory, guards unsafe
  names, and writes atomically (`bus/graph.go`). Delete is the same resolution with `os.Remove`;
  the name guard matters equally — a name escaping its directory must not be deletable either.
- **Rename is copy-validate-remove, in that order.** Write the new name through the existing
  validating path, confirm it resolves, and only then remove the old. The reverse order turns a
  validation failure into data loss.
- **Say which tier.** The most likely user error is deleting the project copy while a user copy
  keeps resolving, or vice versa. Printing the resolved path before acting costs one line and
  prevents the confusion this spec was filed about.
- **Do not add a builtin scope.** Refusing it with a good message is the correct behaviour; the
  prompt-agent already discovered and reported that boundary accurately.

### Key files

| File | Change |
|------|--------|
| `bus/graph.go` | `DeleteGraphDefinition()`, `RenameGraphDefinition()` beside `WriteGraphDefinition()` |
| `cmd/graph.go` | `delete` and `rename` subcommands, usage text |
| `bus/profile.go` | Confirm the `prompt` profile's `muxcode graph *` grant covers both |
| `bus/graph_test.go` | Scope resolution, builtin refusal, missing-name refusal, failed-rename-leaves-original |
| `scripts/test-graph-template-lifecycle.sh` | New — create → export → rename → delete end to end |
| `docs/agent-bus.md` | Document both under `muxcode graph` |

## Implementation

### Phase 1: Delete

- [ ] `DeleteGraphDefinition(name, scope)` — resolve scope dir, apply the same unsafe-name guard as the write path, remove
- [ ] `graph delete` subcommand; report the resolved path before removing
- [ ] Refuse builtins with an explanatory message
- [ ] Tests: removes project copy, removes user copy, refuses builtin, refuses missing name

### Phase 2: Rename

- [ ] `RenameGraphDefinition(old, new, scope)` — write-validate-then-remove ordering
- [ ] `graph rename` subcommand; refuse an existing target unless forced
- [ ] Test: **a rename whose validation fails leaves the original intact** (the ordering guarantee)
- [ ] Test: after rename the old name no longer resolves

### Phase 3: Agent reachability

- [ ] Confirm the `prompt` profile reaches both commands
- [ ] Confirm the agent definition's command table lists them, so the model knows they exist

### Phase 4: Integration test

- [ ] Create `scripts/test-graph-template-lifecycle.sh` — hermetic: scratch project and user graph dirs
- [ ] create → export → modify → create-shadow → rename → delete, asserting resolution tier at each step
- [ ] Assert a deleted project template stops shadowing its builtin (the reverse of the shadowing bug)
- [ ] **Negative controls:** builtin delete refused, missing-name delete refused, failed rename non-destructive
- [ ] Coverage floor so a skipped run cannot report green
- [ ] Run the script and confirm all checks pass

## Status

Draft
