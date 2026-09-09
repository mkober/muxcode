# API Testing as a Control-Pane Surface, Served Without an LLM

Move API testing out of the `prefix + i` modal and into the control pane as a fifth surface —
`Prompt / Launch Graph / Graph Runs / Pending Gates / API` — and serve it from a **headless server
process** shaped like the prompt-agent (MUX-109), not from a Claude Code agent. The objective of the
surface is Postman-type work: browse collections, pick an environment, send a request, read the
response, walk the history.

Sending an HTTP request is fully specified by *collection + request + environment*. Nothing about it
needs a model, and today everything about it is delegated to one: execution, `{{var}}` substitution,
history logging and PII scrubbing are all **prose instructions** in `agents/api-tester.md` that a
Claude agent is asked to follow with `curl | jq`. No Go code sends a request.

Tracking: _(no GitHub issue yet)_

## Context

### What the user asked for (2026-09-08)

| Ask | Reading |
|-----|---------|
| "Move the API agent to the Control panel after `/ Pending Gates / API`" | A fifth surface in the control pane's tab bar, reachable by `Tab` like the other four |
| "Remove the claude agent component" | Retire the Claude Code `api` agent: its definition, provider/model env vars, tool profile, and the `muxcode agent launch api` modal pane |
| "Use a server agent similar to the prompt agent" | A headless, daemon-supervised process with a bus identity — the MUX-109 shape — but **without a model** (below) |
| "Provide postman type features to test API collections" | Collection browser, environment selector, request detail, send, response viewer, history |

### What exists today (verified against the tree)

| Piece | Where | Disposition |
|-------|-------|-------------|
| Data layer | `bus/api.go` (567 lines): `Environment`, `Collection`, `Request`, `ApiHistoryEntry` (`:15-53`); CRUD over `.muxcode/api/{environments,collections}/*.json`; `AppendApiHistory`/`ReadApiHistory` over `.muxcode/api/history.jsonl` (`:346`, `:359`); `ImportApiDir` (`:400`); formatters | **Keep** — this is the model the surface renders |
| CLI | `cmd/api.go` (418 lines): `muxcode api env \| collection \| history \| import` | **Keep**; gains `send` and `serve` |
| Tests | `bus/api_test.go`: 18 tests over the data layer | **Keep** |
| Executor | **None in Go.** `agents/api-tester.md` tells the model to run `curl -s -w … \| jq .`, resolve `{{variable}}` "from the active environment's variables", pipe through `muxcode pii-scrub`, and "track: timestamp, collection, request name…" | **The gap.** Every property the feature depends on is a request to an LLM, not a code path |
| Claude agent wiring | `AgentFileName` → `api-tester` (`bus/launch.go:89`); `MUXCODE_API_CLI` (`:132`); `MUXCODE_API_CLAUDE_MODEL` (`:174`); default model `claude-sonnet-5` (`:227`); system prompt (`:287`); window mapping (`bus/launcher.go:116`); tool profile (`bus/profile.go:849`) granting `curl`/`wget`/`http*`/`python*`/`node*`/`openssl*` plus twenty `Bash(RESP=*)`-style variable-assignment allowances that exist only so the model can script curl | **Remove** |
| Modal | `bus/modal.go:163` — `api` modal: `muxcode agent launch api` (top 80 %) over `muxcode console api` (bottom 20 %), `AutoCap: true`; `config/tmux.conf:51` `bind i run-shell 'muxcode modal open api'`; `:62` menu entry `"API Testing" i` | **Re-point** at the surface |
| Console renderer | `bus/console.go:376` config + `renderAPI` (`:1625`) — renders `.muxcode/api/history.jsonl` with env/collection counts in the empty state | Keep (Decision 2) — it reads the same file the surface will |
| Role | `KnownRoles` (`bus/config.go:17`); already skipped as a "non-agent role" by batch reload (`bus/reload_batch.go:145`); in the PII-scrub role set (`bus/scrub.go:167`), the compact set (`bus/compact.go:162`), diagnose (`bus/diagnose.go:1424`), `main.go:22,208` | Role **stays** as a bus identity; every site that means "a Claude pane" goes |
| Docs | `docs/agent-bus.md:1480` (`muxcode api`), `docs/agents.md:105` (roster row), `README.md:92,208,669`, `CLAUDE.md:108` ("**api**: API testing (modal-only, `prefix + i`)") | **Update** |
| Fixtures | `examples/api/collections/httpbin-basics.json` — six requests (`get-test`, `post-json`, `status-codes`, `headers-echo`, `basic-auth` with `{{test_user}}/{{test_pass}}`, `delay`); `examples/api/environments/httpbin.json`; the same pair already imported under `.muxcode/api/` | Integration-test fixture |

### The prompt-agent pattern — what "similar to the prompt agent" means here

MUX-109's prompt-agent is the one existing example of a bus role with no tmux window. Its properties,
each verified in the tree:

| Property | Where | Carried over? |
|----------|-------|---------------|
| Headless — no window, no TUI | `bus/prompt_agent.go` header comment | Yes |
| Daemon owns the lifecycle: `checkPromptAgent` each poll, restart cooldown, relaunch when the marker's pid is dead | `daemon/daemon.go:372`, `:3748` | Yes — `checkApiAgent` beside it |
| Liveness by pid marker | `HarnessMarkerPath` (`bus/config.go:522`), `IsHarnessActive` (`bus/notify.go:18`) — stale markers self-clean | Yes — same helpers; the name says "harness", the mechanism is a pid file |
| Own log, detached process group | `PromptAgentLogPath` → `BusDir()/prompt-agent.log`; `Setpgid: true` in `StartPromptAgent` | Yes |
| Opt-out env var | `MUXCODE_PROMPT_AGENT_DISABLE=1` (`PromptAgentEnabled`) | Yes — `MUXCODE_API_AGENT_DISABLE=1` |
| Skipped by send-keys notification (harness panes poll their own inbox) | `Notify()` skips harness-marked roles | Yes — the daemon must never try to type at it |
| Surface writes a bus request and reads a **file** on `refresh()`; the render loop never waits on the work | MUX-109 *Result display: headless agent, ambient surface*; `tui/graph_ui.go:540` reads `LoadPromptExchanges` / `LoadPromptActivity` per refresh | Yes — the load-bearing property |
| Excluded from batch reload | `bus/reload_batch.go:145` — `api` is **already** on that skip list | Already true |
| Backend/model selection, harness agent definition, tool profile, provider-selector entry | `PromptBackend`, `ReloadPromptAgent`, `agents/harness/prompt-agent.md`, the `prompt` profile | **No — see below** |

**Where the api server departs from the pattern, and why: it has no model.** The prompt-agent needs an
LLM because its input is free text and its job is intent classification. An API request has no
judgement in it — resolve the variables, build the URL, send it, record what came back. So the server
is a Go process inside the `muxcode` binary (`muxcode api serve`) using stdlib `net/http`, not a
harness role: no backend, no model pin, no gateway key, no tool profile, no token cost, and none of
the failure MUX-109 recorded twice on 2026-08-27 — a model reporting `succeeded:` for a command that
was refused. What the server inherits is the **shape**: headless, daemon-supervised, marker-liveness,
bus-addressable, file-backed surface.

### Why the surface does not send requests itself

The obvious alternative is for the TUI process to call `net/http` directly when the user presses
Enter. Rejected, for three reasons that are each a requirement elsewhere in this spec:

1. **Two executors drift.** The bus path (`muxcode send api api "send …"`, used by edit and by
   graphs) and the surface would each substitute variables, scrub PII and write history. One
   executor, called from both, is the only way the surface shows exactly what an agent would get.
2. **A slow endpoint blocks the pane.** `httpbin-basics/delay` sleeps a second; a hung server sleeps
   until timeout. MUX-109 designed this failure out of the Prompt surface ("the render loop never
   blocks on a model call") and the same rule holds for a network call.
3. **In-flight state must be ambient.** Surface selection is shared session-wide through the
   `control-pane-surface` marker (`bus/control_pane.go`), so every window's pane shows the same
   frame. A request in flight in one window's TUI process would be invisible from every other.

The surface therefore does what the Prompt surface does: write a bus request, return to rendering,
and read the outcome from disk on the next tick.

### Scope boundary

**In:** the fifth surface; the Go executor; the headless server and its daemon supervision; the closed
bus grammar; retiring the Claude `api` agent; re-pointing `prefix + i` and the tmux menu; docs; an
integration test.

**Out (recorded under [Deferred](#deferred)):** Postman collection v2.1 import/export, scripted
pre-request/assertion hooks, response capture into environment variables for chained flows, and
editing collections from inside the surface (the `muxcode api collection add-request` CLI stays the
authoring path).

## Requirements

### Acceptance criteria

- [ ] The control pane tab bar reads `Prompt / Launch Graph / Graph Runs / Pending Gates / API`; `Tab` and `Shift-Tab` cycle through all five, and the shared `control-pane-surface` marker carries `api` so every window lands on the same frame
- [ ] The API surface lists collections and their requests, shows the selected request's **resolved** method, URL, headers and body, and names the active environment in the header
- [ ] `e` cycles the environment; `Enter` sends the selected request; the response — status, duration, size, headers, body with JSON pretty-printed — renders within one tick (`graphTickInterval`, 2 s) of the server writing it
- [ ] History is browsable per selection; choosing a history row re-shows its stored response
- [ ] Sending never blocks the render loop: an in-flight request shows a *sending…* state visibly distinct from *done* and from *server not running*, and `Tab` still cycles surfaces while it is in flight
- [ ] `muxcode api serve` runs headless under daemon supervision — marker liveness, restart cooldown, its own log, `MUXCODE_API_AGENT_DISABLE=1` opt-out — and a killed server is back within the cooldown without user action
- [ ] The server consumes the `api` inbox and answers `muxcode send api api "send <collection>/<request> [--env <name>]"` with a one-line result (`200 · 143ms · 512 B · GET https://…`) plus the scrubbed body, through **the same executor the surface uses**
- [ ] `{{var}}` substitution from the environment applies to path, query, headers and body; an unresolved variable fails the send **naming the variable**, and no request leaves the process
- [ ] Every response body passes through `ScrubPIIWithNotice` before it is written to history or returned on the bus
- [ ] A mutating method (`POST`/`PUT`/`PATCH`/`DELETE`) against an environment not marked local confirms before sending, and the confirm re-checks the selection and environment at the keypress
- [ ] Environment values whose key matches `token|secret|password|key` render masked in the surface; the full value reaches only the wire
- [ ] The Claude `api` agent is gone: no `agents/api-tester.md`, no `MUXCODE_API_CLI` / `MUXCODE_API_CLAUDE_MODEL`, no `api` tool profile, and `muxcode agent launch api` refuses with a pointer to `muxcode api serve`; the provider selector never lists `api`; `muxcode reload api` explains the role is served, not reloaded
- [ ] `prefix + i` and the tmux menu entry land on the API surface instead of launching a Claude agent (mechanism per Decision 1)
- [ ] Every frame is pure and reachable via `--render-once`, honours both `width` and `height`, has an explicit empty state that carries the affordance (`muxcode api import examples/api`), restores selection by `collection/request` id, and advertises every key in the footer
- [ ] Negative controls exist for each degradation: an overflowing fixture is clamped; a non-local mutating send without confirmation fires nothing; an unresolved variable writes no history row
- [ ] `scripts/test-api-surface.sh` passes with a coverage floor
- [ ] Docs updated together with the code: `agent-bus.md`, `agents.md`, `architecture.md`, `configuration.md`, `README.md`, `CLAUDE.md`

### Technical approach

- **Extend the surface machinery exactly as MUX-109 Phase 3 did; do not fork it.** A `viewGraphAPI`
  appended to the `graphView` enum (`tui/graph_ui.go:20`) and to `graphSurfaces` (`:369`); a
  `surfaceName()` arm (`:373`); the name added to `renderSurfaceTabs` (`tui/graph.go:803` — its
  clamp comment already anticipates the bar outgrowing a narrow pane); `surfaceKey`/`surfaceForKey`
  learning `"api"`; a `controlPaneCommand()` case in `bus/control_pane.go`; and an `--api` flag
  beside `--prompt` in `cmd/graph.go:318`. The `directPrompt` precedent (`graph_ui.go:495` — opened
  directly, `q`/`Esc` quits) gives the popup path for free.
- **One executor, three callers.** `bus/api_exec.go` owns `ResolveRequest` (substitution, header
  merge, base-URL precedence) and `Execute` (stdlib `net/http`, timeout, capture, scrub). It is
  called by `muxcode api send` (CLI), by the server's bus loop, and — through the server — by the
  surface. It takes an optional `*http.Client`, nil in production, so tests run against
  `newPipeServer` and **never bind a socket** (`CLAUDE.md`, MUX-152/153).
- **History is the transcript; the in-flight marker is the working state.** Results extend the
  existing `ApiHistoryEntry` in place (`ID`, `Env`, `ResponseHeaders`, `Size`, `Error` — all
  `omitempty`, so old rows still parse and `renderAPI` keeps working) in the project-local
  `.muxcode/api/history.jsonl`, because request history is a project artifact that outlives a
  session, as in Postman. The in-flight marker is session-local — `BusDir()/api-inflight.json`
  holding `{id, collection, request, env, started_at}` — written when the server picks a request
  up, removed when the history row lands. The surface reads both in `refresh()`, never in a
  renderer.
- **The bus grammar is closed.** `send <collection>/<request> [--env <name>]`, `list`,
  `history [<collection>] [--limit N]`. Anything else gets a `usage:` reply that lists the
  collections, and writes nothing. The server interprets no natural language; an agent that wants
  to say "send the get-test request from httpbin-basics using httpbin env" says
  `send httpbin-basics/get-test --env httpbin`. (The alternative — route unparseable text to the
  prompt-agent — is Decision 3.)
- **Consume the inbox through the bus package's own path.** The harness shells out to
  `muxcode inbox` for this (`harness/bus.go:58` → `run`, `:201`); the server lives inside the
  binary, so the consume routine behind `cmd/inbox.go` is exported (or called) rather than
  re-implemented. Receipts (`bus/delivery.go`) are written on consume so the delivery-ack model sees a
  true `acked`, and replies go out with `--reply-to` semantics so `MarkResponded` drains the request.
- **Presence for a windowless role.** `RoleWindowPresent` (`bus/reload.go:301`) decides presence by
  window and will read `api` as absent — correct for reload, wrong for "can I send to it". The
  send/diagnose/health paths need a *served* notion: alive iff the marker pid is alive.
  [MUX-145](./MUX-145-messages-routed-to-windowless-role.md) is defining how a windowless
  role is treated; this spec adds the first role that is windowless **by design** rather than by
  configuration, and should land on whatever predicate MUX-145 settles on rather than a private one.
- **Safety lives in code, not in a definition.** The retired agent's "warn before mutating requests
  to production" was prose. Here: `Environment` gains `Local bool` (default false; the importer sets
  it when `base_url`'s host is `localhost`, `127.0.0.1`, `::1` or ends in `.local`), and a mutating
  method against a non-local environment goes through the confirm frame — which re-reads the
  selection and environment at execution, per [`docs/tui-style.md`](../../tui-style.md) rule 8.
- **Layout follows the Prompt surface.** Split at wide widths (list left; detail and response right),
  stacked below the threshold, mirroring `renderPromptSplit` / `renderPromptStacked`
  (`tui/prompt.go:264`, `:403`); state carried by glyph and text, urgency by color; the
  [TUI checklist](../../tui-style.md#checklist) applies in full, including the three mechanical
  checks.

### Key files

| File | Change |
|------|--------|
| `tools/muxcode/bus/api_exec.go` | **New** — `ResolvedRequest`, `ResolveRequest`, `ApiResult`, `Execute(ctx, req, *http.Client)`, `IsLocalEnvironment`, secret-key masking helper |
| `tools/muxcode/bus/api_agent.go` | **New** — `ApiAgentEnabled`, `StartApiAgent`, `StopApiAgent`, `ApiAgentAlive`, `ApiAgentLogPath`, `ApiInflightPath`; mirrors `prompt_agent.go` minus backend/model |
| `tools/muxcode/bus/api.go` | Extend `ApiHistoryEntry` (`omitempty` fields); `Environment.Local` |
| `tools/muxcode/cmd/api.go` | `send` and `serve` subcommands; `serve` is the bus loop |
| `tools/muxcode/daemon/daemon.go` | `checkApiAgent` beside `checkPromptAgent` (`:372`); stop on cleanup |
| `tools/muxcode/tui/api.go` | **New** — `ApiSurfaceState`, `RenderApiFrame`, `ApiRenderOnce`, loaders (`LoadApiSurface`) |
| `tools/muxcode/tui/graph_ui.go`, `tui/graph.go` | Fifth view: enum, `graphSurfaces`, `surfaceName`, `surfaceKey`, `refresh()` case, key handler, tab bar |
| `tools/muxcode/bus/control_pane.go` | `controlPaneCommand()` `api` case |
| `tools/muxcode/cmd/graph.go` | `--api` flag |
| `tools/muxcode/bus/modal.go`, `config/tmux.conf` | `api` modal → surface; `bind i` and menu entry re-pointed |
| `tools/muxcode/bus/launch.go`, `bus/launcher.go`, `bus/profile.go` | Remove every Claude-pane arm for `api` |
| `agents/api-tester.md` | **Delete** |
| `docs/agent-bus.md`, `docs/agents.md`, `docs/architecture.md`, `docs/configuration.md`, `README.md`, `CLAUDE.md` | See Phase 4 |
| `scripts/test-api-surface.sh` | **New** — Phase 5 |

### Decisions — open, the user's call

| # | Question | Options | Recommendation |
|---|----------|---------|----------------|
| 1 | What does `prefix + i` do once there is no modal agent? | **A** — when control panes are enabled (`ControlPanesEnabled()`), write `api` to the shared surface marker and focus the pane: ambient, no popup over a pane that already shows it. **B** — always open the popup `muxcode graph ui --api` (the `--prompt` direct-mode precedent). **C** — A when panes are enabled, B otherwise | **C**. A alone strands sessions with panes disabled; B alone stacks a popup on top of a pane already able to show the surface |
| 2 | Keep `muxcode console api` / `renderAPI`? | Keep (it reads the same history file, costs nothing, and the console is a different surface) or remove with the modal | **Keep** |
| 3 | Free text on the bus | Closed grammar only (usage reply on anything else), or forward unparseable payloads to the prompt-agent for interpretation | **Closed grammar.** Interpretation re-introduces a model on the path the user just removed one from, and the prompt profile deliberately grants no write tool — it could not log history |
| 4 | Where history lives | Extend `.muxcode/api/history.jsonl` in place (project-local, cross-session) or a new session-local transcript under `BusDir()` | **Extend in place.** Only the in-flight marker is session-local |
| 5 | Marker helper naming | Reuse `HarnessMarkerPath`/`IsHarnessActive` for a process that is not a harness, or introduce a process-neutral alias | Reuse with a one-line comment; a rename is a separate, mechanical change if the misnomer grates |

## Implementation

### Phase 1: Executor in Go

- [ ] `bus/api_exec.go`: `ResolveRequest(col, req, env)` — `{{var}}` substitution in path, query, headers and body from `env.Variables`; environment headers merged **under** request headers; base URL precedence environment → collection; an unresolved variable returns an error naming it and builds nothing
- [ ] `Execute` with stdlib `net/http`: timeout from `MUXCODE_API_TIMEOUT_SECS` (default 30); captures status, duration, size, response headers, body; body passes through `ScrubPIIWithNotice` before the result is returned
- [ ] Optional `*http.Client` parameter, nil in production; every test uses `newPipeServer` — no socket binds
- [ ] Extend `ApiHistoryEntry` (`ID`, `Env`, `ResponseHeaders`, `Size`, `Error`, all `omitempty`); confirm `muxcode api history` and `renderAPI` still read pre-change rows
- [ ] `muxcode api send <collection>/<request> [--env <name>]` — the CLI face of the executor; prints the one-line result and the body; appends history
- [ ] Tests: substitution (positive, and the unresolved-variable negative asserting **no history row**); header merge precedence; scrub applied (a fixture body containing an email yields the notice); timeout; history row shape

### Phase 2: Headless server, daemon-supervised

- [ ] `bus/api_agent.go`: `StartApiAgent` / `StopApiAgent` / `ApiAgentAlive` / `ApiAgentLogPath`, `MUXCODE_API_AGENT_DISABLE`; detached process group; log at `BusDir()/api-agent.log`; marker via the existing helpers (Decision 5)
- [ ] `muxcode api serve` loop: consume the `api` inbox through the bus package's own consume path (export it — do not write a second reader); write the receipt; parse the closed grammar; write the in-flight marker; execute; append history; remove the marker; reply with `reply_to` set so `MarkResponded` drains the request
- [ ] Daemon `checkApiAgent` beside `checkPromptAgent` (`daemon.go:372`) with the same cooldown pattern; `StopApiAgent` on session cleanup
- [ ] Grammar: `send`, `list`, `history`; anything else → `usage:` reply listing collections, no history row, no request
- [ ] Presence: `muxcode send api` is accepted while the marker pid is alive; `muxcode diagnose api` names the pid and log path; the provider selector excludes `api`; `muxcode reload api` explains it is served (`muxcode api serve --restart` stops and starts it) — aligned with whatever windowless-role predicate [MUX-145](./MUX-145-messages-routed-to-windowless-role.md) lands
- [ ] Tests: grammar parse table (accepted and refused forms); one full request → reply through a pipe server; marker liveness and stale-marker cleanup; daemon relaunch after a kill within the cooldown

### Phase 3: The API surface

- [ ] Append `viewGraphAPI`: enum, `graphSurfaces`, `surfaceName()`, `renderSurfaceTabs` (five names — verify the bar still clamps at the narrowest supported pane width), `surfaceKey`/`surfaceForKey` `"api"`, `controlPaneCommand()` `api`, `--api` in `cmd/graph.go`
- [ ] `tui/api.go`: `ApiSurfaceState` (collections, environments, selected `collection/request` id, active env, in-flight marker, latest result, history rows, server-unreachable reason) and a pure `RenderApiFrame(st, width, height)`; `ApiRenderOnce` for `--render-once`
- [ ] Layout: split (list left — collection ▸ request rows with a method glyph; detail and response right) with stacked fallback below the width threshold, mirroring `renderPromptSplit`/`renderPromptStacked`
- [ ] Keys: `↑/↓` select, `Enter` send, `e` environment, `h` history for the selection, `r` toggle response headers, `PgUp/PgDn` scroll the body, `Tab`/`Shift-Tab` surfaces, `q`/`Esc` back — footer advertises every one; Escape disambiguation reuses the existing sequence handling
- [ ] `refresh()` loads collections, environments, history, the in-flight marker and `ApiAgentAlive` — I/O here only; selection restored by id with first-row fallback
- [ ] States readable without color: *idle*, *sending…* (marker present), *done* (latest result), *server not running* (footer names `muxcode api serve` or the disable flag), *no collections* (empty state carries `muxcode api import examples/api`)
- [ ] Secret masking: environment values under `token|secret|password|key` keys render as `••••` in detail and history; the wire gets the value
- [ ] Mutating method against a non-local environment: confirm frame stating method, resolved URL and environment; the keypress re-reads selection and environment before sending
- [ ] Sending writes a bus request to `api` and returns; nothing in the TUI process performs HTTP
- [ ] Frame tests: one assertion per state; clamp **negative control** with a fixture that actually overflows; selection-restore by id; confirm-then-recheck; secrets-masked (a token value never appears in the frame)

### Phase 4: Retire the Claude agent, re-point the modal, update docs

- [ ] Delete `agents/api-tester.md`; remove the `api` arms in `bus/launch.go` (`AgentFileName`, CLI env, model env, default model, system prompt) and `bus/launcher.go:116`; drop the `api` tool profile (`bus/profile.go:849`)
- [ ] `muxcode agent launch api` refuses with a pointer to `muxcode api serve`
- [ ] Replace the `api` modal config (`bus/modal.go:163`) and re-point `bind i` / the menu entry (`config/tmux.conf:51`, `:62`) per Decision 1; `AutoCap` off — the frame is deterministic
- [ ] Audit the remaining `"api"` sites (`compact.go:162`, `scrub.go:167`, `diagnose.go:1424`, `main.go:22,208`, `config.go:17`): keep where the token means the bus role, remove where it means a Claude pane; `grep -rn '"api"' tools/muxcode --include=*.go` afterwards must show only role-meaning sites
- [ ] Docs: `agent-bus.md` (`muxcode api send|serve`, the bus grammar), `agents.md:105` (roster row → served, no LLM), `architecture.md` (control-pane surface list; a *served roles* paragraph next to the prompt-agent's), `configuration.md` (`MUXCODE_API_AGENT_DISABLE`, `MUXCODE_API_TIMEOUT_SECS`; drop `MUXCODE_API_CLI` / `_CLAUDE_MODEL`), `README.md:92,208,669`, `CLAUDE.md:108` routing line
- [ ] The edit agent's delegation example for API testing uses the grammar — note the user's global `~/.claude/CLAUDE.md` carries the old free-text example and is the user's file to change

### Phase 5: Integration test

- [ ] Create `scripts/test-api-surface.sh` — hermetic: scratch `BUS_SESSION`, scratch tmux session, scratch project dir with `examples/api` imported, a stub HTTP server started **by the script** (a script may bind a port; a Go test may not); coverage floor so a skipped section cannot read as green
- [ ] `--render-once` frames: empty, populated, in-flight, done, server-not-running, and clamped narrow/short — assert each state's text marker; negative control: the overflowing fixture stays within `height`
- [ ] `muxcode api send httpbin-basics/get-test --env httpbin` against the stub → history row with status, duration and scrubbed body; an unresolved-variable send → error and **no** row
- [ ] Scratch daemon launches `muxcode api serve`; kill it → relaunched within the cooldown; `muxcode diagnose api` names the pid
- [ ] Bus path: `muxcode send api api "send httpbin-basics/get-test --env httpbin" --wait` returns the one-line result; a free-text payload returns the `usage:` reply and writes no row
- [ ] Live pane: `Tab` cycles five surfaces; `prefix + i` lands on the API surface; a mutating send to a non-local environment shows the confirm and `n` sends nothing
- [ ] Negative wiring controls: `muxcode agent launch api` refuses; `muxcode reload api` explains; the provider selector's output does not list `api`
- [ ] Run the script and record pass/fail counts in this spec

## Deferred

Recorded so they are not re-derived; none is in scope above.

- Postman collection v2.1 import/export (`muxcode api import --postman <file>`)
- Response capture into environment variables (`capture: {"token": "$.access_token"}`) for chained auth flows
- Per-request assertions (expected status, JSONPath equality) rendered as pass/fail in the surface
- Editing collections and requests from inside the surface

## Notes

- [MUX-145](./MUX-145-messages-routed-to-windowless-role.md) — the `api` role becomes
  windowless **by design**; land on its predicate, not a private one.
- [MUX-107](./MUX-107-tui-component-kit.md) — a fifth surface duplicating tab bar, footer, list and
  confirm strengthens the case for the kit; this spec does not depend on it.
- [MUX-146](./MUX-146-remove-research-and-auto-agents.md) — the same removal shape (definition,
  role registration, env vars, docs) applied to two other roles; the audit step in Phase 4 mirrors its
  approach.
- [MUX-109](../completed/MUX-109-prompt-mode-graph-control-pane.md) — the pattern being reused, and
  the record of why a model on a deterministic path is a liability, not a convenience.
- [MUX-037](../completed/MUX-037-api-testing-agent.md) — the original API agent; its data layer and
  CLI survive unchanged.

## Status

**Backlog** — filed 2026-09-08 on the user's request. Not started. 0/53 items.
