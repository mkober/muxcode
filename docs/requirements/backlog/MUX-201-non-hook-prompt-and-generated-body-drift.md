# MUX-201: Non-Hook Prompt Text and Generated Agent Bodies Drift From the Definition

Two injection paths carry role instructions to a non-hook provider (OpenCode, scrape-road Codex,
local), and neither follows the agent definition when it changes. `bus/prompt.go`'s
`Manual Bus Messaging` block tells an `edit` agent, unconditionally, to orchestrate build → test →
review — with no graph-worker carve-out, so it contradicts `agents/code-editor.md`'s Exception in
exactly the graph-worker case. And a generated `.opencode/agents/<role>.md` is rewritten only when
that role is launched on OpenCode, so a body can run weeks behind its source with no signal.

Deferred from [MUX-142](../completed/MUX-142-spawn-worker-delegates-into-wrong-tree.md) ("Defect 1 is
fixed on the Claude road only", 2026-09-03; deferred on the user's decision 2026-10-02).

## Context

### The two paths, verified 2026-10-02

| Path | Where | Behaviour |
|------|-------|-----------|
| Shared prompt block | `BuildSharedPrompt`, `bus/prompt.go:133–147` — gated on `!provider.SupportsHooks()`; the `edit` case emits a numbered build → test → review orchestration with `--wait` | Generated prose, not body text, so `adaptBodyForNonHookProvider` never sees it; `grep -c graph bus/prompt.go` is `0` |
| Generated body | `OpenCodeProvider.WriteAgentConfig` → `writeOpenCodeAgentConfig` (`bus/provider_opencode.go:248–257`), called from `bus/launch.go:1028` at launch | Rewritten **only when that role launches on OpenCode**; gitignored (`.gitignore:19`, `:37`), so nothing tracks drift. The 2026-09-03 case: `edit.md` sixteen days behind its source because edit ran on Claude in that session |

A freshly generated OpenCode `edit` body therefore carries **both** the definition's conditional
Exception ("When your task message opens with `[graph run … · node …]` … Do NOT delegate them") and,
later in the same document, the prompt block's unconditional "After making code changes, manually
orchestrate the build→test→review chain". Whether a model reconciles them is not a property to rely
on. `CheckGraphNodeAuthority` backstops only a **spawn** role owned by a running run; a non-spawn
OpenCode agent that manually chains is not covered.

### Scope

In: the prompt block's graph awareness; detecting or regenerating a stale body; the negative control
that a non-graph OpenCode `edit` still gets its manual-chain instruction. Out: the hook road (Claude,
codex hook road), which has no prompt block and no generated body; MUX-142's guards, which stay as the
backstop.

## Requirements

### Acceptance criteria

- [ ] The graph-worker carve-out reaches non-hook providers by every injection path, not only the
      agent body — specifically `BuildSharedPrompt`'s `Manual Bus Messaging` block says, for `edit`,
      that a task opening `[graph run … · node …]` is **not** orchestrated
- [ ] A generated agent body older than its source definition is detected and surfaced (or
      regenerated), rather than running silently stale — at minimum at session launch and `reload`,
      for every role the session runs on a non-hook provider
- [ ] Negative control: a non-graph OpenCode `edit` agent still receives the manual-chain instruction
      unchanged
- [ ] Negative control: a hook-road role's prompt is byte-for-byte unchanged
- [ ] Docs: `docs/hooks.md` (provider gating) and `docs/agents.md` (OpenCode edit agent) describe the
      carve-out and the staleness check

### Technical approach

| Piece | Option |
|-------|--------|
| Prompt block | Add the carve-out sentence inside the `edit` case of `BuildSharedPrompt`, worded from the same `[graph run … · node …]` preamble `graph_exec.go` writes, so the two instructions agree |
| Staleness | Simplest: regenerate every non-hook role's body at `LaunchSession` and `reload`, not only at that role's own launch — the write is idempotent and cheap. Alternative: compare the body's mtime (or an embedded source hash) with `agents/<file>.md` and log `agent-body-stale` with both times |
| Tests | A prompt test per provider; a body test that ages a generated file past its source and asserts the detection/regeneration |

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/prompt.go` | `BuildSharedPrompt`, the `Manual Bus Messaging` block |
| `tools/muxcode/bus/provider_opencode.go` | `writeOpenCodeAgentConfig`, `adaptBodyForNonHookProvider` |
| `tools/muxcode/bus/launch.go` | `WriteAgentConfig` call site (`:1028`) |
| `tools/muxcode/bus/graph_exec.go` | The ownership preamble the carve-out must match (`:786`) |
| `agents/code-editor.md` | The Exception the prompt block must agree with (`:259`) |
| `docs/hooks.md`, `docs/agents.md` | Docs |

## Implementation

### Phase 1: Prompt carve-out

- [ ] Add the graph-worker carve-out to the `edit` case of the non-hook prompt block
- [ ] Test: non-hook `edit` prompt contains the carve-out; hook-road prompt unchanged; non-graph
      manual-chain text still present

### Phase 2: Body staleness

- [ ] Choose regenerate-at-session-launch or detect-and-log; record the decision here
- [ ] Implement it for every non-hook role at `LaunchSession` and `reload`
- [ ] Test: a body aged past its source is regenerated or logged `agent-body-stale`

### Phase 3: Docs

- [ ] `docs/hooks.md` provider-gating paragraph and `docs/agents.md` OpenCode section updated

### Phase 4: Integration test

- [ ] Create `scripts/test-non-hook-prompt-drift.sh` — hermetic: tree-built binary, scratch session dir,
      `MUXCODE_EDIT_CLI=opencode`
- [ ] Assert the generated `edit` prompt carries the carve-out and the manual-chain text
- [ ] Touch the source definition newer than the generated body, relaunch, assert the body is fresh
      (or the stale row is logged)
- [ ] Negative control: a Claude-provider role's prompt has neither block
- [ ] Coverage floor set to the maximum achievable count
- [ ] Run the script and confirm all checks pass

## Related

- [MUX-142](../completed/MUX-142-spawn-worker-delegates-into-wrong-tree.md) — the spec this was deferred
  from; its guards are the backstop this spec sits in front of
- [MUX-159](../completed/MUX-159-codex-hooks-provider.md) — the provider capability split that defines
  "non-hook"

## Status

Backlog
