# MuxCode

Multi-agent coding environment built on tmux, Neovim, and Claude Code. Each agent runs in its own tmux window, coordinated through a file-based message bus.

## Tech stack

| Layer | Technology |
|-------|------------|
| Launcher | Go (single `muxcode` binary) |
| Hooks | Bash |
| Bus binary | Go 1.22 (stdlib only, no external deps) |
| Agent definitions | Markdown with YAML frontmatter |
| Terminal multiplexer | tmux >= 3.0 |
| Editor | Neovim |
| AI CLI | Claude Code (`claude`) |

## Directory structure

```
scripts/                      # Hook scripts, utility scripts, pollers
agents/                       # Default agent definition files (.md)
agents/harness/               # Simplified agent definitions for local LLM harness
skills/                       # Default skill definition files (.md)
config/                       # settings.json, tmux.conf, nvim/ (managed nvim config)
config/nvim/                  # Neovim config loaded via NVIM_APPNAME=muxcode
├── init.lua                  # Full lazy.nvim config (Dracula, treesitter, render-markdown, telescope)
└── plugin/startscreen.lua    # MuxCode start screen (logo, agents, shortcuts)
docs/                         # Documentation
tools/muxcode/      # Go module — the bus binary
├── bus/                      # Core library
├── cmd/                      # Subcommand handlers
├── daemon/                   # Bus daemon — inbox poller + trigger file monitor
├── tui/                      # Dracula-themed dashboard TUI
└── main.go                   # Entry point
tools/muxcode-llm-harness/    # Go module — standalone local LLM harness
├── harness/                  # Core library
└── main.go                   # Entry point
```

## Build, test, install

| Command | What it does |
|---------|-------------|
| `./build.sh` | Runs `make install` — builds Go binary, installs agents/configs — then `muxcode upgrade-daemons` so all running session daemons re-exec the new binary |
| `muxcode upgrade-daemons [--dry-run]` | Restart all running session daemons/monitors on the installed binary (long-lived daemons otherwise keep the code from their launch). Kills orphan daemons whose tmux session is gone; `build.sh` runs it after `make install` |
| `./test.sh` | Runs `go vet ./...` and `go test -v ./...` in the bus module |
| `make build` | Builds Go binary to `bin/muxcode` |
| `make install` | Build + install binary to `~/.local/bin/`, agents, skills, configs to `~/.config/muxcode/` |
| `make clean` | Remove `bin/` directory |
| `./install.sh` | First-time setup — checks prereqs, builds, configures tmux and Claude Code hooks |
| `bash scripts/test-diff-split.sh` | Integration test for nvim diff split preview (requires running muxcode session) |
| `bash scripts/test-hot-reload.sh` | Integration test for agent hot reload (requires running muxcode session) |
| `bash scripts/test-resize-hook.sh` | Integration test for the `client-resized` window auto-refit hook (requires running muxcode session) |
| `bash scripts/test-echo-as-result.sh` | Integration test for the echo-as-result fix — synthesized bus-response rows never render as a pass (isolated scratch session, no live session needed) |
| `bash scripts/test-lifecycle-log-leak.sh` | Regression test that a full test run leaves `~/.config/muxcode/logs` untouched (runs the suite under a throwaway `HOME`) |
| `bash scripts/test-branch-time-recording.sh` | Integration test for the branch-time requirements-doc sink — JSON read path, idempotent upsert, never-regress reconciliation (hermetic; no live session needed) |
| `bash scripts/test-disk-pressure.sh` | Integration test for the disk-pressure signal and its alert cooldown — healthy machine stays silent, footprint/headroom breaches alert, alert fires once per window not every 60s cycle (safe: never invokes `CleanupStale`) |
| `bash scripts/test-auto-clear.sh` | Integration test for auto-clear between tasks (MUX-103) — a scratch daemon fires `/clear` exactly once per completed task into an enrolled idle pane, guards hold (pending inbox, edit hard-exclusion, unenrolled role), manual `muxcode clear` path works (isolated scratch bus + scratch tmux session; requires the installed binary to include MUX-103) |

Both Go modules have **no external dependencies** (stdlib only).

## Code conventions

### Go (bus binary & harness)

- PascalCase for exported identifiers, camelCase for unexported
- Stdlib only — no third-party imports
- Tests in `*_test.go` files, same package (not `_test` suffix)
- Bus directory path: `/tmp/muxcode-bus-{session}/` in `bus/config.go`

### Bash (hooks & utility scripts)

- `set -euo pipefail` for build/test/install scripts (`build.sh`, `test.sh`, `install.sh`)
- Hooks do NOT use `set -e` — they exit gracefully on errors
- 2-space indentation
- `snake_case` for functions, `UPPER_CASE` for environment variables
- JSON parsing: `jq` primary, `python3` fallback

**Editing pitfalls:**

- **Vim `sil!` in pipe chains**: `sil!` only suppresses the immediately following command, NOT the full `|` pipe chain — every command needs its own `sil!` prefix (e.g. `sil! cmd1 | sil! cmd2 | sil! cmd3`). Without this, errors like E35 cause "Press ENTER" prompts that break subsequent commands.
- **Diff preview jump-to-line**: must be sent as a separate `tmux send-keys` after 150ms sleep — the diff needs scrollbind fully active before jumping. Uses `norm! {LINE}Gzz` (not `:N`) because `norm!` properly triggers scrollbind sync between both diff panes.
- **Process substitution in tool profiles**: `Bash(diff *)` does NOT match `diff <(...)` — Claude Code treats `<()` as a special construct requiring explicit `Bash(diff <(*)`.
- **tmux send-keys text + Enter**: must be sent as two separate `tmux send-keys` calls with a brief delay (100ms). Claude Code's TUI can drop the Enter key when it arrives in the same pty write as the preceding text characters — the agent ends up with text in the input buffer but never submits it.

### Agent definitions

- YAML frontmatter with `description:` field (extracted by `ExtractFrontmatter()` in `bus/launch.go`)
- kebab-case filenames (e.g. `code-editor.md`, `git-manager.md`, `dev-server.md`)
- Role-to-filename mapping in `AgentFileName()` in `bus/launch.go`

### Documentation

- 2-space indentation in markdown
- Title Case for H1, Sentence case for H2+
- Prefer tables and code blocks over prose
- Cross-link docs with relative paths (e.g. `docs/architecture.md`)
- When updating docs, augment existing content — don't rewrite or reorganize
- Feature requirements live in `docs/requirements/` — completed specs in `completed/`, in-progress drafts in `drafts/`, planned/parked specs in `backlog/` (indexed by [`backlog/backlog.md`](docs/requirements/backlog/backlog.md))
- **GitHub tracking (MUX ids)**: each backlog spec carries a stable `MUX-NNN` id assigned in the backlog index — GitHub issue titles, branches (`MUX-NNN-<slug>`), and PR titles (`MUX-NNN: <summary>`) carry the id, and the spec file is renamed to `MUX-NNN-<slug>.md` when its issue is created. `MUX-NNN` matches the `[A-Z][A-Z0-9]*-[0-9]+` key shape existing tooling expects (story-lifecycle spec lookup, branch-time key matching), so no code changes are needed
- **Integration test phase required**: every requirements doc MUST include a dedicated integration test phase (typically the final implementation phase). It must contain either: (1) specific test steps written as checkboxes that can be automated (e.g. `- [ ] Reload build+test agents → verify config changed`), or (2) a step to create a test automation script (e.g. `- [ ] Create scripts/test-{feature}.sh with automated verification`). The test phase should validate end-to-end behavior, not just unit tests.

### Permissions (`.claude/settings.local.json`)

- **No hardcoded user paths**: this is an open-source project — never add absolute paths containing usernames or home directories (e.g. `/Users/mkoberlein/...`). Use relative paths (`tools/muxcode/...`) or generic patterns (`~/.config/muxcode/...`).
- User-specific `Read()` permissions for external dirs (nvim plugins, dotfiles, etc.) belong in the user's global `~/.claude/settings.json`, not in the project-local file.

## Key constraints

- **Edit agent delegation**: never runs build, test, deploy, dev servers, API requests, log tailing, AWS commands, git commands (including read-only like `git status`), or GitHub CLI commands (`gh`). All delegated via message bus. On Claude Code, enforcement is via PreToolUse hook (`muxcode hook guard`). On OpenCode (`MUXCODE_EDIT_CLI=opencode`), enforcement is via `DenyTools` in the tool profile (emitted as OpenCode `deny` permission rules) plus daemon-side pane audit (`checkNonHookEdits()`). The edit agent can run on OpenCode with DeepSeek V4 Pro — set `MUXCODE_EDIT_CLI=opencode` to switch, `MUXCODE_EDIT_CLI=claude` (or unset) to switch back. Dev server lifecycle (start, stop, restart, status) goes to the **serve** agent (F5 window). AWS process execution (`aws lambda invoke`, `aws stepfunctions`, `aws s3 ls`, `aws s3 cp`, `aws s3api`) and ad-hoc script execution (integration tests like `scripts/test-*.sh`, one-off shell scripts) go to the **run** agent. Log tailing (`aws logs`, `kubectl logs`, `docker logs`, `tail -f`) goes to the **watch** agent — watch is strictly read-only log tailing, no AWS mutations or data inspection. API testing requests go to the `api` agent (modal-only role, opened via `prefix + i` or `muxcode modal open api`). PR review reads (Copilot comments, CI failures) go to the **commit** agent with action `pr-read` — never to the review agent. Documentation updates (specs, architecture, requirements) go to the **plan** agent with action `update-docs` — and this is hard-enforced: the `muxcode hook guard` PreToolUse hook blocks the edit agent from `Write`/`Edit`/`NotebookEdit` on any `docs/**/*.md` file (`CheckDocFileGuard` in `bus/hook.go`; the plan agent is exempt, `CLAUDE.md`/`README.md` at repo root remain editable). Jira and Confluence work (reads and writes) also goes to the **plan** agent — see the Atlassian authority constraint below. See [Architecture](docs/architecture.md).
- **Atlassian authority (Jira/Confluence writes)**: reads are open to every role; **writes are gated to the plan agent alone** (`atlassianAuthorityDefault` in `bus/atlassian_authority.go`, enforced by `CheckAtlassianAuthority` at the CLI and by `CheckAtlassianMCPGuard` on the MCP surface, so the rule holds on both roads). Plan owns the shared *written* artifacts — specs under `docs/` and the tracker items they describe. The authority previously sat with edit, justified by edit being the only agent in direct conversation with the user; plan does not have that property, so the human-in-the-loop guarantee is replaced by a scope rule: **plan writes only on an explicit user-initiated request relayed from edit, never as a side effect of a spec or docs change.** That rule exists because the opposite instruction once caused plan to rewrite a Jira description, post a comment, and create an issue link while merely handling a spec revision. Edit is the consent boundary (it talks to the user); plan is the write boundary (it holds the authority). `MUXCODE_ATLASSIAN_AUTHORITY_ROLES` overrides the role list; setting it to the empty string denies every role, which is the right default for a strictly human-owned tracker. `TestAtlassianAuthorityDefault` pins the default so it cannot move silently.
- **Plan agent scope**: the plan agent (F1) is scoped to docs directories only (`docs/`, `CLAUDE.md`, `README.md`). It can read source code for context but never writes outside docs. Tool profile: `bus`, `readonly`, `common`, plus `Write`, `Edit`, read-only git, `tree`, `python3`, `jq`. The `docs` hosted role is mapped to `plan` (messages to `docs` deliver to the plan agent's inbox). The F1 window also hosts a **research agent** via mode cycling — pressing F1 when on the plan window toggles between plan and research modes.
- **Spec verification**: the plan agent automatically verifies implementation progress against the active requirements spec after review completes. Set the active spec with `muxcode spec set <path>`. When the build→test→review chain finishes successfully, the daemon sends a `verify-spec` request to the plan agent with the spec path and changed files. The plan agent reads the spec and changed code, checks off completed acceptance criteria and phase steps (`- [ ]` → `- [x]`), updates the status field, and reports to edit. Controlled by `NotifyPlanOn` in the review `EventChain` config (default: `["success"]`). Only fires when an active spec is set. The same pass **records branch active time** into the spec: plan reads the ledger via `muxcode branch-time show --branch <b> --json` (fresh branches return `seconds: 0`, never an error) and upserts a `## Time Tracking` row — keyed by branch, replaced in place, absolute totals never deltas (idempotent re-recording). Never-regress reconciliation: a ledger reading lower than the doc row (lost/reset store) keeps the doc's larger value and re-seeds the ledger via `muxcode branch-time seed` (a floor that only raises — never `--add`, which double-counts on a non-zero ledger). Branches with no active spec, and repos without `docs/requirements/`, degrade quietly to accumulate-only.
- **Research agent**: runs on OpenCode with DeepSeek V4 Pro on the F1 window (mode index 1). Has its own independent inbox (`inbox/research.jsonl`) — not hosted on edit or plan. Specializes in web searching API docs, platform references, and GitHub projects. Not part of any event chain. Delegates implementation to the active F2 agent via `muxcode mode active --window edit`. Findings persist in `research-history.jsonl` (console) and memory (cross-session).
- **Hook-driven chains**: build→test→review, deploy→run→watch chains are deterministic (bash exit codes), not LLM-driven. Only fires for hook-supporting providers (`provider.SupportsHooks()`). Non-hook providers (OpenCode, Codex CLI, local LLM) use three-layer graceful degradation: (1) config-driven `buildChainInstruction()` generates natural-language chain instructions per role, (2) agent body adaptation via `adaptBodyForNonHookProvider()` (OpenCode) or shared agent config via `WriteAgentConfig()` (Codex) that rewrites hook chain references to manual commands, (3) `CheckSendPolicy()` bypass so non-hook agents can send chain messages that would be blocked for hook agents. See [Hooks](docs/hooks.md).
- **Conditional chains**: `ChainAction` supports a `Conditions` map with 10 condition types (`files_match`, `files_not_match`, `branch_match`, `branch_not_match`, `command_match`, `command_not_match`, `env_set`, `env_equals`, `output_contains`, `exit_code`). Actions in a `ChainActions` slice are evaluated first-match-wins — first action whose conditions pass (or has no conditions) fires. `ChainContext` carries git state (branch, changed files), command output, and exit code. Git info is lazy-loaded via `PopulateGitInfo()` only when conditions or templates reference it. Subscriptions also support conditions via the same mechanism. The run chain's `OnSuccess` uses a `command_match` allowlist (AWS invocations, script executions) so watch fires only for verification-run shapes — never for incidental reads or `muxcode *` commands.
- **Hot reload**: `muxcode reload <role> [--cli <cli>] [--model <model>] [--compact]` stops an agent, reconfigures with optional CLI/model overrides (written to session-scoped runtime override files in `/tmp/muxcode-bus-{session}/config/{role}.env`), and relaunches. Supports multi-role batch reload: `muxcode reload build test review --cli opencode --model opencode-go/minimax-m2.5` reloads 3 agents sequentially (3s gap). `muxcode reload --all --cli opencode` reloads all active agents (excludes edit/auto). `muxcode reload --all --provider claude --cli opencode` filters to only agents currently on Claude. Reload markers suppress daemon health checks during the cycle. Provider selector modal (`prefix + R` or `prefix + b → Provider`) provides visual provider/model selection with multi-agent checkbox section — `a` selects all (excludes edit/auto), `p` selects by current provider, `n` deselects all. Multi-agent confirm shows a live progress view with per-agent status. `muxcode config set/get/list` manages persistent config. Resolution chain: runtime override → per-role env → global env → config file → default. See [Configuration](docs/configuration.md).
- **Codex CLI sandbox**: Codex CLI sandboxes all filesystem writes and blocks outbound network access. This makes it unsuitable for roles that write to `.git` (commit) or push to remotes (commit, deploy). Codex agents that attempt git operations will silently create an alternate `.git` directory in `/tmp/` and fail to push. Only use Codex for read-only roles (review, analyze). Use Claude Code or OpenCode for git and deploy operations.
- **User-initiated commits**: git commits, pushes, and PR creation are never auto-triggered. The automated chain stops at review. Batch changes into logical units — do not commit every small change. Only commit when the user explicitly asks or when a complete logical unit of work is finished. No micro-commits.
- **Attribution stripping**: `LaunchSession()` installs a git `commit-msg` hook (`InstallCommitMsgHook()` in `bus/git_hooks.go`) that strips `Co-authored-by` trailers from every commit. The hook is idempotent (marker-based), chains with existing hooks, and respects `core.hooksPath`. This ensures AI attribution lines never appear in commit history regardless of what the LLM generates.
- **Pre-commit safeguard**: commit delegation blocked when any agent has pending inbox, is busy, or has running procs/spawns. Bypass with `--force`.
- **Auto-CC**: messages from build/test/review/deploy to non-edit agents are copied to edit inbox. Chain/subscription messages use `SendNoCC()` to avoid redundant CC.
- **Agent notifications**: `Notify()` (`bus/notify.go`) writes a timestamp to `trigger-{role}.notify` and wakes agents via send-keys. Non-hook providers (OpenCode TUI, Codex CLI) are routed to `provider.SendWakeUp()` which injects the actual message payload (filtering out self-addressed messages to prevent echo loops). Claude Code agents get "You have new messages" text injection. Display-message (tmux status bar flash) is sent as a human-visible indicator for agents that are active but not idle. Harness panes are skipped (they poll inbox directly).
- **Edit inbox**: two delegation modes — `--wait` blocks until the response arrives (polls every 500ms, timeout: `MUXCODE_INBOX_POLL_TIMEOUT`, default 600s), `--track` creates a tracked task and returns immediately (daemon auto-completes the task and wakes the sender when the response arrives in their inbox). Use `--wait` when the result is needed before proceeding; use `--track` for long-running or fire-and-forget operations where the editor should continue working. Both modes create task entries in `/tmp/muxcode-bus-{session}/tasks/` (view with `muxcode tasks`). `--wait` and `--track` are mutually exclusive.
- **Delegation message hygiene**: bus sends must be **short, single-line, intent-level**. `validatePayload()` (`cmd/send.go`) warns when a payload contains newlines ("may break allowedTools glob matching") or exceeds 500 chars — treat these as errors to fix, not noise, since a long/multi-line payload can miss the `Bash(muxcode *)` permission glob and trigger a prompt instead of delivering cleanly. **Delegate intent, not pre-baked artifacts**: describe what to do and let the receiving agent compose the details — e.g. don't hand the commit agent a full multi-line commit message or exhaustive file list; say what to commit and let it stage tracked files and write the message. Prefer `--track` for delegations; reserve `--wait` for when the result is needed next. A healthy `--wait` polls every 500ms and can look like a hang while the sender drains its inbox backlog — it is not stuck. **File handoff for long/structured content**: when the work genuinely needs a lot of detail (batches, multi-line bodies, per-item instructions), write it to a scratch file (`/tmp/<descriptive-name>.md`, self-describing with IDs/bodies/how-to-process) and send a **short** message pointing the agent at the file — e.g. `muxcode send commit pr-read "Read /tmp/pr188-comment-replies.md and post each entry's body as a reply to its PR #188 review comment id; report how many posted." --track`. The file carries the payload; the bus message stays one short line. See [Agent Bus CLI](docs/agent-bus.md#muxcode-send).
- **Keep agents deliverable (no blocking foreground commands)**: the daemon's `checkIdleAgents()` delivers/notifies **only idle** agents — an agent left **active** never receives its inbox (`diagnose.go` flags `active-with-stale-messages`). Never run a blocking/never-exiting command (`gh pr checks --watch`, `tail -f`, log follows, interactive watchers) in an agent's own pane; route watch-to-completion work to the **watch** agent or a background `muxcode proc`. Recovery for a wedged-active pane is state-dependent: (1) merely *busy* → returns to idle on its own and the daemon delivers; (1b) **at the prompt but delivery stuck** (idle misdetection, dropped send-keys/Enter, stale notified markers, parked input) → `muxcode deliver <role> [--force]` injects the pending inbox via the robust text→delay→Enter→verify path; `--force` also skips the idle gate, clears stale notified markers and parked input. **Never hand-roll `tmux send-keys "You have new messages" Enter`** (text+Enter in one pty write is the dropped-Enter pitfall); (2) genuinely **frozen** TUI (Escape, `Ctrl-U`, **and** `Ctrl-C` all ignored) → keystroke/marker recovery all **fail** (`agent-health` only toggles a marker; `reload`/`GracefulStop` rely on send-keys that never land; a frozen-but-alive process passes `IsAgentAlive` so auto-restart never fires). Reliable fix: **kill the OS process** (`kill -TERM` the role's `claude --agent <file>` PID, then `-KILL`); the dead process triggers daemon auto-restart and the fresh agent re-reads its on-disk inbox (the pending message persists). No single muxcode command yet force-terminates a hung-but-alive agent — see `docs/requirements/backlog/MUX-010-delegation-message-hygiene.md`.
- **Agent wake-up**: the daemon's `checkIdleAgents()` runs every 5 seconds — for each idle agent (including edit) with actionable inbox messages (request-type only, not response-only), it calls `Notify()` which injects "You have new messages" via send-keys. Agents just process messages, reply, and go idle. No agent polls for messages — all are woken by the daemon. On startup, `LaunchSession()` wakes agents with pre-populated inbox messages — for Claude Code agents via send-keys after reaching the `❯` prompt, for non-hook providers (OpenCode, Codex CLI) via `provider.SendWakeUp()` which injects the startup message payload directly.
- **Delivery acknowledgement (receipts + self-poll, default ON)**: the default delivery model that replaces the daemon's pane-scrape "did it look idle?" inference with a **positive per-message receipt** plus **agent self-poll**. On consume, `bus/inbox.go` writes a receipt to the `bus/delivery.go` store: a true `acked` (`ReceiptKindAck`) when the agent's own runtime read it (Claude runs `muxcode inbox --poll --loop` as a background listener kept alive by a `Stop` hook, `cmd/hook.go` `hookStop()`; the local harness consumes in-process) or a weaker `delivered` (`ReceiptKindDelivered`) for OpenCode/Codex TUIs, whose runtime can't run `muxcode inbox` — the daemon's `SendWakeUp()` uses verified injection (`bus/inject_verify.go` `confirmInjectionAndConsume()`: confirm the injected text left the composer, then consume). A daemon backstop `checkPollHealth()` detects a growing **receipt gap** (un-receipted inbox past a threshold), re-drives delivery (`ForceDeliver` / `SendWakeUp`), and alerts edit with a `delivery-gap` event. "Un-receipted" is decided by `hasReceipt()` (`bus/delivery.go`), the single read-side definition of received: `AckedAt > 0` **OR** `Status == responded` — **a reply implies receipt**, and is strictly stronger evidence than a consume-ack (the agent didn't just read the message, it finished the work and answered). The second clause matters because `MarkResponded()` records a response without setting `AckedAt`; without it an answered-but-never-consumed request reads as un-receipted forever, so the gap counts it permanently and the backstop re-drives delivery for work that is already done — observed live as ~21h of repeated re-drives and duplicate LGTM echoes from one review request. `MarkResponded()` is also the single choke point that drains the answered row from the responder's inbox (`ConsumeByID`), resolving the recipient from the original request's `To` so a stale correlation still drains the right inbox, and covering the `--wait` fallback path where a response is correlated with no `ReplyTo`. **Default ON** — the cutover bypasses `checkIdleAgents`/`checkParkedInput`/`checkPaneSweep`; rollback valves in precedence order: `MUXCODE_DELIVERY_ACK_DISABLE=1` (env hard kill switch back to pane-scrape delivery, needs a daemon restart), `MUXCODE_DELIVERY_ACK=off` (env opt-out; `=on` pins it), and the instant restart-free runtime rollback `muxcode delivery-ack off` (writes a `delivery-ack.off` marker the daemon re-reads every poll; `on` clears it). Physically removing the bypassed pane-scrape machinery is a tracked follow-up (`docs/requirements/backlog/MUX-012-remove-gated-pane-scrape-delivery.md`). See [Architecture](docs/architecture.md#delivery-tracking).
- **Auto-clear between tasks (MUX-103)**: episodic Claude agents (review, plan, commit, run, api) can have their conversation cleared automatically after a task completes — each bus request is self-contained and cross-task state lives in `muxcode memory`, so retained context between tasks is dead-weight input-token burn. **Off by default**; enroll roles with `MUXCODE_AUTO_CLEAR_ROLES` (comma-separated; `edit` and `auto` are hard-excluded even when listed — enforced at config parse AND inside the guard, pinned by test) and tune the post-response quiet window with `MUXCODE_AUTO_CLEAR_QUIET_SECS` (default 60). The daemon's `checkAutoClear()` observes task completion through both completion stores (`bus/task.go` for `--wait`/`--track` delegations, responded delivery statuses for chain requests that never create a task) and calls `bus.ClearAgent()` — the guarded `/clear` injection (idle pane, no actionable inbox, no live in-flight task, no reload marker, Claude provider only, not a harness pane, window not mode-cycled to another agent) — at most once per completed task: the per-role marker `auto-clear-{role}.last` gates re-fire, while a failing guard only postpones to a later cycle. Each clear logs an `auto-clear` lifecycle event. Manual one-off: `muxcode clear <role>` runs the same guarded path. Code: `bus/clear.go`, `daemon/daemon.go` (`checkAutoClear`).
- **Daemon identity**: the bus daemon (background supervisor process) uses `daemon` as its bus identity — not `watcher`, which would collide with the `watch` agent (F9 window). `NormalizeBusRole("daemon")` maps to `edit` so reply instructions route to a valid agent. Lifecycle logs show source `daemon`. The `daemon` identity is filtered out of message loop detection.
- **System actions**: `loop-detected`, `compact-recommended`, `proc-complete`, `spawn-complete`, `ollama-down`, `ollama-recovered`, `ollama-restarting`, `agent-down`, `agent-restarting`, `agent-recovered` are excluded from message loop detection (`isSystemAction()`).
- **Agent diagnostics**: `muxcode diagnose <role>` performs automated root cause analysis when an agent isn't responding. Collects evidence from agent state, inbox, notification pipeline, daemon health, and lifecycle timeline, then pattern-matches against 14 known failure modes (stale notified IDs, missed send-keys, idle detection failure, daemon not waking, post-restart wake gap, provider mismatch, reload marker stuck, pending input blocking, active with stale messages, no actionable messages, daemon dead, agent down, receipt gap, unexplained stuck inbox). Outputs human-readable report with Dracula colors (default) or JSON (`--json`). `muxcode diagnose --all` produces a summary table for all agents. Exits non-zero on critical findings.

  **Delivery-model aware**: the checks that reason about a daemon wake (`daemon-not-waking`, `post-restart-wake-gap`) are gated on `bus.AckDeliveryActive()` — the single definition shared with the daemon. Under the delivery-ack cutover no daemon wake follows an `inbox-notify` at all (agents self-poll; receipts are the evidence), so timeline gap annotation is suppressed there and `receipt-gap` covers the model instead. Diagnose having its own stale model of delivery is what made it render a red "expected idle-wake, got none" line per notify on healthy sessions: `idle-wake` is emitted only by `checkIdleAgents`, the very function the cutover bypasses.

  **No false clean verdicts**: `checkUnexplainedEvidence` is a verdict-consistency backstop registered **last** in `diagnosticChecks` (it reads the findings earlier checks produced). If an agent holds actionable messages unconsumed past `diagnoseStuckInboxSecs` and no other check fired, it reports `unexplained-stuck-inbox` as critical rather than "No issues detected". The invariant is asserted at the verdict, not per pattern, because the same false-clean bug recurred three times from three *different* missing detectors — an honest "unexplained" beats a clean bill of health over a wedged agent. `TestRunDiagnostics_NeverCleanWithStuckInbox` pins it. Can be run by any agent to diagnose peers — e.g., the edit agent can run `muxcode diagnose commit` when a delegated command times out. See `bus/diagnose.go`.
- **Lifecycle logging**: persistent JSONL logs at `~/.config/muxcode/logs/{session}.log` record launcher sequence, daemon events, agent launches, auto-accept, and cleanup. Survives session cleanup. View with `muxcode lifecycle show [session]`, filter with `--source`, `--level`, `--event`, `--since`. Rotation at 1000 entries (configurable via `MUXCODE_LIFECYCLE_LOG_MAX`). Purge old logs with `lifecycle purge --days 30`.
- **PII scrubbing**: tool output from `api`, `runner`/`run`, and `watch` roles is redacted before entering the LLM conversation. Harness agents use automatic scrubbing in the executor (`harness/scrub.go`). Claude Code agents pipe output through `muxcode pii-scrub`. Patterns: emails, SSN, credit cards (prefix-anchored), phone numbers (separator-required), AWS keys, JWTs, generic secrets/tokens. **Self-documenting redaction**: when anything is redacted, an in-band banner (`PIIScrubNotice`) is prepended to the output via `ScrubPIIWithNotice()` so PII-sensitive agents don't mistake masked placeholders for real data or compute lengths/sizes/counts over them. Wired into `cmd/scrub.go` and `harness/executor.go`.
- **`--wait` auto-degrade**: `muxcode send --wait` blocks only up to `MUXCODE_WAIT_DEGRADE_SECS` (default 90; `0` disables = full blocking up to `MUXCODE_INBOX_POLL_TIMEOUT`). If no response by then, the send converts to a tracked task and returns so the sender unblocks and drains its inbox; the daemon wakes it when the result lands. Code: `awaitOrTrack`/`degradeWaitSecs` in `cmd/send.go`.
- **Relay-loop suppression**: the `bus.Send` path drops repeated identical agent-to-agent request relays once the same `(from,to,action)` tuple fires `>= MUXCODE_RELAY_SUPPRESS_THRESHOLD` (default 4; `0` disables) within `MUXCODE_RELAY_SUPPRESS_WINDOW` seconds (default 300). Scoped to non-edit senders — prevents wedged relay storms (e.g. `run→watch` when watch is stood down). Code: `bus.CountRecentRequestTuple` (`guard.go`) + guard in `cmd/send.go`.
- **Daemon watchdogs**: four resilience watchdogs self-heal stuck agents, each opt-out via env var with lifecycle events. (1) **Long-active** — `checkActiveWatchdog()` queues a non-invasive advisory to an agent continuously active past `MUXCODE_ACTIVE_WATCHDOG_SECS` (default 600; `0` disables), nudging summarize+escalate; skips `--wait`/poll/reload/harness/non-hook; emits `long-active` event + lifecycle `active-watchdog`. (2) **Stuck-provider** — `checkStuckProviders()` detects non-hook agents (OpenCode/Codex) wedged in a provider loop (signatures like `InternalError.Algo` / "repeated across multiple consecutive rounds" / "No matching discriminator") via two-sighting debounce and auto-reloads in place (cap 3/role, 180s cooldown, then `agent-stuck` alert to edit); `MUXCODE_STUCK_RELOAD_DISABLE=1` disables; lifecycle `stuck-provider-reload`/`stuck-provider-giveup`. Code: `bus/stuck.go` (`PaneShowsProviderLoop`). (3) **Stuck in-flight task expiry** (root-cause fix) — a task stuck `in-flight` (delivered while busy, never responded) used to permanently block all new `(to,action)` sends to that role via the dedup guard (even `--force` couldn't bypass); now `bus.TaskExpired` (`task.go`) lets `HasInFlightTaskForRole`/`FindInFlightTask` (`dedup.go`) ignore expired tasks and `checkTrackedTasks` times them out (lifecycle `task-timeout`). Tasks self-heal after their timeout (default 600s). (4) **Permission-block** — `checkStuckPermissions()` breaks the re-notification loop when a **hook-provider (Claude Code)** agent is wedged at a REJECTED permission prompt it cannot satisfy autonomously (e.g. `./build.sh` denied, no human to approve): it never responds, its request stays actionable, and the idle-delivery safety net re-wakes it endlessly. Detects via `bus.PaneShowsPermissionBlock` ("blocked by permission system", "without explicit user authorization", "rejected. Unable to proceed") gated on a pending request + two-sighting debounce. **Alert-only**: sets `d.permBlocked[role]` so `checkIdleAgents` stops re-waking it, sends one `permission-blocked` event to edit; `clearPermBlock` lifts suppression once the signature clears, the request drains, or the agent dies. `MUXCODE_PERMBLOCK_WATCHDOG_DISABLE=1` disables. Code: `bus/stuck.go`, `daemon/daemon.go` (`checkStuckPermissions`).
- **Daemon self-monitoring**: the daemon writes a Unix timestamp to `watcher.keepalive` at the top of each poll loop. A companion monitor (`muxcode watch --monitor`) checks the keepalive every 15 seconds — if stale (>30s), it kills and relaunches the daemon.
- **Harness circuit breaker**: 3-layer stuck protection — within-turn (filter), within-batch (`MaxAllBlockedTurns=2`), cross-batch (`MaxConsecutiveFailures=3` triggers 30s cooldown). Each batch has 5-minute timeout. See [Agents](docs/agents.md#circuit-breaker).
- **Harness single-shot roles**: build and test roles auto-complete after one successful tool execution (`isSingleShotRole()`). Prevents small models from looping endlessly re-running the same command. After auto-complete, a text-only Ollama call generates the response summary.
- **Harness agent definitions**: `agents/harness/` directory provides simplified agent definitions for local LLMs. Resolution: `agents/harness/` > `.claude/agents/` > `~/.config/muxcode/agents/harness/` > `~/.config/muxcode/agents/`. Shorter, more directive prompts that avoid confusing smaller models.
- **Harness TUI**: Dracula-themed terminal UI with activity log (Ollama calls, tool executions, output previews), status bar (status/uptime left, role/model/provider right), and alternate screen buffer for clean rendering. Tool output previews show the last meaningful line of command output.

## Code reference

### Go bus binary (`tools/muxcode/`)

Build: `cd tools/muxcode && go build .`
Test: `cd tools/muxcode && go test ./...`

| File | Key exports |
|------|-------------|
| `bus/config.go` | `BusDir()`, `InboxPath()`, `LockPath()`, `TriggerFile()`, `PaneTarget()`, `AgentPane()`, `IsSplitLeft()`, `BusRole()`, `NormalizeBusRole()`, `IsKnownRole()`, `WindowForRole()`, `HarnessMarkerPath()`, `GlobalMemoryDir()`, `GlobalMemoryPath()`, `GlobalMemoryArchiveDir()`, `GlobalMemoryArchivePath()`, `ActiveSpecPath()`, `ReadActiveSpec()`, `WriteActiveSpec()`, `ClearActiveSpec()`, path helpers for cron/proc/spawn/webhook/memory |
| `bus/lifecycle.go` | `LifecycleLogDir()`, `LifecycleLogPath()`, `LogLifecycle()`, `LogLifecycleWithPID()`, `ReadLifecycleLog()`, `FilterLifecycleLog()`, `ListLifecycleSessions()`, `PurgeLifecycleLogs()`, `FormatLifecycleEntry()` |
| `bus/message.go` | Message struct, JSONL encoding |
| `bus/inbox.go` | Read/write/consume inbox, `Send()`, `SendNoCC()`, `HasActionableMessages()` |
| `bus/setup.go` | `Init()`, session re-init purge (`resetFile()`, `purgeStaleFiles()`) |
| `bus/inspect.go` | `GetAgentStatus()`, `GetAllAgentStatus()`, `ReadLogHistory()`, `ExtractContext()`, `PreCommitCheck()` |
| `bus/git_hooks.go` | `InstallCommitMsgHook()`, `resolveGitHooksDir()` — auto-installs git `commit-msg` hook to strip `Co-authored-by` trailers in any repo |
| `bus/guard.go` | `ReadHistory()`, `DetectCommandLoop()`, `DetectMessageLoop()`, `CheckLoops()`, `CheckAllLoops()`, `CountRecentRequestTuple()` (relay-loop suppression) |
| `bus/stuck.go` | `PaneShowsProviderLoop()` — provider-loop signature detection backing the daemon's stuck-provider auto-reload watchdog |
| `bus/compact.go` | `CheckCompaction()`, `CheckRoleCompaction()`, `FormatCompactAlert()`, `FilterNewCompactAlerts()` |
| `bus/clear.go` | Auto-clear between tasks (MUX-103): `AutoClearRoles()`, `AutoClearQuietSecs()`, `AutoClearEligible()`, `AutoClearDue()`, `LastTaskCompletion()`, `ClearAgent()`, `ReadAutoClearMarker()`/`WriteAutoClearMarker()` |
| `bus/dedup.go` | `IsDuplicateMessage()`, `SendIfNotDuplicate()`, `SendNoCCIfNotDuplicate()`, `DedupWindowSecs()`, `HasInFlightTaskForRole()`, `FindInFlightTask()` (both ignore expired tasks via `TaskExpired()`) |
| `bus/delivery.go` | `DeliveryStatus`, `CreateDeliveryStatus()`, `MarkDelivered()`, `MarkResponded()`, `ReadDeliveryStatus()`, `ListDeliveryStatuses()`, `CleanExpiredDeliveries()`, `FormatDeliveryStatus()` |
| `bus/task.go` | `Task`, `CreateTask()`, `CompleteTask()`, `TimeoutTask()`, `TaskExpired()` (stuck in-flight task expiry), `ReadTask()`, `ListTasks()`, `CleanExpiredTasks()`, `FormatTask()` |
| `bus/scrub.go` | `ScrubPII()`, `ScrubPIIWithNotice()`, `PIIScrubNotice`, `IsPIISensitiveRole()`, PII/secret regex patterns (mirrored in harness) |
| `bus/provider.go` | `Provider` interface, `ResolveProvider()`, `ResolveProviderCLI()`, `LocalProvider`, `buildChainInstruction()` (config-driven natural-language chain instructions for non-hook providers), `describeOutcome()`, `describeAction()`, `describeConditions()`, `filterMeaningfulActions()` |
| `bus/provider_claude.go` | `ClaudeCodeProvider` — full Claude Code integration (hooks, idle detection, startup acceptance, compact) |
| `bus/provider_opencode.go` | `OpenCodeProvider` — TUI mode (pane detection, send-keys wake-up, agent config generation, tool profile translation, task completion detection, `adaptBodyForNonHookProvider()`, self-message filtering in `SendWakeUp()`) |
| `bus/provider_codex.go` | `CodexProvider` — TUI mode (`codex -a never --no-alt-screen`, send-keys wake-up with self-message filtering, `.codex/AGENTS.md` generation, heuristic task completion detection) |
| `bus/codex_events.go` | Codex JSONL event types, `ParseCodexEvents()`, `AnalyzeCodexEvents()`, `FormatCodexResult()`, `RunCodexExec()` |
| `bus/conditions.go` | `ChainContext`, `ConditionResult`, `EvaluateConditions()` (AND logic, 10 condition types: `files_match`, `files_not_match`, `branch_match`, `branch_not_match`, `command_match`, `command_not_match`, `env_set`, `env_equals`, `output_contains`, `exit_code`), `BuildChainContext()`, `BuildChainContextFromFlags()`, `PopulateGitInfo()` (lazy git), `FormatConditionResults()`, `ValidateConditions()`, `fileGlobMatch()` |
| `bus/hook.go` | `ProcessBashHook()`, `ProcessAnalyzeHook()`, `ProcessGuardHook()`, `ResolveChain()`, `ExpandMessage()` |
| `bus/console.go` | `DefaultConsoleConfigs()`, `ConsoleConfig`, `RunConsole()`, per-role renderers (Dracula theme) |
| `bus/profile.go` | `DefaultConfig()`, `MuxcodeConfig`, `ToolProfile`, `EventChain`, `ChainAction` (with `Conditions` map), `ChainActions` (slice with single-object/array JSON marshal/unmarshal), `ResolveTools()`, `ResolveChain()` (accepts `*ChainContext`, first-match-wins on action arrays), `ResolveChainVerbose()`, `ExpandMessageWithContext()` (supports `${branch}`/`${changed_files}` with lazy git), `CheckSendPolicy()` (provider-aware — bypasses deny for non-hook providers), `ChainShouldNotifyAnalyst()` (`NotifyAnalystOn` field), `ChainShouldNotifyPlan()` (`NotifyPlanOn` field — notifies plan agent to verify spec progress), `ValidateConfig()`, `resolveRoleAlias()` (normalizes legacy profile key aliases to canonical role names) |
| `bus/search.go` | BM25: `tokenize()`, `stem()`, `buildCorpus()`, `bm25Score()`, `SearchMemoryBM25()`, `SearchMemoryWithOptions()` |
| `bus/rotation.go` | `NeedsRotation()`, `RotateMemory()`, `PurgeOldArchives()`, `ReadMemoryWithHistory()`, `AllMemoryEntriesWithArchives()`, `ListMemoryRoles()` |
| `bus/launch.go` | `LaunchConfig`, `AgentFileName()`, `RoleCLIEnvVar()`, `RoleClaudeModelEnvVar()`, `RoleClaudeModelDefault()`, `InlineFallbackPrompt()`, `ExtractFrontmatter()`, `ResolveAgentFile()`, `BuildAgentsJSON()`, `ResolveVenv()`, `BuildSharedPrompt()`, `ResolveLaunchConfig()`, `BuildExecArgs()`, `PreLaunchSetup()`. Default windows: `plan edit build test serve review deploy run watch commit` |
| `bus/atlassian.go` | `LoadAtlassianConfig()`, `AtlassianConfig`, `JiraRead()`, `JiraUpdate()`, `JiraComment()`, `JiraReadComments()`, `FormatJiraComments()`, `JiraListLinkTypes()`, `JiraLinkIssues()`, `FormatLinkTypes()`, `JiraLinkType`, `JiraListTransitions()`, `JiraTransitionIssue()`, `FormatTransitions()`, `JiraTransition`, `JiraSearch()`, `FormatJiraSearch()`, `JiraSearchResult`, `JiraCreateSubtask()`, `JiraCommentEntry`, `ConfluenceRead()`, `ConfluenceUpdate()`, `ConfluenceSearch()`, `flattenADF()` |
| `bus/api.go` | API testing: `Environment`, `Collection`, `Request`, `ApiHistoryEntry` structs, CRUD, `ImportApiDir()`, formatters |
| `bus/cron.go` | Cron scheduling: structs, parsing, CRUD, execution, formatting |
| `bus/proc.go` | `StartProc()`, `CheckProcAlive()`, `RefreshProcStatus()`, `StopProc()`, `CleanFinished()` |
| `bus/spawn.go` | `StartSpawn()`, `StopSpawn()`, `RefreshSpawnStatus()`, `GetSpawnResult()`, `CleanFinishedSpawns()` |
| `bus/webhook.go` | `ServeWebhook()`, `WriteWebhookPid()`, `ReadWebhookPid()`, `IsWebhookRunning()`, `StopWebhookProcess()` |
| `bus/workflow.go` | `WorkflowState` (16 states including `StateRunning`, `StateRunFail`, `StateWatching`, `StateWatchFail`), `WorkflowStateEntry`, `ReadWorkflowState()`, `TransitionWorkflow()`, `WithFiles()`, `WithOutcome()`, `FormatWorkflowState()`, `FormatWorkflowStateCompact()`, `WorkflowStateColor()`, `HasNewMessageFrom()` |
| `bus/subscribe.go` | `Subscription` (with `Conditions` map), `AddSubscription()`, `MatchSubscriptions()` (accepts `*ChainContext` for condition evaluation), `FireSubscriptions()` (accepts `*ChainContext`), `ExpandSubscriptionMessage()` (supports `${branch}`/`${changed_files}`) |
| `bus/context.go` | `ContextFilesForRole()`, `AllContextFilesForRole()`, `FormatContextPrompt()`, `FormatContextList()` |
| `bus/detect.go` | `DetectProject()`, `AutoContextFiles()`, `conventionText()`, `FormatDetectOutput()` |
| `bus/demo.go` | `RunDemo()`, `BuiltinScenarios()`, `ScaleDelay()` |
| `bus/ollama.go` | `OllamaClient`, `ChatComplete()`, `CheckHealth()` |
| `bus/tools.go` | `BuildToolDefs()`, `IsToolAllowed()`, `globMatch()` |
| `bus/executor.go` | `ToolExecutor`, `Execute()` — bash/read/glob/grep/write/edit |
| `bus/agent.go` | `AgentLoop()`, `AgentConfig`, `buildSystemPrompt()`, `processMessages()` |
| `bus/health.go` | `CheckOllamaInference()`, `LocalLLMRoles()`, `RestartOllama()`, `RestartLocalAgent()` |
| `bus/agent_health.go` | `IsAgentAlive()`, `IsAgentStopped()`, `MarkAgentStopped()`, `ClearAgentStopped()`, `IsAgentHealthExcluded()`, `RoleHasWindow()`, `FormatAgentHealthAlert()`, `AgentHealthAlertKey()` |
| `bus/override.go` | `WriteRuntimeOverride()`, `ReadRuntimeOverrides()`, `LoadRuntimeOverrides()`, `ClearRuntimeOverrides()`, `RuntimeOverridePath()` — session-scoped runtime config overrides |
| `bus/reload.go` | `ReloadAgent()`, `ReloadAll()`, `GracefulStop()`, `ReloadTarget()`, `ReloadMarkerPath()`, `IsReloading()`, `IsReloadMarkerStale()`, `CleanStaleReloadMarkers()` |
| `bus/reload_batch.go` | `AgentReloadStatus`, `ActiveAgentStatuses()`, `ReloadableRoles()`, `ReloadResult`, `ReloadBatch()`, `AbbreviateModel()`, `FormatReloadResults()` |
| `bus/remote.go` | `RemoteSession`, `DiscoverSessions()`, `FormatSessionList()`, `RemoteAgentCapture()`, `RemoteAgentIsIdle()`, `RemoteInboxSummary`, `GetRemoteInbox()`, `FormatRemoteInbox()`, `RemoteOverview()`, `TmuxListWindowNames()` — cross-session investigation |
| `bus/provider_options.go` | `ProviderOption`, `AvailableProviders()`, `ProviderByIndex()`, `ProviderByCLI()`, `ResolveActiveAgentWindow()`, `WindowFKey()` — provider selector data layer |
| `bus/config_file.go` | `GetShellConfig()`, `ResolveConfigPath()`, `SetShellConfigValue()` — shell-sourceable config file read/write |
| `bus/watcher_health.go` | `KeepalivePath()`, `IsKeepaliveStale()`, `TouchKeepalive()` — daemon keepalive monitoring |
| `bus/resize.go` | `ResizeAllWindows()`, `listAllWindows()`, `attachedFitSize()` — `muxcode resize` backing the `client-resized` hook; two-pass refit of every window across **all** sessions (attached: `resize-window -A`; detached: explicit fit size), replacing the old single-session xargs one-liner |
| `bus/deliver.go` | `ForceDeliver()`, `DeliverResult` — `muxcode deliver <role> [--force]` force-delivers an agent's pending inbox via the robust wake-up path, bypassing daemon idle detection; `--force` skips the idle gate and clears stale notified markers (recovery for `active-with-stale-messages` / dropped send-keys) |
| `cmd/` | Subcommand handlers (one per CLI command) |
| `daemon/daemon.go` | Bus daemon: inbox polling, trigger debounce, cron/proc/spawn/loop/compaction/ollama checks, auto-clear trigger (`checkAutoClear()`), resilience watchdogs (`checkActiveWatchdog()`, `checkStuckProviders()`, `checkTrackedTasks()` in-flight task timeout) |
| `tui/` | Dashboard TUI (Dracula theme), Provider selector TUI (`provider_select.go`), Remote session browser TUI (`remote.go`) |
| `tui/remote.go` | `RemoteUI`, `NewRemoteUI()`, `Run()` — interactive cross-session browser with session list, agent detail, and content views (capture/inbox/diagnose) |

### Go LLM harness (`tools/muxcode-llm-harness/`)

Build: `cd tools/muxcode-llm-harness && go build .`
Test: `cd tools/muxcode-llm-harness && go test ./...`

| File | Key exports |
|------|-------------|
| `harness/config.go` | `Config`, `DefaultConfig()`, `InboxPath()`, `HistoryPath()` |
| `harness/ollama.go` | `OllamaClient`, `ChatComplete()`, `CheckHealth()` |
| `harness/bus.go` | `BusClient`, `ConsumeInbox()`, `Send()`, `Lock()/Unlock()`, `ResolveTools()`, `LogHistory()` |
| `harness/tools.go` | `BuildToolDefs()`, `IsToolAllowed()`, `GlobMatch()` |
| `harness/executor.go` | `Executor`, `Execute()` — bash/read/glob/grep/write/edit, `ScrubPII` flag |
| `harness/filter.go` | `Filter`, `Check()`, `isInboxCommand()`, `isSelfSend()`, `commandHash()` |
| `harness/prompt.go` | `BuildSystemPrompt()`, `LocalLLMInstructions()`, `RoleExamples()`, `ReadAgentDefinition()` |
| `harness/loop.go` | `Run()`, `processBatch()`, `logToolToHistory()`, `isSingleShotRole()`, `toolOutputPreview()`, circuit breaker (cooldown, batch timeout), single-shot auto-complete |
| `harness/events.go` | `EventKind` constants (`EventStartup`, `EventToolOutput`, etc.), `Event` struct |
| `harness/tui.go` | `TUISink`, `NewTUISink()`, Dracula-themed TUI with activity log, status bar, alternate screen buffer |
| `harness/scrub.go` | `ScrubPII()`, `IsPIISensitiveRole()`, PII/secret regex patterns |
| `harness/message.go` | `Message`, `ParseMessages()`, `FormatTask()` |

### Bash scripts

- Hooks consume JSON from stdin via `cat` — parse with `jq` or `python3`
- Preview hook detects edit window via `tmux display-message -p '#W'` — exits immediately if not `edit`
- Analyze hook writes trigger file at `/tmp/muxcode-analyze-{session}.trigger` — format: `<timestamp> <filepath>` per line

### Agent definitions, skills, context

- **Agent files**: 3-tier resolution: `.claude/agents/` > `~/.config/muxcode/agents/` > defaults. Frontmatter extraction by `ExtractFrontmatter()` in `bus/launch.go`. See [Agents](docs/agents.md).
- **Skill files**: 3-tier resolution: `.muxcode/skills/` > `~/.config/muxcode/skills/` > `skills/`. YAML frontmatter with `name`, `description`, `roles`, `tags`.
- **Context files**: `context.d/shared/*.md` (all roles) + `context.d/<role>/*.md`. Priority: project > user > auto-detected.
- **Tool profiles**: `bus/profile.go` — per-role permissions with `Include` (shared groups), `CdPrefix`, `Tools`. See [Agents](docs/agents.md#tool-profiles).
- **Config files**: shell-sourceable, resolution: `$MUXCODE_CONFIG` > `.muxcode/config` > `~/.config/muxcode/config`. See [Configuration](docs/configuration.md).

## See also

- [Architecture](docs/architecture.md) — system design, data flows, bus protocol, left-pane pollers, session re-init
- [Agent Bus CLI](docs/agent-bus.md) — full CLI reference for all subcommands
- [Agents](docs/agents.md) — roles, permissions, local LLM, tool profiles, Ollama health, LLM harness
- [Hooks](docs/hooks.md) — hook system, chain behavior, customization
- [Configuration](docs/configuration.md) — env vars, directory structure, examples
- [Backlog](docs/requirements/backlog.md) — planned features
