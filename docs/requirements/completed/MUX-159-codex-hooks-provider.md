# A Codex Hooks Provider: Put Codex Agents on the Deterministic Chain Road

**Tracking:** [mkober/muxcode#76](https://github.com/mkober/muxcode/issues/76)

Codex CLI ships lifecycle hooks — verified 2026-09-08 on the installed `codex-cli 0.153.4` and against
the official reference — and muxcode does not use them. `CodexProvider.SupportsHooks()` returns
`false` (`bus/provider_codex.go:390`, "Codex CLI's hook system is not integrated"), so every codex
agent runs the **non-hook road**: chain instructions pasted into its prompt, reply reminders injected
as text, and the daemon **pane-scraping** to guess when a task finished and what it said.

That road produced every incident of 2026-09-08: a rule line closed the graph's build and test nodes
and held them for manual approval on three runs ([MUX-154](../backlog/MUX-154-codex-status-line-closes-tracked-tasks.md)),
a review reply injected as a prompt drove a fourteen-request test↔review loop
([MUX-009](../backlog/MUX-009-response-echo-chain-retrigger.md)), `verify-spec` fired on chrome, and the build
agent answered three requests by restating test's status line as its own `EXIT=0` (`6b53863`).
Codex's hooks are the same shape as the Claude hooks muxcode already runs — `PreToolUse`,
`PostToolUse`, `Stop`, the same `{"decision":"block","reason":…}` answer — so the fix is to write a
`hooks.json`, not to build a new protocol.

## Context

### What Codex ships (verified)

Read from the installed binary's strings and from the official hooks reference
(`learn.chatgpt.com/docs/hooks`, redirected from `developers.openai.com/codex/hooks`).

| Aspect | Codex CLI 0.153.4 |
|--------|-------------------|
| Events | `SessionStart`, `SessionEnd`, `PreToolUse`, `PostToolUse`, `PermissionRequest`, `UserPromptSubmit`, `Stop`, `Interrupt`, `SubagentStart`, `SubagentStop`, `PreCompact`, `PostCompact` — the binary carries the snake_case names (`pre_tool_use` … `stopped`) and `HookStarted`/`HookCompleted` events |
| Config files | `~/.codex/hooks.json` (user), `<repo>/.codex/hooks.json` (project), or `[hooks]` tables in either `config.toml`; layers merge, and a layer holding both forms warns at startup. Plugins bundle `hooks/hooks.json` |
| Handlers | `command` (any executable) and `mcp_tool`; `prompt`/`agent` handler types parse but are skipped. `matcher` is a regex over the tool name; `timeout` in seconds, default **600** |
| Tool names | `Bash` for shell **and** unified exec ("match as `Bash`"); `apply_patch` for file edits |
| stdin (snake_case) | common: `session_id`, `cwd`, `hook_event_name`, `turn_id`; tools: `tool_name`, `tool_input`, `tool_response` ("tool-specific output"); Stop: `stop_hook_active`, `last_assistant_message` |
| stdout (camelCase) | `PreToolUse`: `permissionDecision` (`allow`/`deny`) + `updatedInput`, `systemMessage`, `additionalContext`. `Stop`/`SessionStart`/`UserPromptSubmit`: `continue`, `stopReason`, `systemMessage`, `suppressOutput`, `additionalContext` |
| Stop semantics | `continue: false` ends the turn and takes precedence; **`decision: "block"` + `reason` makes Codex continue with `reason` as a new user prompt** — the binary's own string: *"Stop hook requested continuation without a prompt; ignoring the block"* |
| Trust | Non-managed hooks need review and **persisted hash-based trust** (`/hooks` in the TUI to inspect, trust, or disable); an untrusted file prints a warning at startup and its hooks are skipped. `--dangerously-bypass-hook-trust` runs enabled hooks for one invocation ("intended only for automation that already vets hook sources"). Managed hooks (system, MDM, cloud, `requirements.toml`) are trusted by policy; `allow_managed_hooks_only` restricts to those |
| Feature flag | On by default; `[features] hooks = false` disables (`codex_hooks` is a deprecated alias) |
| Environment | No documented variables beyond plugin roots — a hook command inherits the codex process's environment |
| Where they fire | **Confirmed live in the interactive TUI** (Phase 1 spike, 2026-09-08, `scripts/spike-codex-hooks.sh`): `SessionStart`, `UserPromptSubmit`, `PreToolUse`/`PostToolUse` for `Bash` and `apply_patch`, `Stop` and `SessionEnd` all fired and were captured. `codex exec` not tested (out of scope) |
| **Live:** stdin common fields | `session_id`, `turn_id`, `transcript_path`, `cwd`, `hook_event_name`, `model`, `permission_mode` (`bypassPermissions` under `-a never`) |
| **Live:** `Bash` payloads | `tool_input.command` is a plain string; `tool_use_id` is `exec-<uuid>`; **`PostToolUse` `tool_response` is a bare string of stdout with no exit code** (`"spike-ok\n"` for `echo spike-ok; exit 3`) |
| **Live:** `apply_patch` payloads | the whole patch arrives in `tool_input.command` (`*** Begin Patch` / `*** Add File: <absolute path>` / `*** End Patch`); `tool_response` is exec-style text — `Exit code: 0`, `Wall time: …`, `Output:`, `Success. Updated the following files:`, `A <path>` |
| **Live:** where the exit code lives | the rollout transcript named by `transcript_path`: an `event_msg`/`item_completed` record whose `payload.item.id` equals the hook's `tool_use_id`, carrying `exit_code` (3 / 0) and `status` (`failed` / `completed`); `FileChange` items carry `status` only |
| **Live:** `Stop` | `stop_hook_active` false, then **true on the continuation's own Stop**; `last_assistant_message`; `{"decision":"block","reason":…}` continued the agent with `reason` as its next prompt (marker word echoed) |
| **Live:** `UserPromptSubmit` | `prompt` field; `hookSpecificOutput.additionalContext` reached the model (marker echoed in all three turns) |
| **Live:** `SessionStart` / `SessionEnd` | fire; `source: "startup"`, `reason: "other"` |
| **Live:** trust store | **not created** under `--dangerously-bypass-hook-trust` (no `~/.codex/hooks.state*`); the binary names `hooks.state` and a `config/batchWrite … hook trust` path — pre-seeding stays undocumented, Decision 2 stays **A** |
| **Live:** version output | `codex --version` → `codex-cli 0.153.4`; `[features] hooks` is readable from `config.toml` before launch (`CodexHooksFeatureDisabled`) |
| **Live:** fixtures | every payload above is pinned under `bus/testdata/codex-hooks/` (13 files + README, paths rewritten to `/home/dev`) |

Nothing is configured on this machine: no `~/.codex/hooks.json`, no `[hooks]` in `~/.codex/config.toml`,
no trust store yet. The repo's `.codex/` holds only the generated `AGENTS.md` (gitignored,
`.gitignore:20-21`) and `review/`. After the Phase 1 spike there is still no trust store — the bypass
flag created none — and `.codex/hooks.json` is now written per launch by `PrepareCodexHooks` and
gitignored beside `AGENTS.md` (`.gitignore:24`).

### What muxcode's Claude hooks do (verified)

| Piece | Where |
|-------|-------|
| Registration | `config/settings.json` — `PreToolUse` `Bash` → `muxcode hook guard`; `Write\|Edit\|NotebookEdit` → `muxcode hook guard` + preview; `PostToolUse` `Bash` → `muxcode hook bash`, `Write\|Edit\|NotebookEdit` → `muxcode hook analyze`, `Write\|Edit` → `muxcode hook comment-block`; `Stop` → `muxcode hook stop`. Merged into `~/.claude/settings.json` by `install.sh:813` |
| Subcommands | `cmd/hook.go:22-32`: `bash` (`:45`), `guard` (`:234`), `analyze` (`:305`), `inbox-poll` (`:381`), `stop` (`:434`), `comment-block` (`:344`) — each returns early when `provider.SupportsHooks()` is false |
| Event shape | `ToolEvent` (`bus/hook.go:15`): `tool_name`, `tool_input.{command,file_path,…}`, `tool_response` / `tool_result` / `exit_code`, `stop_hook_active`; `GetExitCode()` digs `exit_code` out of `tool_response`, treats `interrupted` and an `Error:` stderr as failure |
| Identity | `BusSession()` reads `BUS_SESSION` (`bus/config.go:76`), `BusRole()` reads `AGENT_ROLE` (`:92`) — environment, so a hook subprocess inherits them from the pane muxcode launched |
| Answers | `FormatGuardBlock`/`FormatStopBlock` (`bus/hook.go:1229`) emit `{"decision":"block","reason":…}`; `DecideStopHook` (`:1285`) blocks a stop when the inbox listener is dead or actionable messages are pending |
| What `hook bash` does | `ProcessBashHook` (`:532`) writes the console-history row with the real exit code and `triggerChain` (`cmd/hook.go:120`) fires build→test→review — the deterministic chain |
| Guards | `CheckGuard` (`:783`), `CheckBashFileWriteGuard` (`:1045`), `CheckDocFileGuard` (`:1126`), `CheckAtlassianCommandGuard` (`:1178`) — all `PreToolUse`, all Claude-only today |

### The mapping

| Codex event / field | muxcode today | Fit |
|---------------------|---------------|-----|
| `PostToolUse`, matcher `Bash` | `muxcode hook bash` | Direct. Console-history row with a real exit code; `triggerChain`. `tool_response` is "tool-specific" — Phase 1 records the real shape so `GetExitCode()` can read it |
| `PreToolUse`, matcher `Bash` | `muxcode hook guard` | Direct. Answer `permissionDecision: "deny"` + `permissionDecisionReason` instead of `decision: block` |
| `PreToolUse`/`PostToolUse`, matcher `apply_patch` | guard / analyze / comment-block on `Write\|Edit` | Same intent, different `tool_input` — a patch, not `file_path` + `content`. Phase 1 captures it; the doc-file and never-author guards need the touched paths |
| `Stop` | `muxcode hook stop` | Direct, and better: `stop_hook_active` exists, and `decision: "block"` + `reason` becomes the next prompt — a delivery channel, not just a veto |
| `UserPromptSubmit` → `additionalContext` | — (no Claude equivalent used) | New: expand a fixed wake sentence into the inbox payload at prompt time |
| `SessionStart` | — | Optional: register the pane, write the launch lifecycle row |
| `PermissionRequest` | — | Not needed: agents launch `-a never` or `on-request` (`provider_codex.go:52-56`) |

### What `SupportsHooks()` really gates — and why it cannot simply flip

The flag is consulted in **44 places**, and they do not all ask the same question. Three
capabilities hide behind one boolean:

| Question the gate really asks | Sites |
|-------------------------------|-------|
| *Do chains fire from hooks?* (so the prompt need not carry them) | `SharedPrompt` (`bus/prompt.go:62,102,129,216`), `CheckSendPolicy` bypass (`bus/profile.go:521`), `refuseWithoutDefinition` (`bus/launch.go:911`), `checkDefinitionless`/`definitionApplied` (`daemon/definition_watchdog.go:76,184`) |
| *Does the agent self-poll its inbox?* (receipt-ack road vs injection) | `Notify`, `SendWakeUpWithText`, `HasPendingInput`, `ClearParkedInput` (`bus/notify.go:604,863,409,455`), `checkIdleAgents` (`daemon.go:2311`), `checkParkedInput` (`:2475`), `checkPaneSweep` (`:2594`), `listenerless` (`:2107`), `checkPollHealth` (`:2052`), `checkIdleTaskCompletion` (`:3225`), `New` (`:252`) |
| *Is the pane the only evidence?* (scrape for completion, edits, stuck loops) | `checkNonHookTasks` (`:2777`) → `DetectTaskCompletion`, `checkNonHookEdits` (`:3020`), `checkStuckProviders` (`:1129`), `checkActiveWatchdog` skip (`:1019`), `checkStuckPermissions` (`:1284`), `AgentIsWorking` (`bus/timetrack.go:220`) |
| *Does the TUI need auto-accept / a different stop sequence?* (a TUI question, not a hook one) | `GracefulStop`, `wakeAfterReload` (`bus/reload.go:129,164,488`), `modeAutoAcceptAndWake` (`bus/mode.go:492`), `AutoAccept` (`bus/launcher.go:731`) |

Flipping the boolean flips all four at once. A codex agent with hooks has chains and evidence, but it
still has no `❯` prompt (`IsIdle` returns `false`, `IdlePromptChar` is `""`, `provider_codex.go:189,394`)
and — unless Decision 1 goes the listener way — no background self-poll. The first job is therefore
to **split the capability**, not to flip it.

### Delivery on the hook road — the design question

Claude's receipt-ack road works because the agent runs `muxcode inbox --poll --loop` in the
background and the `Stop` hook re-launches it. Whether a codex shell session can hold a background
process across turns is unverified (the binary has unified-exec sessions — `exec_command`,
`write_stdin` — but nothing says they outlive a turn). Codex's own hooks offer a road that needs no
listener at all:

| Moment | Mechanism |
|--------|-----------|
| Turn ends | `Stop` hook (`muxcode hook stop`): actionable inbox pending → consume, write the receipt, answer `decision: "block"` with `reason` = the messages plus the reply instruction — Codex continues with them as its next prompt. Nothing pending → answer nothing; the agent idles |
| Idle agent, new message | `SendWakeUp` injects the **fixed** sentence `You have new messages` — never a payload |
| That prompt is submitted | `UserPromptSubmit` hook (`muxcode hook prompt-submit`): recognizes the sentence, consumes the inbox, writes receipts, returns the messages as `additionalContext` |

Three properties follow. A response is **never injected as a prompt** — it arrives as context, which
is MUX-009's fix at the root. Every consume writes a true `acked` receipt from the agent's own
process, the same evidence Claude's listener produces. And the reminder text that
`provider_codex.go:339-353` wraps around every injection today ("IMPORTANT: … you MUST run …
— REMINDER …") goes away, because the reply instruction rides in `additionalContext` once, not in
the pane forever.

### Trust

muxcode writes the `hooks.json` itself, which is exactly the "automation that already vets hook
sources" the bypass flag names. But the file sits in the repo, agent-writable, and an agent that
appends its own handler must not get it trusted through muxcode's flag. So the flag is passed **only
when the file on disk hashes to what muxcode wrote** (the writer records the hash under
`BusDir()/codex-hooks.<role>.sha256`; `BuildExecArgs` re-hashes and refuses to launch with the flag
on a mismatch, lifecycle `codex-hooks-tampered`). Pre-seeding Codex's own trust store is the
cleaner end state — its file and hash form are undocumented; Phase 1 finds them (the binary names a
`hooks.state` and a *"config/batchWrite … updating hook trust"* path).

### Scope boundary

**In:** codex in the interactive TUI muxcode launches; the capability split; the `hooks.json` writer
and trust handling; hook subcommands accepting codex payload shapes; chains and console history on
`PostToolUse`; Stop/`UserPromptSubmit` delivery; guards on `PreToolUse`; per-role opt-in then default
flip; docs; integration test.

**Out:** OpenCode and the local harness (no hook system to integrate); `codex exec` workers
(`bus/codex_events.go` already parses their `--json` stream — a different road); `mcp_tool` handlers
and plugins; `PermissionRequest`; removing the scrape machinery itself
([MUX-012](../backlog/MUX-012-remove-gated-pane-scrape-delivery.md) — this spec makes codex stop *needing*
it, and MUX-012 deletes it).

## Requirements

### Acceptance criteria

- [x] A codex agent launched with hooks enabled has `<repo>/.codex/hooks.json` written by muxcode before launch, and the hooks run in the TUI — proven by a `PostToolUse` row in the console history after its first shell command, with the real exit code — live section 2026-09-09 00:02: hooks written for the live session, `PostToolUse` row from the TUI carrying the transcript exit code
- [x] `SupportsHooks()` no longer conflates chains, self-poll and scrape: each of the 44 gates asks the capability it actually needs, and a table in `docs/hooks.md` says which
- [x] Build→test→review fires from `PostToolUse` for codex exactly as for Claude; the codex prompt carries no chain instruction and no reply reminder; `CheckSendPolicy` grants it no bypass
- [x] A graph `send` node whose codex agent ran the command routes on an **authoritative** history row — no unverified hold on a passing build or test (negative control: a failing command routes failure, not unknown) — `TestCodexHookRow_IsAuthoritativeForGraph` pins both outcomes and a status-less row as unknown
- [x] `checkNonHookTasks` never scrapes a hook-enabled codex agent; no `task-detected` row is written for it in a full session — *by design (`PaneIsEvidence()` false, `daemon.go` `checkNonHookTasks`); the live run's scratch session has no daemon, so the full-session proof is still outstanding; **2026-09-09** the skip is pinned where it executes by `daemon/codex_hook_road_test.go` — `TestCheckNonHookTasks_HookCodexNeverScraped` never captures the hook-road pane, leaves its task in flight and synthesizes nothing, while a scrape-road codex in the same session is scraped to completion with exactly one `task-detected` row — **green 00:55** (`go test ./...` exit 0 as a hook row, 2341 PASS / 0 FAIL); the full-session observation stays an observation, the daemon-level pin is the proof*
- [x] A pending actionable message is delivered by the `Stop` hook as the agent's next prompt, with a true `acked` receipt written before Codex continues
- [x] An idle hook-enabled codex agent is woken with the fixed sentence only; the payload arrives through `UserPromptSubmit` → `additionalContext`; **a `type: response` is never injected as prompt text** (MUX-009 negative control: the receiving agent's chain does not re-fire)
- **Deferred → live-run follow-up (2026-09-09 09:40, user-approved):** `PreToolUse` on `Bash` and `apply_patch` runs `hook guard`; a denied command answers `permissionDecision: "deny"` with the reason, and the command does not run (positive control first: an allowed command passes) — *hermetic half done (allow passes silently, deny answers the JSON — script); that Codex then refuses to run the command is proven only live*
- **Deferred → [MUX-157](../backlog/MUX-157-role-boundary-an-agent-can-ignore.md) (2026-09-09 09:40, user-approved):** The never-author and doc-file guards hold for codex build/test/review — [MUX-157](../backlog/MUX-157-role-boundary-an-agent-can-ignore.md)'s missing road — *doc-file holds on every patch path; the never-author rule set does not exist in `guardRulesForRole` yet (MUX-157's to add — it will apply to both providers unchanged)*
- [x] The trust flag is passed only when `hooks.json` hashes to what muxcode wrote; a tampered file refuses launch with a lifecycle row
- [x] Every `muxcode hook` subcommand is a no-op outside a muxcode session (`BUS_SESSION` unset) — a developer's own codex in this repo is unaffected
- [x] Per-role opt-in (`MUXCODE_CODEX_HOOKS`, then per-role `MUXCODE_<ROLE>_CODEX_HOOKS`) with the scrape road as the fallback; an older codex (no hooks) or `[features] hooks = false` is detected and falls back with a lifecycle row, never a silent half-state
- [x] `scripts/test-codex-hooks.sh` passes with a coverage floor; its live section skips with a reason when no codex ≥ 0.153 is installed — hermetic 37/37 (2026-09-09 00:07) and live 7/7 (00:02, `MUXCODE_CODEX_HOOKS_LIVE=1`); without the gate the live section prints its skip reason. The first run's 33/35 was the in-flight task guard on same-action sends, fixed with per-section action names — see Phase 7. *Floor raised to 39 on 2026-09-09 (two evidence-rule cases) and run at 01:15 — **hermetic 39/39, exit 0** (run agent, inside graph run `1788930816-spec-to-pr-f7fb2610`); the live 7/7 stands from 00:02 and was not re-run*
- [x] Docs updated with the code: `hooks.md`, `architecture.md` (Codex CLI Agent Flow), `configuration.md`, `agents.md`, `CLAUDE.md`

### Technical approach

- **Split before flipping.** Replace the single boolean on the `Provider` interface (`bus/provider.go:66`)
  with three questions — `SupportsHooks()` (chains and guards fire from hooks), `SelfPollsInbox()`
  (a background listener exists, so notify may rely on receipts) and `PaneIsEvidence()` (completion
  and edits must be scraped) — and route each of the 44 sites to the question it asks. Claude answers
  yes/yes/no, the scrape codex yes-less no/no/yes, hook codex yes/no/no. The TUI questions
  (`GracefulStop`, `AutoAccept`, mode wake) key on the provider name, which is what they were about.
- **Write, don't install.** `CodexProvider.WriteAgentConfig` (`provider_codex.go:400`) already writes
  `.codex/AGENTS.md` per launch; it writes `.codex/hooks.json` beside it, atomically, gitignored,
  from one Go template: `PreToolUse` `Bash|apply_patch` → `muxcode hook guard`; `PostToolUse` `Bash`
  → `muxcode hook bash`, `apply_patch` → `muxcode hook analyze`; `Stop` → `muxcode hook stop`;
  `UserPromptSubmit` → `muxcode hook prompt-submit`. Project scope, because `AGENTS.md` is already
  project scope and `~/.codex` would leak into every repo.
- **One parser, two dialects.** `ParseToolEvent` (`bus/hook.go:44`) learns the codex `tool_response`
  shape and `apply_patch` input (paths extracted from the patch header) instead of a second event
  type; `FormatGuardBlock` gains a codex emitter (`hookSpecificOutput.permissionDecision`), `hookStop`
  keeps `{"decision":"block","reason"}` because Codex reads that form too.
- **Delivery through hooks, not the pane** (Decision 1). `hook stop` consumes and delivers; `hook
  prompt-submit` expands the wake sentence; `SendWakeUp` for hook codex injects the sentence and
  nothing else. `MarkResponded`/receipts unchanged — the consume is the ack.
- **Guards become enforcement on codex.** `hookGuard` already returns early for non-hook providers
  (`cmd/hook.go:254`); once codex is a hook provider, `HasGuardRules` decides per role, and MUX-157's
  build/test/review rules get the road they lack.
- **Trust with an integrity check** (Decision 2): hash on write, re-hash on launch, flag only on match.
- **Opt-in, then default.** `MUXCODE_CODEX_HOOKS=1` for a session, per-role override, default flipped
  in Phase 6 only after the live integration section is green on this machine.

### Key files

| File | Change |
|------|--------|
| `tools/muxcode/bus/provider.go` | Capability split on the interface; every provider answers all three |
| `tools/muxcode/bus/provider_codex.go` | `SupportsHooks` by opt-in and version; `WriteAgentConfig` writes `hooks.json` + hash; `BuildExecArgs` passes the trust flag under the integrity check; `SendWakeUp` injects the fixed sentence for hook codex |
| `tools/muxcode/bus/codex_hooks.go` | **New** — template, writer, hasher, version detection (`codex --version` ≥ 0.153), `[features] hooks` read |
| `tools/muxcode/bus/hook.go` | `ParseToolEvent` codex dialect (`tool_response`, `apply_patch`), codex guard emitter, `hook prompt-submit` logic |
| `tools/muxcode/cmd/hook.go` | `prompt-submit` subcommand; codex answers from `guard`/`stop`; no-op without `BUS_SESSION` on every subcommand |
| `tools/muxcode/bus/prompt.go`, `bus/profile.go` | Chain text and send-policy bypass keyed on the split capability |
| `tools/muxcode/bus/notify.go`, `daemon/daemon.go` | Self-poll and scrape gates re-keyed; `checkNonHookTasks`/`checkNonHookEdits` skip hook codex |
| `config/settings.json` | Unchanged — Claude's registration stays where it is |
| `.gitignore` | `.codex/hooks.json` |
| `scripts/test-codex-hooks.sh` | **New** — Phase 7 |
| `docs/hooks.md`, `docs/architecture.md`, `docs/configuration.md`, `docs/agents.md`, `CLAUDE.md` | Phase 6 |

### Decisions — taken 2026-09-08 with the recommended options

| # | Question | Options | Recommendation |
|---|----------|---------|----------------|
| 1 | Delivery road for hook codex | **A** background listener like Claude (`muxcode inbox --poll --loop` kept alive by `Stop`) — depends on a codex shell session surviving across turns, unverified. **B** `Stop` + `UserPromptSubmit` delivery, no listener | **B** — it needs nothing unverified, removes payload injection entirely, and writes the same receipts |
| 2 | Trust | **A** `--dangerously-bypass-hook-trust` under muxcode's integrity check. **B** pre-seed Codex's trust store. **C** managed `requirements.toml` | **A** now; move to **B** once Phase 1 records the store's file and hash form; **C** is an enterprise layer, not a developer machine |
| 3 | Where `hooks.json` lives | Project `<repo>/.codex/hooks.json` (beside `AGENTS.md`) vs user `~/.codex/hooks.json` | **Project** — user scope leaks into every repo and every non-muxcode codex |
| 4 | Rollout | Opt-in env → default on after the live test, or default on from the start | **Opt-in first**; the scrape road stays as the fallback the flag selects |

**Taken** (Phase 1, on the user's one-pass instruction): **1 → B** — `hook stop` + `hook prompt-submit`,
no listener (`SelfPollsInbox()` false). **2 → A** — `--dangerously-bypass-hook-trust` under the sha256
check; the trust store was not created under the flag and its form is undocumented, so **B** stays open.
**3 → project** — `<repo>/.codex/hooks.json`, gitignored. **4 → opt-in first** — `codexHooksDefault = false`
in `bus/codex_hooks.go` at hand-off, **flipped to `true` 2026-09-09 00:10** after the live run went green
(Phase 6 step 4); the env variables are now the opt-out.

## Implementation

### Phase 1: Establish the contract (spike — no behaviour change)

- [x] Write a throwaway `.codex/hooks.json` whose every handler is `muxcode hook record` (new, hidden): append the raw stdin JSON and the event name to `BusDir()/hook-capture.jsonl` — `scripts/spike-codex-hooks.sh`, kept for future codex versions
- [x] Launch one codex agent through muxcode with `--dangerously-bypass-hook-trust` and drive it through a shell command, an `apply_patch` edit, a turn end and a fresh prompt; confirm hooks fire in the TUI — run 2026-09-08 in a scratch tmux session inside this repo
- [x] Record the real `tool_response` shape for `Bash` (where the exit code lives), the `apply_patch` `tool_input` shape, and the `Stop`/`UserPromptSubmit` payloads — as fixtures under `bus/testdata/codex-hooks/`
- [x] Confirm `Stop` `decision: "block"` + `reason` continues the agent with `reason` as the prompt, and that `stop_hook_active` is set on the continuation's own Stop
- [x] Confirm `UserPromptSubmit` `additionalContext` reaches the model (a marker phrase the agent is asked to echo)
- [x] Find Codex's trust store: file, hash input, whether it can be pre-seeded; record the finding under Decision 2 — finding: not created under the bypass flag, form undocumented
- [x] Check `codex --version` output shape for the version gate, and whether `[features] hooks = false` is readable from `config.toml` before launch
- [x] Record every finding in this spec's *What Codex ships* table; take Decisions 1–4

### Phase 2: Capability split and the hooks.json writer

- [x] Split `SupportsHooks()` into the three questions on `Provider`; re-key all 44 sites; table them in `docs/hooks.md` — plus `IsClaudeTUI()` for the TUI-identity sites and `IdlePromptChar() != ""` for the active watchdog
- [x] Negative control: with hooks **off**, every codex behaviour is byte-for-byte what it is today (chain text present, scrape active, injection with reminders) — pinned by the existing codex tests plus a golden of `SharedPrompt` — `TestCodexHooks_ScrapeRoadUnchanged` pins the `SharedPrompt` markers (Manual Bus Messaging, Console History Logging, the daemon-wake sentence, `REMINDER`) present off and absent on; string pins, not a byte golden
- [x] `bus/codex_hooks.go`: template → `.codex/hooks.json`, atomic write, sha256 recorded under `BusDir()`; `.gitignore` entry
- [x] Version gate and feature-flag read; an ineligible codex logs `codex-hooks-unavailable` and stays on the scrape road
- [x] `BuildExecArgs`: trust flag only when the on-disk hash matches; mismatch → `codex-hooks-tampered`, launch refused
- [x] `ParseToolEvent` codex dialect from the Phase 1 fixtures; `GetExitCode()` reads the real field — from the rollout transcript by `tool_use_id`; no transcript → unknown, never the Claude default `0`
- [x] Tests: writer output, hash match/mismatch, version gate, parser against every fixture, no-op without `BUS_SESSION` — 13 in `codex_hooks_test.go`, 19 in `hook_codex_test.go`; the `BUS_SESSION` no-op is asserted across every subcommand in the script's hermetic section

### Phase 3: Chains and evidence on the hook road

- [x] `PostToolUse` `Bash` → `hook bash` → `ProcessBashHook` writes the console-history row with the real exit code and fires `triggerChain` for codex build/test/deploy/run
- [x] `SharedPrompt` emits no chain instruction and no reply reminder for hook codex; `CheckSendPolicy` grants it no bypass
- [x] `checkNonHookTasks` and `checkNonHookEdits` skip hook codex; `checkStuckProviders` keys on `PaneIsEvidence()`
- [x] Graph: `deriveSendOutcome` finds an authoritative row for a codex node — pin with a build that fails (`failure`, routed to `fix`) and one that passes (`success`, no hold) — **2026-09-09 00:28 finding**: the pin holds only when the build is a lone call. The first `spec-to-pr` run on this spec (`1788927531-spec-to-pr-7fbfba25`) parked its build node on `graph-unverified-hold` after the codex build agent answered with one Bash call — three `muxcode send` acks, `./build.sh`, a hand-typed result — which `ClassifyCommand` read as a bus command: no exit-code row, no chain, and the prose reply became the only history row (`source: bus-response`, `outcome: unknown`). `./build.sh` itself had passed. The hold was correct; the run was canceled at 00:39 and the gap is closed by the evidence guard (items 8–12)
- [x] `apply_patch` `PostToolUse` → `hook analyze` with the paths from the patch — one trigger per path
- [x] Negative control: a scrape-road codex agent in the same session still gets `task-detected` completions — *design only: `PaneIsEvidence()` true keeps the scrape checks; no daemon-level test, and the live control is gated; **delivered 2026-09-09 00:27** by the graph's implement spawn (`spawn-e0035225`) as the same-session control inside each of the three `daemon/codex_hook_road_test.go` tests — the scrape-road role is captured, its task completed with a synthesized response and one `task-detected` row, one provider-loop sighting counted, the dirty tree diffed and the analyze trigger written — **green 00:55**, ticked*
- [x] Tests for each, including the graph pins — *open: the `PaneIsEvidence()` skip in the daemon has no unit test (pinned at the provider level only); everything else in this phase is tested; **delivered 2026-09-09 00:27** — `daemon/codex_hook_road_test.go` (`TestCheckNonHookTasks_HookCodexNeverScraped`, `TestCheckStuckProviders_HookCodexNotJudgedByPane`, `TestCheckNonHookEdits_HookCodexSkipped`) runs the real checks against a stubbed pane through the existing `d.capturePane`/`d.agentAlive` seams, with the road chosen by the activation marker (`bus.CodexHooksMarkerPath`, newly exported) so `ResolveProvider` answers `PaneIsEvidence()` rather than a stub — **green 00:55**, ticked*
- [x] Hook-road evidence guard — `bus/evidence_guard.go`, `CheckEvidenceGuard` run by `hookGuard` after the delegation rules for build, test and deploy: a build/test/deploy statement must be the only statement in its call (a leading `cd … &&` and env assignments exempt; `;`/`&&`/`||`/newline chains, pipes and a trailing `&` denied in the provider's dialect with a `guard-denied` row, the reason naming the statement and telling the agent to run it alone and send acks separately) — shipped 00:38; review 00:46 requested two must-fixes (`./build.sh &` passed as one statement; `cd /tmp; ./build.sh && echo done` was prefix-stripped to `echo done`) and a comment nit — fixed (a background `&` denied with its own reason; statements split before the exemption, so a `cd` joined by `;` is not the prefix; the incident narrative moved to the docs); then a third must-fix at 00:52 — `patternHeadIs` (`hook.go:418`) compared a whole multiword pattern such as `go test` with the first token, silencing the documented `MUXCODE_*_PATTERNS` overrides and adjacent redirections like `./build.sh>/tmp/log` — fixed by `headAtBoundary`; review LGTM 00:54:59 (0 must-fix), **green 00:55**; the second run's 01:18 review found one more should-fix — `./build.sh 0<&0`, an input-fd duplication, split at the `&` and denied as backgrounding — and the `splitShellStatements` test-only wrapper (nit); both fixed 01:27 (`parseShellStatements` reads `<&` as a redirection beside `>&`, with a foreground/background `0<&0` pair in `TestCheckEvidenceGuard_BackgroundDenied`), re-review LGTM 01:29:55
- [x] Tests: `bus/evidence_guard_test.go` — the live bundled call denied and shown to classify as `CmdBus`, bundled shapes, background forms with their foreground positive controls, lone commands allowed, no-evidence compounds allowed (the definitions' own log-and-report sequence), role scope, splitter table, plus the `cd … ;`, background and pattern-boundary regressions from the three review rounds — **green 00:55**
- [x] `scripts/test-codex-hooks.sh` floor 37 → 39: a lone `./build.sh 2>&1` from build passes the guard; the live bundled shape answers `permissionDecision=deny` with an "only statement" reason — cases added 2026-09-08 (the user declined re-running the script twice that night); **run 2026-09-09 01:15** by the run agent for the second `spec-to-pr` run's implement spawn (`1788930816-spec-to-pr-f7fb2610`, `spawn-e2669d9b`): `bash scripts/test-codex-hooks.sh` exit 0 as a run-history row, **hermetic 39/39**, both evidence-guard cases green, live section skipped by its opt-in gate; watch read the log clean — ticked
- [x] Definitions say it: `agents/code-builder.md` step 2 and `agents/test-runner.md` step 1 run the build/test command as its own tool call, never bundled with a `muxcode send` or piped; test-runner's `go vet … && go test …` fallback — a compound the rule refuses — split into separate calls; `infra-deployer.md` checked and unchanged (its one-statement deploy passes) — and since 01:27 test-runner's fallback runs `go vet` as its own call **as a precheck**: a failing vet fails the run, a passing one is not the verdict, so the suite still runs (Notes (d))
- [x] Docs: `CLAUDE.md` "Hook-road evidence guard" constraint and the `bus/evidence_guard.go` code-reference row; `docs/hooks.md` hook-guard section carries the rule, the roles, the allowed and denied shapes, the reason text and the incident, and its Testing section the new floor

### Phase 4: Delivery on the hook road

- [x] `hook stop` for codex: actionable inbox → consume, receipt, `decision: "block"` with the messages and one reply instruction; nothing pending → no output
- [x] `hook prompt-submit`: recognizes the fixed wake sentence, consumes, writes receipts, returns `additionalContext`; any other prompt passes untouched; self-addressed and chrome payloads filtered at consume
- [x] `SendWakeUp` for hook codex injects the sentence only; `provider_codex.go:339-353` reminder wrapping is not applied — `injectWakeSentence`
- [x] `checkIdleAgents`/`checkParkedInput`/`checkPaneSweep` treat hook codex by receipts, not by pane; `checkPollHealth`'s receipt-gap backstop still covers it — `checkIdleAgents` hands non-self-poll roles to `provider.SendWakeUp` (the sentence), the other two are `IsClaudeTUI`-gated
- [x] MUX-009 negative control: deliver a `type: response` to a hook codex agent → its chain does not re-fire and the response text never appears in the pane as a prompt — `TestCodexStopDelivery_ResponseOnlyNeverPrompts`
- [x] MUX-154 negative control: no synthesized response is ever sent for a hook codex task; a `--wait` on it returns the agent's own reply — *by design (`PaneIsEvidence()` false → `checkNonHookTasks` never synthesizes); no test; **2026-09-09** `TestCheckNonHookTasks_HookCodexNeverScraped` asserts `FindResponseSince` finds nothing for the hook-road role while the scrape-road control gets its synthesized reply — a test now, **green 00:55***
- [x] `deliver --force` for hook codex re-injects the sentence and clears markers, never a payload — *by design (`ForceDeliver` → `SendWakeUpWithText` → `injectWakeSentence`); no test until **2026-09-09 02:09**: `injectWakeSentence` now sends through the `TmuxSendLiteral`/`TmuxSendKeys` seam (text and Enter still separate writes with the delay), and `TestCodexForceDeliver_HookRoadSentenceOnly` (`bus/codex_hooks_test.go`) pins stale notified markers cleared, sentence-only injection, inbox preserved with no receipt, markers re-marked, a non-force negative control and a Claude payload control — suite green 02:09:24 (`go test ./...` exit 0 hook row), review LGTM 02:10:07, ticked*
- [x] Tests: stop with/without pending, prompt-submit expansion and pass-through, receipts written, the two negative controls — *open: the MUX-154 control's test landed 2026-09-09 in `daemon/codex_hook_road_test.go`, **green 00:55**; the rest are `TestCodexStopDelivery_*` ×4, `TestCodexPromptSubmitContext`, `TestCodexWakeUp_HookRoadNeverConsumesInbox`; `deliver --force` joined 02:09 with `TestCodexForceDeliver_HookRoadSentenceOnly`*

### Phase 5: Guards on the hook road

- **Deferred → MUX-157 (2026-09-09 09:40):** `PreToolUse` `Bash` → `hook guard` → codex `deny` answer; `apply_patch` → doc-file and never-author guards on the patch's paths — *deny answer and the doc-file guard on every patch path shipped; the never-author family has no rules yet (MUX-157), so nothing enforces it on either provider; the decision core is `GuardDecisionFor` since 02:20 (item 3)*
- [x] Positive control first: an allowed command and an allowed edit pass with no output — script hermetic section
- **Deferred → MUX-157 (2026-09-09 09:40):** MUX-157 rules for build/test/review resolve through `HasGuardRules`/`CheckGuard` unchanged — one rule set, two providers — *unverifiable until MUX-157 adds the rules; the road is in place — and since **02:20** it is one function, `GuardDecisionFor`, with no provider-specific branch left (`cmd/hook.go` shed 70 lines; `FormatGuardBlockFor` only picks the dialect), so a rule added to `guardRulesForRole` reaches both providers by construction; open only because the rules do not yet exist to resolve*
- [x] Denials are attributable: lifecycle `guard-denied` names role, tool and reason
- [x] Tests: deny/allow for each guard family against codex payloads — *Bash and doc-file covered (script: allow then deny for both tools); **2026-09-09 02:20** `TestGuardDecisionFor_CodexPayloads` (`bus/hook_codex_test.go`) drives the extracted provider-agnostic core `GuardDecisionFor` with codex fixture payloads through every existing family in hook order — Atlassian write authority (every role), delegation, hook-road evidence, doc-file on each `apply_patch` path (`guardedPaths`) — allow and deny per family; suite green 02:22:34, review LGTM 02:23:13. The never-author family has no rules yet, so its tests are MUX-157's to add with them — ticked on the families that exist*

### Phase 6: Docs and rollout

- [x] `docs/hooks.md`: replace the "only Claude Code's hooks are integrated" paragraph; add the capability table and the codex event mapping — new `## Codex hooks` section (event mapping, payload dialect, delivery moments, trust, `BUS_SESSION` no-op, eligibility); `hook stop` and event-format notes updated
- [x] `docs/architecture.md` Codex CLI Agent Flow: the hook road, the delivery moments, the trust check — flow rewritten as two roads plus a *Two roads (MUX-159)* paragraph
- [x] `docs/configuration.md`: `MUXCODE_CODEX_HOOKS`, per-role override, `codex-hooks-*` lifecycle events; `CLAUDE.md` constraint line; `docs/agents.md` roster note — new `### Codex hooks` section with `CODEX_HOME` and `guard-denied`; `CLAUDE.md` capability-split constraint and `test-codex-hooks` in the test list; `agents.md` provider row, sandbox note corrected, hook-road paragraph, differences and receipts tables
- [x] Flip the default to on for codex ≥ 0.153 once Phase 7's live section is green on this machine; keep the env as the opt-out — live 7/7 at 00:02; `codexHooksDefault = true` landed 2026-09-09 00:10 (`MUXCODE_CODEX_HOOKS=0` or the per-role variable opts out). Flipped by the user (edit's 00:08 hand-off had left it pending the user's call; the decision was relayed at 00:13); an ineligible codex still falls back to the scrape road
- [x] Note in MUX-012 that hook codex no longer needs the scrape machinery it deletes

### Phase 7: Integration test

- [x] Create `scripts/test-codex-hooks.sh` — hermetic section: feed the Phase 1 fixtures to `muxcode hook bash|guard|stop|prompt-submit` in a scratch bus and assert the history row, the chain message, the deny JSON, the stop block JSON and the `additionalContext`; coverage floor — floor **37** exact (35 at hand-off, +2 MCP-matcher denial checks: the `PreToolUse` matcher admits `mcp__*` tool names and an Atlassian MCP write from build is denied through the guard); also asserts the guard positive controls, the `BUS_SESSION` no-op across every subcommand, opt-out restoring the scrape road, and a tampered file refused; uses a distinct action name per section so the in-flight task guard never suppresses a later section's edit→build send (the 30s dedup window is disabled as well)
- [x] Live section (requires `codex` ≥ 0.153 on PATH; skipped **with reason** otherwise): scratch session, one codex build agent with hooks on, dispatch a build → console-history row with the real exit code, chain request to test sent, **no** `task-detected` row for that role — run 2026-09-09 00:02 with `MUXCODE_CODEX_HOOKS_LIVE=1`: hooks written for the live session, `PostToolUse` row written from the TUI, the row carries the transcript exit code, build→test chain request sent by the hook (live checks 1–4). *The `task-detected` absence is not asserted: no daemon runs in the scratch session, so it would be vacuous there; it rests on `PaneIsEvidence()` false*
- [x] Live: deliver a request while idle → the pane shows only the wake sentence; the request's receipt reads `acked`; the reply correlates — live checks 5–6: the wake sentence was expanded by the hook with an `ack` receipt written, and the codex agent's reply reached edit
- [x] Live: deliver a response → no prompt text, no chain re-fire (MUX-009) — live check 7: the payload never appeared in the pane as a prompt; the no-re-fire half is the hermetic MUX-009 control (a response alone answers nothing and stays in the inbox)
- **Deferred → live-run follow-up (2026-09-09 09:40):** Live: a denied command (`git commit` from build) does not run and `guard-denied` is logged — *the live section has no denial; hermetically `git commit` from edit is denied with `permissionDecision=deny` and `guard-denied` is logged, but that Codex then refuses to run the command is not exercised*
- **Deferred → live-run follow-up (2026-09-09 09:40):** Live: tamper `.codex/hooks.json` → launch refused, `codex-hooks-tampered` logged; restore → launches — *refusal and lifecycle row proven hermetically at the launch code path (`agent config` exits non-zero before any exec); "restore → launches" is not asserted and no live tamper is run*
- [x] Negative control: `MUXCODE_CODEX_HOOKS=0` → the old road, `task-detected` present, chain text in the prompt — *old road proven hermetically (marker cleared, `hooks.json` removed, `hook bash` writes nothing for the opted-out role) and the prompt text by `TestCodexHooks_ScrapeRoadUnchanged`; `task-detected` present needs a daemon scraping a scrape-road agent — no run has exercised it, but since 2026-09-09 the daemon-level test's scrape-road control asserts exactly that row; **green 00:55***
- [x] Run the script and record pass/fail counts in this spec — **hermetic 37/37** (2026-09-09 00:07, exit 0) and **live 7/7** (00:02, `env MUXCODE_CODEX_HOOKS_LIVE=1`, exit 0), both via the run agent. The first run (2026-09-08 23:44) was 33/35, failing `context lacks the payload` (prompt-submit) and `orphan hook consumed the inbox` (outside-a-session); root cause was the **in-flight task guard** (`HasInFlightTaskForRole`) suppressing a later section's edit→build send while an earlier section's same-action request was still in flight — not the hooks, and not the dedup window as first recorded here — fixed by giving every section its own action name (the dedup window is disabled as well); the floor rose to 37 with the MCP-matcher checks

## Notes

- **2026-09-08 hand-off** (edit, one pass on the user's instruction; Phases 1–5 and 7 code in the
  tree, uncommitted when this was written): 32 new Go tests (`codex_hooks_test.go` 13,
  `hook_codex_test.go` 19) plus `scripts/test-codex-hooks.sh` (hermetic floor 37 — 35 at hand-off; the first
  run's 33/35 was the in-flight task guard on same-action edit→build sends, fixed with per-section action
  names; **proven 2026-09-09: hermetic 37/37 and live 7/7** with `MUXCODE_CODEX_HOOKS_LIVE=1`, which
  launches a real codex in this repo and spends API usage — the default was then flipped on at 00:10,
  `codexHooksDefault = true`). Caveats: (a) MUX-157's
  never-author rule set does not exist in `guardRulesForRole` yet — the hook road runs `hook guard`
  for codex on `Bash` and `apply_patch`, so the rules apply to both providers when MUX-157 adds them;
  the doc-file guard already holds on patch paths; (b) the `checkNonHookTasks`/`checkNonHookEdits`
  skip is by `PaneIsEvidence()` with no daemon-level unit test — the split is pinned at the provider
  level, the graph routing at the row level; (c) the MUX-154 negative control and `deliver --force` on
  hook codex were covered by design, not by a test — both since tested (the MUX-154 control 00:27 in
  `daemon/codex_hook_road_test.go`; `deliver --force` 02:09 by `TestCodexForceDeliver_HookRoadSentenceOnly`,
  once `injectWakeSentence` went through the `TmuxSendLiteral`/`TmuxSendKeys` seam); (d) Phase 2's negative control pins `SharedPrompt`
  by marker strings, not a byte golden. Pass counts are recorded under Phase 7 item 8.
- **2026-09-09 00:18–00:56** — the first `spec-to-pr` run on this spec (`1788927531-spec-to-pr-7fbfba25`,
  started by the user for Phase 3): `implement` (spawn `spawn-e0035225`) delivered items 6–7 in 542 s;
  `build` parked on the unverified hold recorded under Phase 3 item 4; the run was canceled at 00:39
  because only a person can release the hold and the guard invalidates its build evidence. Side
  findings: (a) `./build.sh` → `muxcode upgrade-daemons` fails inside the codex build sandbox —
  `ps: fork/exec /bin/ps: operation not permitted` (`bus/upgrade.go:69` shells out to `ps -axo`) — so a
  codex build agent never cycles the daemon while `build.sh` still exits 0; filed as
  [MUX-161](../backlog/MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md). (b) Three
  `graph-authority-refused spawn-e0035225` rows at 00:27:31: the implement spawn ran a test-classified
  command itself despite reporting "build/test left to the graph", and its hook chain was refused from
  firing into graph-owned roles — the authority guard working, and one more instance of
  [MUX-157](../backlog/MUX-157-role-boundary-an-agent-can-ignore.md)'s class, an instruction in prose
  that nothing enforced. (c) `verify-spec` fired at 00:46:24, two seconds after a review reply that
  answered `EXIT=1` (changes requested) — the plan notification did not read the verdict. (d) A lone
  `go vet ./...` classifies as a **test** success (`go*vet` sits in `DefaultTestPatterns`,
  `bus/hook.go:254`), so each cycle's chain fired review before the suite had run — harmless to
  outcomes because the newest row wins, but a decision is owed: drop `go*vet` from the test
  patterns, or keep vet as a separate non-chaining call (a `command_match` condition on the
  test→review chain could exclude it); bundling it with the suite is exactly what the evidence
  rule now refuses. **Decided 2026-09-09 01:27** (the second run's fix spawn, `spawn-567c25f0`): a
  third class — `CmdTestPrecheck`, `DefaultTestPrecheckPatterns = {"go*vet"}`,
  `MUXCODE_TEST_PRECHECK_PATTERNS` — whose failure is test evidence and whose success is not.
  `go*vet` left `DefaultTestPatterns`; `bus.ChainEvent` is now the single decision on what a call
  feeds (a passing precheck answers `""`: workflow moves to `testing`, no history row, no chain;
  `HookBashResult.Chain` replaces `Chained` and `cmd/hook.go` fires exactly that); test patterns are
  consulted before precheck ones, so a user-listed vet is a full run. Pinned by
  `TestProcessBashHook_TestPrecheck`, `TestChainEvent` and `TestClassifyCommand_TestPrecheckOverrides`
  (`bus/hook_test.go`), and seen live at 01:28:33 — the test agent's lone `go vet` transitioned the
  workflow and wrote nothing, the suite's row at 01:29:03 was the only test evidence and closed the
  node. The `0<&0` should-fix and the wrapper nit landed in the same pass (`parseShellStatements`
  reads `<&` as a redirection beside `>&`, with a foreground/background `0<&0` pair in the tests);
  re-review **LGTM 01:29:55, EXIT=0** (0 must-fix, 0 should-fix, 0 nits). Documented in
  `docs/hooks.md` (hook bash, chain, evidence rule) and `docs/configuration.md` (Hook Configuration).
- **2026-09-09 01:13–01:18** — the second `spec-to-pr` run on this spec (`1788930816-spec-to-pr-f7fb2610`,
  started by the user for Phase 3 again, one minute after the 01:12 session relaunch): `implement`
  (`spawn-e2669d9b`, 127 s) ported nothing — it ran item 10 through the run agent (hermetic 39/39) and
  left the tree as it stood; `build` green (`./build.sh` exit 0 as a hook row — the codex build agent's
  first call was denied by the evidence guard for bundling, its second was the lone call the rule asks
  for; `upgrade-daemons` failed on `ps` once more, MUX-161's fourth sighting); `test` green (`go vet`
  and `go test -p 1 -count=1 -v ./...` exit 0 as hook rows; the test agent was likewise denied once for
  a 17-statement bundle, then compliant); `review` **EXIT=1** at 01:18:11 — must-fix
  `agents/test-runner.md:13`: the now-separate `go vet` publishes a CmdTest success and fires
  test→review before the suite (`bus/hook.go:254`, `cmd/hook.go:105`), and `triggerChain` then
  suppresses the suite's own row while the state is Reviewing/Reviewed (`cmd/hook.go:146`) — finding
  (d) above, confirmed by the reviewer, now owned by the run's fix spawn (`spawn-567c25f0`); should-fix
  `bus/evidence_guard.go:153`: `./build.sh 0<&0`, an input-descriptor duplication, splits at the `&`
  and is denied as backgrounding — recognize `<&` as a redirection beside `>&`, with a positive control
  and a trailing-background negative control; nit `:171`: `splitShellStatements` is a test-only
  production wrapper. And finding (c) again: `verify-spec` reached plan in the same second as
  `graph-node-done review -> failure` — the daemon's `plan-verify` is not gated on the verdict on the
  graph road either, and the graph's own `update-spec` node stays pending until review passes, so
  every failed review costs one redundant verification. The fix spawn (`spawn-567c25f0`, 582 s)
  resolved all three findings — see (d) above — and the re-run was green: build 01:28:14 and
  `go test -p 1 -count=1 -v ./...` 01:29:03 exit 0 as hook rows, review LGTM 01:29:55 (EXIT=0); the
  run reached `update-spec` at 01:29:56 and the Phase 3 commit gate followed: the user opened it at
  01:40 and the commit node landed **`59d57b9`** ("MUX-159 Phase 3: Chains and evidence on the hook
  road", 01:43:34, 20 files) — after two daemon `task-stall-redrive`s on the commit dispatch. `loop-check`
  then re-seeded `implement` on the reused worker `spawn-e2669d9b` (01:43:44), which was stopped as a
  leftover seconds later, and the run **failed at 01:44:39** ("node implement failed with no live
  edge") — Phase 4 was never started by it. Edit's executor fixes for both incidents (graph workers
  persist and read as `parked`, lost workers are replaced under the redrive cap, the idle-task watchdog
  defers to the executor, redrives skip a busy pane — `docs/architecture.md`) are outside this spec and
  uncommitted; their first review at 01:57:38 was EXIT=1 (two must-fixes) with the suite red at
  01:58:01, and `verify-spec` fired on that failed review as well — finding (c), third time. Their
  re-review at 02:04:15 was LGTM (0 must-fix; one should-fix left, best-effort cleanup in
  `failClosed`/`StopSpawn`) with `daemon/idle_task_test.go` added — but no `go test` row had followed
  the 01:58:01 red when this was written, so the suite is unverified on the fixed tree. A third review
  at 02:06:01 was clean (0/0/0: replacement cleanup collects `StopSpawn` errors and names still-live
  workers, `StopSpawn` keeps `running` when the kill fails and the window lives, and the spawn row in
  `docs/architecture.md` now says session checkout, not worktree) — still with no `go test` row after
  the three reds of 01:58–01:59 (`./...`, `./bus`, `-run TestExecR…`), only `gofmt` calls — until
  **02:09:24**, when `go test -p 1 -count=1 -v ./...` exited 0 as a hook row on the tree that also
  added the force-delivery test (Phase 4 item 7); review LGTM 02:10:07. That tree was the user's
  **third `spec-to-pr` run** (`1788933783-spec-to-pr-2babbc1e`, Phase 4, started ~02:03): its implement
  spawn delivered the force-delivery test on top of the executor fixes, build 02:08:27 / test 02:09:29 /
  review 02:10:10 all green, and two `verify-spec`s reached plan one second apart — the daemon's
  review-complete notification (02:10:09) and the run's own `update-spec` node (02:10:10) — the
  double fire that finding (c)'s ungated notification produces on every green graph review. Phase 4
  closed on that verification; the run then sat at its Phase 4 commit gate. The user opened it at
  02:12:31 and the commit node landed **`c6809c0`** ("MUX-159 Phase 4: Delivery on the hook road",
  02:14:26, one `task-stall-redrive` on the dispatch). `loop-check` re-seeded `implement` on the
  **reused** worker `spawn-099b02fe` (02:14:28 — the executor fixes working as designed), which
  extracted the PreToolUse decision into `GuardDecisionFor`/`guardedPaths` with
  `TestGuardDecisionFor_CodexPayloads` (Phase 5 items 3/5; `firstNonEmpty` removed, `cmd/hook.go`
  −70 lines); build 02:21:07 green; `go test -v ./...` **red at 02:21:50 on a Go build-cache miss**
  (`could not import crypto/md5 (open …/Library/Caches/go-build/…: no such file or directory)` — the
  daemon's disk-pressure purge emptying the user's real `GOCACHE` under a running suite, MUX-160's
  hazard) and green on the immediate re-run at 02:22:34; review LGTM 02:23:13; the daemon-and-node
  `verify-spec` pair again at 02:23:15/16.
- **2026-09-09 09:40 close (user-approved re-scope, relayed by edit)** — the third run committed Phase 5's
  guard-core extraction as **`b0db67a`** at 09:32 (gate opened by the user; the tree was clean after it)
  and looped once more with nothing left to implement: build, test and review green 09:36–09:38 on an
  unchanged tree. The six open checkboxes were all outside this spec's reach — Phase 5 items 1/3 and
  AC 9 wait for MUX-157's never-author rules to exist; Phase 7 items 5/6 and AC 8 need a live codex to
  refuse a denied command and to relaunch after a tamper is restored. `SpecOpenItems` counts every
  `- [ ]` in the file (ACs included), so the loop's `spec_phases_remaining` would have sent the run back
  to Phase 5 forever. On the user's instruction the six were converted from checkboxes to *Deferred*
  bullets: three to [MUX-157](../backlog/MUX-157-role-boundary-an-agent-can-ignore.md) (recorded in its
  Notes and backlog row) and three to a live-run follow-up under the backlog's *Ideas without specs*.
  The spec closes at 61/67 with those six deferred; the run's next `loop-check` reads no phases
  remaining and proceeds to `final-gate` — push and PR are the user's.
- [MUX-154](../backlog/MUX-154-codex-status-line-closes-tracked-tasks.md) — the immediate patch to
  the scrape (chrome signatures, consumer refusal). This spec removes the scrape's *reason to exist*
  for codex; both are needed, in that order.
- [MUX-009](../backlog/MUX-009-response-echo-chain-retrigger.md) — fixed at the root by Phase 4: a response
  is delivered as context, never as a prompt.
- [MUX-148](../backlog/MUX-148-node-outcome-reads-command-ran-as-task-done.md) — authoritative-row provenance
  on the graph road; Phase 3 gives codex nodes authoritative rows for the first time, so its
  attribution rules apply to them too.
- [MUX-157](../backlog/MUX-157-role-boundary-an-agent-can-ignore.md) — Phase 5 is the codex enforcement road
  it lists as missing.
- [MUX-153](../backlog/MUX-153-codex-test-agent-cannot-run-the-suite.md) — hooks do not change the
  sandbox; a codex test agent still needs the socket-free suite.
- [MUX-012](../backlog/MUX-012-remove-gated-pane-scrape-delivery.md) — deletes what this spec stops needing.
- `bus/codex_events.go` parses `codex exec --json` — the non-interactive road. Whether hooks fire
  there too is one of Phase 1's questions; if they do, spawn workers on codex can share this wiring.
- Version pin: verified on `codex-cli 0.153.4`; the hooks reference names no minimum version, so the
  gate is empirical (`codex --version` ≥ the verified build) until a floor is published.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-144-wait-human-gate-openable-by-any-agent | 11h 24m | 2026-09-09 00:12 |
| MUX-159-codex-hooks-provider | 2h 22m | 2026-09-09 09:38 |

The MUX-144 branch predates this spec (its key is MUX-144, and the same branch row appears in that
spec's table); its total is that branch's absolute active time, not this spec's share of it. The work
moved to its own branch on 2026-09-09; the MUX-159 row is that branch's absolute time.

## Status

**Complete** — closed 2026-09-09 09:40 at 61/67 with six items deferred on the user's instruction (Phase 5 items 1/3 and AC 9 → MUX-157; Phase 7 items 5/6 and AC 8 → live-run follow-up; see Notes). Phases 1, 2, 3, 4 and 6 complete in full (8/8, 7/7, 12/12, 8/8, 5/5); Phase 5 and Phase 7 complete for everything this spec could close (3/3 and 6/6, the rest deferred); acceptance criteria 12/12 likewise. Ready to move to `completed/` when the user says so. As of `b0db67a` (Phase 5 guard-core extraction, committed 09:32 by the third run's commit node; `c6809c0` Phase 4 at 02:14; `59d57b9` Phase 3 at 01:43; `3d3fd9b` the first pass of all seven phases): `scripts/test-codex-hooks.sh` is proven —
hermetic 37/37 (2026-09-09 00:07), live 7/7 (00:02, `MUXCODE_CODEX_HOOKS_LIVE=1`) and, at the raised floor, **hermetic 39/39 (01:15, run agent)**; Go tests pass
and review is clean (0 must-fix) per edit; the hook road is **on by default** since 00:10
(`codexHooksDefault = true`, env opts out). **In `59d57b9` (work of 2026-09-09 00:27–01:27)**: the graph's
implement spawn delivered the daemon-level tests for Phase 3 items 6–7 (which also test Phase 4 item 6
and the scrape-road half of Phase 7 item 7 and AC 5), and edit shipped the hook-road evidence guard
after the run's build hold (Phase 3 items 8–12) — three review rounds (00:46, 00:52, 00:53) found
three must-fixes on the guard, all fixed; review LGTM 00:54:59 and **green 00:55** on the final tree
(`go vet` and `go test ./...` exit 0 as hook rows, 2341 PASS / 0 FAIL / 2 SKIP), on which the
pending-green items were ticked. Deferred at close: the live clauses the script does not assert
(Phase 7 items 5/6 and AC 8: a live denial and restore-after-tamper), and MUX-157's never-author rules (Phase 5 items 1/3, AC 9 — item 5 closed 02:20 on the families that exist). `deliver --force` on hook codex (Phase 4 item 7) closed 02:09 with `TestCodexForceDeliver_HookRoadSentenceOnly`, on the first green suite (02:09:24) of the tree that also carries the executor fixes; review LGTM 02:10:07 — the third `spec-to-pr` run (`1788933783-spec-to-pr-2babbc1e`, Phase 4), which went on to commit Phase 5 as `b0db67a`. The second run's 01:18 review must-fix (a lone `go vet` fired review before the suite) was resolved at 01:27 by the test-precheck class and its re-review was LGTM at 01:29:55, EXIT=0 (Notes (d)); Phase 3 was committed as `59d57b9` at 01:43 and the run then failed on its re-seeded implement worker (Notes). Moved to `drafts/` and set as the active spec 23:00 on the user's instruction;
edit implemented all seven phases in one pass, taking the recommended option on Decisions 1–4. Filed
2026-09-08 on the user's request, from the same-day finding that codex ships hooks muxcode ignores.
