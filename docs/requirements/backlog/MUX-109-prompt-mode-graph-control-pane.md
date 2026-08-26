# Prompt Mode and Prompt-Agent for the Graph Control Pane

Add a **Prompt** surface to the [control pane](../completed/MUX-108-control-pane.md) and a small
local-model **prompt-agent** behind it, so graph operations can be named in natural language
instead of navigated to — launch a graph, ask after a run, approve a named gate, or compose a
new project-local graph definition. The same surface can instead **inject** the typed text into
the window's main agent, making the pane a universal input line rather than a graph-only console.

## Context

### Why now

The control pane is permanent, full-width, and present on every agent window. It already hosts
three graph surfaces cycled with `Tab`/`Shift-Tab`. Every graph operation available there is
reached by drilling through a list, and every operation *not* there is reached by typing a
`muxcode graph` command in some other pane. Naming the thing you want is faster than finding it,
and composing a new graph today means hand-writing JSON against a schema whose only documentation
is the validator that rejects it.

The scope is deliberately small — resolve a name, read a status, approve a named gate, draft a
definition — which is exactly the shape a local model can serve. That keeps the feature off the
metered providers and inside the existing harness.

### What already exists (verified against the tree, not assumed)

This table is the reason the implementation is smaller than it sounds. Each row was checked
before this spec was written.

| Capability | State today | Evidence |
|------------|-------------|----------|
| Three-scope resolution `project > user > builtin` | **Already implemented** | `ResolveGraphTemplate()` — `bus/graph.go:509`; `.muxcode/graphs` → `~/.config/muxcode/graphs` → builtin |
| Three-scope enumeration, highest-precedence dedup | **Already implemented** | `ListGraphTemplates()` — `bus/graph.go:535`, `addDir` over all three tiers |
| CLI and TUI both resolve through the scoped helpers | **Already wired** | `cmd/graph.go:121` (run), `:238` (validate), `:254` (list); `tui/graph_ui.go:382`, `:819` |
| Surface cycle over top-level views | `Tab` / `Shift-Tab` | `graphSurfaces` — `tui/graph_ui.go:282`; `cycleSurface()` `:508`; key handling `:448`, `:492` |
| Surface selection shared across every pane | Marker file `control-pane-surface` | `bus/control_pane.go:111`–`:122` |
| Pane's starting surface | `MUXCODE_CONTROL_PANE_SURFACE` → `runs` / `gates` / `launcher` | `controlPaneCommand()` — `bus/control_pane.go:34` |
| **Free-text input inside a graph surface** | **Already exists** — the `${intent}` argument prompt | `viewGraphIntent`, `intentInput []rune` (`tui/graph_ui.go:252`), `handleIntentKey()` `:695` |
| Robust literal injection into a pane | `TmuxSendLiteral()` | `bus/tmux.go:145` |
| Local-LLM role discovery | Env-driven, no hardcoded role list | `LocalLLMRoles()` — `bus/health.go:103`, reads `MUXCODE_*_CLI=local` plus a generic scan |
| Gate rule predicate shared with the validator | `NodeRequiresGate()` | `bus/graph.go:150` |

Two consequences worth stating plainly:

- **Graph scope enumeration is done.** The originating brief proposed a phase to "confirm/extend
  project + user graph enumeration". Enumeration and resolution already cover all three tiers and
  are already wired into both the CLI and the launcher surface. What is genuinely missing is the
  **write** path — nothing in the tree creates `.muxcode/graphs/` or writes a definition into it.
  Phase 1 therefore shrinks to a verification step plus directory-creation-on-write.
- **A text input primitive already exists.** `viewGraphIntent` is a working in-surface line editor
  with its own key handler. The Prompt surface should extend that pattern, not invent a second one.

### Provenance of the pieces being extended

The three surfaces this feature joins were not all built by the same spec, and the code's file
layout does not track the split — everything lives in `tui/graph_ui.go`, but the surfaces and the
cycling between them arrived separately.

| Piece | Delivered by | Note |
|-------|--------------|------|
| The three graph surfaces (run browser, template launcher, gate queue) | [MUX-031](../completed/MUX-031-graph-run-tui.md) | Built as views; no cycling between them |
| `Tab`/`Shift-Tab` cycling (`graphSurfaces`, `cycleSurface()`) and gate auto-show | [MUX-105](../completed/MUX-105-force-respond-escalation.md) | Both symbols introduced by commit `c86594c`, alongside the force-respond ladder |
| The permanent full-width pane hosting them on every window | [MUX-108](../completed/MUX-108-control-pane.md) | Pane index 2; panes 0 and 1 keep their indices |

Worth knowing before editing: extending the cycle means touching a construct that came in with the
force-respond work, so a change there sits next to escalation code it has nothing to do with.

### Scope boundary

The prompt-agent **interprets and dispatches**; it does not implement. It has no repo write access,
no git, no Atlassian, and no ability to edit source. Its entire useful surface is a handful of
`muxcode graph` subcommands plus writing graph JSON into two directories. If a request needs more
than that, the correct outcome is to inject it into the main agent, not to grow the prompt-agent.

## Requirements

### Acceptance criteria

- [ ] A **Prompt** surface joins the control pane's `Tab`/`Shift-Tab` cycle, and the tab bar names it
- [ ] `MUXCODE_CONTROL_PANE_SURFACE=prompt` starts the pane on it; an unknown value still degrades to the run list
- [ ] The surface renders as a pure function of a snapshot — no I/O in the renderer — and is reachable via `--render-once`
- [ ] The surface clamps to the pane's `width` **and** `height`; a long prompt or a long reply degrades rather than overflowing
- [ ] The empty state (no prompt typed, no history) is explicit and keeps header, tab bar, and footer
- [ ] The footer advertises every key the surface accepts, including the inject/interpret toggle
- [ ] The input line names its destination at all times — which agent an injected prompt would reach, or that it will be interpreted — so the mode is readable without color
- [ ] A `prompt` bus role exists with its own inbox and is accepted by `IsKnownRole()`
- [ ] The prompt-agent runs on the local harness (Ollama); it never launches Claude Code, OpenCode, or Codex
- [ ] Its tool profile permits only `muxcode graph` subcommands, reads/writes under the two graph directories, and the injection path — verified by a **negative control** asserting a repo write and a git command are both denied
- [ ] Intent: **launch** — a named or described graph resolves and starts via `muxcode graph run`, across all three scopes
- [ ] Intent: **status** — questions about in-flight and completed runs answer from `graph list` / `graph status`
- [ ] Intent: **gates** — pending `wait_human` gates can be listed, and approved **only** when the user's prompt names the gate or run
- [ ] A prompt that does not name a specific gate never approves one — pinned by a negative-control test using deliberately suggestive phrasing ("approve whatever is waiting")
- [ ] Single-use gate approval semantics from [MUX-014](../completed/MUX-014-graph-agent-orchestrator.md) are unchanged — re-entering a gate still demands a fresh approval
- [ ] Intent: **create** — a described workflow is composed into a graph definition, validated, and written project-local by default
- [ ] A definition failing `Validate()` is **reported, never written** — pinned by a test asserting no file appears on the failure path
- [ ] A prompt-composed graph placing a commit or Atlassian node outside a `wait_human` gate is rejected by the existing validator rule, with no bypass
- [ ] Injection delivers the typed text to the window's active main agent via `TmuxSendLiteral()` (text → delay → Enter), never a hand-rolled `send-keys`
- [ ] Injection targets the window's **active** agent, respecting mode-cycled windows
- [ ] A dash-leading prompt injects intact, per [MUX-104](../completed/MUX-104-send-keys-dash-payload.md)
- [ ] Ollama health monitoring covers the new role, and the surface states plainly when the model is unreachable rather than appearing to accept a prompt it cannot serve
- [ ] `CheckCommitAuthority` and `CheckAtlassianAuthority` remain the runtime backstop, unchanged
- [ ] `scripts/test-prompt-mode.sh` passes, and its assertions include the negative controls above

### Technical approach

- **Extend the existing surface machinery, do not fork it.** A new `graphView` appended to
  `graphSurfaces`, a `surfaceName()` arm, a `renderSurfaceTabs()` entry, and a
  `controlPaneCommand()` case. The shared `control-pane-surface` file already propagates the
  selection to every pane.
- **Reuse `viewGraphIntent`'s line editor.** `handleIntentKey()` already handles printable bytes,
  backspace, Enter, and Escape inside a surface. Generalising it beats writing a second editor with
  its own subtly different Escape handling — and Escape disambiguation is a documented trap
  (`tui/graph_ui.go:477` distinguishes bare Escape from `ESC [ Z`).
- **The pane is the UI; the harness agent is the interpreter.** The surface writes a bus request to
  the `prompt` role and renders the reply. It must never block the pane's render loop on a model
  call — an unreachable Ollama has to leave a responsive frame that says so.
- **Intent classification is the model's only judgement call.** Everything downstream is a CLI
  invocation with validated arguments. Keep the classifier's output a small closed set
  (`launch` / `status` / `gates` / `approve` / `create` / `inject`) so an unparseable answer fails
  closed to "I did not understand" rather than to an arbitrary action.
- **Approval requires a named target, checked in code and not only in the prompt.** The dispatcher
  should refuse an approve intent whose gate/run identifier was not present in the user's typed
  text. A small model instructed not to over-approve will eventually over-approve; the guard that
  matters is the one outside the model.
- **Write path creates its directory.** `.muxcode/graphs/` does not exist in a fresh checkout;
  the create flow must `MkdirAll` before writing, and write atomically like the run store does.
- **Harness conventions apply as-is.** Short directive agent definition in `agents/harness/`,
  circuit breaker, batch timeout. Single-shot behaviour should be considered: most prompt intents
  are one tool call and done, which is exactly the shape `isSingleShotRole()` exists for.

### Key files

| File | Change |
|------|--------|
| `tui/graph_ui.go` | New Prompt view: `graphSurfaces` entry, `surfaceName()`, tab bar, renderer, key handler; generalise the intent line editor |
| `tui/graph_ui_test.go` | Frame tests: empty state, clamping, footer keys, destination label, unreachable-model state |
| `bus/control_pane.go` | `controlPaneCommand()` case for `prompt`; unknown-surface degradation unchanged |
| `bus/graph.go` | Graph definition **write** helper (`MkdirAll` + atomic write, validate-before-write); scope-aware target selection |
| `bus/config.go` | `prompt` role: `KnownRoles`, inbox, window/pane mapping |
| `bus/profile.go` | Narrow `prompt` tool profile — `muxcode graph *`, graph-dir read/write, injection; nothing else |
| `bus/health.go` | Confirm `LocalLLMRoles()` picks up the role from `MUXCODE_PROMPT_CLI=local` (env-driven — may need no change) |
| `agents/harness/prompt-agent.md` | New harness agent definition — short, directive, closed intent set |
| `cmd/graph.go` | Reuse for create/validate paths if a subcommand seam is cleaner than a library call |
| `scripts/test-prompt-mode.sh` | New — integration test (Phase 7) |
| `docs/architecture.md`, `docs/configuration.md`, `docs/agent-bus.md`, `CLAUDE.md` | Document the surface, the role, the env vars, and the authority boundary |

### Authority boundaries

These are requirements, not guidance. Each one exists because the prompt-agent sits behind a
free-text box, which is the least predictable input in the system.

| Boundary | Rule |
|----------|------|
| Gate approval | Only when the user's typed prompt **names** the gate or run. The agent never originates an approval, never infers one from context, and never approves in bulk |
| Graph writes | Only under `.muxcode/graphs/` (project, default) or `~/.config/muxcode/graphs/` (user, only when the user says global). Never `docs/`, never source, never builtin |
| Validation | `Validate()` runs before any write. The commit/Atlassian-behind-`wait_human` rule applies to prompt-composed graphs identically — a graph cannot be laundered around it by asking nicely |
| Runtime backstop | `CheckCommitAuthority` / `CheckAtlassianAuthority` unchanged. The prompt path adds no new authority and holds none |
| Injection | Forwards text to a main agent. It does not execute, and it does not compose the text on the user's behalf |

## Implementation

### Phase 1: Graph definition scopes — verify, then add the write path

- [ ] Confirm `ResolveGraphTemplate()` / `ListGraphTemplates()` cover project, user, and builtin (expected: already true — record the evidence rather than re-implementing)
- [ ] Confirm `graph run`, `graph validate`, `graph list`, and the TUI launcher all resolve through those helpers
- [ ] Add a graph-definition write helper: `MkdirAll` the target dir, validate, then write atomically
- [ ] Unit test: writing to a fresh checkout with no `.muxcode/graphs/` succeeds and creates the directory
- [ ] Unit test: a definition failing `Validate()` leaves **no file behind**

### Phase 2: Prompt bus role and harness agent

- [ ] Add the `prompt` role — `KnownRoles`, inbox path, window/pane mapping
- [ ] Add `agents/harness/prompt-agent.md` — short, directive, closed intent set
- [ ] Add the narrow `prompt` tool profile
- [ ] Confirm `LocalLLMRoles()` picks the role up from `MUXCODE_PROMPT_CLI=local`; extend only if it does not
- [ ] Negative-control test: the profile denies a repo write and denies a git command

### Phase 3: Prompt surface in the control pane

- [ ] Append the Prompt view to `graphSurfaces`; add `surfaceName()` and tab-bar entries
- [ ] Add the `prompt` case to `controlPaneCommand()`; unknown surfaces still degrade to the run list
- [ ] Generalise the `viewGraphIntent` line editor for reuse; keep Escape/`ESC [ Z` disambiguation intact
- [ ] Render: header, tab bar, input line with destination label, reply/history body, footer
- [ ] Explicit empty state; clamp to `width` and `height`; reachable via `--render-once`
- [ ] Frame tests including a **negative control** for clamping (a fixture that actually overflows)

### Phase 4: Prompt intents — launch, status, gates

- [ ] Classify a prompt into the closed intent set; an unparseable result fails closed
- [ ] `launch` — resolve across all three scopes and start via `muxcode graph run`
- [ ] `status` — answer from `graph list` / `graph status`
- [ ] `gates` — list pending `wait_human` gates
- [ ] `approve` — dispatch only when the typed text names the gate/run; guard enforced in code, not only in the model prompt
- [ ] Negative-control test: "approve whatever is waiting" approves nothing
- [ ] Confirm single-use approval semantics still hold on a retried gate

### Phase 5: Graph creation flow

- [ ] Compose a definition from a described workflow
- [ ] Validate before writing; report failures verbatim, write nothing
- [ ] Write project-local by default; user-global only on explicit instruction
- [ ] Test: a composed graph with an ungated commit node is rejected by the existing validator rule

### Phase 6: Prompt injection to the active main agent

- [ ] Resolve the window's active agent, respecting mode-cycled windows
- [ ] Deliver via `TmuxSendLiteral()` — text → delay → Enter; no hand-rolled `send-keys`
- [ ] Inject/interpret selection via an explicit toggle, with the destination always visible in the input line
- [ ] Test: a dash-leading prompt injects intact (MUX-104 regression shape)
- [ ] Test: with the window mode-cycled, injection reaches the *active* agent, not the default role

### Phase 7: Integration test

- [ ] Create `scripts/test-prompt-mode.sh` — hermetic where possible: scratch `BUS_SESSION`, scratch tmux session, scratch graph dirs
- [ ] Surface appears in the `Tab` cycle and `MUXCODE_CONTROL_PANE_SURFACE=prompt` starts on it (`capture-pane`)
- [ ] `--render-once` frames: empty state, clamped long prompt, unreachable-model state
- [ ] Launch intent starts a run against a scratch graph dir; run appears in `graph list`
- [ ] Status intent reports that run
- [ ] Named-gate approval releases the gate; unnamed "approve whatever is waiting" does **not**
- [ ] Create intent writes a valid definition into the scratch project dir; an invalid one writes nothing
- [ ] Injection delivers a dash-leading payload intact into a scratch pane
- [ ] Ollama dependency: skip-with-reason when Ollama is absent, and assert the model-unreachable frame still renders — the test must never pass **vacuously** by skipping everything silently
- [ ] Run the script and confirm all checks pass

## Open decisions

Two choices the brief left open. Recommendations given, both worth confirming before Phase 2.

| Decision | Recommendation | Reasoning |
|----------|----------------|-----------|
| Prompt-agent hosting — own tmux window vs. headless daemon-launched harness process | **Headless**, no window | The control pane exists on every window; its interpreter should be one session-wide process, not a window competing for a function key. Caveat worth pricing: harness agents today run *in* a pane with the harness TUI, so headless is a genuine change to how the harness is launched, not just configuration |
| Inject vs. interpret selection — toggle key vs. prompt prefix | **Explicit toggle with a persistent destination label** | A prefix fails silently when forgotten, and the two failure directions are both bad: a message meant for Edit gets executed as a graph op, or a graph op lands as text in Edit's composer. A visible mode that names its destination makes the mistake unavailable rather than merely unlikely |

## Status

Draft
