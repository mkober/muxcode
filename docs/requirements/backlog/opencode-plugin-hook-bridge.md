# OpenCode Plugin Hook Bridge

OpenCode has no Claude Code-style shell hooks, so muxcode treats it as a non-hook provider and chains degrade to natural-language instructions the LLM must follow — the direct cause of observed chain-retrigger storms. But OpenCode *does* ship a typed plugin system (JS/TS event hooks including `tool.execute.before/after` and `session.idle`). A small muxcode-authored plugin, auto-installed at agent launch, can shell out to the muxcode CLI on those events and restore deterministic build→test→review chains for OpenCode agents.

## Context

### Current state

- `OpenCodeProvider.SupportsHooks()` returns `false` (`bus/provider_opencode.go:241`) — no PreToolUse/PostToolUse, so `ProcessBashHook()` chain resolution never fires for OpenCode agents.
- Three-layer graceful degradation substitutes for hooks: (1) `buildChainInstruction()` (`bus/provider.go`) injects natural-language chain instructions, (2) `adaptBodyForNonHookProvider()` (`bus/provider_opencode.go:590`) rewrites hook references in agent bodies, (3) `CheckSendPolicy()` bypass lets non-hook agents send chain messages manually.
- The degradation is LLM-driven and therefore unreliable: see [`response-echo-chain-retrigger`](./response-echo-chain-retrigger.md) — `build` re-fired `request:test` 5× in 3.5 min (84.9K tokens burned) because chain decisions live in the model, not in deterministic code. The daemon also carries non-hook-only compensation paths (history synthesis at `daemon.go:2634-2982`, wake-up payload injection) that exist solely because no hook reports tool completion.

### Research findings (2026-08-18)

- **OpenCode v1 plugin surface** (stable): JS/TS modules in `.opencode/plugins/`, `~/.config/opencode/plugins/`, or npm packages via `opencode.json`. Events include `tool.execute.before`, `tool.execute.after`, `session.idle`, `session.created`, `session.error`, `file.edited`, `message.updated`, permission events.
- **OpenCode v2 plugin API** (explicitly beta — "entrypoints, hooks, draft shapes, and configuration may change"): `Plugin.define({id, setup})` registered under `plugins` in `opencode.json(c)`; hooks via `ctx.tool.hook("execute.before"/"execute.after")`, `ctx.session.hook("context"/"http.request"/"http.response")`, `ctx.aisdk.hook(...)`.
- **No native shell hooks** in either version — nothing like Claude Code's `PostToolUse` settings entries. Community precedent exists for layering shell hooks on the plugin API (KristjanPikhof/OpenCode-Hooks: YAML-defined command hooks as a plugin).
- **Version strategy**: target the v1 event surface first (stable, richer event list); add a v2 `Plugin.define` variant once the v2 API stabilizes.

### Design sketch

A single TypeScript plugin file (embedded in the Go binary, written per-launch by `WriteAgentConfig()` alongside the existing agent definition):

- `tool.execute.after` for bash-like tools → exec `muxcode hook bash` with session/role env and the tool's command + exit code on stdin (same JSON shape `ProcessBashHook()` already consumes) → deterministic chain resolution, history logging, and analyze triggers come back for free.
- `session.idle` → touch an idle marker or invoke a lightweight `muxcode` signal, giving the daemon a positive idle signal instead of pane-scrape inference.
- Session/role plumbing via env vars (`BUS_SESSION`, role) injected at launch — the plugin must be inert (no-op, no errors) when they are absent so a user running OpenCode outside muxcode is unaffected.

### Capability split — not a wholesale `SupportsHooks()` flip

The plugin restores *chain events* (PostToolUse-equivalent) but not *pre-execution guarding* (there is a `tool.execute.before` event, but wiring `muxcode hook guard` through it is a separate trust decision — OpenCode permission enforcement stays with `DenyTools`). Flipping `SupportsHooks()` to true would wrongly disable the guard-related degradation and Claude-specific idle/startup machinery. Introduce a narrower capability (e.g. `SupportsChainEvents()`) that the daemon and prompt builders consult where the concern is chain firing, leaving guard and TUI handling on the non-hook paths.

## Requirements

### Acceptance criteria

- [ ] OpenCode agents fire build→test→review chain messages deterministically via the plugin, not via LLM-followed instructions
- [ ] `WriteAgentConfig()` installs/refreshes the plugin at every OpenCode agent launch; stale plugin versions are overwritten (version marker in the file)
- [ ] Plugin is inert outside a muxcode session (missing env vars → no-op, no errors surfaced to the OpenCode user)
- [ ] Chain-instruction degradation (`buildChainInstruction()` injection) is suppressed for roles where the plugin is active, so agents are not double-instructed
- [ ] Graceful degradation fully preserved when the plugin fails to load or is disabled (`MUXCODE_OPENCODE_PLUGIN_DISABLE=1`)
- [ ] Guard behavior unchanged: `DenyTools` remains the enforcement path; `SupportsHooks()` still returns `false`
- [ ] Existing provider and daemon tests still pass

### Key files

| File | Change |
|------|--------|
| `bus/provider_opencode.go` | Embed plugin source; `WriteAgentConfig()` writes it to `.opencode/plugins/`; `SupportsChainEvents()` |
| `bus/provider.go` | New capability method on the `Provider` interface (default false); consult it in `buildChainInstruction()` |
| `bus/prompt.go` | Skip chain-instruction injection when chain events are plugin-backed |
| `cmd/hook.go` | Accept plugin-originated invocations (env-identified role) on the existing `hook bash` path |
| `daemon/daemon.go` | Gate non-hook compensation paths (history synthesis, chain nudges) on `SupportsChainEvents()` |
| `docs/architecture.md`, `docs/hooks.md`, `docs/agents.md` | Document the bridge and the capability split |

## Implementation

### Phase 1: Plugin artifact and installation

- [ ] Author the TypeScript plugin (v1 event surface): `tool.execute.after` → `muxcode hook bash`, `session.idle` → idle signal
- [ ] Embed in the Go binary; `WriteAgentConfig()` writes/refreshes it per launch with a version marker
- [ ] Inert-outside-muxcode behavior + `MUXCODE_OPENCODE_PLUGIN_DISABLE=1` opt-out
- [ ] Unit tests: emission, refresh-on-version-change, disable flag

### Phase 2: Chain event wiring

- [ ] Plumb session/role env into the OpenCode launch so the plugin can address the bus
- [ ] Verify `ProcessBashHook()` handles plugin-originated events (command classification, chain resolution, history logging)
- [ ] Unit tests: a simulated `tool.execute.after` event produces the same chain sends as a Claude PostToolUse event

### Phase 3: Capability split and degradation gating

- [ ] Add `SupportsChainEvents()` to the `Provider` interface; OpenCode returns true when the plugin is installed and enabled
- [ ] Suppress `buildChainInstruction()` injection and daemon chain-compensation paths for chain-event-capable roles
- [ ] Confirm guard, idle detection, and wake-up paths are untouched
- [ ] Unit tests: capability gating in prompt build and daemon checks

### Phase 4: Integration test

- [ ] Create `scripts/test-opencode-plugin-bridge.sh` (requires a running muxcode session with an OpenCode agent)
- [ ] Test: plugin file present and version-current after agent launch
- [ ] Test: run a build command in the OpenCode agent → verify the chain message (`request:test`) lands via the hook path (history entry has hook provenance, not LLM-sent)
- [ ] Test: `MUXCODE_OPENCODE_PLUGIN_DISABLE=1` reload → plugin absent, chain instructions re-appear in agent config
- [ ] Run the script and verify all checks pass

## Open questions

- v2 plugin API adoption timing — revisit once the beta stabilizes; the v1 surface is the compatibility target until then
- Whether `session.idle` signaling should replace or merely supplement pane-based `IsIdle()` for OpenCode
- Whether `tool.execute.before` should eventually back `muxcode hook guard` (would harden delegation enforcement beyond `DenyTools`, but adds a trust dependency on the plugin loading)

## Provenance

Filed by the plan agent on 2026-08-18 after researching OpenCode 2's extensibility surface at the user's request (question: "does opencode 2 have a hooks feature"). Sources: opencode.ai v1 plugin docs, opencode.ai/v2/docs/build/plugins (beta API), KristjanPikhof/OpenCode-Hooks community plugin. User approved filing the spec.

## Status

Backlog
