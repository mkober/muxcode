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
| `muxcode upgrade-daemons [--dry-run] [--force] [--session <name>]` | Restart running session daemons/monitors on the installed binary — a long-lived daemon otherwise keeps the code from its launch. Version-aware: each daemon records its build in `daemon.version` at startup, and one matching this binary (`Info.SameBuild` — version, commit **and** date, so a dirty rebuild still cycles) is skipped unless `--force`. `--dry-run` names both sides per session; `--session` scopes to one daemon. Kills orphan daemons whose tmux session is gone; `build.sh` runs it after `make install` |
| `./test.sh` | Runs `go vet ./...` and `go test -v ./...` in the bus module |
| `muxcode version [--json] [--at-least vX.Y.Z]` | Print the stamped identity; `--version`/`-v` print the same and never route to the launcher. `--at-least` exits 0 (at or past), 1 (older), 2 (uncomparable — an untagged dev build) for script preconditions. Stamped via Makefile `-X` ldflags from `git describe`; unstamped builds fall back to Go's VCS info, then `devel` |
| `make build` | Builds Go binary to `bin/muxcode` (prints the stamped version) |
| `make install` | Build + install binary to `~/.local/bin/`, agents, skills, configs to `~/.config/muxcode/` |
| `make clean` | Remove `bin/` directory |
| `./install.sh` | First-time setup — checks prereqs, builds, configures tmux and Claude Code hooks |
| `git push origin vX.Y.Z` | A `v*` tag push runs `release.yml`: `./test.sh` gate → darwin/linux × amd64/arm64 stamped via `make build VERSION=<tag>` → `sha256sums.txt` → `gh release create --generate-notes`. `gh workflow run release.yml -f tag=vX.Y.Z` publishes an already-pushed tag. Manual tagging stays user-approved, via the commit agent |
| _(automatic)_ | `.github/workflows/auto-release.yml` cuts a patch release on every merged PR: next `vX.Y.Z` from the latest release tag, tagged on the merge commit, then `release.yml` via `workflow_call` — **not** the `v*` push trigger, because a tag pushed with `GITHUB_TOKEN` raises no workflow event. **Label a PR `skip-changelog` to cut no release** (docs and backlog churn carry it, so the version tracks shipped code). Triggered on `pull_request`/`closed` so labels are on the payload; a direct push to main cuts nothing. A `concurrency` group stops simultaneous merges computing the same tag |
| `bash scripts/release-labels.sh` | Idempotently creates the five labels `.github/release.yml` reads: `breaking`, `type:feature`, `type:defect`, `docs`, and `skip-changelog`. A GitHub mutation, so route it through the commit agent |
| `bash scripts/test-*.sh` | Integration tests, one per feature. Each is hermetic unless noted and carries a coverage floor so a skipped section cannot report green; read the script's header for what it covers. Live-session required: `test-diff-split`, `test-hot-reload`, `test-resize-hook`. Feature tests: `test-echo-as-result`, `test-lifecycle-log-leak`, `test-branch-time-recording`, `test-disk-pressure`, `test-auto-clear` (MUX-103), `test-graph-orchestrator` (MUX-014), `test-control-pane` (MUX-108), `test-force-respond` (MUX-105), `test-send-keys-dash` (MUX-104), `test-prompt-mode` (MUX-109), `test-close-spec-guard` (MUX-114), `test-multi-phase-graph` (MUX-121), `test-graph-tui` (MUX-031), `test-pane-targeting` (MUX-117), `test-fkey-labels` (MUX-134), `test-verify-spec-refire` (MUX-007), `test-restart-definition` (MUX-136), `test-version` (MUX-138). Most need an installed muxcode >= v0.1.0 |

Both Go modules have **no external dependencies** (stdlib only).

## Code conventions

### Go (bus binary & harness)

- PascalCase for exported identifiers, camelCase for unexported
- Stdlib only — no third-party imports
- Tests in `*_test.go` files, same package (not `_test` suffix)
- Bus directory path: `/tmp/muxcode-bus-{session}/` in `bus/config.go`
- **No test may bind a socket** — use `newPipeServer` (both modules), never `httptest.NewServer`. A sandboxed agent cannot listen, so a socket-bound test panics before any assertion and `set -e` then hides every later module (MUX-152/153). Production code takes an optional `*http.Client` (nil in production) as the seam

### Bash (hooks & utility scripts)

- `set -euo pipefail` for build/test/install scripts (`build.sh`, `test.sh`, `install.sh`)
- Hooks do NOT use `set -e` — they exit gracefully on errors
- 2-space indentation
- `snake_case` for functions, `UPPER_CASE` for environment variables
- JSON parsing: `jq` primary, `python3` fallback

**Editing pitfalls:**

- **Vim `sil!` in pipe chains**: `sil!` suppresses only the command immediately after it, not the whole `|` chain — every command needs its own prefix (`sil! cmd1 | sil! cmd2`), or an E35 raises a "Press ENTER" prompt that breaks the commands after it.
- **Diff preview jump-to-line**: send as a separate `tmux send-keys` after a 150ms sleep, since scrollbind must be active before jumping. Use `norm! {LINE}Gzz`, not `:N` — only `norm!` syncs scrollbind across both diff panes.
- **Process substitution in tool profiles**: `Bash(diff *)` does NOT match `diff <(...)` — Claude Code treats `<()` as a special construct requiring explicit `Bash(diff <(*)`.
- **tmux send-keys text + Enter**: two separate calls with a ~100ms delay. Claude's TUI drops an Enter arriving in the same pty write as the text before it, leaving the agent with a full input buffer it never submits.

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
- **GitHub tracking (MUX ids)**: each backlog spec carries a stable `MUX-NNN` id from the backlog index, used in issue titles, branches (`MUX-NNN-<slug>`), PR titles, and the spec filename once its issue exists. It matches the `[A-Z][A-Z0-9]*-[0-9]+` shape existing tooling expects, so no code changes are needed
- **Integration test phase required**: every requirements doc MUST end with an integration test phase containing either automatable checkbox steps (`- [ ] Reload build+test agents → verify config changed`) or a step to create `scripts/test-{feature}.sh`. It validates end-to-end behaviour, not just units.

### Permissions (`.claude/settings.local.json`)

- **No hardcoded user paths**: this is open source — never add absolute paths carrying a username or home directory. Use relative (`tools/muxcode/...`) or generic (`~/.config/muxcode/...`) forms.
- User-specific `Read()` permissions for external dirs (nvim plugins, dotfiles, etc.) belong in the user's global `~/.claude/settings.json`, not in the project-local file.

## Key constraints

- **Edit agent delegation**: never runs build, test, deploy, dev servers, API requests, log tailing, AWS commands, git (including read-only `git status`), or `gh`. All delegated via the bus. Enforced by the `muxcode hook guard` PreToolUse hook on Claude, and by `DenyTools` plus the daemon pane audit (`checkNonHookEdits()`) on OpenCode. Routing — **serve**: dev-server lifecycle. **run**: AWS process execution and ad-hoc scripts (`scripts/test-*.sh`). **watch**: log tailing only, no mutations or data inspection. **api**: API testing (modal-only, `prefix + i`). **commit** with action `pr-read`: PR review reads — never the review agent. **plan** with action `update-docs`: all docs, hard-enforced by `CheckDocFileGuard` blocking edit from `docs/**/*.md` (plan exempt; root `CLAUDE.md`/`README.md` stay editable); Jira and Confluence too — see the Atlassian authority constraint. `MUXCODE_EDIT_CLI=opencode|claude` switches edit's provider. See [Architecture](docs/architecture.md).
- **Atlassian authority (Jira/Confluence writes)**: reads are open to every role; **writes are gated to the plan agent alone** (`atlassianAuthorityDefault` in `bus/atlassian_authority.go`, enforced by `CheckAtlassianAuthority` at the CLI and by `CheckAtlassianMCPGuard` on the MCP surface, so the rule holds on both roads). Plan owns the shared *written* artifacts — specs under `docs/` and the tracker items they describe — but is not in conversation with the user, so the human-in-the-loop guarantee is a scope rule instead: **plan writes only on an explicit user-initiated request relayed from edit, never as a side effect of a spec or docs change.** Without it, plan once rewrote a Jira description, posted a comment and created an issue link while merely handling a spec revision. Edit is the consent boundary; plan is the write boundary. `MUXCODE_ATLASSIAN_AUTHORITY_ROLES` overrides the list; empty denies every role, the right default for a human-owned tracker. `TestAtlassianAuthorityDefault` pins it.
- **Plan agent scope**: plan (F1) writes only `docs/`, `CLAUDE.md` and `README.md`; it reads source for context but never writes outside docs. Tool profile: `bus`, `readonly`, `common` plus `Write`, `Edit`, read-only git, `tree`, `python3`, `jq`. The hosted `docs` role maps to plan. F1 also hosts **research** via mode cycling (press F1 on the plan window to toggle).
- **Spec verification**: after review completes, plan verifies progress against the active spec (`muxcode spec set <path>`). The daemon sends `verify-spec` with the spec path and changed files; plan reads both, ticks completed criteria and phase steps (`- [ ]` → `- [x]`), updates status and reports to edit. Controlled by `NotifyPlanOn` in the review `EventChain` config (default: `["success"]`). Only fires when an active spec is set. The same pass **records branch active time**: plan reads `muxcode branch-time show --branch <b> --json` and upserts a `## Time Tracking` row keyed by branch — absolute totals, never deltas, so re-recording is idempotent. Never-regress: a ledger lower than the doc row keeps the doc's larger value and re-seeds via `branch-time seed` (a floor that only raises — never `--add`, which double-counts). No active spec, or no `docs/requirements/`, degrades quietly to accumulate-only.
- **Research agent**: F1 mode index 1, with its own inbox (`inbox/research.jsonl`) — not hosted on edit or plan, and in no event chain. Web-searches API docs and platform references; delegates implementation to the active F2 agent via `muxcode mode active --window edit`. Findings persist in `research-history.jsonl` and memory. Its hold window is created on first cycle, so until then it is configurable but not reloadable.
- **Hook-driven chains**: build→test→review and deploy→run→watch are deterministic (bash exit codes), not LLM-driven, and fire only for hook-supporting providers (`provider.SupportsHooks()`). Non-hook providers degrade in three layers: (1) `buildChainInstruction()` generates natural-language chain instructions per role, (2) `adaptBodyForNonHookProvider()` (OpenCode) or `WriteAgentConfig()` (Codex) rewrites hook chain references to manual commands, (3) a `CheckSendPolicy()` bypass lets them send chain messages a hook agent may not. See [Hooks](docs/hooks.md).
- **Conditional chains**: `ChainAction.Conditions` supports 11 types (`files_match`/`_not_match`, `branch_match`/`_not_match`, `command_match`/`_not_match`, `env_set`, `env_equals`, `output_contains`, `exit_code`, `spec_phases_remaining`). A `ChainActions` slice is first-match-wins: the first action whose conditions pass, or has none, fires. `ChainContext` carries git state, command output and exit code, with git lazy-loaded via `PopulateGitInfo()` only when referenced. Subscriptions use the same mechanism. The run chain's `OnSuccess` uses a `command_match` allowlist so watch fires only for verification-run shapes, never incidental reads or `muxcode *`.
- **Provider/model changes are user-approved only**: NEVER reload an agent onto a different CLI or model without explicit user approval — not to route around a wedged or crash-looping agent, not as recovery. A plain same-provider `muxcode reload <role>` is fine for recovery, but **restarting an agent the user is actively watching is still worth asking about first**; changing what runs an agent is always the user's call.
- **Hot reload**: `muxcode reload <role>... [--cli] [--model] [--compact]` stops, reconfigures (overrides written to `/tmp/muxcode-bus-{session}/config/{role}.env`) and relaunches; multiple roles run sequentially with a 3s gap. `--all` covers active agents (excludes edit/auto), `--provider <cli>` filters to agents currently on that provider. Reload markers suppress daemon health checks during the cycle — a marker left by an interrupted reload makes a healthy agent read as `excluded`. A **windowless role is configured, not reloaded** (`ConfigOnlyRole`): both the runtime override and the shell config are written, and nothing is stopped. Provider selector modal (`prefix + R`): `a` selects all (excludes edit/auto and windowless roles), `p` by provider, `n` none. `muxcode config set/get/list` manages persistent config. Resolution chain: runtime override → per-role env → global env → config file → default. See [Configuration](docs/configuration.md).
- **Codex CLI sandbox**: `-s/--sandbox` selects `read-only`, `workspace-write` or `danger-full-access`, and `--add-dir` grants extra writable roots. Network is restricted separately and by default, which is why the harness tests cannot pass on a Codex agent at all — `TestProcessBatch_SimpleResponse` binds a local socket. `BuildExecArgs` (`bus/provider_codex.go`) passes `-s workspace-write` plus one `--add-dir` per external root **for the build role only** (`codexWritableRoots`); every other role passes no `-s` and inherits the default policy, where writes inside the workspace succeed and writes outside are refused. Without those grants `./build.sh` compiles and then dies in `make install` with `Operation not permitted` on `~/.local/bin`. The roots track the Makefile's `PREFIX`/`BINDIR`/`CONFIGDIR` plus `~/.claude/commands` and the Go caches asked of `go env` — never assumed, since a warm cache hides a missing `GOCACHE` grant until a source file changes. **Every root is resolved through `filepath.EvalSymlinks` before it is granted** (`resolveWritableRoots`): Codex refuses a root containing a symlink component, and that refusal kills the whole sandbox, not just that root — the shell fails before startup so every command dies pre-execution with no output, which reads as a hung model rather than a flag error.
- **User-initiated commits**: git commits, pushes, and PR creation are never auto-triggered. The automated chain stops at review. Batch changes into logical units — do not commit every small change. Only commit when the user explicitly asks or when a complete logical unit of work is finished. No micro-commits.
- **Attribution stripping**: `LaunchSession()` installs a git `commit-msg` hook (`InstallCommitMsgHook()`) stripping `Co-authored-by` trailers from every commit — idempotent, chains with existing hooks, respects `core.hooksPath`. AI attribution never reaches history whatever the LLM generates.
- **Pre-commit safeguard**: commit delegation blocked when any agent has pending inbox, is busy, or has running procs/spawns. Bypass with `--force`.
- **Auto-CC**: messages from build/test/review/deploy to non-edit agents are copied to edit inbox. Chain/subscription messages use `SendNoCC()` to avoid redundant CC.
- **Agent notifications**: `Notify()` (`bus/notify.go`) writes a timestamp to `trigger-{role}.notify` and wakes agents via send-keys. Non-hook providers (OpenCode TUI, Codex CLI) are routed to `provider.SendWakeUp()` which injects the actual message payload (filtering out self-addressed messages to prevent echo loops). Claude Code agents get "You have new messages" text injection. Display-message (tmux status bar flash) is sent as a human-visible indicator for agents that are active but not idle. Harness panes are skipped (they poll inbox directly).
- **Edit inbox**: two mutually exclusive delegation modes — `--wait` blocks for the response (500ms polls, `MUXCODE_INBOX_POLL_TIMEOUT`, default 600s); `--track` creates a tracked task and returns at once, the daemon completing it and waking the sender when the response lands. Use `--wait` when the result is needed next, `--track` otherwise. Both create entries in `/tmp/muxcode-bus-{session}/tasks/` (`muxcode tasks`).
- **Delegation message hygiene**: bus sends must be **short, single-line, intent-level**. `validatePayload()` warns on newlines or >500 chars — treat as errors, since such a payload misses the `Bash(muxcode *)` glob and prompts instead of delivering. **Delegate intent, not pre-baked artifacts**: say what to commit and let the agent stage and write the message, rather than handing it a full commit message or file list. Prefer `--track`; reserve `--wait` for when the result is needed next — a healthy `--wait` polls every 500ms and can look like a hang while the sender drains its backlog. **Re-requests must state what changed**: name the change ("the Phase C fix landed — run it now"), never repeat the original request verbatim; an identical re-send after a reported failure reads as a duplicate and the agent will wait rather than re-run. **File handoff for long content**: write the detail to `/tmp/<name>.md` and send a short message pointing at it — the file carries the payload, the bus message stays one line. **Reply IDs must be looked up, never reconstructed**: a fabricated `--reply-to` never correlates, so the request starves and the daemon re-drives it while you believe you answered. **A reply whose action does not match your request is suspect** — stale correlations arrive carrying your request's id (MUX-154). See [Agent Bus CLI](docs/agent-bus.md#muxcode-send).
- **Keep agents deliverable (no blocking foreground commands)**: the daemon's `checkIdleAgents()` delivers/notifies **only idle** agents — an agent left **active** never receives its inbox (`diagnose.go` flags `active-with-stale-messages`). Never run a blocking or never-exiting command (`gh pr checks --watch`, `tail -f`, interactive watchers) in an agent's own pane; route watch-to-completion work to the **watch** agent or a background `muxcode proc`. Recovery is state-dependent: (1) merely *busy* → returns to idle on its own; (1b) **at the prompt but delivery stuck** (idle misdetection, dropped Enter, stale notified markers, parked input) → `muxcode deliver <role> [--force]`; `--force` also skips the idle gate and clears stale markers and parked input. **Never hand-roll `tmux send-keys "You have new messages" Enter`** (text+Enter in one pty write is the dropped-Enter pitfall); (2) genuinely **frozen** TUI (Escape, `Ctrl-U` and `Ctrl-C` all ignored) → every keystroke/marker recovery fails, and a frozen-but-alive process passes `IsAgentAlive` so auto-restart never fires. Reliable fix: **kill the OS process** (`kill -TERM` then `-KILL`); the daemon auto-restarts and the fresh agent re-reads its on-disk inbox. No muxcode command yet force-terminates a hung-but-alive agent — see `docs/requirements/backlog/MUX-010-delegation-message-hygiene.md`.
- **Agent wake-up**: `checkIdleAgents()` runs every 5s — for each idle agent (edit included) holding actionable messages (request-type only, not response-only) it calls `Notify()`, injecting "You have new messages" via send-keys. Agents process, reply, go idle; none polls. At startup `LaunchSession()` wakes agents with pre-populated inboxes — Claude after the `❯` prompt appears, non-hook providers via `provider.SendWakeUp()` injecting the payload directly.
- **Delivery acknowledgement (receipts + self-poll, default ON)**: replaces the daemon's pane-scrape "did it look idle?" inference with a **positive per-message receipt** plus **agent self-poll**. On consume, `bus/inbox.go` writes a receipt to `bus/delivery.go`: a true `acked` when the agent's own runtime read it (Claude runs `muxcode inbox --poll --loop` as a background listener kept alive by a `Stop` hook; the harness consumes in-process), or a weaker `delivered` for OpenCode/Codex TUIs, whose runtime cannot run `muxcode inbox` — there `SendWakeUp()` uses verified injection (`bus/inject_verify.go`: confirm the text left the composer, then consume). The `checkPollHealth()` backstop detects a growing **receipt gap**, re-drives delivery and alerts edit with `delivery-gap`. "Un-receipted" is decided by `hasReceipt()`, the single read-side definition: `AckedAt > 0` **OR** `Status == responded` — **a reply implies receipt**, stronger evidence than a consume-ack. The second clause is load-bearing: `MarkResponded()` sets no `AckedAt`, so without it an answered-but-never-consumed request reads as un-receipted forever and the backstop re-drives finished work. `MarkResponded()` is also the single choke point draining the answered row from the responder's inbox (`ConsumeByID`), resolving the recipient from the original request's `To` so a stale correlation still drains the right inbox. **Default ON** — the cutover bypasses `checkIdleAgents`/`checkParkedInput`/`checkPaneSweep`. Rollback valves, in precedence order: `MUXCODE_DELIVERY_ACK_DISABLE=1` (hard kill switch, needs a daemon restart), `MUXCODE_DELIVERY_ACK=off`/`=on`, and `muxcode delivery-ack off` (restart-free marker the daemon re-reads each poll). Removing the bypassed pane-scrape machinery is tracked as MUX-012. See [Architecture](docs/architecture.md#delivery-tracking).
- **Auto-clear between tasks (MUX-103)**: episodic Claude agents (review, plan, commit, run, api) can have their conversation cleared after each task — bus requests are self-contained and cross-task state lives in `muxcode memory`, so retained context is dead-weight token burn. **Off by default**; enroll roles with `MUXCODE_AUTO_CLEAR_ROLES` (comma-separated; `edit` and `auto` are hard-excluded even when listed — enforced at config parse AND inside the guard, pinned by test) and tune the post-response quiet window with `MUXCODE_AUTO_CLEAR_QUIET_SECS` (default 60). `checkAutoClear()` reads both completion stores (tasks, and responded delivery statuses for chain requests that create none) and calls `bus.ClearAgent()` — a guarded `/clear` (idle pane, no actionable inbox, no live task, no reload marker, Claude only, not a harness pane, window not mode-cycled) at most once per task via the `auto-clear-{role}.last` marker; a failing guard only postpones. Manual: `muxcode clear <role>`. Code: `bus/clear.go`.
- **Daemon identity**: the daemon's bus identity is `daemon`, not `watcher` (which would collide with the `watch` agent). `NormalizeBusRole("daemon")` maps to `edit` so reply instructions route somewhere valid, and `daemon` is filtered out of loop detection.
- **System actions**: `isSystemAction()` excludes `loop-detected`, `compact-recommended`, `proc-`/`spawn-complete`, the `ollama-*` and `agent-*` events from message loop detection.
- **Graph orchestration (MUX-014)**: an explicit DAG control plane over the bus — `muxcode graph run|validate|list|status|cancel|retry|approve` (`cmd/graph.go`). Chains are linear and first-match; graphs add fan-out (`map`), fan-in barriers (`join`: `all`/`any`/`quorum`), branches (`condition`, reusing `EvaluateConditions()` — no second dialect), capped loops (`max_iterations`; **uncapped cycles are a validation error**), and human gates (`wait_human`). The daemon executes edges — no LLM decides node succession, and an edge delivers an ordinary bus message or spawn, so tool profiles, receipts, PII scrub and the watchdogs all still apply. **Authority gates (MUX-144)**: `graph validate` rejects a commit or Atlassian node not downstream of a `wait_human` gate (topology), and `ApproveGraphGate` calls `CheckGateApprovalAuthority` (`bus/gate_authority.go`) — in the executor, not the CLI, since `graph approve` and the graph TUI both reach it. Default authority is **the user alone, no agent**; `MUXCODE_GATE_AUTHORITY_ROLES` opts a role in **from the config file only** — the environment is deliberately ignored, because an env-read list let any agent widen it by prefixing the variable to the command it was just refused (**review P1: narrowed in `d4ae976`, still open** — the config file is agent-writable and its path honours `$MUXCODE_CONFIG`, so only daemon-side authority closes it). **No agent may approve a gate on a run it created**, whatever the list says. Releases and refusals are attributable: `approved_by` from `BusActorVerified`, plus `graph-gate-approved`/`-refused` events. **Phase 4 gap**: graph sends carry `From = "daemon"`, which `CheckCommitAuthority` normalizes to the authorized `edit`, so the runtime backstop judges a dispatch on the normalized sender rather than the gate's recorded approval. Until it lands the gate is the only control on that path, and a forged marker still defeats it — hence `unverifiedHoldReleased` re-reads `approved_by` daemon-side. **Gate approval is single-use**: dispatching a `wait_human` node purges any prior `approved` marker, so re-entering a gate via `graph retry --from` or a loop edge demands a fresh `graph approve` (`TestExecHumanGateRetryRequiresFreshApproval`). Durable per-run state lives under `BusDir()/graphs/<run-id>/` (atomic writes), so **the first executor tick after a daemon restart *is* the resume scan** — there is no separate recovery path. Templates resolve `project > user > builtin`; 7 ship built in (`req-code-pr` → `spec-to-pr`, `story-lifecycle` removed — both retired names fail). Two invariants only the integration run catches: the inbox must normalize the reply target (`daemon → edit`) or completions are never recorded, and unknown-outcome completions must count toward a join barrier or a join with a non-hook provider upstream hangs forever. See [Architecture](docs/architecture.md#graph-orchestration-control-plane), [CLI](docs/agent-bus.md#muxcode-graph).
- **Agent diagnostics**: `muxcode diagnose <role>` root-causes an unresponsive agent from its state, inbox, notification pipeline, daemon health and lifecycle timeline, matching 15 known failure modes (stale notified IDs, missed send-keys, idle-detection failure, daemon not waking or dead, post-restart wake gap, provider mismatch, stuck reload marker, pending input, active with stale messages, agent down, receipt gap, version mismatch, unexplained stuck inbox). The version mismatch (MUX-138) is a **warning** — a daemon on a different build than the binary running diagnose, fixed by `muxcode upgrade-daemons`; it deliberately does not explain a stuck inbox, being true of every session between an install and its rollout. Human-readable (Dracula) or `--json`; `--all` gives a summary table; exits non-zero on critical findings.

  **Delivery-model aware**: checks reasoning about a daemon wake (`daemon-not-waking`, `post-restart-wake-gap`) are gated on `bus.AckDeliveryActive()`, the definition shared with the daemon. Under the ack cutover no daemon wake follows an `inbox-notify` (agents self-poll), so gap annotation is suppressed and `receipt-gap` covers the model instead — otherwise diagnose renders a false "expected idle-wake, got none" per notify on healthy sessions.

  **No false clean verdicts**: `checkUnexplainedEvidence` runs **last** in `diagnosticChecks` and reads the findings earlier checks produced. If an agent holds actionable messages past `diagnoseStuckInboxSecs` and nothing else fired, it reports `unexplained-stuck-inbox` as critical rather than "No issues detected" — the invariant is asserted at the verdict, not per pattern, because the same false-clean bug recurred from three different missing detectors (`TestRunDiagnostics_NeverCleanWithStuckInbox`). Can be run by any agent to diagnose peers — e.g., the edit agent can run `muxcode diagnose commit` when a delegated command times out. See `bus/diagnose.go`.
- **Lifecycle logging**: persistent JSONL at `~/.config/muxcode/logs/{session}.log` records launcher sequence, daemon events, agent launches and cleanup, surviving session cleanup. `muxcode lifecycle show [session]` with `--source`/`--level`/`--event`/`--since`; rotates at 1000 entries (`MUXCODE_LIFECYCLE_LOG_MAX`); `lifecycle purge --days 30`.
- **PII scrubbing**: tool output from `api`, `run` and `watch` is redacted before entering the conversation — harness agents scrub in the executor, Claude agents pipe through `muxcode pii-scrub`. Patterns: emails, SSN, credit cards (prefix-anchored), phone numbers (separator-required), AWS keys, JWTs, generic secrets. **Self-documenting**: `ScrubPIIWithNotice()` prepends a `PIIScrubNotice` banner whenever anything was redacted, so an agent does not mistake placeholders for real data or compute lengths over them.
- **`--wait` auto-degrade**: blocks only up to `MUXCODE_WAIT_DEGRADE_SECS` (default 90, `0` = full blocking to `MUXCODE_INBOX_POLL_TIMEOUT`). With no response by then the send converts to a tracked task and returns, so the sender unblocks and drains its inbox; the daemon wakes it when the result lands. **A degraded return is not a failure** — never go scrape a pane for the answer. `awaitOrTrack` in `cmd/send.go`.
- **Relay-loop suppression**: `bus.Send` drops repeated identical agent-to-agent **request** relays once the same `(from,to,action)` tuple fires `>= MUXCODE_RELAY_SUPPRESS_THRESHOLD` (default 4, `0` disables) within `MUXCODE_RELAY_SUPPRESS_WINDOW` seconds (default 300). Non-edit senders only. Note it does not cover **response** ping-pong, which `DetectMessageLoop` reports but does not stop.
- **Daemon watchdogs**: four self-healing watchdogs, each opt-out by env var and each logging lifecycle events. (1) **Long-active** — `checkActiveWatchdog()` advises an agent active past `MUXCODE_ACTIVE_WATCHDOG_SECS` (default 600, `0` disables) to summarize+escalate; skips `--wait`/poll/reload/harness/non-hook. (2) **Stuck-provider** — `checkStuckProviders()` detects a non-hook agent wedged in a provider loop (`PaneShowsProviderLoop`, two-sighting debounce) and reloads it in place (cap 3/role, 180s cooldown, then an `agent-stuck` alert); `MUXCODE_STUCK_RELOAD_DISABLE=1` disables. (3) **Stuck in-flight task expiry** — a task stuck `in-flight` (delivered while busy, never responded) used to block every new `(to,action)` send to that role via the dedup guard, `--force` included; `bus.TaskExpired` now lets `HasInFlightTaskForRole`/`FindInFlightTask` ignore expired tasks and `checkTrackedTasks` times them out (lifecycle `task-timeout`, default 600s). (4) **Permission-block** — `checkStuckPermissions()` breaks the re-notification loop when a Claude agent is wedged at a REJECTED permission prompt it cannot satisfy autonomously: it never responds, its request stays actionable, and idle-delivery re-wakes it endlessly. Detects via `bus.PaneShowsPermissionBlock` gated on a pending request + two-sighting debounce. **Alert-only**: sets `d.permBlocked[role]` so `checkIdleAgents` stops re-waking it and sends one `permission-blocked` event; `clearPermBlock` lifts it when the signature clears, the request drains, or the agent dies. `MUXCODE_PERMBLOCK_WATCHDOG_DISABLE=1` disables. Code: `bus/stuck.go`, `daemon/daemon.go`.
- **Daemon self-monitoring**: the daemon timestamps `daemon.keepalive` each poll loop; a companion monitor (`muxcode watch --monitor`) checks it every 15s and relaunches the daemon if it goes stale (>30s).
- **Local LLM harness** (`tools/muxcode-llm-harness/`): **circuit breaker** — 3 layers, within-turn (filter), within-batch (`MaxAllBlockedTurns=2`), cross-batch (`MaxConsecutiveFailures=3` → 30s cooldown); 5-minute batch timeout. **Single-shot roles** — build and test auto-complete after one successful tool execution (`isSingleShotRole()`) so small models cannot loop re-running the same command, then a text-only Ollama call writes the summary. **Definitions** resolve `agents/harness/` > `.claude/agents/` > `~/.config/muxcode/agents/harness/` > `~/.config/muxcode/agents/` — shorter, more directive prompts. **TUI** — Dracula, activity log, status bar, alternate screen. See [Agents](docs/agents.md#circuit-breaker).

## Code reference

### Go bus binary (`tools/muxcode/`)

Build: `cd tools/muxcode && go build .`
Test: `cd tools/muxcode && go test ./...`

| Area | Files |
|------|-------|
| Paths, roles, actors | `bus/config.go` (`BusDir`, `PaneTarget`, `NormalizeBusRole`, `WindowForRole`, `BusActorVerified`, active-spec helpers) |
| Messaging | `bus/message.go`, `bus/inbox.go` (`Send`, `SendNoCC`), `bus/dedup.go`, `bus/delivery.go` (receipts), `bus/task.go`, `bus/subscribe.go` |
| Authority | `bus/commit_authority.go`, `bus/atlassian_authority.go`, `bus/gate_authority.go` |
| Chains & conditions | `bus/profile.go` (`ToolProfile`, `EventChain`, `ResolveChain`), `bus/conditions.go` (11 condition types), `bus/hook.go` |
| Graph control plane | `bus/graph.go`, `bus/graph_templates.go`, `bus/graph_run.go`, `bus/graph_exec.go`, `bus/graph_port.go`, `cmd/graph.go` |
| Providers | `bus/provider.go` (interface, `ResolveProvider`), `bus/provider_claude.go`, `bus/provider_opencode.go`, `bus/provider_codex.go`, `bus/codex_events.go` |
| Launch & reload | `bus/launch.go`, `bus/launcher.go` (default windows), `bus/reload.go`, `bus/reload_batch.go`, `bus/override.go`, `bus/mode.go` |
| Panes & tmux | `bus/pane.go` (identity resolution), `bus/tmux.go`, `bus/control_pane.go`, `bus/resize.go`, `bus/deliver.go` |
| Health & diagnosis | `bus/agent_health.go`, `bus/daemon_health.go`, `bus/diagnose.go`, `bus/stuck.go`, `bus/guard.go`, `bus/upgrade.go`, `bus/version.go` |
| Memory & search | `bus/search.go` (BM25), `bus/rotation.go`, `bus/context.go`, `bus/compact.go`, `bus/clear.go` |
| Integrations | `bus/atlassian.go`, `bus/api.go`, `bus/webhook.go`, `bus/cron.go`, `bus/proc.go`, `bus/spawn.go`, `bus/scrub.go` (PII) |
| Daemon | `daemon/daemon.go` — poll loop, watchdogs, `checkGraphRuns`, `checkAutoClear`, `checkTrackedTasks` |
| TUI | `tui/` — dashboard, `provider_select.go`, `graph_ui.go`, `remote.go`, `styles.go` (palette, `Pad`, `StripAnsi`) |
| CLI | `cmd/` — one handler per subcommand |

Exports change often; grep the file rather than trusting a list here.

### Go LLM harness (`tools/muxcode-llm-harness/`)

Build: `cd tools/muxcode-llm-harness && go build .`
Test: `cd tools/muxcode-llm-harness && go test ./...`

A standalone module mirroring the bus agent loop for local models: `config.go`, `ollama.go`, `bus.go` (inbox/send/lock), `tools.go`, `executor.go` (bash/read/glob/grep/write/edit + `ScrubPII`), `filter.go` (repeat/self-send guards), `prompt.go`, `loop.go` (`Run`, circuit breaker, single-shot auto-complete), `events.go`, `tui.go`, `scrub.go`, `message.go`.

### Bash scripts

- Hooks consume JSON from stdin via `cat` — parse with `jq` or `python3`
- Preview hook detects edit window via `tmux display-message -p '#W'` — exits immediately if not `edit`
- Analyze hook writes trigger file at `/tmp/muxcode-analyze-{session}.trigger` — format: `<timestamp> <filepath>` per line
- Integration scripts assert their binary precondition through `scripts/lib/muxcode-version.sh` — `require_muxcode_version "$MUX" vX.Y.Z MUX-NNN` wraps `muxcode version --at-least`: an older binary fails (exit 1), while an untagged dev build has no semver rank (exit 2) and continues with a note, so the tree-built binary developers run between tags stays testable. A pre-MUX-138 binary routes `version` to the launcher as a project path and exits 1 — read as "older", correctly

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
- [TUI Style Guide](docs/tui-style.md) — palette, glyph/key vocabulary, layout anatomy, and the structural rules (pure renderers, clamp-to-pane, negative controls, explicit empty states) with the incidents that produced them
- [Backlog](docs/requirements/backlog/backlog.md) — planned features
