# Hooks

## Overview

Muxcode uses Claude Code's hook system to integrate the AI agent with tmux and neovim. Hooks run before or after tool execution, receiving the tool event as JSON on stdin. Most hooks are implemented as subcommands of `muxcode hook` (Go binary); two remain as shell scripts for tmux/vim timing-sensitive operations.

Most hooks are **async** — they do not block the AI agent from continuing. Three are sync: `hook guard` (rejects prohibited commands before they run), `hook stop` (can block a turn's stop to re-launch the inbox listener), and `hook comment-block` (its PostToolUse block decision must reach the model).

**Provider gating**: hooks fire for providers whose `SupportsHooks()` is true — **Claude Code always, and Codex CLI on the hook road** ([MUX-159](requirements/completed/MUX-159-codex-hooks-provider.md)), which is on by default for an eligible codex (≥ 0.153, `[features] hooks` not disabled); `MUXCODE_CODEX_HOOKS=0` or `MUXCODE_{ROLE}_CODEX_HOOKS=0` opts a session or role out (see [Configuration](configuration.md#codex-hooks)). Codex ships the same event vocabulary (verified 2026-09-08 on the installed `codex-cli 0.153.4`: `SessionStart`/`SessionEnd`, `PreToolUse`/`PostToolUse`/`PermissionRequest`, `UserPromptSubmit`, `Stop`/`Interrupt`, `SubagentStart`/`SubagentStop`, `PreCompact`/`PostCompact`), configured in `<repo>/.codex/hooks.json`, which `PrepareCodexHooks` (`bus/codex_hooks.go`) writes before every codex launch from `CodexHooksTemplate()` — see [Codex hooks](#codex-hooks). OpenCode, scrape-road Codex, and local LLM agents skip hook processing entirely — the hook functions (`hookBash`, `hookGuard`, `hookAnalyze`, `hookInboxPoll`, `hookStop`, `hookCommentBlock`) exit early when the provider is non-hook. For non-hook agents, three layers replace hooks: (1) role-specific manual bus messaging instructions in the system prompt, (2) agent body text adaptation (OpenCode: `adaptBodyForNonHookProvider()`, Codex CLI: shared `.codex/AGENTS.md`) that rewrites hook chain references to manual commands, (3) `CheckSendPolicy()` bypass that allows non-hook agents to send chain messages (build→test, test→review) that would be blocked for hook agents where chains fire automatically.

**Three capabilities, not one flag.** Until MUX-159 `SupportsHooks()` answered four different questions at 44 call sites, so it could not be flipped for Codex without also claiming a `❯` prompt and a background listener it does not have. The `Provider` interface (`bus/provider.go`) now separates them, and each site asks the one it needs:

| Question | Gate | Claude | Codex (hook road) | Codex (scrape road), OpenCode, local | Sites |
|----------|------|--------|-------------------|--------------------------------------|-------|
| Do chains, guards and console history fire from hooks — so the prompt carries no chain text and `CheckSendPolicy` grants no bypass? | `SupportsHooks()` | yes | yes | no | `prompt.go` manual-messaging block and send restrictions, `profile.go` `CheckSendPolicy`, every `cmd/hook.go` subcommand |
| Does a background listener consume and ack the inbox? | `SelfPollsInbox()` | yes | **no** | no | `prompt.go` protocol text, `notify.go` `Notify`/`SendWakeUpWithText`, `daemon.go` `checkIdleAgents`/`listenerless`/`checkPollHealth` recovery and the force-respond pane gate, `reload.go` `wakeAfterReload`, `mode.go`, `launcher.go` startup wake |
| Is the pane the only evidence of completion and edits? | `PaneIsEvidence()` | no | **no** | yes | `daemon.go` `checkNonHookTasks`, `checkNonHookEdits`, `checkStuckProviders`, `checkIdleTaskCompletion` |
| Is this the Claude TUI (slash commands, `❯` prompt, permission model, `--agent` argv)? | `IsClaudeTUI(p)` | yes | no | no | `prompt.go` compact text, `launch.go` `refuseWithoutDefinition`, `definition_watchdog.go`, `reload.go` exit sequence, `notify.go` `HasPendingInput`/`ClearParkedInput`, `daemon.go` `checkParkedInput`/`checkPaneSweep`/`checkStuckPermissions`, `timetrack.go` |
| Does an idle-prompt glyph exist to watch for? | `IdlePromptChar() != ""` | yes | no | no | `daemon.go` `checkActiveWatchdog` |

A hook-road codex agent is therefore *hooks yes, self-poll no, scrape no*: chains and guards fire deterministically, delivery rides the `Stop` and `UserPromptSubmit` hooks instead of a listener, and nothing about it is inferred from the pane.

## Hook Configuration

Hooks are configured in `.claude/settings.json` in your project:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "muxcode hook guard"}]
      },
      {
        "matcher": "Write|Edit|NotebookEdit",
        "hooks": [{"type": "command", "command": "muxcode-preview-hook.sh", "async": true}]
      },
      {
        "matcher": "Read|Bash|Grep|Glob",
        "hooks": [{"type": "command", "command": "muxcode-diff-cleanup.sh", "async": true}]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Write|Edit|NotebookEdit",
        "hooks": [{"type": "command", "command": "muxcode hook analyze", "async": true}]
      },
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "muxcode hook bash", "async": true}]
      },
      {
        "matcher": "Write|Edit",
        "hooks": [{"type": "command", "command": "muxcode hook comment-block"}]
      }
    ],
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "muxcode hook stop"}]
      }
    ]
  }
}
```

You can copy a pre-configured template:
```bash
cp ~/.config/muxcode/settings.json .claude/settings.json
```

## Codex hooks

Codex agents on the hook road ([MUX-159](requirements/completed/MUX-159-codex-hooks-provider.md)) get their hooks from `<repo>/.codex/hooks.json`, not `.claude/settings.json`, and nothing is installed by hand: `PrepareCodexHooks` (`bus/codex_hooks.go`) runs from `ConfigureLaunch`/`WriteAgentConfig` before every codex launch — env opt-out check → eligibility → atomic write → sha256 marker under `BusDir()/codex-hooks/<role>.sha256`. The file is gitignored beside `.codex/AGENTS.md`.

| Codex event | Matcher | Handler | Answer shape |
|-------------|---------|---------|--------------|
| `PreToolUse` | `Bash\|apply_patch` | `muxcode hook guard` | `hookSpecificOutput.permissionDecision: "deny"` + `permissionDecisionReason` (`FormatCodexGuardDeny`); silence allows |
| `PostToolUse` | `Bash` | `muxcode hook bash` | none — writes the console-history row with the real exit code and fires the chain |
| `PostToolUse` | `apply_patch` | `muxcode hook analyze` | none — one analyze trigger per path the patch names |
| `Stop` | — | `muxcode hook stop` | `{"decision":"block","reason":…}` when an actionable request is pending (`CodexStopDelivery`); nothing otherwise |
| `UserPromptSubmit` | — | `muxcode hook prompt-submit` | `hookSpecificOutput.additionalContext` carrying the inbox when the prompt is the fixed wake sentence (`CodexPromptSubmitContext`); any other prompt passes untouched |

**Payload dialect.** One parser, two dialects: `ParseToolEvent` accepts Codex's shapes beside Claude's (`bus/hook_codex.go`; live-captured fixtures under `bus/testdata/codex-hooks/`). Two differences matter. `apply_patch` arrives as the whole patch text in `tool_input.command`, so `normalizeCodex` fills `Patch`, `PatchPaths` (every `*** Add/Update/Delete File:` path) and `FilePath`. And **`PostToolUse` for `Bash` carries no exit code** — `tool_response` is a bare string of stdout — so `GetExitCode()` reads the real status from the rollout transcript named by `transcript_path`: the `item_completed` record whose `payload.item.id` equals the hook's `tool_use_id` (`CodexExitCodeFromTranscript`, six 250 ms retries because the record can land after the hook fires). No transcript → `""` = unknown, never the Claude default `0`; an `apply_patch` response carries an `Exit code: N` line and is read from that. A history row written this way is authoritative for graph routing (`deriveSendOutcome`): a passing codex build routes `success` and a failing one `failure`, never an unverified hold.

**Delivery without a listener.** A codex TUI cannot keep `muxcode inbox --poll --loop` alive in the background, so the hook road delivers at three moments: (1) **turn end** — `hook stop` consumes any pending actionable request, writes a true `acked` receipt (`ConsumeInboxForHook`) and blocks the stop with the messages plus one reply instruction as `reason`, which Codex takes as its next prompt; (2) **idle** — `SendWakeUp` types the fixed sentence `You have new messages` and nothing else — no payload, no reminder wrapping, no consume (`injectWakeSentence`); (3) **the sentence is submitted** — `hook prompt-submit` consumes, writes receipts and returns the messages as `additionalContext`. A `type: response` is never typed into the pane as a prompt, which is the [MUX-009](requirements/backlog/MUX-009-response-echo-chain-retrigger.md) echo closed at its root; self-addressed and chrome payloads are dropped at consume. The consume-side drop is the second line: at `Send`, `isLoopingSelfSend` (`bus/inbox.go`) refuses every self-addressed message except the launch-time bootstrap, and since 2026-09-09 that exemption is keyed on **type and action** (`isStartupBootstrap`: `request:startup` only) — a codex test agent that answered its bootstrap with `--reply-to` had its `response:startup` ride the action-only exemption back into its own inbox and acknowledged its own acknowledgement every 5 s, an echo `DetectMessageLoop` alerted on but, being alert-only, could not stop ([MUX-169](requirements/drafts/MUX-169-startup-self-reply-echo-loop.md)).

**Trust.** Codex runs only hooks it trusts (hash-persisted, reviewed via `/hooks`). muxcode wrote the file, so `BuildExecArgs` passes `--dangerously-bypass-hook-trust` — but **only while the file on disk hashes to the recorded marker** (`CodexHooksTrusted`). A mismatch is `ErrCodexHooksTampered`: the launcher refuses (`refuseTamperedCodexHooks`, lifecycle `codex-hooks-tampered`) rather than trust a handler an agent appended. A `hooks.json` muxcode did not write is left alone and the role stays on the scrape road. Codex's own trust store was not created under the bypass flag on 0.153.4 and its format is undocumented, so pre-seeding it stays open (spec Decision 2).

**Outside a session.** Every `muxcode hook` subcommand is a no-op unless the raw `BUS_SESSION` variable is set (`hookSession()` in `cmd/hook.go` — deliberately not `bus.BusSession()`, whose fallbacks would resolve a session for a developer's own codex in this repo).

**Eligibility and fallback.** `CodexHooksEligible` requires `codex --version` ≥ `CodexHooksMinVersion` (`0.153.0`) and no `[features] hooks = false` in `.codex/config.toml` or `$CODEX_HOME/config.toml`; an ineligible codex logs `codex-hooks-unavailable` with the reason and runs the scrape road byte-for-byte as before (`TestCodexHooks_ScrapeRoadUnchanged`). Rollout: opt-in at hand-off (`codexHooksDefault = false`), **flipped to on 2026-09-09 00:10** once the live section of `scripts/test-codex-hooks.sh` went green on a real codex (7/7, hermetic 37/37); the env variables are now the opt-out.

## Hook Descriptions

### hook guard (edit guard)

**Command:** `muxcode hook guard`
**Phase:** PreToolUse
**Trigger:** Bash
**Mode:** sync (blocks tool execution)
**Window:** edit (delegation rules); build, test and deploy (the hook-road evidence rule below); any role with an Atlassian authority limit

Blocks prohibited commands in the edit window (build, test, deploy, git commands) and returns delegation instructions. It runs before the tool executes and can reject the command (`hook stop` and `hook comment-block` are the other sync hooks).

**What it blocks:**
- Build commands: `./build.sh`, `make`, `go build`, `pnpm build`, `cargo build`
- Test commands: `./test.sh`, `go test`, `jest`, `pytest`
- Deploy commands: `cdk`, `terraform`, `pulumi`
- Git commands: all `git` subcommands
- Log tailing: `tail -f`, `aws logs`, `kubectl logs`, `docker logs`

When a command is blocked, the hook returns a rejection with instructions to delegate via the message bus instead.

**Hook-road evidence rule (build, test, deploy).** `CheckEvidenceGuard` (`bus/evidence_guard.go`) runs after the delegation rules and denies a Bash call that bundles a build, test or deploy statement with anything else, or backgrounds it. The reason is mechanical: `hook bash` classifies a call by its **first** statement (`ClassifyCommand`, the same patterns as `MUXCODE_BUILD_PATTERNS` and friends) and records the exit code of its **last**, so a call describes the build only when the build is the whole call. On 2026-09-09 00:28 a codex build agent answered a `spec-to-pr` build node with one call — three `muxcode send … --type response` acks, `./build.sh`, then a hand-typed build-result — which classified as a bus command: no exit-code row, no build→test chain, and the graph parked on an unverified hold for a build that had passed. Prompt text is advice; this is the enforcement.

| Shape | Verdict |
|-------|---------|
| `./build.sh`, `./build.sh 2>&1`, `./build.sh >/tmp/build.log 2>&1`, `./build.sh 0<&0`, `bash ./build.sh`, `make 2>&1` | allowed — one statement; redirections (`2>&1`, `0<&0`, `&>`, `>\|`) are not statements — an input-fd duplication is not a trailing `&` (the 2026-09-09 01:18 review's should-fix) |
| `cd tools/muxcode && go build ./...`, `GOFLAGS=-mod=mod go build ./...` | allowed — a leading `cd … &&` or env assignment is the prefix `stripCommandPrefix` already ignores for classification |
| `muxcode send … ; ./build.sh`, `./build.sh && go vet ./...`, `./test.sh; echo EXIT=$?`, `cdk deploy --all \|\| echo failed` | denied — chained (`;`, `&&`, `\|\|`, newline) |
| `./build.sh 2>&1 \| tail -20`, `go test ./... \| tee log` | denied — piped: the call exits with `tail`'s status, so a failing build would record success |
| `./build.sh &` | denied — backgrounded: the call returns at launch, so the hook would record the launch, not the result |
| `cd /tmp; ./build.sh` | denied — a `cd` joined by `;` is not the exempt prefix |
| `muxcode send … ; muxcode log build … --exit-code 0`, `gofmt -l . ; ls` | allowed — a compound with no build/test/deploy statement is not the rule's business (the definitions' own log-and-report sequence is one) |

The denial (Claude `decision: block`, Codex `permissionDecision: deny`) names the statement and how many others it is bundled with, says to run it alone — a leading `cd … &&` or env assignment is fine, a pipe or `;`/`&&` chain is not — and to send acks or reports in a separate call; a `guard-denied` lifecycle row names role, tool and reason. Scope is `HasEvidenceGuard`: build, test and deploy only — edit and plan are denied those commands outright by their delegation rules, and run's verdict is the whole call's exit code by design. `agents/code-builder.md` and `agents/test-runner.md` state the rule in their sequences (test-runner's old `go vet … && go test …` fallback was itself a compound; vet is now its own call, a [precheck](#hook-bash-bash-hook)). Both providers, one rule; the agent's own reply still closes the node. Covered by `bus/evidence_guard_test.go` (the splitter is `parseShellStatements`; `0<&0` has a foreground/background pair) and two hermetic cases in `scripts/test-codex-hooks.sh` (floor 37 → 39, run 39/39 on 2026-09-09 01:15).

### muxcode-preview-hook.sh

**Phase:** PreToolUse
**Trigger:** Write, Edit, NotebookEdit
**Window:** edit only (detected via `tmux display-message -p '#W'`; exits immediately if the current window is not `edit`)

Opens the target file in nvim and shows a diff preview of the proposed change before the user accepts or rejects it.

**What it does:**
1. Dismisses any pending "Press ENTER" prompt and ensures normal mode
2. Cleans stale diff from a previously rejected edit (skips if temp file < 3s old — concurrent invocation guard)
3. Opens the file at the line about to be changed (folds open, search highlight cleared)
4. For Edit tool: generates a temp file with the proposed change via `python3` (required — no diff without it)
5. Opens a horizontal diff split with `scrollbind` (original below, proposed above), syntax matching the file type
6. Jumps to the changed line after a 150ms delay — sent as a separate `tmux send-keys` so scrollbind is fully active before the jump

**Implementation details:**
- Each nvim command in a `|` pipe chain needs its own `sil!` prefix — the modifier only suppresses the immediately following command, not the full chain. Without this, errors like E35 cause "Press ENTER" prompts that break subsequent commands.
- The jump-to-line uses `norm! {LINE}Gzz` (not `:N`) because `norm!` properly triggers scrollbind sync between both diff panes.
- Concurrent invocations (from global + project `.claude/settings.json` both firing the hook) are handled via temp file age detection — if the temp file is < 3 seconds old, the second invocation exits immediately.

**Customization:**
- `MUXCODE_PREVIEW_SKIP` — space-separated substrings of file paths to skip (default: `/.claude/settings.json /.claude/CLAUDE.md /.muxcode/`)

### muxcode-diff-cleanup.sh

**Phase:** PreToolUse
**Trigger:** Read, Bash, Grep, Glob
**Window:** edit only

Lightweight cleanup hook. If a diff preview is still open from a previously rejected edit, this closes it before the next tool runs.

### hook analyze (analyze hook)

**Command:** `muxcode hook analyze`
**Phase:** PostToolUse
**Trigger:** Write, Edit, NotebookEdit

Signals that a file was edited. Performs three tasks:

1. **Workflow transition**: Transitions the [workflow state machine](architecture.md#workflow-state-machine) to `editing` (clears outcomes if regressing from a later state)
2. **Trigger file**: Appends the edited file path to the trigger file for the bus daemon
3. **Event routing**: Sends file-change events to appropriate agents based on file type (uses `--no-notify` — no status bar flash for file-change events)
4. **Diff cleanup**: In the edit window, waits ~1s for the async preview hook to finish, then closes the diff preview and reloads the file at the changed line. The delay prevents the cleanup from racing ahead of the preview setup.

**NotebookEdit:** For `NotebookEdit` tool events, `file_path` is extracted from `tool_input.notebook_path`. The diff preview opens the `.ipynb` file at the raw JSON level.

**File routing rules** (configurable via `MUXCODE_ROUTE_RULES`):
- Test/spec files -> test agent
- Infrastructure files (cdk, terraform, pulumi, stack, construct) -> deploy agent
- Source files (.ts, .js, .py, .go, .rs) -> build agent

**Matching mechanics:** Rules are evaluated in order (first match wins). Each rule's pattern is `|`-separated substrings matched case-sensitively against the full file path. Files matching no rule skip routing silently.

### hook bash (bash hook)

**Command:** `muxcode hook bash`
**Phase:** PostToolUse
**Trigger:** Bash

Detects build, test, deploy, and git commands, drives event chains, transitions the [workflow state machine](architecture.md#workflow-state-machine), and logs history with error extraction:

```
Build success        → trigger test agent
Test success         → trigger review agent
Deploy-apply success → trigger verify (self-loop to deploy agent)
Any failure          → notify edit agent
```

Deploy commands are split into two categories:
- **Deploy patterns** (`MUXCODE_DEPLOY_PATTERNS`): all deploy commands — logged to deploy history
- **Deploy-apply patterns** (`MUXCODE_DEPLOY_APPLY_PATTERNS`): mutation-only commands (deploy, destroy, apply) — trigger the verify chain

Preview commands (`cdk diff`, `terraform plan`, `pulumi preview`) match deploy patterns for history logging but do **not** trigger verification.

Test commands are split the same way:
- **Test patterns** (`MUXCODE_TEST_PATTERNS`): the suite — its exit code is the test stage's verdict, its row the evidence
- **Test precheck patterns** (`MUXCODE_TEST_PRECHECK_PATTERNS`, default `go*vet`): gates run before the suite — a **failing** precheck is the stage failing (test-history row, failure path, exactly like a failed suite); a **passing** one proves nothing about a suite that has not run, so it transitions the workflow to `testing` but writes no row and fires no chain

`bus.ChainEvent` (`bus/hook.go`) is the one decision on what a classified call feeds — `build`, `test`, `deploy`, or `run`/`watch` for an unclassified call in those roles — and answers nothing for a bus command, a git or deploy-preview call, or a passing precheck; `hook bash` fires exactly the chain it names (`HookBashResult.Chain`). The class exists because on 2026-09-09 `go*vet` sat in the test patterns and, once the evidence rule above had test-runner run vet as its own call, every passing vet fired test→review before the suite had started — and the suite's real result was then dropped by the Reviewing guard in `triggerChain`, so a review ran on a suite nobody had seen pass. Test patterns are consulted before precheck ones, so a command listed in `MUXCODE_TEST_PATTERNS` is a full test run even if it also matches a precheck; the evidence rule treats a precheck as a test statement, so bundling it is denied the same way. Pinned by `TestProcessBashHook_TestPrecheck`, `TestChainEvent` and `TestClassifyCommand_TestPrecheckOverrides` (`bus/hook_test.go`), and seen live the same night at 01:28: the test agent's lone `go vet` moved the workflow and wrote nothing, and the suite's row at 01:29 was the only test evidence and closed the graph node.

Also sends events to the analyst for analysis (conditional on outcome — build/test only notify analyst on failure or unknown exit codes, deploy notifies on all outcomes).

After the primary chain action, the hook fires event subscriptions — matching `subscriptions.jsonl` entries by event+outcome pattern and sending fan-out messages via `SendNoCC()` (no auto-CC to edit). Use `muxcode subscribe add` to configure.

**Customization:**
- `MUXCODE_BUILD_PATTERNS` — pipe-separated patterns for build command detection
- `MUXCODE_TEST_PATTERNS` — pipe-separated patterns for test command detection
- `MUXCODE_TEST_PRECHECK_PATTERNS` — pipe-separated patterns for test prechecks (default `go*vet`): failure is test evidence, success feeds nothing
- `MUXCODE_DEPLOY_PATTERNS` — pipe-separated patterns for deploy command detection (all deploy commands)
- `MUXCODE_DEPLOY_APPLY_PATTERNS` — pipe-separated patterns for deploy-apply commands that trigger the verify chain

**Error extraction:** For failed build and test commands, the hook extracts error-relevant lines from tool output into an `errors` field in the history JSONL. The regex matches common error patterns: `error:`, `ERR!`, `failed`, `fatal`, `panic`, `FAIL:`, `not found`, `undefined`, `syntax error`, `permission denied`, etc. Test patterns additionally match `assert` and `expect`. The left-pane log views prefer the `errors` field over raw `output` when displaying failures, surfacing diagnostic information instead of noise like "Exit code: 1".

**JSON parsing:** Implemented in Go using `encoding/json` — no external dependencies (`jq`/`python3` not required). The preview hook (`muxcode-preview-hook.sh`) still uses `python3` for generating proposed file content; without it, no split diff appears in nvim.

### hook comment-block (comment blocker)

**Command:** `muxcode hook comment-block`
**Phase:** PostToolUse
**Trigger:** Write, Edit
**Mode:** sync (block decision reaches the model)

Enforces the code-comments skill: flags multi-line comment blocks written inside function bodies. Unlike the other PostToolUse hooks it carries no `async` flag — it runs synchronously so its block decision is fed back to the model, which sees the reason and can hoist the prose to a doc comment or extract a named function instead.

- **Scans only what the edit introduced** — Edit's `new_string` or Write's `content`, never the whole file — so pre-existing comment blocks in a touched file are never flagged. Reported line numbers are relative to the edited fragment, not the file.
- **Fires at 3+ consecutive comment lines at body indentation.** Column-zero runs (file headers, license blocks, package/module docs) are exempt by design — that boundary is where the skill tells authors to write. The threshold is deliberately looser than the skill's own rule (at most one short line in a body): two lines can be a wrapped sentence, three is a paragraph — the unambiguous case worth interrupting for.
- **Exempt paths**: unsupported languages are never scanned (only mapped extensions are — `//` markers: `.go .ts .tsx .js .jsx .java .rs .c .cc .cpp .h`; `#` markers: `.py .sh .bash .rb`), and test files (`_test.go`, `.test.ts`, `.spec.js`, `test_*` prefix, `/tests/`, `/spec/`, …) are skipped — a test's comment often *is* the specification of the behavior under test. `#` lines inside Python docstrings are not counted.
- **Provider-gated** like every hook — no-op for non-hook providers.

Backed by `ScanCommentBlocks()` / `IsCommentBlockExempt()` / `FormatCommentBlockReason()` in `bus/comment_block.go` (`hookCommentBlock()` in `cmd/hook.go`).

### hook stop (self-poll re-launch)

**Command:** `muxcode hook stop`
**Phase:** Stop (fires when the agent finishes a turn)
**Mode:** sync (can block the stop)

Part of the [delivery-acknowledgement](architecture.md#delivery-tracking) redesign. Keeps a Claude Code agent's background inbox listener (`muxcode inbox --poll --loop`) alive: when the agent ends a turn without an active `--poll`/`--wait` listener, the Stop hook **blocks the stop** with instructions to re-launch the listener, so the agent never goes silent and stops receiving delegated work. This is the single point of reliability for Claude self-poll delivery.

- **Provider-gated** — for a self-polling provider (Claude Code) it re-launches the listener; for a hook-road provider without one (Codex, `SelfPollsInbox()` false) it branches to `CodexStopDelivery` and blocks the stop only when an actionable request is pending, handing the messages over as `reason` (see [Codex hooks](#codex-hooks)); a no-op for non-hook providers and outside a session.
- **Loop-guarded** — respects `stop_hook_active` so a re-launch can't recurse.
- **The daemon's fallback never interrupts a live turn.** When the listener does die, delivery falls to the daemon's forced road (`ForceDeliver`, the stall watchdog's re-drive, receipt-gap recovery), which types into the pane behind an Escape preamble — Claude's tool-interrupt key. Those roads first ask `AgentIsWorking()`, and a working pane is skipped (`deliver-skipped-busy`, `redrive-skipped-busy`, `stall-skipped-busy`) rather than woken, because the `❯` composer renders mid-tool-call and the bare prompt is not evidence of idleness ([MUX-171](requirements/backlog/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md); see [Daemon watchdogs](architecture.md#daemon-watchdogs)).
- Backed by the pure `DecideStopHook` / `StopHookAction` / `StopHookPollReason` / `FormatStopBlock` helpers in `bus/hook.go` (`hookStop()` in `cmd/hook.go`).
- Relevant under the receipt cutover (now the **default**): self-poll delivery is on unless rolled back via `MUXCODE_DELIVERY_ACK_DISABLE` (hard kill switch), `MUXCODE_DELIVERY_ACK=off`, or `muxcode delivery-ack off`, each of which reverts to daemon-push delivery.

## Hook Event Format

Hooks receive JSON on stdin with this structure:

```json
{
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "/path/to/file.ts",
    "old_string": "original code",
    "new_string": "modified code"
  },
  "tool_response": {
    "exit_code": 0,
    "stdout": "...",
    "stderr": ""
  }
}
```

PreToolUse hooks receive `tool_input` only (no response yet).
PostToolUse hooks receive both `tool_input` and `tool_response`.

Codex CLI sends the same envelope plus `hook_event_name`, `session_id`, `turn_id`, `transcript_path`, `cwd`, `model`, `permission_mode` and `tool_use_id`. Three shape differences: `tool_input.command` is a plain string (argv is also accepted), `apply_patch` puts the whole patch text in `tool_input.command`, and `tool_response` for `Bash` is a bare stdout string with **no exit code** — the real code is read from the rollout transcript. See [Codex hooks](#codex-hooks); every shape is pinned by a fixture in `bus/testdata/codex-hooks/`.

## Build-Test-Review Chain

The chain is **hook-driven**, ensuring deterministic behavior:

1. Build agent runs `./build.sh` (or configured build command)
2. `hook bash` detects build command completed
3. If exit code 0: hook sends `request:test` to test agent
4. Test agent runs tests (a precheck such as `go vet`, run as its own call first, feeds the chain only when it fails — see [hook bash](#hook-bash-bash-hook))
5. Hook detects test command completed
6. If exit code 0: hook sends `request:review` to review agent
7. Review agent reviews `git diff`, replies with findings

On failure at any step, the hook notifies edit directly with the error details.

Each chain step also transitions the [workflow state machine](architecture.md#workflow-state-machine): build success → `testing` (with `build_outcome: success`), test success → `reviewing` (with `test_outcome: success`), failures → `build-failed` or `test-failed`. File edits during or after the chain regress the state to `editing` and clear all accumulated outcomes.

**Key property:** Agents are NOT responsible for chaining. They only run their command and reply. The hook guarantees the chain fires deterministically based on exit codes.

## Deploy-Run-Watch Chain

When a deploy-apply command succeeds, the hook triggers a run→watch chain:

1. Deploy agent runs `cdk deploy` (or `terraform apply`, `pulumi up`, etc.)
2. `hook bash` detects deploy-apply command completed
3. If exit code 0: hook sends `request:run` to the run agent
4. Run agent executes validation commands (AWS resource health, API smoke tests)
5. If run succeeds: hook sends `request:watch` to the watch agent
6. Watch agent monitors logs (CloudWatch, k8s, Docker) and reports findings to edit
7. On failure at any step: hook notifies edit directly with error details

Preview commands (`cdk diff`, `terraform plan`) are logged to deploy history but do **not** trigger the chain. Deploy failures transition the workflow state to `deploy-failed`.

The run→watch step is gated by a **`command_match` allowlist** — the run chain's `OnSuccess` is a first-match-wins action array (`runWatchActions()` in `bus/profile.go`) where each action carries one `command_match` condition for a verification-run shape (`aws *` invocations, first-token-anchored `.sh` script executions such as `bash *.sh`, `./*.sh`, `scripts/*.sh`). Commands matching no shape fire nothing, so incidental reads in the run window (`cat`, `ls`, `grep`) never trigger a watch request; `muxcode *` commands are additionally excluded via `command_not_match`. Patterns anchor the script to the first token because glob `*` spans spaces — a bare `*.sh *` would also match `ls -la x.sh`, a read of a script path rather than an execution. See [`run-chain-watch-overfire`](requirements/completed/MUX-084-run-chain-watch-overfire.md); integration test: `scripts/test-run-chain-scope.sh`.

## Conditional chains

Chain actions support condition expressions that control when they fire. Conditions are evaluated as AND logic — all conditions in an action must pass for it to fire.

### Condition types

| Condition | Value | Passes when |
|-----------|-------|-------------|
| `files_match` | glob pattern | Any changed file matches the pattern |
| `files_not_match` | glob pattern | No changed file matches the pattern |
| `branch_match` | regex | Current branch name matches |
| `branch_not_match` | regex | Current branch name does not match |
| `command_match` | glob pattern | The triggering command matches the pattern |
| `command_not_match` | glob pattern | The triggering command does not match the pattern |
| `env_set` | env var name | Environment variable is set and non-empty |
| `env_equals` | `VAR=value` | Environment variable equals the specified value |
| `output_contains` | substring | Command output contains the substring |
| `exit_code` | integer | Command exit code equals the value |

### Action arrays (first-match-wins)

Each chain outcome (`on_success`, `on_failure`, `on_unknown`) accepts either a single action or an array. When multiple actions are configured, the first action whose conditions all pass fires — remaining actions are skipped:

```json
{
  "build": {
    "on_success": [
      {
        "target": "deploy",
        "action": "deploy",
        "message": "Deploy to staging",
        "conditions": { "branch_match": "^release/" }
      },
      {
        "target": "test",
        "action": "test",
        "message": "Run tests"
      }
    ]
  }
}
```

In this example, build success on a `release/*` branch sends to deploy; on any other branch, it falls through to test.

### Chain context

`ChainContext` carries runtime state for condition evaluation:

- **Branch** — current git branch name
- **ChangedFiles** — list of uncommitted changed files
- **Command** — the triggering command line (evaluated by `command_match`/`command_not_match`)
- **Output** — command stdout/stderr
- **ExitCode** — command exit code

Git information (branch, changed files) is lazy-loaded via `PopulateGitInfo()` — only called when conditions or message templates reference `${branch}` or `${changed_files}`.

### Message templates

Chain messages support template variables: `${exit_code}`, `${command}`, `${branch}`, `${changed_files}`. Templates are expanded via `ExpandMessageWithContext()`.

### CLI

```bash
muxcode chain <event> <outcome> [--verbose] [--files F] [--branch B] [--output O] [--exit-code N] [--command CMD] [--dry-run] [--no-notify]
```

Use `--verbose` to see per-condition PASS/FAIL results. Use `--files`, `--branch`, `--output` to override context values for testing.

## Testing

The diff preview integration can be tested with `scripts/test-diff-split.sh`:

```bash
bash scripts/test-diff-split.sh
```

**Requirements:** Running muxcode session with nvim in `edit.0`.

**Phases:**
1. **Setup** — creates a test file, verifies nvim opens it
2. **PreToolUse (Edit)** — simulates preview hook, verifies diff split (2 windows, diffmode on)
3. **PostToolUse (accepted)** — simulates analyze hook, verifies cleanup (1 window, temp file removed)
4. **Stale cleanup** — simulates rejected edit, ages the temp file, verifies stale diff is cleaned on next preview
5. **Skip patterns** — verifies `MUXCODE_PREVIEW_SKIP` skips matching files
6. **Write tool** — verifies Write tool opens file without diff split (no `old_string`)

`scripts/test-codex-hooks.sh` covers the codex hook road hermetically — the live-captured fixtures fed to every `muxcode hook` subcommand in a scratch bus; floor **39** (37 plus the two evidence-rule cases: a lone `./build.sh` from build passes, the live bundled shape is denied) — and, with `MUXCODE_CODEX_HOOKS_LIVE=1` and a codex ≥ 0.153 on `PATH`, drives a real codex agent through seven live checks; without the gate the live section prints its skip reason.

## Creating Custom Hooks

You can add project-specific hooks alongside the muxcode hooks in `.claude/settings.json`. Hooks are additive — multiple hooks can match the same tool.

Example: add a linting hook that runs after file edits:

```json
{
  "matcher": "Write|Edit",
  "hooks": [
    {"type": "command", "command": "my-lint-hook.sh", "async": true}
  ]
}
```
