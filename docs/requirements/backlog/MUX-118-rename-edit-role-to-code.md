# Rename the F2 `edit` Role to `code`, and "editor" to "coder"

Rename the F2 window from **Edit** to **Code**, and the "editor" vocabulary to "coder" across the
codebase.

The rename is small to describe and large to execute, for one reason: **the F2 window name is not a
label, it is the role identity.** `BusRole()` (`bus/config.go:93`) falls back to the tmux window name
`#W` when `AGENT_ROLE`/`BUS_ROLE` are unset, so the string `edit` is simultaneously the display name,
the bus address, the inbox filename, the authority key, and the hard-exclusion key. Changing it
changes all of them at once.

Tracking: _(no GitHub issue yet)_

## Context

### Why this is not a find-and-replace

`edit` and `editor` carry **three unrelated meanings** in this tree, and only the first should be
renamed. This is the single most important fact in this spec.

| Meaning | Examples | Rename? |
|---------|----------|---------|
| **1. The agent role** — the F2 orchestrator | `KnownRoles`, `commitAuthorityDefault`, `autoClearExcluded`, `MUXCODE_EDIT_CLI`, `agents/code-editor.md` | ✅ **Yes** |
| **2. The Edit *tool*** — Claude Code's `Write`/`Edit`/`NotebookEdit` | `CheckEditGuard`, `checkNonHookEdits`, `executeEdit`, the `Write\|Edit\|NotebookEdit` hook matcher | ❌ **Never** — these name a Claude Code tool this project does not own |
| **3. A text editor** — Neovim in the left pane, TUI line editing | `MUXCODE_EDITOR` (*default: nvim*), `sendEditorCommand`, `sendPlanEditorCommand`, `editLine`, `editLineAt`, `StateEditing` | ❌ **Never** — `sendCoderCommand` for launching nvim would be actively misleading |

Meaning 3 is the trap in the request as phrased. "Rename editor to coder throughout" reads
naturally, but **most `editor` occurrences in the Go code mean Neovim**, not the agent:
`launcher.go:23` declares `Editor string // MUXCODE_EDITOR (default: nvim)`. Renaming those would
produce a config variable called `MUXCODE_CODER` whose value is `nvim`.

### Measured blast radius

Counts taken 2026-08-28 on `MUX-114-close-spec-completion-check`:

| Pattern | `.go` | `.md` | `.sh` |
|---------|------|------|------|
| whole-word `edit` | 1377 | — | — |
| lines containing `edit` | 1474 | 1664 | 104 |
| `editor` | 43 | 92 | 4 |
| `code-editor` | 15 | 28 | 3 |

Distinct `edit*` identifiers, by frequency — note how many belong to meanings 2 and 3:

```
1402 edit        68 Edit       42 editor     22 editing
  18 StateEditing 12 edits     12 CheckEditGuard  11 editMsgs
  10 Editor        9 editLineAt 8 lastEditDiffCheck 7 editLine
   7 editAllow     7 checkNonHookEdits  6 executeEdit
```

### The target name carries its own hazard

`code` is a **worse** identifier than `edit` for future maintenance. Of ~4125 lines containing
"code" in the Go tree, ~3698 are `muxcode`, `opencode`, `codex`, `claude code`, or the existing
`code-builder` / `code-reviewer` / `code-researcher` / `code-editor` agent files. Only 325 are a
standalone whole-word `code`.

Consequences to accept deliberately, not discover later:

- Every future `grep -r code` over this repo is dominated by the project's own name.
- Three sibling agent files already begin `code-`, so `code` as a *role* sits confusingly beside
  `code-builder` (the **build** role) and `code-reviewer` (the **review** role) — neither of which
  is the `code` role.
- `WindowForRole`/`NormalizeBusRole` gain a role whose name is a substring of the binary itself.

This does not block the rename. It should be a recorded trade, and it argues for tooling that
matches on **word boundaries and known key sites**, never bare substrings.

## Open decisions

These change the work and are **not** decided here:

- [ ] **Does the bus role identity change, or only the display label?** A display-only rename (tmux
      window title shows `Code`, role stays `edit`) is perhaps a day and near-zero risk. A full
      identity rename touches authority gates and live sessions. The request says "throughout the
      codebase", which reads as the full rename — confirm before starting.
- [ ] **What does `agents/code-editor.md` become?** Applying "editor → coder" literally yields
      `code-coder.md`, which is redundant. Candidates: `coder.md`, `code-agent.md`.
- [ ] **What about `agents/editor-analyst.md`** (the `analyze` role)? It means *"the analyst that
      serves the editor"*. `coder-analyst.md` follows the rename; leaving it is also defensible.
- [ ] **Are `MUXCODE_EDIT_CLI` / `MUXCODE_EDIT_MODEL` renamed, with or without a back-compat
      alias?** These appear in users' `~/.config/muxcode/config` files, outside this repo. A hard
      rename silently drops a user's model selection back to default.

## Requirements

### Acceptance criteria

- [ ] F2's window displays **Code**, and `muxcode` launches it under the new name
- [ ] `muxcode send code <action> "..."` delivers; the old `edit` address either resolves via
      `NormalizeBusRole` or is a documented removal — **decided, not left ambiguous**
- [ ] Git-mutation authority still admits exactly one role, and `TestCommitAuthorityDefault` (or its
      renamed equivalent) pins it — the F2 agent remains the only role that may request commits
- [ ] Atlassian consent boundary intact: plan still writes only on a request relayed from the F2
      role
- [ ] Auto-clear still hard-excludes the F2 role **and** `auto`, at both config parse and guard
- [ ] **No identifier belonging to meaning 2 or 3 was renamed** — `CheckEditGuard`, `executeEdit`,
      `checkNonHookEdits`, `editLine`, `editLineAt`, `StateEditing`, `MUXCODE_EDITOR`,
      `sendEditorCommand`, `sendPlanEditorCommand` all keep their names, pinned by a test
- [ ] The `Write|Edit|NotebookEdit` PreToolUse hook matcher is untouched and the nvim diff preview
      still fires
- [ ] A session running at upgrade time does not silently lose queued messages (see migration below)
- [ ] `go vet ./...` and `go test ./...` green in both modules
- [ ] Docs updated: `CLAUDE.md`, `docs/architecture.md`, `docs/agents.md`, `docs/agent-bus.md`,
      `docs/configuration.md`, `docs/hooks.md`, and every `docs/requirements/**` cross-reference
      that names the role
- [ ] Historical accuracy preserved: completed specs describing what the **edit** agent did are not
      rewritten to claim a role that did not exist at the time

### Technical approach

Rename by **enumerated key sites**, never by bulk substitution. The identifier census above is the
input; each site is changed knowingly.

Anchor sites for the role identity:

| Site | File | Note |
|------|------|------|
| `KnownRoles` | `bus/config.go:13` | role registry |
| `splitLeftWindows` | `bus/config.go:26` | window has an nvim left pane |
| `BusRole()` | `bus/config.go:93` | **resolves role from tmux `#W`** — the reason this is not cosmetic |
| `NormalizeBusRole()` | `bus/config.go:609` | natural home for an `edit → code` back-compat alias |
| `commitAuthorityDefault` | `bus/commit_authority.go:29` | `[]string{"edit"}` — git mutation gate |
| `autoClearExcluded` | `bus/clear.go:23` | `{"edit": true, "auto": true}` |
| `AgentFileName()` | `bus/launch.go:53` | returns `code-editor` |
| `RoleCLIEnvVar()` | `bus/launch.go:89` | `MUXCODE_EDIT_CLI` family |
| Auto-CC + send policy | `cmd/send.go:147,240` | `from != "edit"`, `to != "edit"` |
| Doc-file guard window check | `cmd/hook.go:375` | `if window != "edit"` |
| Mode cycling | `bus/reload.go:231` | `edit↔auto` on F2 |
| tmux binding | `config/tmux.conf:10` | F2 binding keys on `window_index`, not name — verify |

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/config.go` | Role registry, window↔role mapping, `BusRole()` |
| `tools/muxcode/bus/commit_authority.go` | Git-mutation authority default |
| `tools/muxcode/bus/atlassian_authority.go` | Consent boundary referencing the F2 role |
| `tools/muxcode/bus/clear.go` | Auto-clear hard exclusion |
| `tools/muxcode/bus/launch.go` | Agent file + env var mapping |
| `tools/muxcode/bus/launcher.go` | Window construction; **`Editor`/nvim — do not rename** |
| `tools/muxcode/cmd/send.go` | Send policy, auto-CC |
| `tools/muxcode/cmd/hook.go` | Doc-file guard window check |
| `agents/code-editor.md` | Agent definition (rename pending decision above) |
| `agents/editor-analyst.md` | Analyst definition (rename pending decision above) |
| `config/tmux.conf` | F2 binding and window setup |
| `scripts/test-*.sh` | ~10 integration scripts reference the edit window/role |

### Migration — the failure mode that bites users

Bus state is keyed by role name on disk: `/tmp/muxcode-bus-{session}/inbox/edit.jsonl`, plus tasks,
delivery receipts, notified markers, and runtime override files `config/{role}.env`. A session
running across the upgrade has an `edit.jsonl` that the new binary never reads.

- [ ] Decide and document: rename-on-startup migration, a `NormalizeBusRole` alias that keeps
      reading the old path, or "restart your session" as a stated release note
- [ ] Whichever is chosen, **queued-but-unread messages must not vanish silently**

## Implementation

### Phase 1: Decide scope and freeze the census

- [ ] Resolve the four open decisions above with the user
- [ ] Re-run the identifier census (this spec's numbers age with the tree)
- [ ] Produce the enumerated key-site list; classify every `edit*`/`editor*` identifier as
      meaning 1, 2, or 3
- [ ] Record the `code`-as-substring trade in `docs/architecture.md`

### Phase 2: Role identity

- [ ] Rename the role in `KnownRoles`, `splitLeftWindows`, and window mapping
- [ ] Add the `edit → code` alias to `NormalizeBusRole()` (or record why not)
- [ ] Move `commitAuthorityDefault`, `autoClearExcluded`, send policy, and the doc-guard check
- [ ] Update `AgentFileName()` and `RoleCLIEnvVar()`
- [ ] Update the default window list and `config/tmux.conf`

### Phase 3: Agent definitions, vocabulary, env vars

- [ ] Rename the agent definition file(s) per the Phase 1 decision
- [ ] Rename meaning-1 "editor" prose to "coder"; **leave meanings 2 and 3 untouched**
- [ ] Env var rename plus back-compat handling per the Phase 1 decision

### Phase 4: Migration

- [ ] Implement the chosen bus-state migration
- [ ] Verify a session upgraded mid-flight keeps its queued messages

### Phase 5: Docs

- [ ] `CLAUDE.md`, `architecture.md`, `agents.md`, `agent-bus.md`, `configuration.md`, `hooks.md`
- [ ] Sweep `docs/requirements/**` for role references, **preserving historical accuracy** in
      completed specs
- [ ] Resolve every relative link after any file rename (a spec rename has a multi-file fan-out —
      resolve targets, never trust a prefix grep)

### Phase 6: Integration test

- [ ] Create `scripts/test-role-rename.sh` (hermetic: scratch bus + scratch tmux session)
- [ ] Test: F2 window presents as **Code** and its agent resolves to the new role via `#W`
- [ ] Test: `muxcode send code <action>` delivers and is receipted
- [ ] Test: the old `edit` address behaves exactly as the Phase 1 decision specifies (alias
      resolves, or fails with a clear message) — **assert the decision, not whichever happens**
- [ ] Test: a commit request from the renamed role is **admitted**; from any other role **denied**
      (both directions — a gate that always allows would pass a one-sided test)
- [ ] Test: auto-clear still refuses the renamed role and `auto`
- [ ] **Negative control**: `CheckEditGuard`, `executeEdit`, `checkNonHookEdits`, `editLine`,
      `MUXCODE_EDITOR`, `sendEditorCommand` still exist under their original names, and the
      `Write|Edit|NotebookEdit` hook matcher is unchanged
- [ ] Test: bus-state migration preserves a queued message across the rename
- [ ] Coverage floor so a skipped run cannot report green
- [ ] Run the script and verify all checks pass

## Risks

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Bulk find/replace hits meanings 2 and 3 | Renames a Claude Code tool guard and nvim plumbing; `MUXCODE_CODER=nvim` | Enumerated key sites + negative-control test |
| Authority gate renamed inconsistently | `commitAuthorityDefault` naming a role that no longer exists = **no role may commit**, or worse, a stale `"edit"` that nothing matches fails open | Pin both directions by test |
| Live sessions lose queued messages | Inbox/task/receipt files are keyed by role name on disk | Phase 4 migration + explicit release note |
| Users' configs carry `MUXCODE_EDIT_CLI` | Lives outside this repo; silent fallback to default model | Back-compat alias or loud warning |
| `code` collides with `muxcode`/`opencode` | Future greps and reviews get noisy | Recorded trade; word-boundary matching |
| Completed specs rewritten | Destroys the historical record of what the edit agent actually did | Phase 5 explicitly preserves history |

## Status

Backlog
