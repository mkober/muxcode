# Research Agent — Dual-Provider Split View

Turn the **research agent** (F1 window, research mode) into a dual-provider
side-by-side agent: two different AI provider CLIs run in adjacent panes (e.g.
**Codex on the left, Claude on the right**, or **Claude on the left, OpenCode on
the right**), and the research agent has visibility into both so it can compare
answers and bounce ideas between the two models.

Immediate scope is **two** panes for research, but the identity scheme is built
to scale to N panes from day one (see "Numbered identity convention" below).

### Numbered identity convention (`<role>-N`)
Each agent pane gets a **numbered, role-suffixed bus identity** — `research-1`,
`research-2`, … (and the same pattern for any future multi-pane role, e.g.
`editor-1`, `editor-2`). Numbers are **1-based** and assigned by a fixed spatial
order: **left-to-right, then top-to-bottom**.

```
 1 = top-left      2 = top-right
 3 = bottom-left   4 = bottom-right
```

This is the single ordering rule for any pane count: a 2-pane row is `1 | 2`; a
2×2 grid is `1 2 / 3 4`. Tmux panes are 0-indexed, so identity `N` maps to tmux
pane index `N-1` (`research-1` → pane `.0`, `research-2` → pane `.1`). The scheme
is reusable: any role that grows multiple panes follows `<role>-N` with the same
spatial ordering.

## Context

### Origin
The research agent today is a single-provider agent. It runs on the F1 window in
"research" mode (reached by cycling F1 plan ⇄ research), defaults to OpenCode +
DeepSeek, and has its own independent inbox (`inbox/research.jsonl`). It
specializes in web searches and hands implementation off to the active F2 agent.

The goal of this feature is to run **two providers at once** in the research
window so the agent can play them off each other — ask the same question of two
different models, have one critique the other's answer, and synthesize a stronger
result than either model alone.

### Current state (as built)

| Concern | Mechanism | File / function |
|---------|-----------|-----------------|
| Research launch | On-demand when F1 first cycles to research mode | `bus/mode.go` `modeCreateAgent()` |
| Window layout | Hold window `research`: pane 0 = `muxcode console research`, pane 1 = agent | `bus/mode.go` ~L331-344 |
| Mode cycling | `swap-window` between `plan` and `research` hold windows — one full window in the F1 slot at a time | `bus/mode.go` `modeSwitchTo()` |
| Provider resolution | 1:1 role→CLI; research defaults to `opencode` | `bus/provider.go` `ResolveProviderCLI()`, `roleDefaultCLI()` |
| Pane targeting | Agent is **always pane 1**; `AgentPane()` returns `"1"` unconditionally | `bus/config.go` `AgentPane()`, `PaneTarget()` |
| Bus identity | Single `research` identity — one inbox, one lock, one override file, one `AGENT_ROLE` | `bus/config.go` `NormalizeBusRole()`, `InboxPath()` |
| Provider switch | `muxcode reload research --cli <cli>` writes a runtime override and relaunches pane 1 | `bus/reload.go`, `bus/override.go` |
| Auto-accept / wake | `modeAutoAcceptAndWake()` polls pane 1 only | `bus/mode.go` ~L446-529 |

### Structural constraints (why this is non-trivial)
The current architecture assumes **one agent per window, always in pane 1, with
one bus identity**. Every notification, wake-up, reload, auto-accept, and
idle-detection path hardcodes `.1`. Specific blockers surfaced during research:

- `AgentPane()` / `PaneTarget()` always target pane 1 — no concept of a pane-2 agent.
- One inbox / lock / override / `AGENT_ROLE` per role — two live processes can't both be `research`.
- `modeSwitchTo()` swaps **entire windows**, not panes — the F1 slot shows one window at a time.
- `modeCreateAgent()` creates exactly one `split-window -h` (console + agent).
- `ReloadTarget()`, `AutoAccept()`, and `modeAutoAcceptAndWake()` all assume two panes max and target `.1`.
- Provider selection is a strict 1:1 role→CLI mapping; `ModeCycleState.Current` is a single index.

These constraints mean the feature needs a **design phase** before implementation.

## Requirements

### Acceptance criteria
- [ ] The research window opens with **two provider panes side by side**
      (`research-1` left, `research-2` right), each running a different CLI.
- [ ] The provider-per-pane pairing is **configurable** (e.g. `research-1`=Codex /
      `research-2`=Claude, or `research-1`=Claude / `research-2`=OpenCode) via env
      vars and/or config.
- [ ] Each provider pane runs as a distinct, addressable bus identity (`research-1`,
      `research-2`, …) with its own inbox — messages to one never collide with another.
- [ ] Identities follow the `<role>-N` numbered convention with left-to-right then
      top-to-bottom ordering; identity `N` resolves to tmux pane `N-1`. The scheme
      supports >2 panes without renaming.
- [ ] The research agent can **broadcast one query to all provider panes** and
      collect all responses.
- [ ] The research agent can **relay one provider's output to the others** for
      critique/refinement ("here is what the other model said — improve on it"),
      enabling the "bounce ideas off each other" workflow.
- [ ] A combined/synthesized view of all providers' context is available to the
      research agent (shared context — decision recorded in design phase).
- [ ] F1 mode cycling still works: cycling into research mode shows the multi-pane
      layout; cycling away hides it cleanly (no orphaned panes/windows).
- [ ] Every provider pane gets startup trust/bypass acceptance and idle wake-up
      (auto-accept extended beyond pane 1).
- [ ] `muxcode reload research-N --cli <cli>` switches the provider of a single
      pane independently, leaving the others untouched.
- [ ] Single-provider research still works when only one provider is configured
      (graceful fallback — multi-pane mode is opt-in).
- [ ] Docs updated: `docs/agents.md` (research role), `docs/architecture.md`
      (window/pane model), `docs/configuration.md` (new env vars).

### Non-goals
- Making other agents (build, test, etc.) multi-provider now — research only.
  (The `<role>-N` convention is designed to extend to them later, but that work
  is out of scope here.)
- **Shipping** more than two panes now — the immediate deliverable is 2 panes for
  research. The numbering/identity scheme must not preclude >2, but a 3+ layout is
  not built in this spec.
- Automatic "winner" selection between the models (the agent/user decides).
- Changing the F1 plan ⇄ research swap-window mechanism itself (the multi-pane
  layout lives inside the research window).

## Technical approach

Three concerns: **(1) layout** — fit two agent panes (plus optional console) in
one window; **(2) identity** — give each provider pane a distinct bus identity;
**(3) context bridge** — let the research agent see and relay between both.

### 1. Pane layout — DECIDED: 2-pane (drop console)
In dual mode the research window is **two equal agent panes** — `research-1` on
the left (tmux pane `.0`), `research-2` on the right (tmux pane `.1`). The
`console research` pane is **dropped** in dual mode so each provider gets full
width (research activity moves to a status line or is omitted while dual mode is
active). Single-provider fallback keeps the existing console + agent layout.

```
+-----------+-----------+        identity → tmux pane
| research-1| research-2|        research-1 → .0  (top-left)
| (pane .0) | (pane .1) |        research-2 → .1  (top-right)
|   LEFT    |   RIGHT   |        research-3 → .2  (bottom-left)  [future]
+-----------+-----------+        research-4 → .3  (bottom-right) [future]
```

`modeCreateAgent()` (`bus/mode.go`) creates the panes; in dual mode it skips the
console and instead launches two provider agents (one per pane). All downstream
`.1`-hardcoded pane targeting (`AgentPane()`, `PaneTarget()`, `ReloadTarget()`,
auto-accept) must resolve a pane index **from the numbered identity** (`research-N`
→ pane `.{N-1}`) instead of assuming `.1`. Note: dropping the console means pane
`.0` now hosts an **agent** (not a console) in dual mode — `HasConsoleView` /
`IsSplitLeft` logic must branch on dual mode.

### 2. Numbered bus identities (`research-N`)
Introduce numbered role-suffixed identities so the bus layer stays 1:1 per process:

- `research-1`, `research-2`, … — one per pane, numbered by the left-to-right /
  top-to-bottom rule. The scheme generalizes to any role as `<role>-N`.
- Each gets its own inbox, lock, runtime-override file, and `AGENT_ROLE`.
- A single pane-index resolver maps identity → tmux pane: `research-N` → pane
  `.{N-1}`. `PaneTarget()` / `AgentPane()` call this instead of returning `.1`.
- `NormalizeBusRole()` / `WindowForRole()` / `IsKnownRole()` recognize the
  `<role>-N` pattern (parse the suffix) rather than enumerating fixed identities,
  so adding panes needs no new role registrations.
- The user-facing `research` identity is retained as a coordinator/alias (design
  decides whether `research` maps to `research-1`, a thin orchestrator, or a
  broadcast alias to all panes).

### 3. Provider configuration
Add a way to specify a provider per numbered pane:

- Per-pane env vars: `MUXCODE_RESEARCH_1_CLI`, `MUXCODE_RESEARCH_2_CLI`, …
  (fallback to existing `MUXCODE_RESEARCH_CLI` for single-provider mode).
- Or a single ordered var: `MUXCODE_RESEARCH_PROVIDERS="codex,claude"` — position
  in the list is the pane number (`codex`→`research-1`, `claude`→`research-2`).
- Persisted equivalents via `muxcode config set`.
- `RoleCLIEnvVar("research-N")` returns the per-pane var; `ResolveProviderCLI()` /
  `ResolveLaunchConfig()` resolve per numbered identity.

### 4. Context bridge ("bounce ideas off each other") — DECIDED: auto cross-critique
The core feature. Every research query is **automatically dual-asked and
cross-critiqued** — no manual relay step:

```
User asks research a question
  → fan-out to LEFT + RIGHT (both answer independently)
  → LEFT is shown RIGHT's answer, asked to refine/critique
  → RIGHT is shown LEFT's answer, asked to refine/critique
  → coordinator synthesizes both refined answers into one result
```

Mechanism (hybrid of bus relay + shared context):
- **Bus relay** drives turn-taking: a query to `research` fans out via `Send()`
  to all `research-N` panes; each reply is captured; round two forwards each
  provider's round-one answer into the others' prompts for critique.
- **Shared transcript** (`research-shared.md` or in-memory) holds the full
  cross-model conversation so the coordinator can synthesize and so each pane has
  the others' running context.
- The coordinator (the `research` identity) orchestrates the fan-out → critique →
  synthesize cycle and presents the combined result.

### Key files

| File | Purpose / change |
|------|------------------|
| `tools/muxcode/bus/mode.go` | `modeCreateAgent()`, `modeSwitchTo()`, `modeAutoAcceptAndWake()` — multi-pane creation, per-pane accept/wake |
| `tools/muxcode/bus/config.go` | `AgentPane()`, `PaneTarget()`, `WindowForRole()`, `NormalizeBusRole()`, `IsKnownRole()`, `modeRoles` — parse `<role>-N`, resolve identity → pane `.{N-1}` |
| `tools/muxcode/bus/provider.go` | `ResolveProviderCLI()`, `roleDefaultCLI()` — per-numbered-pane provider resolution |
| `tools/muxcode/bus/launch.go` | `RoleCLIEnvVar()`, `ResolveLaunchConfig()`, `RunAgentLaunch()` — launch one provider per `research-N` identity |
| `tools/muxcode/bus/reload.go` | `ReloadTarget()`, `ReloadAgent()` — per-pane (`research-N`) reload targeting |
| `tools/muxcode/bus/override.go` | Per-numbered-pane runtime override files |
| `tools/muxcode/bus/inbox.go` | Fan-out / relay helper for the context bridge |
| `config/tmux.conf` | F1 cycle behavior if layout changes |
| `agents/code-researcher.md` | Agent body — instructions for the dual-provider bounce workflow |
| `docs/agents.md`, `docs/architecture.md`, `docs/configuration.md` | Documentation |
| `tools/muxcode/bus/mode_test.go` | Tests for dual-pane creation, identity resolution, relay |

## Implementation

### Phase 1: Design decisions
- [x] Decide pane layout: **2-pane (drop console)** — two equal provider panes.
- [x] Decide context-bridge mechanism: **auto cross-critique** (bus relay for
      turn-taking + shared transcript for context).
- [x] Decide identity scheme: **numbered `<role>-N`** (`research-1`, `research-2`),
      1-based, ordered left-to-right then top-to-bottom; identity `N` → pane `.{N-1}`.
- [ ] Decide whether `research` maps to `research-1`, a thin orchestrator, or a
      broadcast alias to all panes (back-compat for the existing identity).
- [ ] Decide provider config surface: per-pane `MUXCODE_RESEARCH_N_CLI` vars vs
      ordered `MUXCODE_RESEARCH_PROVIDERS` list (or support both).
- [ ] Decide default pairing when dual mode is enabled with no explicit config.
- [ ] Record remaining decisions in the "Open questions" section below.

### Phase 2: Pane layout and multi-pane launch
- [ ] Extend `modeCreateAgent()` to build the 2-pane (no-console) layout.
- [ ] Launch one provider agent per numbered identity (`research-1`, `research-2`).
- [ ] Add a pane-index resolver that parses `<role>-N` and returns tmux pane
      `.{N-1}`; route `AgentPane()`/`PaneTarget()` through it instead of `.1`.
- [ ] Ensure mode cycling (`swap-window`) keeps the multi-pane layout intact on
      enter/exit with no orphaned panes.

### Phase 3: Numbered bus identities and provider config
- [ ] Make `NormalizeBusRole()`/`WindowForRole()`/`IsKnownRole()` recognize the
      `<role>-N` pattern (parse suffix) rather than enumerating fixed identities.
- [ ] Wire per-numbered-pane inbox, lock, and runtime-override paths.
- [ ] Add per-pane provider env vars (`MUXCODE_RESEARCH_N_CLI`) and/or ordered
      `MUXCODE_RESEARCH_PROVIDERS`; resolve in `ResolveProviderCLI()`.
- [ ] Extend `muxcode reload research-N` to target a single pane independently.
- [ ] Preserve single-provider fallback when only one provider is configured.

### Phase 4: Context bridge
- [ ] Implement broadcast: a `research` query fans out to both provider panes.
- [ ] Implement relay: forward one provider's output into the other's prompt for
      critique/refinement.
- [ ] Implement the shared-context mechanism (per Phase 1 decision).
- [ ] Update `agents/code-researcher.md` with the dual-provider bounce workflow.

### Phase 5: Auto-accept, wake-up, and lifecycle
- [ ] Extend `modeAutoAcceptAndWake()` to accept startup prompts and wake **both**
      provider panes.
- [ ] Verify idle detection / notification works for both identities.
- [ ] Verify graceful stop / reload clears both panes cleanly.

### Phase 6: Docs
- [ ] Update `docs/agents.md` research role section (dual-provider behavior).
- [ ] Update `docs/architecture.md` window/pane model (multi-pane research window).
- [ ] Update `docs/configuration.md` with the new env vars / config keys.

### Phase 7: Integration test
- [ ] Create `scripts/test-research-dual-provider.sh` (runs inside a live muxcode session).
- [ ] Test: cycling F1 into research mode opens **two** agent panes with the
      configured providers — `research-1` in pane `.0`, `research-2` in pane `.1`
      (assert via `tmux list-panes` and provider detection).
- [ ] Test: a broadcast query reaches both `research-1` and `research-2` inboxes.
- [ ] Test: relaying one provider's output into the other's prompt works (the
      other pane receives the first's content).
- [ ] Test: `muxcode reload research-2 --cli <cli>` switches only `research-2`'s
      provider; `research-1` is unaffected.
- [ ] Test: single-provider fallback — with only one provider configured, research
      opens with one agent pane as before.
- [ ] Test: cycling F1 away from research and back leaves no orphaned panes.
- [ ] Run the script and verify all checks pass.

### Resolved
- [x] **Pane layout** → 2-pane, drop the console; two equal provider panes.
- [x] **Bounce mode** → automatic cross-critique (every query dual-asked, each
      model shown the other's answer to refine, coordinator synthesizes).
- [x] **Identity scheme** → numbered `<role>-N` (`research-1`, `research-2`, …),
      1-based, ordered left-to-right then top-to-bottom; identity `N` → tmux pane
      `.{N-1}`. Generalizes to >2 panes and to other roles (`editor-1`, …) without
      renaming.

### Still open
- [ ] Does `research` (the existing identity) map to `research-1`, a thin
      orchestrator, or a broadcast alias to all panes?
- [ ] Context bridge plumbing: bus relay + shared transcript file vs in-memory
      transcript — confirm during Phase 4 design.
- [ ] Provider config surface: per-pane `MUXCODE_RESEARCH_N_CLI` vars, ordered
      `MUXCODE_RESEARCH_PROVIDERS` list, or both?
- [ ] Does multi-pane mode become the research default, or stay opt-in behind a
      flag/env?
- [ ] How does the provider-selector modal (`prefix + R`) represent a multi-pane
      window with per-pane providers?
- [ ] Token-cost guardrail: auto cross-critique multiplies calls (N× ask + N×
      critique per query) — cap rounds or make the critique round optional?

## Status

Draft
