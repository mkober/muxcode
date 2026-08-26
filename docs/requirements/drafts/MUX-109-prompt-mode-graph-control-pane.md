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
- [ ] The prompt-agent runs **headless** — no tmux window, no harness TUI (no `--tui` flag)
- [ ] Prompt results are displayed **in the Prompt surface**, read from a session-global transcript on `refresh()`
- [ ] The surface **never blocks** on inference: with a prompt in flight, the pane still redraws and `Tab`/`Shift-Tab` still cycle away and back
- [ ] Three states are visually distinct — *working*, *finished*, and *model unreachable* — so a slow answer is never mistaken for a broken one
- [ ] A result raised on one window's pane is visible on every window's pane, since the transcript is session-global
- [ ] Reading the transcript happens in `refresh()`, not in a render function — the renderer stays pure and `--render-once` still works
- [ ] The footer advertises every key the surface accepts, including the inject/interpret toggle
- [ ] The input line names its destination at all times — which agent an injected prompt would reach, or that it will be interpreted — so the mode is readable without color
- [ ] A `prompt` bus role exists with its own inbox and is accepted by `IsKnownRole()`
- [ ] The prompt-agent runs on the local harness (Ollama); it never launches Claude Code, OpenCode, or Codex
- [ ] The model is chosen by configuration, not code — no model name is hardcoded in the role's path; the role inherits the global default `qwen3:4b` with `MUXCODE_PROMPT_MODEL` left unset (see [Model selection](#model-selection))
- [ ] **One model resident:** every local role — prompt included — runs `qwen3:4b` with no per-role pin, so no combination of active agents can put a second set of weights in memory
- [ ] Build, test, commit, and watch still complete their normal tasks on the smaller model, and the single-shot roles do not loop
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
- [ ] The installer reports Ollama as **required**, not optional, and an install missing it is visibly incomplete
- [ ] A user who declines the model pull still gets a working install — Prompt mode degrades to a stated "model not available" frame and the existing first-run auto-pull covers it
- [ ] If `-y/--yes` behaviour around the pull changes at all, `install.sh`'s usage text and `README.md` change with it — no silently broken promise
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

### Result display: headless agent, ambient surface

**Decided:** the prompt-agent runs headless (no tmux window, no harness TUI) and its results are
displayed in the control pane's Prompt surface.

That combination poses the one genuinely new design question in this spec: a headless process has no
pane, and the Prompt surface is a *renderer* inside the control-pane TUI — not a bus role with an
inbox. So how does an asynchronous reply reach it?

**Answer: a session-global transcript file the agent appends to and the surface reads on refresh.**
This is not a new pattern — it is how the research agent already works: `renderResearch`
(`bus/console.go:1536`) shows findings from `research-history.jsonl`. The prompt path mirrors it.

| Step | Mechanism |
|------|-----------|
| User types in the Prompt surface, presses Enter | Surface calls `bus.Send` to the `prompt` role and immediately returns to rendering |
| Headless harness agent consumes its inbox | Standard bus delivery; no special path |
| Agent executes the intent and records the outcome | Appends a record to a session-global transcript, e.g. `BusDir()/prompt-history.jsonl` |
| Surface shows the result | `refresh()` reads the transcript, exactly as it already calls `LoadRunListRows()` and `ListGraphTemplates()` per view (`tui/graph_ui.go:371`) |

Three properties this buys, each of which is a requirement rather than a nicety:

- **The render loop never blocks on a model call.** The surface writes a request and reads a file;
  it never waits on inference. A 4B model thinking for several seconds must leave the pane fully
  responsive, including `Tab` to another surface and back.
- **The transcript is session-global, so the result is ambient.** Every window's control pane shows
  the same Prompt surface content. Ask a question on the `edit` window, walk to `review`, and the
  answer is there. This follows the pane's existing model — surface selection is already shared
  session-wide via the `control-pane-surface` marker (`bus/control_pane.go:111`).
- **The renderer stays pure.** Reading the transcript happens in `refresh()`, which gathers the
  snapshot; the render function takes that snapshot and returns a string. This is the existing seam
  and the `--render-once` contract depends on it.

**In-flight state must be visible.** Between Enter and the reply there is a gap the user can see
into. The surface has to show that a prompt is working — and distinguish *working* from *finished*
from *the model is unreachable*. A surface that looks identical while thinking and while broken is
the failure mode to design out; on a 4B doing a Phase 5 composition, that gap will not be brief.

### Model selection

**Decided: `qwen3:4b`** (2.5 GB) — as the **single model for every local role**, replacing
`qwen2.5:7b` as the global default. The prompt role does *not* pin its own model; it inherits.

The governing constraint is **one model resident at a time**. Ollama keeps a model loaded while it
is in use, so two roles on two different models means two models in memory on an 18 GB machine
already running a full multi-agent session. A per-role pin would have created exactly that, and
would also have made a *required* feature add a second mandatory download. One model for all local
roles avoids both: one pull, one resident set of weights, and `MUXCODE_PROMPT_MODEL` left unset so
there is only one thing to change if it ever moves.

The 4B size is affordable for an always-available background interpreter whose common path is
classifying a short line of text into a closed set.

**The known risk, stated plainly:** 4B is the smallest model in the candidate set, and the `create`
intent (Phase 5) is the hardest thing asked of it. That risk is deliberately absorbed by design
rather than by model size — the closed intent set fails closed on an unparseable answer, and
validate-before-write means a weak composer produces *no file*, never a bad one. If Phase 5 proves
unusable, the escalation ladder below applies, and `qwen3:8b` is the first rung.

Three things constrain the choice:

| Constraint | Detail |
|------------|--------|
| **Tool calling is mandatory** | Stated twice in the code — `bus/ollama.go:24` and `harness/config.go:16`. This filters the field before quality is even considered |
| **Memory is shared, not dedicated** | Development machine is an Apple M3 Pro with **18 GB unified memory**, running a full multi-agent session (tmux, nvim, several Claude/OpenCode processes). At q4 a 4B costs ~2.5 GB, a 7–8B ~5 GB, a 14B ~9 GB. Spending half of shared RAM on a background interpreter whose purpose is saving keystrokes is a bad trade; a 32B is not viable at all. This weighs heavier than usual here because the interpreter is *always available*, not summoned |
| **The two jobs differ in difficulty** | Intents `launch`/`status`/`gates`/`approve`/`inject` are short-input, closed-output classification — easy. Intent `create` (Phase 5) is structured generation against global schema constraints (join sanity, capped-cycle rule, reachability, gate-before-commit) — genuinely hard for a small local model |

**Candidates worth measuring before settling.** Sizes and tool support below were read from
Ollama's own library pages (2026-08-26), not from secondary write-ups:

| Model | Size (Ollama) | Notes |
|-------|---------------|-------|
| `qwen2.5:7b` | ~4.7 GB | **Outgoing default** — being replaced. Worth keeping in view: it is the only class this repo has actually run, so it is the reference point any regression is measured against |
| `qwen3:8b` | 5.2 GB | **First escalation rung.** Same family as the chosen default, so a swap changes capability without changing prompt or tool-calling conventions. Explicit agent/tool-integration focus; widely rated the strongest small agent model |
| **`qwen3:4b`** | **2.5 GB** | **← Chosen — as the single global default for all local roles.** Smallest resident footprint of the credible tool-callers, which is what the one-model-at-a-time constraint optimises for. Intent classification is the easy half of the job; Phase 5 is the risk it carries |
| `granite4.1:8b` | 5.3 GB | **Apache 2.0** — the most permissive license of the group, which matters more than usual for a required dependency of an open-source project. Explicit function-calling support |
| `granite4.1:3b` | 2.1 GB | Smallest credible tool-caller found; same Apache 2.0 terms |

Two cautions:

- **Gemma 4** advertises native structured JSON output, which is exactly the Phase 5 need — but this
  repo already names it as a looper: `isSingleShotRole()` exists partly to stop "small models (e.g.
  gemma4, qwen2.5-coder)" re-running the same command ([`docs/agents.md`](../../agents.md) line 714).
  First-hand repo experience outranks a capability claim; trial it sceptically if at all.
- **Tool-calling specialists** (Llama-3-Groq-Tool-Use 8B and similar BFCL-tuned variants) score well
  on function-calling benchmarks, but a model tuned narrowly for emitting calls is not obviously good
  at *composing* a constrained JSON document — the Phase 5 half. Availability on Ollama was not
  confirmed here.

**No hardcoding required.** `roleModelEnvVar()` (`harness/config.go`) has a `default` arm building
`MUXCODE_{ROLE}_MODEL`, so a `prompt` role gets **`MUXCODE_PROMPT_MODEL`** with no code change.
Resolution is `MUXCODE_PROMPT_MODEL` → `MUXCODE_OLLAMA_MODEL` → default. Model choice is
configuration, and Phase 5 experience should be allowed to move it. Under the one-model decision
`MUXCODE_PROMPT_MODEL` stays **unset** — it exists as an escape hatch, not as the mechanism.

**Moving the global default has a blast radius outside this feature.** `qwen2.5:7b` is the default
in four places, and every local role inherits it:

| Site | Current |
|------|---------|
| `harness/config.go:28` | `OllamaModel: "qwen2.5:7b"` |
| `bus/ollama.go:35` | `Model: "qwen2.5:7b"` |
| `install.sh:580` | `OLLAMA_MODEL="${MUXCODE_OLLAMA_MODEL:-qwen2.5:7b}"` |
| [`docs/agents.md`](../../agents.md) line 280 | documents the default as `qwen2.5:7b` |

Two things an implementer must not discover the hard way:

- **Existing local roles get smaller, not just newer — accepted deliberately.** Any role running
  `MUXCODE_{ROLE}_CLI=local` — build, test, commit, watch — moves from a 7B to a 4B. **Decided: all
  of them take the 4B; no role keeps a pin**, because one exception reintroduces the second resident
  model the rule exists to prevent. This is a real capability reduction for roles tuned against 7B,
  and the single-shot machinery exists precisely because small models loop — so Phase 2 verifies
  those roles on the new model rather than assuming they survive it. A decision knowingly taken is
  not the same as one whose consequences went unmeasured.
- **The docs already misstate the default.** [`docs/agents.md`](../../agents.md) line 273 and
  [`README.md`](../../../README.md) line 160 both say `qwen2.5-coder:7b` while the code says
  `qwen2.5:7b`. Fix the drift in the same change rather than propagating a second inconsistent value.

**For Phase 5, the validator is load-bearing — not the model.** Validate-before-write with nothing
written on failure means a mediocre composer is *safe*, merely sometimes unhelpful. So if
composition quality disappoints, escalate in this order, and reach for a larger model last:

1. Constrain generation with Ollama's structured-output / JSON-schema mode instead of free generation
2. Template-fill from the 5 builtin graphs rather than composing from scratch
3. Step up to `qwen3:8b` — same family, so nothing but capability changes (+2.7 GB)
4. Only then consider anything larger, and only for the `create` intent — accepting the memory cost

Rungs 1 and 2 are worth trying *before* rung 3 even if 8B is affordable: a schema-constrained 4B
that cannot emit an invalid shape beats an unconstrained 8B that emits a plausible wrong one.

### Provisioning: Ollama moves from optional to required

Prompt mode is a **required feature**, not an opt-in one. That changes muxcode's dependency
posture, because today Ollama is explicitly optional and the system is designed to work without it.

**The current state of the machine**, checked 2026-08-26: Ollama is installed
(`/opt/homebrew/bin/ollama`) but has **zero models pulled** — `~/.ollama/models/blobs` is 0 B,
`manifests/` is empty, and the server is not running.

**The installer already has most of the machinery.** `install.sh:578`–`:602` checks for Ollama,
detects whether the model is pulled, and offers the pull — defaulting the model to `qwen2.5:7b`,
the same default the harness uses globally. What changes for a required feature is the **tier** and
the **model set** — not the mechanism:

| Aspect | Today | Needed for a required Prompt mode |
|--------|-------|-----------------------------------|
| Ollama's placement | "Optional components" section; reports `not found (optional — local LLM agents)` | Reported as required — either in the `PREREQS` table (`tool\|minimum\|required\|version-command`, where Ollama does not appear at all today) or as a distinctly-tiered check |
| Model pull under `-y/--yes` | **Explicitly declined.** Commented in `install.sh:588` as "the one thing `--yes` will not accept on the user's behalf", and promised in both `install.sh:17` and [`README.md`](../../../README.md) line 333 | **Unchanged — stays declined.** The tension between a required feature and a documented "we never download multiple GB unasked" promise is resolved in favour of the promise: the model arrives via the lazy first-run pull instead. At `qwen3:4b` that deferred cost is 2.5 GB |
| Which model is pulled | One model: `OLLAMA_MODEL="${MUXCODE_OLLAMA_MODEL:-qwen2.5:7b}"` (`install.sh:580`) | **Still one model — but a different one.** The default moves to `qwen3:4b`. Deliberately *not* two: one resident model is the governing constraint. Note the existing readiness check greps `${OLLAMA_MODEL%%:*}` (`install.sh:584`), so a machine holding `qwen2.5:7b` would still match a `qwen2.5`-prefixed probe — the check must compare the model actually required |
| Missing model at runtime | Falls back: `note "model auto-pulls on first local agent run"` (`install.sh:583`, `:598`) | Keep this. It is the existing, already-advertised degradation path and it makes a declined pull recoverable rather than fatal |

**Recommendation: require the dependency, keep the download consented.** Report Ollama as required
so an install without it is visibly incomplete, but leave the multi-GB pull behind the existing
consent gate and let the established lazy auto-pull on first agent run cover the declined case.
Prompt mode then ships enabled and works out of the box after first use, without a silent 4.7 GB
download and without breaking a promise the installer makes in writing twice.

**Decided: `--yes` keeps declining the pull** (see [Decisions](#decisions)). Scripted and CI installs
must not silently download gigabytes, and the promise is made in writing in three places. The
`qwen3:4b` choice softens the cost of deferring it anyway — 2.5 GB on first agent run, not 5+.

### Key files

| File | Change |
|------|--------|
| `tui/graph_ui.go` | New Prompt view: `graphSurfaces` entry, `surfaceName()`, tab bar, renderer, key handler; generalise the intent line editor |
| `tui/graph_ui_test.go` | Frame tests: empty state, clamping, footer keys, destination label, unreachable-model state |
| `bus/control_pane.go` | `controlPaneCommand()` case for `prompt`; unknown-surface degradation unchanged |
| `bus/graph.go` | Graph definition **write** helper (`MkdirAll` + atomic write, validate-before-write); scope-aware target selection |
| `bus/config.go` | `prompt` role: `KnownRoles`, inbox, window/pane mapping; transcript path helper (`BusDir()/prompt-history.jsonl`) beside the existing helpers, purged on session re-init |
| `bus/launch.go` / daemon | Launch the prompt-agent headless — no window, no `--tui`; supervise it like the other long-lived processes |
| `bus/profile.go` | Narrow `prompt` tool profile — `muxcode graph *`, graph-dir read/write, injection; nothing else |
| `bus/health.go` | Confirm `LocalLLMRoles()` picks up the role from `MUXCODE_PROMPT_CLI=local` (env-driven — may need no change) |
| `agents/harness/prompt-agent.md` | New harness agent definition — short, directive, closed intent set |
| `cmd/graph.go` | Reuse for create/validate paths if a subcommand seam is cleaner than a library call |
| `harness/config.go:28`, `bus/ollama.go:35` | Global default `qwen2.5:7b` → `qwen3:4b` |
| `install.sh` | Re-tier Ollama as required (`PREREQS` table or a distinct tier); default `qwen3:4b` (`:580`); tighten the prefix-grep readiness check (`:584`); keep the pull consent-gated at `:588`; keep the lazy-pull fallback |
| `README.md`, `docs/agents.md` | Ollama's optional/required status, the prerequisite list, the `--yes` row if its behaviour changes, and the stale `qwen2.5-coder:7b` default (`README.md:160`, `agents.md:273`) |
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

- [x] Confirm `ResolveGraphTemplate()` / `ListGraphTemplates()` cover project, user, and builtin (expected: already true — record the evidence rather than re-implementing)
- [x] Confirm `graph run`, `graph validate`, `graph list`, and the TUI launcher all resolve through those helpers
- [x] Add a graph-definition write helper: `MkdirAll` the target dir, validate, then write atomically
- [x] Unit test: writing to a fresh checkout with no `.muxcode/graphs/` succeeds and creates the directory
- [x] Unit test: a definition failing `Validate()` leaves **no file behind**

**Phase 1 complete.** `WriteGraphDefinition(g, scope)` (`bus/graph.go:613`) validates *before* touching
the filesystem, so a rejected definition leaves neither file nor directory; writes go through
`atomicWriteFile` (tmp + rename), extracted in `graph_run.go:139` so the template path and the run
store share one crash-safety implementation. Scope constants `GraphScopeProject`/`GraphScopeUser`
with `graphScopeDir()` resolve the two writable tiers. Five tests added, and they are not vacuous:
the fresh-checkout case `chdir`s into a `t.TempDir()` so `.muxcode/graphs/` genuinely does not exist,
asserts no `.tmp` residue, and **round-trips the written file back through `ResolveGraphTemplate`**
asserting `source == "project"` — wiring checked at both ends. The failure case asserts the graph
*directory* was never created, which is strictly stronger than "no file" and pins validation ordering.
An unsafe-name guard (`/`, `\`, leading `.`) was added beyond what this phase asked for.

> **Not yet wired.** `WriteGraphDefinition` currently has **no non-test caller** — its consumer is the
> Phase 5 create flow. Tracked here so it is not mistaken for dead code later, the way
> `ComputeReadyNodes` was in [MUX-014](../completed/MUX-014-graph-agent-orchestrator.md).

### Phase 2: Prompt bus role and harness agent

- [ ] **Installer:** re-tier Ollama from "Optional components" to required, so an install without it reports as incomplete rather than as fine
- [ ] **Global default:** move `qwen2.5:7b` → `qwen3:4b` at `harness/config.go:28`, `bus/ollama.go:35`, and `install.sh:580`; fix the `qwen2.5-coder:7b` drift in `docs/agents.md:273` and `README.md:160` in the same change
- [ ] **Installer:** tighten the readiness check at `install.sh:584` — the `${OLLAMA_MODEL%%:*}` prefix grep must not report "ready" on a differently-sized sibling of the required model
- [ ] **Regression check on the roles that inherit the change:** exercise build, test, commit, and watch under `MUXCODE_{ROLE}_CLI=local` on `qwen3:4b` and confirm each still completes its normal task
- [ ] Specifically confirm the **single-shot** roles (build, test) do not loop on the smaller model — `isSingleShotRole()` exists because small models re-run commands, and 4B is smaller than what prompted it
- [ ] Confirm no role pins a model of its own, so exactly one set of weights can ever be resident
- [ ] **Installer:** keep the multi-GB pull behind its existing consent gate; preserve the lazy "auto-pulls on first local agent run" fallback for the declined case
- [ ] **Installer:** update the `-y/--yes` usage text and [`README.md`](../../../README.md) in the *same* change if the pull's non-interactive behaviour is altered at all
- [ ] Locally: `ollama pull <model>` and confirm `ollama serve` is reachable — nothing below is exercisable until the model store is non-empty (see [Model selection](#model-selection))
- [ ] Add the `prompt` role — `KnownRoles`, inbox path, window/pane mapping
- [ ] Add `agents/harness/prompt-agent.md` — short, directive, closed intent set
- [ ] Launch the agent **headless**: no window, no `--tui` (the harness's default path, `LogSink`); decide and document who owns the process lifecycle — daemon-supervised alongside the other long-lived processes is the natural fit
- [ ] Add the transcript path helper (`BusDir()/prompt-history.jsonl`) in `bus/config.go` beside the other path helpers, and purge it on session re-init like other per-session state
- [ ] Add the narrow `prompt` tool profile
- [ ] Confirm `LocalLLMRoles()` picks the role up from `MUXCODE_PROMPT_CLI=local`; extend only if it does not
- [ ] Confirm `MUXCODE_PROMPT_MODEL` resolves via `roleModelEnvVar()`'s `default` arm — expected to need no code change
- [ ] Negative-control test: the profile denies a repo write and denies a git command

### Phase 3: Prompt surface in the control pane

- [ ] Append the Prompt view to `graphSurfaces`; add `surfaceName()` and tab-bar entries
- [ ] Add the `prompt` case to `controlPaneCommand()`; unknown surfaces still degrade to the run list
- [ ] Generalise the `viewGraphIntent` line editor for reuse; keep Escape/`ESC [ Z` disambiguation intact
- [ ] Render: header, tab bar, input line with destination label, reply/history body, footer
- [ ] Read the transcript in `refresh()` (never in a render function) and render the latest exchanges, oldest-clamped to the pane height
- [ ] Distinct *working* / *finished* / *model-unreachable* states, each readable without color
- [ ] Verify the pane still redraws and cycles surfaces while a prompt is in flight — no blocking on inference
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
- [ ] Guard the skip path with a **coverage floor**: assert a minimum number of non-skipped checks, so a machine with no model pulled reports "skipped" loudly rather than green. This is not hypothetical — it is the current state of the development machine, so the skip branch will be the *first* branch exercised
- [ ] Run the script and confirm all checks pass

## Decisions

Every open question raised while drafting this spec has been settled. Resolved rows are struck
through rather than deleted, so an implementer can see what was weighed — and, more usefully, what
the rejected option would have cost.

| Decision | Resolution | Reasoning |
|----------|----------------|-----------|
| ~~Prompt-agent hosting — own tmux window vs. headless~~ | **Decided: headless, no window; results render in the Prompt surface** | The control pane exists on every window, so its interpreter should be one session-wide process rather than a window competing for a function key. See [Result display](#result-display-headless-agent-ambient-surface) for how an async reply reaches a renderer. **Correction to an earlier draft of this spec:** headless is not a change to how the harness launches — it is the harness's *default*. `--tui` is opt-in (`main.go:47`) and the no-flag path uses `LogSink`, whose own comment reads "headless mode" (`harness/events.go:48`) |
| ~~Inject vs. interpret selection — toggle key vs. prompt prefix~~ | **Decided: explicit toggle with a persistent destination label** | A prefix fails silently when forgotten, and both failure directions are bad: a message meant for Edit gets executed as a graph op, or a graph op lands as text in Edit's composer. A visible mode that names its destination makes the mistake unavailable rather than merely unlikely |
| ~~Should `-y/--yes` now accept the multi-GB model pull?~~ | **Decided: no — keep it declined; the lazy first-run pull covers it** | `install.sh:588` calls this "the one thing `--yes` will not accept on the user's behalf", and `install.sh:17` plus [`README.md`](../../../README.md) line 333 both promise it in writing. Scripted and CI installs must not silently download gigabytes. The `qwen3:4b` decision softens the cost anyway: 2.5 GB on first run rather than 5+ |
| ~~Does the prompt role share the global default model, or pin its own?~~ | **Decided: share — the global default moves to `qwen3:4b`** | Resolved in [Model selection](#model-selection). Only one model may be resident at a time, so a per-role pin was rejected: it would put two models in memory whenever both roles were active, and add a second mandatory download for a required feature. `MUXCODE_PROMPT_MODEL` stays unset |
| ~~Do existing local roles accept the 4B, or keep a 7B pin?~~ | **Decided: accept the 4B everywhere — no per-role pins** | One model resident, for every local role, with no exceptions. This keeps the memory rule absolute rather than eroding it one pin at a time. The cost is accepted knowingly: build/test/commit/watch were tuned against a 7B and now run a 4B, so Phase 2 carries a regression check rather than an assumption |

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-109-prompt-mode-graph-control-pane | 9m | 2026-08-26 15:56 |

## Status

In Progress
