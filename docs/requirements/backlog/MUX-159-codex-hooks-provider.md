# A Codex Hooks Provider: Put Codex Agents on the Deterministic Chain Road

**Tracking:** [mkober/muxcode#76](https://github.com/mkober/muxcode/issues/76)

Codex CLI ships lifecycle hooks — verified 2026-09-08 on the installed `codex-cli 0.153.4` and against
the official reference — and muxcode does not use them. `CodexProvider.SupportsHooks()` returns
`false` (`bus/provider_codex.go:390`, "Codex CLI's hook system is not integrated"), so every codex
agent runs the **non-hook road**: chain instructions pasted into its prompt, reply reminders injected
as text, and the daemon **pane-scraping** to guess when a task finished and what it said.

That road produced every incident of 2026-09-08: a rule line closed the graph's build and test nodes
and held them for manual approval on three runs ([MUX-154](./MUX-154-codex-status-line-closes-tracked-tasks.md)),
a review reply injected as a prompt drove a fourteen-request test↔review loop
([MUX-009](./MUX-009-response-echo-chain-retrigger.md)), `verify-spec` fired on chrome, and the build
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
| Unstated | Whether hooks fire in `codex exec` as well as the interactive TUI. The `/hooks` review command and the binary's *"hooks/list failed in TUI"* place the feature in the TUI, which is the mode muxcode launches |

Nothing is configured on this machine: no `~/.codex/hooks.json`, no `[hooks]` in `~/.codex/config.toml`,
no trust store yet. The repo's `.codex/` holds only the generated `AGENTS.md` (gitignored,
`.gitignore:20-21`) and `review/`.

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
([MUX-012](./MUX-012-remove-gated-pane-scrape-delivery.md) — this spec makes codex stop *needing*
it, and MUX-012 deletes it).

## Requirements

### Acceptance criteria

- [ ] A codex agent launched with hooks enabled has `<repo>/.codex/hooks.json` written by muxcode before launch, and the hooks run in the TUI — proven by a `PostToolUse` row in the console history after its first shell command, with the real exit code
- [ ] `SupportsHooks()` no longer conflates chains, self-poll and scrape: each of the 44 gates asks the capability it actually needs, and a table in `docs/hooks.md` says which
- [ ] Build→test→review fires from `PostToolUse` for codex exactly as for Claude; the codex prompt carries no chain instruction and no reply reminder; `CheckSendPolicy` grants it no bypass
- [ ] A graph `send` node whose codex agent ran the command routes on an **authoritative** history row — no unverified hold on a passing build or test (negative control: a failing command routes failure, not unknown)
- [ ] `checkNonHookTasks` never scrapes a hook-enabled codex agent; no `task-detected` row is written for it in a full session
- [ ] A pending actionable message is delivered by the `Stop` hook as the agent's next prompt, with a true `acked` receipt written before Codex continues
- [ ] An idle hook-enabled codex agent is woken with the fixed sentence only; the payload arrives through `UserPromptSubmit` → `additionalContext`; **a `type: response` is never injected as prompt text** (MUX-009 negative control: the receiving agent's chain does not re-fire)
- [ ] `PreToolUse` on `Bash` and `apply_patch` runs `hook guard`; a denied command answers `permissionDecision: "deny"` with the reason, and the command does not run (positive control first: an allowed command passes)
- [ ] The never-author and doc-file guards hold for codex build/test/review — [MUX-157](./MUX-157-role-boundary-an-agent-can-ignore.md)'s missing road
- [ ] The trust flag is passed only when `hooks.json` hashes to what muxcode wrote; a tampered file refuses launch with a lifecycle row
- [ ] Every `muxcode hook` subcommand is a no-op outside a muxcode session (`BUS_SESSION` unset) — a developer's own codex in this repo is unaffected
- [ ] Per-role opt-in (`MUXCODE_CODEX_HOOKS`, then per-role `MUXCODE_<ROLE>_CODEX_HOOKS`) with the scrape road as the fallback; an older codex (no hooks) or `[features] hooks = false` is detected and falls back with a lifecycle row, never a silent half-state
- [ ] `scripts/test-codex-hooks.sh` passes with a coverage floor; its live section skips with a reason when no codex ≥ 0.153 is installed
- [ ] Docs updated with the code: `hooks.md`, `architecture.md` (Codex CLI Agent Flow), `configuration.md`, `agents.md`, `CLAUDE.md`

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

### Decisions — open, the user's call

| # | Question | Options | Recommendation |
|---|----------|---------|----------------|
| 1 | Delivery road for hook codex | **A** background listener like Claude (`muxcode inbox --poll --loop` kept alive by `Stop`) — depends on a codex shell session surviving across turns, unverified. **B** `Stop` + `UserPromptSubmit` delivery, no listener | **B** — it needs nothing unverified, removes payload injection entirely, and writes the same receipts |
| 2 | Trust | **A** `--dangerously-bypass-hook-trust` under muxcode's integrity check. **B** pre-seed Codex's trust store. **C** managed `requirements.toml` | **A** now; move to **B** once Phase 1 records the store's file and hash form; **C** is an enterprise layer, not a developer machine |
| 3 | Where `hooks.json` lives | Project `<repo>/.codex/hooks.json` (beside `AGENTS.md`) vs user `~/.codex/hooks.json` | **Project** — user scope leaks into every repo and every non-muxcode codex |
| 4 | Rollout | Opt-in env → default on after the live test, or default on from the start | **Opt-in first**; the scrape road stays as the fallback the flag selects |

## Implementation

### Phase 1: Establish the contract (spike — no behaviour change)

- [ ] Write a throwaway `.codex/hooks.json` whose every handler is `muxcode hook record` (new, hidden): append the raw stdin JSON and the event name to `BusDir()/hook-capture.jsonl`
- [ ] Launch one codex agent through muxcode with `--dangerously-bypass-hook-trust` and drive it through a shell command, an `apply_patch` edit, a turn end and a fresh prompt; confirm hooks fire in the TUI
- [ ] Record the real `tool_response` shape for `Bash` (where the exit code lives), the `apply_patch` `tool_input` shape, and the `Stop`/`UserPromptSubmit` payloads — as fixtures under `bus/testdata/codex-hooks/`
- [ ] Confirm `Stop` `decision: "block"` + `reason` continues the agent with `reason` as the prompt, and that `stop_hook_active` is set on the continuation's own Stop
- [ ] Confirm `UserPromptSubmit` `additionalContext` reaches the model (a marker phrase the agent is asked to echo)
- [ ] Find Codex's trust store: file, hash input, whether it can be pre-seeded; record the finding under Decision 2
- [ ] Check `codex --version` output shape for the version gate, and whether `[features] hooks = false` is readable from `config.toml` before launch
- [ ] Record every finding in this spec's *What Codex ships* table; take Decisions 1–4

### Phase 2: Capability split and the hooks.json writer

- [ ] Split `SupportsHooks()` into the three questions on `Provider`; re-key all 44 sites; table them in `docs/hooks.md`
- [ ] Negative control: with hooks **off**, every codex behaviour is byte-for-byte what it is today (chain text present, scrape active, injection with reminders) — pinned by the existing codex tests plus a golden of `SharedPrompt`
- [ ] `bus/codex_hooks.go`: template → `.codex/hooks.json`, atomic write, sha256 recorded under `BusDir()`; `.gitignore` entry
- [ ] Version gate and feature-flag read; an ineligible codex logs `codex-hooks-unavailable` and stays on the scrape road
- [ ] `BuildExecArgs`: trust flag only when the on-disk hash matches; mismatch → `codex-hooks-tampered`, launch refused
- [ ] `ParseToolEvent` codex dialect from the Phase 1 fixtures; `GetExitCode()` reads the real field
- [ ] Tests: writer output, hash match/mismatch, version gate, parser against every fixture, no-op without `BUS_SESSION`

### Phase 3: Chains and evidence on the hook road

- [ ] `PostToolUse` `Bash` → `hook bash` → `ProcessBashHook` writes the console-history row with the real exit code and fires `triggerChain` for codex build/test/deploy/run
- [ ] `SharedPrompt` emits no chain instruction and no reply reminder for hook codex; `CheckSendPolicy` grants it no bypass
- [ ] `checkNonHookTasks` and `checkNonHookEdits` skip hook codex; `checkStuckProviders` keys on `PaneIsEvidence()`
- [ ] Graph: `deriveSendOutcome` finds an authoritative row for a codex node — pin with a build that fails (`failure`, routed to `fix`) and one that passes (`success`, no hold)
- [ ] `apply_patch` `PostToolUse` → `hook analyze` with the paths from the patch
- [ ] Negative control: a scrape-road codex agent in the same session still gets `task-detected` completions
- [ ] Tests for each, including the graph pins

### Phase 4: Delivery on the hook road

- [ ] `hook stop` for codex: actionable inbox → consume, receipt, `decision: "block"` with the messages and one reply instruction; nothing pending → no output
- [ ] `hook prompt-submit`: recognizes the fixed wake sentence, consumes, writes receipts, returns `additionalContext`; any other prompt passes untouched; self-addressed and chrome payloads filtered at consume
- [ ] `SendWakeUp` for hook codex injects the sentence only; `provider_codex.go:339-353` reminder wrapping is not applied
- [ ] `checkIdleAgents`/`checkParkedInput`/`checkPaneSweep` treat hook codex by receipts, not by pane; `checkPollHealth`'s receipt-gap backstop still covers it
- [ ] MUX-009 negative control: deliver a `type: response` to a hook codex agent → its chain does not re-fire and the response text never appears in the pane as a prompt
- [ ] MUX-154 negative control: no synthesized response is ever sent for a hook codex task; a `--wait` on it returns the agent's own reply
- [ ] `deliver --force` for hook codex re-injects the sentence and clears markers, never a payload
- [ ] Tests: stop with/without pending, prompt-submit expansion and pass-through, receipts written, the two negative controls

### Phase 5: Guards on the hook road

- [ ] `PreToolUse` `Bash` → `hook guard` → codex `deny` answer; `apply_patch` → doc-file and never-author guards on the patch's paths
- [ ] Positive control first: an allowed command and an allowed edit pass with no output
- [ ] MUX-157 rules for build/test/review resolve through `HasGuardRules`/`CheckGuard` unchanged — one rule set, two providers
- [ ] Denials are attributable: lifecycle `guard-denied` names role, tool and reason
- [ ] Tests: deny/allow for each guard family against codex payloads

### Phase 6: Docs and rollout

- [ ] `docs/hooks.md`: replace the "only Claude Code's hooks are integrated" paragraph; add the capability table and the codex event mapping
- [ ] `docs/architecture.md` Codex CLI Agent Flow: the hook road, the delivery moments, the trust check
- [ ] `docs/configuration.md`: `MUXCODE_CODEX_HOOKS`, per-role override, `codex-hooks-*` lifecycle events; `CLAUDE.md` constraint line; `docs/agents.md` roster note
- [ ] Flip the default to on for codex ≥ 0.153 once Phase 7's live section is green on this machine; keep the env as the opt-out
- [ ] Note in MUX-012 that hook codex no longer needs the scrape machinery it deletes

### Phase 7: Integration test

- [ ] Create `scripts/test-codex-hooks.sh` — hermetic section: feed the Phase 1 fixtures to `muxcode hook bash|guard|stop|prompt-submit` in a scratch bus and assert the history row, the chain message, the deny JSON, the stop block JSON and the `additionalContext`; coverage floor
- [ ] Live section (requires `codex` ≥ 0.153 on PATH; skipped **with reason** otherwise): scratch session, one codex build agent with hooks on, dispatch a build → console-history row with the real exit code, chain request to test sent, **no** `task-detected` row for that role
- [ ] Live: deliver a request while idle → the pane shows only the wake sentence; the request's receipt reads `acked`; the reply correlates
- [ ] Live: deliver a response → no prompt text, no chain re-fire (MUX-009)
- [ ] Live: a denied command (`git commit` from build) does not run and `guard-denied` is logged
- [ ] Live: tamper `.codex/hooks.json` → launch refused, `codex-hooks-tampered` logged; restore → launches
- [ ] Negative control: `MUXCODE_CODEX_HOOKS=0` → the old road, `task-detected` present, chain text in the prompt
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- [MUX-154](./MUX-154-codex-status-line-closes-tracked-tasks.md) — the immediate patch to
  the scrape (chrome signatures, consumer refusal). This spec removes the scrape's *reason to exist*
  for codex; both are needed, in that order.
- [MUX-009](./MUX-009-response-echo-chain-retrigger.md) — fixed at the root by Phase 4: a response
  is delivered as context, never as a prompt.
- [MUX-148](./MUX-148-node-outcome-reads-command-ran-as-task-done.md) — authoritative-row provenance
  on the graph road; Phase 3 gives codex nodes authoritative rows for the first time, so its
  attribution rules apply to them too.
- [MUX-157](./MUX-157-role-boundary-an-agent-can-ignore.md) — Phase 5 is the codex enforcement road
  it lists as missing.
- [MUX-153](./MUX-153-codex-test-agent-cannot-run-the-suite.md) — hooks do not change the
  sandbox; a codex test agent still needs the socket-free suite.
- [MUX-012](./MUX-012-remove-gated-pane-scrape-delivery.md) — deletes what this spec stops needing.
- `bus/codex_events.go` parses `codex exec --json` — the non-interactive road. Whether hooks fire
  there too is one of Phase 1's questions; if they do, spawn workers on codex can share this wiring.
- Version pin: verified on `codex-cli 0.153.4`; the hooks reference names no minimum version, so the
  gate is empirical (`codex --version` ≥ the verified build) until a floor is published.

## Status

**Backlog** — filed 2026-09-08 on the user's request, from the same-day finding that codex ships
hooks muxcode ignores. Not started. 0/62 items.
