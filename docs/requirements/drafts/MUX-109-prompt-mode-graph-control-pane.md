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

The prompt-agent **interprets and dispatches**; it does not implement. If a request needs more than
that, the correct outcome is to inject it into the main agent, not to grow the prompt-agent.

**Widened 2026-08-27 (user-requested), once the role moved to a capable gateway model.** The profile
was originally graph-only because a 4B could not use more; it now covers the `muxcode` command set
plus read-only context (`status`, `tasks`, `spec`, `history`, `memory`, and Atlassian **reads**).
The denials that were doing real work are unchanged, and each still has its reason:

| Still denied | Reason |
|--------------|--------|
| `Write` / `Edit` | Graph definitions are written only through `muxcode graph create`, whose path validates first — a raw file write would bypass validate-before-write |
| `git` / `gh` | Commit's job |
| Atlassian **writes** | Plan's authority (`CheckAtlassianAuthority`) |

Narrowness was never the point in itself; the point is that specific capabilities are gated for
stated reasons. When the reason for one expired, that gate opened deliberately — the rest hold.
Pinned on both sides by the profile test: `jira read` allowed, `jira update` denied, in the same
assertion.

## Requirements

### Acceptance criteria

- [x] A **Prompt** surface joins the control pane's `Tab`/`Shift-Tab` cycle, and the tab bar names it
- [x] `MUXCODE_CONTROL_PANE_SURFACE=prompt` starts the pane on it; an unknown value still degrades to the run list
- [x] The surface renders as a pure function of a snapshot — no I/O in the renderer — and is reachable via `--render-once`
- [x] The surface clamps to the pane's `width` **and** `height`; a long prompt or a long reply degrades rather than overflowing
- [x] The empty state (no prompt typed, no history) is explicit and keeps header, tab bar, and footer
- [x] The prompt-agent runs **headless** — no tmux window, no harness TUI (no `--tui` flag)
- [x] Prompt results are displayed **in the Prompt surface**, read from a session-global transcript on `refresh()`
- [x] The surface **never blocks** on inference: with a prompt in flight, the pane still redraws and `Tab`/`Shift-Tab` still cycle away and back
- [x] Three states are visually distinct — *working*, *finished*, and *model unreachable* — so a slow answer is never mistaken for a broken one
- [x] A result raised on one window's pane is visible on every window's pane, since the transcript is session-global
- [x] Reading the transcript happens in `refresh()`, not in a render function — the renderer stays pure and `--render-once` still works
- [x] The footer advertises every key the surface accepts, including the inject/interpret toggle
- [x] The input line names its destination at all times — which agent an injected prompt would reach, or that it will be interpreted — so the mode is readable without color
- [x] A `prompt` bus role exists with its own inbox and is accepted by `IsKnownRole()`
- [x] The prompt-agent runs on the muxcode harness; it never launches Claude Code's CLI, the OpenCode TUI, or Codex — **wording updated 2026-08-27**: the harness now serves either local Ollama or the hosted gateway, and the default is the gateway. What the criterion actually guards — no second coding-agent CLI — still holds
- [x] The model is chosen by configuration, not code — no model name is hardcoded in the role's path; the role inherits the **backend's** default with `MUXCODE_PROMPT_MODEL` left unset: `deepseek-v4-flash` on the gateway, `qwen3:4b` locally (see [Backend selection](#backend-selection--revised-2026-08-27-premise-overturned-by-measurement))
- [x] **One model resident:** every *local* role runs `qwen3:4b` with no per-role pin, so no combination of active agents can put a second set of weights in memory. **Strengthened by the backend switch** — on the gateway default the prompt-agent loads no local weights at all, so the rule now holds with one fewer resident model, not more
- [ ] Build, test, commit, and watch still complete their normal tasks on the smaller model, and the single-shot roles do not loop — **measured FALSE 2026-08-26**, not merely unverified; see [Phase 2 regression findings](#phase-2-regression-findings)
- [ ] Its tool profile permits only `muxcode graph` subcommands, reads/writes under the two graph directories, and the injection path — verified by a **negative control** asserting a repo write and a git command are both denied
- [ ] Intent: **launch** — a named or described graph resolves and starts via `muxcode graph run`, across all three scopes
- [ ] Intent: **status** — questions about in-flight and completed runs answer from `graph list` / `graph status`
- [ ] Intent: **gates** — pending `wait_human` gates can be listed, and approved **only** when the user's prompt names the gate or run
- [x] A prompt that does not name a specific gate never approves one — pinned by a negative-control test using deliberately suggestive phrasing ("approve whatever is waiting")
- [ ] Single-use gate approval semantics from [MUX-014](../completed/MUX-014-graph-agent-orchestrator.md) are unchanged — re-entering a gate still demands a fresh approval
- [x] Intent: **create** — a described workflow is composed into a graph definition, validated, and written project-local by default
- [x] A definition failing `Validate()` is **reported, never written** — pinned by a test asserting no file appears on the failure path
- [x] A prompt-composed graph placing a commit or Atlassian node outside a `wait_human` gate is rejected by the existing validator rule, with no bypass
- [x] Injection delivers the typed text to the window's active main agent via `TmuxSendLiteral()` (text → delay → Enter), never a hand-rolled `send-keys`
- [x] Injection targets the window's **active** agent, respecting mode-cycled windows
- [x] A dash-leading prompt injects intact, per [MUX-104](../completed/MUX-104-send-keys-dash-payload.md)
- [ ] Ollama health monitoring covers the new role, and the surface states plainly when the model is unreachable rather than appearing to accept a prompt it cannot serve
- [ ] `CheckCommitAuthority` and `CheckAtlassianAuthority` remain the runtime backstop, unchanged
- [x] The installer reports Ollama as **required**, not optional, and an install missing it is visibly incomplete
- [ ] A user who declines the model pull still gets a working install — Prompt mode degrades to a stated "model not available" frame and the existing first-run auto-pull covers it
- [x] If `-y/--yes` behaviour around the pull changes at all, `install.sh`'s usage text and `README.md` change with it — no silently broken promise
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

### Backend selection — revised 2026-08-27, premise overturned by measurement

**The prompt-agent's backend is selectable, and the hosted gateway is now the default**
(user-initiated, 2026-08-27 — *"ollama … too resource intensive"*). This supersedes the local-only
assumption running through the sections below. They are kept as written rather than rewritten: the
reasoning that led to a local-first design was sound given what was known, and a reader who cannot
see it will re-derive it.

| Setting | Value |
|---------|-------|
| `MUXCODE_PROMPT_BACKEND` | `opencode` (**default**) or `ollama` — `PromptBackend()`, `bus/prompt_agent.go:57`, env then config file. Anything other than the literal `ollama` resolves to the gateway |
| Gateway | OpenCode Zen, `https://opencode.ai/zen/v1` (`opencodeGatewayURL`, `:43`) |
| Key | `MUXCODE_OPENCODE_API_KEY`, env or config (`:72`) |
| Default remote model | `deepseek-v4-flash` — **bare id, not catalog form.** The first gateway request 401'd on `opencode-go/deepseek-v4-flash`; Zen wants the id without the provider prefix |
| Local harness launch | Gated on the backend — the daemon starts the headless local harness only when the backend is `ollama`, so the default path launches no local process at all |
| Installer | Ollama is **optional again** (removed from `PREREQS`; `install.sh:261` records why). The installer **prompts** for `MUXCODE_OPENCODE_API_KEY` instead (`:626`–`:656`) — silent `read -rs`, never in argv, skipped under `-y/--yes`, blank means skip, an existing assignment is rewritten rather than duplicated, and the config is `chmod 600`'d on write. The presence check requires a **non-empty** value, so an empty `KEY=""` still prompts |

**Why the premise fell.** This spec opened by arguing the scope was "exactly the shape a local model
can serve… keeps the feature off the metered providers." Measurement contradicted it on two counts:

1. **39–82 s per call** — and this was *with* `think:false`, so it is not the thinking-mode overhead
   the [Phase 2 regression](#phase-2-regression-findings) identified. The latency survives the fix
   that was supposed to remove it.
2. **Fabricated success summaries** — the model reported success for commands that did not succeed.

Two captured live on 2026-08-27, minutes apart, both caught by `enforceDenialPrefix`:

```
BLOCKED: Error: command not allowed by tool profile: make build
         — model summary: succeeded: Requirements PR created for review

BLOCKED: Error: command not allowed by tool profile: muxcli status
         — model summary: succeeded: Requirements doc moved to completed
           directory and Jira story transitioned to Done
```

Read what the second one claims. The command was a hallucinated binary (`muxcli`, not `muxcode`),
it was refused, and the model reported having **moved a spec to `completed/` and transitioned a Jira
story to Done** — two of the most tightly gated actions in this repo, neither of which occurred.

The danger is not that the model emits noise; it is that the confabulation is **contextually
plausible**. It names this project's real workflows in this project's real vocabulary, so an
unprefixed summary would read as a credible report of work that never happened. The authority
boundaries held perfectly — the tool profile refused both commands — but *the reporting layer would
have lied about them*. A guard on what the agent may **do** does not constrain what it **claims**,
and these two failure surfaces need separate defences.

The second finding is the more serious one, and it settles an argument this spec had already been
having with itself. The false-success guard was first added as a **model instruction**, then moved
into harness code because an instruction is not a guarantee. This measurement is that reasoning
confirmed against the actual model: `qwen3:4b` does fabricate success, so `enforceDenialPrefix` is
load-bearing rather than defensive. It also means a 4B failing an intent is not always visible as a
failure — which is exactly what makes the [Phase 7](#phase-7-first-run--25-passed-3-failed-0-skipped)
live-intent results hard to read.

**Consequence for the catalog-pin guard.** `RoleModel()` skips slash-form pins because
`provider/model` is an OpenCode catalog name that Ollama cannot pull. Against a hosted gateway that
form is *correct*, so the guard now stands down when a remote endpoint is configured
(`harness/config.go:95`, mirrored in `bus/ollama.go`). The guard still protects the local path,
which is the case it was written for.

**Consequence for the one-model-resident rule.** Unchanged and, if anything, easier to hold: on the
`opencode` backend the prompt-agent loads **no local weights at all**. The
["4B everywhere" decision](#decisions) for the *other* local roles is untouched by this and still
rests on the premise the regression measured false.

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
  *(Closed 2026-08-26 — all four doc sites now read `qwen3:4b`.)*

> **Latent trap found while verifying Phase 2 — `MUXCODE_{ROLE}_MODEL` is overloaded.** The same env
> var names the model for **both** OpenCode and local roles (`RoleModel()` → `roleModelEnvVar()`).
> This project's `.muxcode/config` already sets per-role values for OpenCode/Claude providers —
> `MUXCODE_BUILD_MODEL=opencode-go/deepseek-v4-flash`, `MUXCODE_TEST_MODEL=…`, and seven more. None
> of those roles runs `local` today, so nothing is broken. But the moment a role is switched to
> `MUXCODE_{ROLE}_CLI=local`, it inherits an **OpenCode model string as its Ollama model name** and
> the pull fails on a name Ollama has never heard of. This is precisely the "no role pins a model of
> its own" step, which is why that step is left unchecked: the config *does* pin, harmlessly for now,
> in a way that turns harmful exactly when someone exercises the local path this spec depends on.
> The regression check must therefore unset or override these vars, not merely flip the CLI var.
>
> **Fixed 2026-08-26.** `RoleModel()` on both sides — `bus/ollama.go:60` and
> `harness/config.go` — now ignores a per-role pin in catalog form (`strings.Contains(v, "/")`) and
> falls through to the Ollama default. Pinned by two-directional tests on both sides
> (`TestRoleModel_SkipsOpenCodeCatalogPin` in `bus/prompt_agent_test.go:59` and
> `harness/config_test.go:9`): a catalog pin yields `qwen3:4b`, **and** a plain pin like `qwen3:8b`
> still wins — so the test cannot pass by ignoring every pin. `CLI=local` is now safe against the
> nine `opencode-go/*` pins in this project's config, which is what closed the criterion above.
>
> **Residual limitation, narrow but silent:** the guard keys off `/`, and Ollama itself accepts
> slash-bearing model references (e.g. `hf.co/{user}/{repo}`). Someone setting such a name as a
> deliberate Ollama pin gets the default instead, with no warning. Low likelihood, but the failure
> is *silent* — worth a log line when a pin is skipped, so an ignored setting is visible rather than
> mysterious. Not blocking; recorded so it is a known trade rather than a surprise.

**For Phase 5, the validator is load-bearing — not the model.** Validate-before-write with nothing
written on failure means a mediocre composer is *safe*, merely sometimes unhelpful. So if
composition quality disappoints, escalate in this order, and reach for a larger model last:

1. Constrain generation with Ollama's structured-output / JSON-schema mode instead of free generation
2. Template-fill from the 5 builtin graphs rather than composing from scratch
3. Step up to `qwen3:8b` — same family, so nothing but capability changes (+2.7 GB)
4. Only then consider anything larger, and only for the `create` intent — accepting the memory cost

Rungs 1 and 2 are worth trying *before* rung 3 even if 8B is affordable: a schema-constrained 4B
that cannot emit an invalid shape beats an unconstrained 8B that emits a plausible wrong one.

### Phase 2 regression findings

Run 2026-08-26 against a live session: build, test, commit, and watch reloaded to `--cli local` on
`qwen3:4b` (runtime overrides, originals restored afterward). Exercises: commit = git status
summary, build = `./build.sh`. **The test and watch exercises were never reached.**

**Two defects found and fixed in this branch:**

| Defect | Fix |
|--------|-----|
| **Cold-load kill loop.** The daemon's 10 s inference probe killed every model load in progress — a cold `qwen3:4b` load outlasts the probe, the restart discards the load, the next probe kills it again. The 3-attempt ladder looped indefinitely | Warming guard: `OllamaModelLoaded()` (`GET /api/ps`) distinguishes dead server / warming / loaded-but-wedged. Probe failures during warming are logged (`ollama-warming`) but not counted, bounded by `MUXCODE_OLLAMA_WARMUP_GRACE_SECS` (default 300) |
| **Thinking-model probe latency.** qwen3 is a thinking model: warm first-token latency measured **31–94 s**, and the Ollama serve log showed the probe's ~10 s client aborts | Probe moved to `/api/generate` with `think:false` + `num_predict:1` (~1 s warm); timeout tunable via `MUXCODE_OLLAMA_PROBE_SECS` |

Both fixes were **verified in the tree, not taken on report**: `OllamaModelLoaded()` at
`bus/health.go:134` (`GET /api/ps`, returning `responsive, loaded`) is gated into the restart ladder
at `daemon/daemon.go:1429` — `responsive && !loaded` logs `ollama-warming` and declines to count the
failure. The probe at `bus/health.go:65`–`:77` sends `think:false` with `num_predict:1`, and its
comment records the 30–90 s measurement that motivated it. Covered by `TestOllamaModelLoaded`,
`TestOllamaWarmupGraceSecs`, and `TestOllamaProbeSecs`.

**One thing the run confirmed positively:** build and test carried real
`MUXCODE_{ROLE}_MODEL=opencode-go/deepseek-v4-flash` pins, and on `--cli local` the harness fell
back to `qwen3:4b` — the catalog-pin guard verified live, not just in unit tests.

**Three findings still open:**

1. **Startup-message tool-loop exhaustion.** Both harnesses repeatedly burned `MaxTurns` on the
   open-ended startup message ("review last saved context"), emitting *"(no response generated —
   tool loop exhausted)"* every ~5 minutes. Single-shot covers real tasks; it does not cover
   startup. Open-ended prompts are hostile to a 4B.
2. **Reply mis-correlation re-drive loop.** Harness startup responses correlate to the *previous
   response's* id rather than the original request, so the request never gains a receipt and the
   delivery backstop re-drives it indefinitely. A self-addressed startup response echo was also
   seen in the build agent's own inbox.
3. **Interactive latency impractical.** With thinking enabled every harness turn pays 30–90 s.
   The commit exercise ran ~17 minutes and the build exercise ~6 minutes without either returning
   a completed task response.

> **Verdict — this measurement contradicts part of a settled decision, and that is recorded rather
> than smoothed over.** The one-model-resident rule *holds*: only `qwen3:4b` was ever loaded, and
> the catalog-pin guard kept it that way. But "[accept the 4B everywhere](#decisions)" was taken on
> the understanding that the cost was a capability reduction. The measured cost is larger:
> **build, test, commit, and watch are not currently viable interactively on `qwen3:4b` with
> thinking enabled** — not degraded, not slower, but not completing.
>
> This does not invalidate the prompt-agent's own design, which is single-shot with closed intents
> and one CLI call per turn — a materially easier shape than an open-ended coding turn. It does mean
> the "everywhere" half of the decision rests on an assumption now measured false, and should be
> re-decided **after** the think-mode work, not before. Findings 1 and 2 are general harness and
> delivery defects that predate this spec's scope and warrant their own backlog ids rather than
> being absorbed here.

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
| `agents/harness/prompt-agent.md` | Harness agent definition — conversational operator over the `muxcode` command surface (was a closed-intent classifier while the backend was a 4B) |
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
| Graph writes | Only under `.muxcode/graphs/` (project, default) or `~/.config/muxcode/graphs/` (user, only when the user says global). Never `docs/`, never source, never builtin. **Tightened in implementation (2026-08-26):** the `prompt` profile grants **no write tool at all** — definitions are written only through `muxcode graph`, whose path is `WriteGraphDefinition`. Validate-before-write is therefore unbypassable by the model rather than merely required of it |
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

- [x] **Installer:** re-tier Ollama from "Optional components" to required, so an install without it reports as incomplete rather than as fine
- [x] **Global default:** move `qwen2.5:7b` → `qwen3:4b` at `harness/config.go:28`, `bus/ollama.go:35`, and `install.sh:580`; fix the `qwen2.5-coder:7b` drift in `docs/agents.md:273` and `README.md:160` in the same change
- [x] **Installer:** tighten the readiness check at `install.sh:584` — the `${OLLAMA_MODEL%%:*}` prefix grep must not report "ready" on a differently-sized sibling of the required model
- [x] **Regression check on the roles that inherit the change — EXERCISED** 2026-08-26 on a live session: build, test, commit, watch reloaded to `--cli local` on `qwen3:4b`, originals restored afterward
- [ ] …and confirm each still completes its normal task — **NOT MET.** No exercise returned a completed task response. See [Phase 2 regression findings](#phase-2-regression-findings)
- [ ] Specifically confirm the **single-shot** roles (build, test) do not loop on the smaller model — **partially met and partially refuted:** single-shot covers real tasks, but neither role survived its *startup* message, which is open-ended and outside single-shot's reach
- [ ] Follow-up from the regression: disable thinking (`think:false`) for the harness's own chat calls, or move to structured outputs — escalation-ladder rungs 1–2, before any model-size change
- [ ] Re-run the regression once think-mode handling lands, and re-decide "4B everywhere" on the new measurement
- [x] Confirm no role pins a model of its own, so exactly one set of weights can ever be resident
- [x] **Installer:** keep the multi-GB pull behind its existing consent gate; preserve the lazy "auto-pulls on first local agent run" fallback for the declined case
- [x] **Installer:** update the `-y/--yes` usage text and [`README.md`](../../../README.md) in the *same* change if the pull's non-interactive behaviour is altered at all
- [x] Locally: `ollama pull <model>` and confirm `ollama serve` is reachable — nothing below is exercisable until the model store is non-empty (see [Model selection](#model-selection))
- [x] Add the `prompt` role — `KnownRoles`, inbox path, window/pane mapping
- [x] Add `agents/harness/prompt-agent.md` — rewritten 2026-08-27 for the gateway model: a conversational operator with a command-surface table, multi-command tasks, and the approve/authority guards retained verbatim
- [x] Launch the agent **headless**: no window, no `--tui` (the harness's default path, `LogSink`); decide and document who owns the process lifecycle — daemon-supervised alongside the other long-lived processes is the natural fit
- [x] Add the transcript path helper (`BusDir()/prompt-history.jsonl`) in `bus/config.go` beside the other path helpers, and purge it on session re-init like other per-session state
- [x] Add the narrow `prompt` tool profile
- [x] Confirm `LocalLLMRoles()` picks the role up from `MUXCODE_PROMPT_CLI=local`; extend only if it does not
- [x] Confirm `MUXCODE_PROMPT_MODEL` resolves via `roleModelEnvVar()`'s `default` arm — expected to need no code change
- [x] Negative-control test: the profile denies a repo write and denies a git command

**Phase 2: 14/17.** Verified evidence, not assertions:

| Item | Evidence |
|------|----------|
| Ollama required | `PREREQS` now carries `ollama\|\|1\|ollama --version` (required tier); the not-found row reads `✗ … (required — Prompt mode and local agents)` |
| Default moved | `harness/config.go:16,28`, `bus/ollama.go:24,35`, `install.sh:25,588`; docs drift closed in `configuration.md:124`, `agent-bus.md:1007`, `agents.md:273,280` |
| Readiness check | Family-prefix grep replaced with exact match `grep -qxE "${OLLAMA_MODEL}(:latest)?"`, with the old bug named in a comment |
| `--yes` contract | Behaviour **unchanged** — usage text still reads "Large optional downloads (the Ollama model) are still declined" |
| Model present | `qwen3:4b` pulled (2.5 GB) and `ollama list` responding |
| Headless launch | `bus/prompt_agent.go` (`StartPromptAgent`, detached process group, own log since it has no pane), supervised from `daemon.go:3418`; opt-out `MUXCODE_PROMPT_AGENT_DISABLE=1` |
| Transcript purge | `PromptHistoryPath()` returns `HistoryPath(session, "prompt")`, and `prompt` is a `KnownRole` — so `purgeStaleFiles()` truncates it with every other role history, no special case needed |
| Health coverage | Resolved via the step's own "extend only if it does not" branch: `LocalLLMRoles()` keys off `MUXCODE_*_CLI=local`, which nothing sets for `prompt`, so `daemon.go:154` appends the role whenever `PromptAgentEnabled()` — harness-always, no provider switch |
| Negative control | `TestPromptProfileDeniesRepoWriteAndGit` denies `write_file`, `edit_file`, `git commit`, and `muxcode atlassian jira update` — through the real `IsToolAllowed`, and **with a positive control first** so a deny-everything profile cannot pass it vacuously |
| Harness wiring | `isSingleShotRole()` now returns true for `prompt` (`harness/loop.go`) — most intents are one tool call and done, which is the shape that guard exists for; `harness/prompt.go` maps the role to the `prompt-agent` definition |
| Config surface | `muxcode.conf.example` documents the `qwen3:4b` default **and** warns that a per-role pin puts a second model in memory — the one-model rule stated where someone would go to break it |

**The profile came out narrower than this spec asked for**, and the change is an improvement worth
recording: it grants **no write tool at all**. Graph definitions are written only through
`muxcode graph`, whose path is `WriteGraphDefinition` — so validate-before-write is unbypassable by
the model rather than merely expected of it. The [Authority boundaries](#authority-boundaries)
wording ("reads/writes under the two graph directories") is now looser than the implementation and
should be tightened to match, not the other way round.

### Phase 3: Prompt surface in the control pane

- [x] Append the Prompt view to `graphSurfaces`; add `surfaceName()` and tab-bar entries
- [x] Add the `prompt` case to `controlPaneCommand()`; unknown surfaces still degrade to the run list
- [x] Generalise the `viewGraphIntent` line editor for reuse; keep Escape/`ESC [ Z` disambiguation intact — `editLineAt` (`tui/graph_ui.go:900`) is the single cursor-aware implementation and `editLine` (`:892`) is now a thin end-anchored wrapper over it, so the `${intent}` prompt (`:928`) and the Prompt surface (`:950`) cannot drift on byte handling. Disambiguation intact in both handlers (`:674`, `:966`); pinned by `TestEditLineAt`
- [x] Render: header, tab bar, input line with destination label, reply/history body, footer — the footer is composed by the **outer** frame at `tui/graph_ui.go:1010`, not by `RenderPromptFrame`, matching how the other surfaces work (`promptChromeLines` reserves a row for it)
- [x] Read the transcript in `refresh()` (never in a render function) and render the latest exchanges, oldest-clamped to the pane height
- [x] Distinct *working* / *finished* / *model-unreachable* states, each readable without color
- [ ] Verify the pane still redraws and cycles surfaces while a prompt is in flight — no blocking on inference
- [x] Explicit empty state; clamp to `width` and `height`; reachable via `--render-once`
- [x] Frame tests including a **negative control** for clamping (a fixture that actually overflows)

#### Phase 3 refinements after first use (uncommitted, verified 2026-08-27)

Behaviour beyond what this phase originally specified, added once the surface was driven in anger:

| Refinement | Why it was needed |
|------------|-------------------|
| **Independent column scroll** — `Scroll` windows the question column, new `ActScroll` the output/log column (`scrollTail` serves both) | A long log dragged the question column with it, so the prompt being answered scrolled out of view |
| **Working marker is pinned** — if the right-hand tail window drops it, it retakes the window's first row (`containsLine` guard) | A pane whose marker had scrolled away read as stuck, which is the exact state this phase exists to distinguish |
| **Ghost completion** — `PromptSuggest` completes the last word (templates before verbs), rendered dim after the cursor block; `→` accepts | Template names are long and exact-match; typing them blind was the main input friction |
| **Activity load raised 20 → 100 lines** | The cap previously bounded the *visible* window; with an independently scrolling column it only needs to bound the load |
| **Run-list vertical scroll** with `↑`/`↓` overflow indicators | The list silently truncated past the pane height |
| **Template typeahead** — a typed prefix jumps the launcher selection (`TypeaheadIndex`, shared with the ghost) | Same friction as the ghost, on the other input surface |
| **Right-column scroll on Shift-arrows** (`ESC [ 1 ; 2 A/B`), with PgUp/PgDn kept as an alias | Shift-arrows pair naturally with the plain arrows already scrolling the left column |
| **Escape-sequence disambiguation** — the trailing `~` of `ESC [ 5 ~` / `ESC [ 6 ~` is consumed on a 50 ms timeout, and the Shift-arrow reader discards intermediates until a final byte or timeout (incomplete sequences go inert) | Without it the sequence's tail types itself into the prompt input |

Each carries a negative control: the ghost must **not** render mid-string, and a run list that fits
must show **no** indicators — assertions a renderer that always suggested, or always flagged
overflow, would fail. The footer advertises the new keys (`Shift↑↓ Scroll·R`, `←→ Cursor`), per the
rule that a surface names every key it accepts — PgUp/PgDn still work as an unadvertised alias.

Landing alongside these, on the same branch but belonging to the **graph DAG view**
([`MUX-031`](../completed/MUX-031-graph-run-tui.md)'s surface, not this spec's): the DAG cursor now follows a
*running* run to its active node (waiting > running > ready), pausing when the user moves the selection by hand
and re-arming only once the node they parked on — live at the moment they parked — settles. A completed run never
moves the cursor, so post-mortem browsing stays stable. Recorded here because the branch is MUX-109's and the work
would otherwise go unlogged; it closes no item in this spec. `TestGraphUI_CursorFollowsRunProgress` pins all four
behaviours, including the two that keep the cursor still.

Two open items are untouched by this work and stay open: the in-flight redraw check below (a runtime
property, and `scripts/test-prompt-mode.sh` has no assertion for it), and all of Phase 4.

### Phase 4: Prompt intents — launch, status, gates

- [ ] Classify a prompt into the closed intent set; an unparseable result fails closed — **RE-OPENED 2026-08-27**: the agent definition was rewritten for the gateway model into a conversational operator that may run several commands per task (*"check, then act, then verify"*), replacing "pick exactly one intent, else do nothing". That was the correct call for a capable model, but it retires the fail-closed default this step recorded. Re-specify what the new failure mode is before re-checking
- [ ] `launch` — resolve across all three scopes and start via `muxcode graph run`
- [ ] `status` — answer from `graph list` / `graph status`
- [ ] `gates` — list pending `wait_human` gates
- [x] `approve` — dispatch only when the typed text names the gate/run; guard enforced in code, not only in the model prompt
- [x] Negative-control test: "approve whatever is waiting" approves nothing
- [x] Confirm single-use approval semantics still hold on a retried gate

> **Near-miss worth keeping: the guard silently stopped applying (2026-08-27).** A top-level
> `muxcode approve` alias was added — a good ergonomic fix, since the model invented that exact
> shortening twice. But `checkApproveGuard` matched the literal token sequence
> `muxcode` `graph` `approve`, so the alias had no `graph` token and the guard returned
> *"not an approve — nothing to guard"*. `muxcode approve <run> <node>` would have executed
> **without** requiring the user's words to name the gate.
>
> Nothing broke. No test failed, no error surfaced — the guard just quietly stopped covering a new
> spelling of the action it exists to guard. Caught by asking "does the guard still see this?" when
> the interface changed, and closed the same hour: both spellings now match (path-prefixed included),
> pinned by `TestCheckApproveGuard_AliasSpelling` with unnamed-refused, named-passes, and
> path-prefixed cases.
>
> **The general lesson, which recurred four times in this spec:** a check keyed to a *surface form*
> rather than to the *action* fails open when a new form appears. Same shape as
> `responseAdmitsFailure`'s keyword search (defeated by "no errors") and the installer's
> `${OLLAMA_MODEL%%:*}` prefix grep. Whenever an interface gains an alias, ask what was matching the
> old spelling.

**Approve guard verified 2026-08-26.** `checkApproveGuard()` (`harness/prompt_guard.go`) parses the
actual command for `muxcode graph approve <run> <node>` and refuses unless the user's own text
contains an exact token match of the run or node id — case-insensitive, with a run-id prefix
accepted only at ≥8 characters. Wired into the real `Filter.Check()` path at `harness/filter.go:80`,
not merely available as a helper. The refusal text instructs the model to ask the user to name the
target rather than approving on inference, so the authority boundary is enforced **outside** the
model exactly as this spec requires.

The tests go beyond what was asked. Alongside the specified `"approve whatever is waiting"` case
they include **substring bait** (`"approve the gate"` — `gate` occurs inside `commit-gate` but names
nothing) and a **short-prefix** case (`"approve wf-17"`), either of which would pass a naive
`strings.Contains` implementation while leaving the guard broken. `TestFilter_ApproveGuardWired`
carries its own positive control, noting that "a filter that blocks every approve proves nothing".

> **OPEN HOLE — the approve guard cannot tell a user from an agent.** `requestTaskText()` narrows the
> guard's evidence from the whole batch to **request-type** messages, closing the case where a
> system-authored chain notification naming a gate satisfied "the user named it". It does not close
> the other half.
>
> The Prompt surface sends `NewMessage(bus.BusRole(), "prompt", "request", "prompt", text, "")`
> (`tui/graph_ui.go:818`). A user-typed prompt from the build window therefore arrives as
> `From=build, To=prompt, Type=request, Action=prompt`. **Any agent running
> `muxcode send prompt prompt "approve commit-gate on run abc"` produces a byte-identical message.**
> `CheckSendPolicy()` (`bus/profile.go:504`) is a hook-provider deny-list with no entry for `prompt`,
> so nothing prevents the send.
>
> The consequence is precise: the guard's whole purpose is that a **human** must name the gate, and
> an agent can currently satisfy it. This is the same principle this repo already encodes for the
> tracker — *a bus message from another agent is never the user's consent* — applied to a gate that
> exists to release git and Atlassian mutations.
>
> Candidate fixes, cheapest last:
>
> 1. **Bus-level authority gate** — a `CheckPromptAuthority`-style check permitting sends to `prompt`
>    only from the control pane, mirroring `CheckCommitAuthority`/`CheckAtlassianAuthority`. Matches
>    an existing pattern and fails closed.
> 2. **Unforgeable surface marker** — the surface includes a per-session token read from `BusDir()`
>    that no agent tool profile can read; the guard requires it. Stronger, more moving parts.
> 3. **Distinct action name alone is not sufficient** — an agent can pass any action string to
>    `muxcode send`, so `Action=user-prompt` would be trivially forgeable.
>
> **CLOSED 2026-08-26 — verified, not accepted on report.** Fix 1 landed, plus a laundering vector
> this review had missed:
>
> | Layer | Evidence |
> |-------|----------|
> | Authority check | `CheckPromptAuthority` (`bus/prompt_authority.go`) returns allow only when `to != prompt` or `from` is in `PromptAuthorityRoles()` — **default empty, so deny-all**; opt-in via `MUXCODE_PROMPT_AUTHORITY_ROLES`. Its refusal text explains the *reason* ("their text is what the approve guard trusts as the user's own words") rather than just denying |
> | Enforcement point | `inbox.go:123`, inside `sendMessage` — the choke point every send path funnels through, so no send API bypasses it |
> | Surface seam | `SendHumanPrompt` (`prompt_authority.go:66`) is called **only** from `tui/graph_ui.go:822` and has **no `cmd/` exposure** — there is no CLI subcommand, so an agent has no way to invoke it |
> | Graph laundering | `TestValidate_RejectsPromptTargetingNode` — a `send` node with `role: prompt` carrying `"approve commit-gate on run wf-123"` fails validation. **This vector was not in my report**; a graph node could otherwise have originated the "user's words" through the executor |
> | Tests | Deny-all default, an `IgnoresOtherTargets` control proving it does not over-block, env opt-in, bus-level `Send` refusal, `SendHumanPrompt` delivery + response passthrough, and the laundering test's **positive control** (the same node targeting `build` must still validate) |
>
> The remaining half of the gates criterion is *listing*, which is a permitted `graph list` read and
> is exercised in Phase 7 — the authority half is now closed at three independent layers.

### Phase 5: Graph creation flow

- [x] Compose a definition from a described workflow
- [x] Validate before writing; report failures verbatim, write nothing
- [x] Write project-local by default; user-global only on explicit instruction
- [x] Test: a composed graph with an ungated commit node is rejected by the existing validator rule

### Phase 6: Prompt injection to the active main agent

- [x] Resolve the window's active agent, respecting mode-cycled windows
- [x] Deliver via `TmuxSendLiteral()` — text → delay → Enter; no hand-rolled `send-keys`
- [x] Inject/interpret selection via an explicit toggle, with the destination always visible in the input line
- [x] Test: a dash-leading prompt injects intact (MUX-104 regression shape)
- [x] Test: with the window mode-cycled, injection reaches the *active* agent, not the default role

### Phase 7: Integration test

- [x] Create `scripts/test-prompt-mode.sh` — hermetic where possible: scratch `BUS_SESSION`, scratch tmux session, scratch graph dirs
- [x] Surface appears in the `Tab` cycle and `MUXCODE_CONTROL_PANE_SURFACE=prompt` starts on it (`capture-pane`)
- [x] `--render-once` frames: empty state, clamped long prompt, unreachable-model state
- [ ] Launch intent starts a run against a scratch graph dir; run appears in `graph list`
- [x] Status intent reports that run
- [ ] Named-gate approval releases the gate; unnamed "approve whatever is waiting" does **not**
- [x] Create intent writes a valid definition into the scratch project dir; an invalid one writes nothing
- [x] Injection delivers a dash-leading payload intact into a scratch pane
- [x] Ollama dependency: skip-with-reason when Ollama is absent, and assert the model-unreachable frame still renders — the test must never pass **vacuously** by skipping everything silently
- [x] Guard the skip path with a **coverage floor**: assert a minimum number of non-skipped checks, so a machine with no model pulled reports "skipped" loudly rather than green. This is not hypothetical — it is the current state of the development machine, so the skip branch will be the *first* branch exercised

### Phase 7 after the fixes — 26 passed, 2 failed, 1 skipped (stable ×3)

Re-run 2026-08-27 on the gateway backend, identical across logs 7, 8 and 9 — verified by reading all
three, not by one sample. Every fix from the first run is demonstrably working:

| Fix | Evidence |
|-----|----------|
| Gateway backend | **0.8–1.6 s** per call, against 39–82 s on `qwen3:4b`. The timeout hypothesis is dead |
| Gated negative control | Reports `SKIP: unnamed-approve negative control undetermined — positive control failed` instead of falsely passing. Firing on precisely the case it was built for |
| `live_diag` | Fires on failure and its output identified the real cause |
| `create` intent | Now **passes** — composed and wrote a valid definition |
| Approve guard, both spellings | Verified after the alias gap was closed |

> **Run 10 (2026-08-27, after the probe-burn hardening in `765ec06`) — the diagnosis below is
> incomplete.** Result: **26/2/1 again**, the fourth consecutive identical outcome, with the same two
> checks failing. The hardening measurably worked at what it targeted and did not move the result:
>
> | | Run 9 | Run 10 |
> |---|---|---|
> | `not allowed by tool profile` rejections | 4 | **3** |
> | Turn exhaustion (`Prompt 10/10`) | 1 | **2** |
> | Result | 26/2/1 | 26/2/1 |
>
> Fewer probes, *more* exhaustion, same failures. So probe-burn is a **contributing factor, not the
> sole cause** — something else is consuming the turn budget before the required command. Four fix
> attempts have now produced identical results (tool-profile widening, approve alias,
> `--wait`/`--track` flag fix, probe-burn hardening), which is itself the finding: **the next step is
> to instrument where the turns actually go, not to guess at another cause.** A per-turn dump of what
> the model called would settle in one run what four attempts have not.
>
> *(The run agent's first report of this run was a MUX-112 fabrication — a login banner. The numbers
> here come from the log, and its later correction independently matched.)*

**The two remaining failures share one cause, and it is not capability.** Run 9 shows **four**
`not allowed by tool profile` rejections and the loop reaching `Prompt 10/10`: the model spends its
turn budget on probes the profile denies, and never reaches the required command. Run 8 shows the
same behaviour in a different costume — a malformed `muxcode send --wait --track` (mutually
exclusive) burning a turn.

So `launch` and `named approve` fail for a **model habit**, not a product defect and not a limit of
the gateway model. The agent definition already carries a "never probe" instruction; it is not
sufficient on its own — the same instruction-versus-enforcement split this spec has hit repeatedly.

Candidate fixes, on the maintainer's decision:

1. A **first-tool-call-must-be-the-command** rule in the agent definition — narrower and more
   checkable than "never probe".
2. **Raise the prompt harness turn budget** — cheap, but treats the symptom; a model that probes
   four times will probe six.

Rung 1 is the better first move for the same reason the escalation ladder prefers constraint over
capacity: a bigger budget makes the failure rarer without making it wrong less often.

*(One thing I initially misread: the `can't find window: build` line in the daemon log is scratch-session
noise, not part of the failure path. Recorded because it looked causal and was not.)*

### Phase 7 first run — 25 passed, 3 failed, 0 skipped

Run 2026-08-27 (`/tmp/test-prompt-mode-2.log`). The script itself is sound: 349 lines, a real coverage
floor (`PASS >= 18` against 20 mechanical checks), granular skip-with-reason, and a guard that skips
the approve checks when the gate never reaches `waiting`. **0 skipped means the live section actually
ran** — Ollama and `qwen3:4b` were present, so these are genuine live results, not deferrals.

Everything mechanical passed: the authority gate refuses an unauthorized request and explains the
rule, all four `--render-once` frames, the 40-column clamp, the full graph-create matrix
(valid accepted, written project-local, resolves at project tier, ungated commit rejected citing the
rule, rejection wrote nothing), the live surface (pane opens on Prompt, `Ctrl-T` flips to inject
naming the active agent, dash-leading payload injected intact, receipt shown, `Tab` cycles).

**Three failures, all live-model intents, all timeouts:** launch (3 min), named approve (3 min),
create (4 min).

> **The negative control is currently vacuous — this is the finding that matters.** The run recorded
> `ok: unnamed approve released nothing (negative control)` alongside
> `FAIL: named approve released the gate (3min timeout)`. Nothing released the gate *at all*, so the
> control passed because the whole approve path was inert, not because the guard discriminated. **A
> negative control whose paired positive case fails proves nothing.**
>
> **Fixed 2026-08-27, verified in the script.** The control is now gated on its positive control:
> it reports `ok … (negative control, validated by the positive control)` only when the named
> approve passed, and otherwise `skip "unnamed-approve negative control undetermined — positive
> control failed"`. Reporting it as a **skip** rather than silently dropping it means the coverage
> floor still counts it, so an undetermined control cannot quietly inflate the pass total. The
> failure path also now calls `live_diag`, dumping the harness log tail so "tool call emitted late"
> and "never emitted" become distinguishable — the ambiguity that made the first run's three
> failures unreadable.
>
> Note what this means about the recorded 25/3/0: **that run's pass count was already wrong in the
> reassuring direction**, because it included an `ok` for a control that had proven nothing.
>
> This is [MUX-014](../completed/MUX-014-graph-agent-orchestrator.md)'s trap repeating: there,
> "join barrier held" and "z held back" both passed in the 15/22 run because nothing had dispatched.
> The script already guards the *gate-never-waiting* case (line 301) but not this one — the gate
> **was** waiting and nothing could act on it. **Fix: make the unnamed-approve control conditional on
> the named-approve check having passed**, so it can only report a pass when it had something to
> discriminate against.

**On "latency, not a code defect":** plausible, but not established by this run. `status intent
produced a response` shows the model replies, but not that it emitted and executed a *tool call* —
which is what the three failing intents all require and status alone may not. The two hypotheses
(slow tool calls vs. unreliable tool-call emission at 4B) are not yet distinguished. Distinguish them
before choosing a fix: the escalation ladder's rungs 1–2 (structured output, template-fill) address
the second, while think-mode work addresses the first. The 4-minute create failure carries the
script's own hint — *"escalation ladder territory if this persists"*.

This corroborates the [Phase 2 regression findings](#phase-2-regression-findings): the same 30–90 s
per-turn cost that made build/test/commit/watch non-viable is the leading explanation here.
- [ ] Run the script and confirm all checks pass

## Known gaps at close-out

Recorded 2026-08-28. **26 of 36 acceptance criteria and 49 of 61 implementation steps are met** —
22 checkboxes remain open. No box above was ticked without evidence, and nothing below is checked
off to make the spec read better than it is.

The rows are ordered by evidence status, because the distinction matters more than the count:
**measured failing** means the behaviour was exercised and did not work; **unverified** means no
test exercises it either way. Six of these were measured, not merely skipped.

| Unmet | Why it matters | Evidence status |
|-------|----------------|-----------------|
| Intent: **launch** — resolve across scopes and start via `graph run` (criteria + Phase 4 + Phase 7 steps) | A core intent of the feature. The pane can be told to launch a graph and will not | **Measured failing.** Timed out ×4 consecutive runs; turn budget exhausted before the command. Tracked as [MUX-115](../backlog/MUX-115-prompt-agent-turn-budget-exhaustion.md) |
| Intent: **named gate approval** releases the gate (criteria + Phase 7 step) | The gate path is the authority-sensitive one; a gate that cannot be approved by name is not usable | **Measured failing.** Same exhaustion signature, same 4 runs. [MUX-115](../backlog/MUX-115-prompt-agent-turn-budget-exhaustion.md) |
| Paired **unnamed-approve negative control** | Its positive control fails, so it can only report `skip` — it has nothing to discriminate against | **Undetermined by design.** Correctly reports skip rather than a vacuous pass (the fix made after run 1) |
| Criterion: build, test, commit, watch still complete on the smaller model; single-shot roles do not loop | This is a **regression to roles outside this feature**, not a gap in it. The 4B could not complete tasks at all with thinking enabled | **Measured FALSE 2026-08-26.** See [Phase 2 regression findings](#phase-2-regression-findings). Mitigated in practice by the gateway default, but the criterion as written is refuted |
| Phase 2: confirm each exercised role completes its normal task | Same root as above | **Not met.** No exercise returned a completed task response |
| Phase 2: confirm single-shot roles do not loop | Partially refuted — single-shot covers real tasks, but neither role survived its *startup* message, which is open-ended and outside single-shot's reach | **Partially met, partially refuted.** Startup-message defect tracked as [MUX-110](../backlog/MUX-110-harness-startup-tool-loop-exhaustion.md) |
| Phase 4: classify a prompt into the closed intent set; unparseable fails closed | **Re-opened deliberately 2026-08-27.** The agent definition was rewritten for the gateway model into a conversational operator that may run several commands per task, retiring the "pick exactly one intent, else do nothing" default | **Superseded, not failed.** The new failure mode is unspecified — re-specify before re-checking |
| Criterion + Phase 7: `scripts/test-prompt-mode.sh` passes | The spec's own test gate. It does not pass | **26 passed / 2 failed / 1 skipped**, stable across 4 runs |
| Intent: **status** answers from `graph list` / `graph status` | Partial evidence only | **Unverified as written.** `status intent produced a response` is recorded, but not that it emitted and executed a *tool call* — which is the part the failing intents need |
| Intent: **gates** — list pending `wait_human` gates | Listing is the read half of the gate path | **Unverified.** No check exercises the list path independently of approval |
| Criterion: tool profile permits only the intended surface, with a negative control denying a repo write and a git command | This is the sandbox claim. It is asserted in the profile test but not by the integration suite's negative control as the criterion words it | **Partially verified.** Profile test pins `jira read` allowed / `jira update` denied; the repo-write and git denials are not exercised end-to-end |
| Criterion: single-use gate approval semantics from MUX-014 unchanged | A regression here would let a retried run pass a gate nobody approved — the exact defect `TestExecHumanGateRetryRequiresFreshApproval` exists to prevent | **Unverified for the prompt path.** MUX-014's own test still passes; no check covers approval *arriving via the prompt-agent* |
| Criterion: `CheckCommitAuthority` / `CheckAtlassianAuthority` remain the runtime backstop | The whole authority argument rests on these being unbypassable from the new surface | **Unverified from this surface.** The guards are unchanged in code; no test drives them *through* the prompt-agent |
| Criterion: Ollama health monitoring covers the new role; surface states plainly when the model is unreachable | With the gateway default the local path is opt-in, so this is now a secondary path — but the criterion is still unmet | **Unverified.** The unreachable-model frame renders (`--render-once`); health-monitor coverage of the role is not exercised |
| Criterion: a user who declines the model pull still gets a working install | Install-path promise made in `install.sh` and `README.md` | **Unverified.** No install-path test exercises the declined-pull degradation |
| Phase 3: pane still redraws and cycles surfaces while a prompt is in flight | The non-blocking claim. Asserted by construction (async transcript read in `refresh()`), not by a test | **Unverified.** The design makes blocking structurally unlikely; nothing measures it |
| Phase 2 follow-ups: disable thinking / structured outputs, then re-run the regression and re-decide "4B everywhere" | The decision row for "4B everywhere" is recorded with its premise already refuted | **Not started.** Escalation-ladder rungs 1–2 |

**If this spec is reopened, start with [MUX-115](../backlog/MUX-115-prompt-agent-turn-budget-exhaustion.md).**
Two of the failing rows collapse into it, and its first phase is instrumentation precisely because
four fix attempts have already been spent guessing at this cause.

**The row most likely to be misread is the 4B regression.** It is not a gap in Prompt mode — it is a
measured regression to build, test, commit and watch, recorded here only because this spec is what
moved the default model. The gateway backend means the default path no longer triggers it, which is
mitigation, not repair.

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
| Which backend serves the prompt-agent? | **Decided 2026-08-27 (user-initiated): selectable, with `opencode` (Zen gateway) as the default; `ollama` opt-in** | The spec's "a local model is the right shape for this scope" premise was overturned by measurement: 39–82 s per call *with* `think:false`, plus fabricated success summaries — see [Backend selection](#backend-selection--revised-2026-08-27-premise-overturned-by-measurement). Landed in two steps: selectable first, then the default flipped on the resource cost. Local inference is preserved as an opt-in, not removed |
| Should Ollama stay a required installer prereq? | **Decided: no — optional again** | It was promoted to required *because* Prompt mode needed a local model. With the gateway as default, the default path never touches Ollama, so a required-tier prereq would demand a multi-GB dependency most installs never use. The installer checks `MUXCODE_OPENCODE_API_KEY` instead |
| ~~Do existing local roles accept the 4B, or keep a 7B pin?~~ | **Decided: accept the 4B everywhere — no per-role pins.** ⚠️ **Premise since measured false — needs re-deciding** | One model resident, for every local role, with no exceptions. The cost was accepted as a *capability reduction*. The [regression check](#phase-2-regression-findings) measured something worse: with thinking enabled those roles do not complete tasks at all. The memory rule itself held perfectly. Re-decide after the think-mode follow-up — this row stays as written so the decision and its refutation sit together |

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-109-prompt-mode-graph-control-pane | 7h 40m | 2026-08-27 18:12 |

## Status

In Progress
