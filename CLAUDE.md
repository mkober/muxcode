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
| `bash scripts/test-*.sh` | Integration tests, one per feature. Each is hermetic unless noted and carries a coverage floor so a skipped section cannot report green; read the script's header for what it covers. Live-session required: `test-diff-split`, `test-hot-reload`, `test-resize-hook`. Feature tests: `test-echo-as-result`, `test-lifecycle-log-leak`, `test-branch-time-recording`, `test-disk-pressure`, `test-auto-clear`, `test-graph-orchestrator`, `test-control-pane`, `test-force-respond`, `test-send-keys-dash`, `test-prompt-mode`, `test-close-spec-guard`, `test-multi-phase-graph`, `test-graph-tui`, `test-pane-targeting`, `test-fkey-labels`, `test-verify-spec-refire`, `test-restart-definition`, `test-version`, `test-codex-hooks` (its live section runs a real codex only with `MUXCODE_CODEX_HOOKS_LIVE=1`). Most need an installed muxcode >= v0.1.0 |

Both Go modules have **no external dependencies** (stdlib only).

## Code conventions

### Go (bus binary & harness)

- PascalCase for exported identifiers, camelCase for unexported
- Stdlib only — no third-party imports
- Tests in `*_test.go` files, same package (not `_test` suffix)
- Bus directory path: `/tmp/muxcode-bus-{session}/` in `bus/config.go`
- **No test may bind a socket** — use `newPipeServer` (both modules), never `httptest.NewServer`. A sandboxed agent cannot listen, so a socket-bound test panics before any assertion and `set -e` then hides every later module. Production code takes an optional `*http.Client` (nil in production) as the seam

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
- Each backlog spec carries a stable id from the backlog index, used in its filename, branch (`<id>-<slug>`), issue and PR titles
- **Integration test phase required**: every requirements doc MUST end with an integration test phase containing either automatable checkbox steps (`- [ ] Reload build+test agents → verify config changed`) or a step to create `scripts/test-{feature}.sh`. It validates end-to-end behaviour, not just units.

### Permissions (`.claude/settings.local.json`)

- **No hardcoded user paths**: this is open source — never add absolute paths carrying a username or home directory. Use relative (`tools/muxcode/...`) or generic (`~/.config/muxcode/...`) forms.
- User-specific `Read()` permissions for external dirs (nvim plugins, dotfiles, etc.) belong in the user's global `~/.claude/settings.json`, not in the project-local file.

## Key constraints

- **Edit agent delegation**: never runs build, test, deploy, dev servers, API requests, log tailing, AWS commands, git (even `git status`) or `gh` — everything goes through the bus, enforced by the `muxcode hook guard` PreToolUse hook on Claude and by `DenyTools` plus the daemon pane audit on OpenCode. Routing: **serve** dev servers; **run** AWS execution and ad-hoc scripts (`scripts/test-*.sh`); **watch** log tailing only; **api** API testing (`prefix + i`); **commit** with action `pr-read` for PR reads (never review); **plan** with `update-docs` for all docs and for Jira/Confluence — `CheckDocFileGuard` blocks edit from `docs/**/*.md` (root `CLAUDE.md`/`README.md` stay editable). `MUXCODE_EDIT_CLI=opencode|claude` switches edit's provider. See [Architecture](docs/architecture.md).
- **Atlassian authority**: reads are open to every role; writes are gated to plan alone (`CheckAtlassianAuthority` at the CLI, `CheckAtlassianMCPGuard` on MCP). Plan writes only on an explicit user request relayed from edit, never as a side effect of a docs change — edit is the consent boundary, plan the write boundary. `MUXCODE_ATLASSIAN_AUTHORITY_ROLES` overrides the list; empty denies every role.
- **Plan agent scope**: plan (F1) writes only `docs/`, `CLAUDE.md` and `README.md`; it reads source but never writes elsewhere. The hosted `docs` role maps to plan. F1 also hosts **research** via mode cycling — its own inbox, no chain; it web-searches references and delegates implementation to the active F2 agent.
- **Spec verification**: after review the daemon sends plan `verify-spec` for the active spec (`muxcode spec set <path>`); plan ticks criteria and phase steps, updates status, reports to edit, and upserts a `## Time Tracking` row keyed by branch from `muxcode branch-time show` — absolute totals, never-regress (re-seed with `branch-time seed`, never `--add`). Gated by `NotifyPlanOn` on the review chain (default `["success"]`).
- **Hook-driven chains**: build→test→review and deploy→run→watch are deterministic (exit codes), firing only where `provider.SupportsHooks()` — Claude always, Codex on the hook road (on by default; `MUXCODE_CODEX_HOOKS=0` opts out). Other providers degrade to prompt chain instructions (`buildChainInstruction()`), rewritten definition bodies and a `CheckSendPolicy()` bypass. `bus.ChainEvent` names the one chain a call feeds; a **test precheck** (`go vet`, `DefaultTestPrecheckPatterns`, `MUXCODE_TEST_PRECHECK_PATTERNS`) feeds it only on failure — a passing vet writes no row and fires nothing, because on 2026-09-09 a lone passing `go vet` fired test→review before the suite ran and the suite's own success was then dropped by the Reviewing guard. See [Hooks](docs/hooks.md).
- **Hook-road evidence guard**: for build, test and deploy, `hook guard` denies a Bash call that bundles a build/test/deploy statement with anything else — `muxcode send` acks, a `;`/`&&` chain, a pipe (`CheckEvidenceGuard`, `bus/evidence_guard.go`; a leading `cd … &&` or env assignment is fine). The PostToolUse hook classifies a call by its first statement and records the last one's exit code, so a bundled build writes no authoritative row, fires no chain, and a graph node parks on an unverified hold — the 2026-09-09 spec-to-pr build hold, where a codex build agent put three acks, `./build.sh` and a hand-typed result in one call. Both providers, one rule; the agent's own reply still closes the node.
- **Provider capabilities are three questions**: `SupportsHooks()` (chains, guards and history fire from hooks), `SelfPollsInbox()` (a background listener acks — Claude only), `PaneIsEvidence()` (completion and edits must be scraped — OpenCode, scrape-road Codex, local); `IsClaudeTUI()` answers TUI-identity questions (slash commands, `❯`, `--agent` argv). **Codex hook road** (yes/no/no): `PrepareCodexHooks` writes `<repo>/.codex/hooks.json` before launch when opted in (`MUXCODE_CODEX_HOOKS=1`, per-role `MUXCODE_<ROLE>_CODEX_HOOKS`) and eligible (codex ≥ 0.153, `[features] hooks` not off); `--dangerously-bypass-hook-trust` is passed only while the file hashes to muxcode's marker (tampered → launch refused); `hook bash` reads the real exit code from the rollout transcript by `tool_use_id` (the `PostToolUse` payload carries only stdout); delivery rides `hook stop` (pending request → next prompt, `acked` receipt first) and `hook prompt-submit` (wake sentence → `additionalContext`) — a payload is never typed into the pane. Every `muxcode hook` subcommand no-ops without raw `BUS_SESSION`. On by default for an eligible codex; `MUXCODE_CODEX_HOOKS=0` (or the per-role variable) opts out. See [Hooks](docs/hooks.md#codex-hooks).
- **Conditional chains**: `ChainAction.Conditions` (11 types: `files_match`/`_not_match`, `branch_match`/`_not_match`, `command_match`/`_not_match`, `env_set`, `env_equals`, `output_contains`, `exit_code`, `spec_phases_remaining`); a `ChainActions` slice is first-match-wins; `ChainContext` lazy-loads git via `PopulateGitInfo()`. Subscriptions share the mechanism. The run chain's `OnSuccess` is a `command_match` allowlist so watch fires only for verification runs.
- **Provider/model changes are user-approved only**: never `reload --cli/--model` without explicit approval, not even as recovery. A plain same-provider `muxcode reload <role>` is fine, but ask before restarting an agent the user is watching.
- **Hot reload**: `muxcode reload <role>... [--cli] [--model] [--compact]` (overrides in `/tmp/muxcode-bus-{session}/config/{role}.env`); `--all` excludes edit/auto, `--provider <cli>` filters. Reload markers suppress health checks, so a marker left by an interrupted reload makes a healthy agent read `excluded`. Windowless roles are configured, not reloaded. Selector modal: `prefix + R`. `muxcode config set/get/list` persists config; resolution: runtime override → per-role env → global env → config file → default. See [Configuration](docs/configuration.md).
- **Codex CLI sandbox**: `BuildExecArgs` passes `-s workspace-write` plus one `--add-dir` per external root **for the build role only** (`codexWritableRoots`: the Makefile's `PREFIX`/`BINDIR`/`CONFIGDIR`, `~/.claude/commands`, the Go caches from `go env`); other roles inherit the default policy (workspace writable, outside refused). Every root is resolved through `filepath.EvalSymlinks` first — Codex refuses a symlinked root, and the refusal kills the whole sandbox, which reads as a hung model. Network is off by default, so the harness's socket-binding tests cannot pass on a Codex agent.
- **User-initiated commits**: commits, pushes and PRs are never auto-triggered; the chain stops at review. Commit only when asked or when a complete logical unit is finished — no micro-commits.
- **Attribution stripping**: `LaunchSession()` installs an idempotent `commit-msg` hook (`InstallCommitMsgHook()`) that strips `Co-authored-by` trailers; it chains with existing hooks and respects `core.hooksPath`.
- **Pre-commit safeguard**: commit delegation is blocked while any agent has a pending inbox, is busy, or has running procs/spawns; `--force` bypasses.
- **Auto-CC**: messages from build/test/review/deploy to non-edit agents are copied to edit; chain and subscription messages use `SendNoCC()`.
- **Agent notifications**: `Notify()` writes `trigger-{role}.notify` and wakes idle agents via send-keys — Claude gets "You have new messages"; listenerless providers get `provider.SendWakeUp()` (payload injection on the scrape road, the fixed sentence on the codex hook road); harness panes are skipped.
- **Edit inbox**: `--wait` blocks for the response (`MUXCODE_INBOX_POLL_TIMEOUT`, default 600s); `--track` returns at once and the daemon wakes the sender when the response lands. Both record a task under `tasks/` (`muxcode tasks`).
- **Delegation message hygiene**: sends are short, single-line and intent-level — `validatePayload()` warns on newlines or >500 chars, and such a payload misses the `Bash(muxcode *)` glob and prompts instead of delivering. Delegate intent, not pre-baked commit messages or file lists. Prefer `--track`; `--wait` only when the result is needed next. Re-requests must state what changed — an identical re-send reads as a duplicate. Long content goes in `/tmp/<name>.md` with a one-line pointer. Reply IDs are looked up, never reconstructed; a reply whose action does not match your request is suspect. See [Agent Bus CLI](docs/agent-bus.md#muxcode-send).
- **Keep agents deliverable**: the daemon delivers only to idle agents, so never run a blocking command (`gh pr checks --watch`, `tail -f`, interactive watchers) in an agent's own pane — route it to **watch** or a background `muxcode proc`. Recovery: busy → returns to idle on its own; at the prompt but stuck (dropped Enter, stale markers, parked input) → `muxcode deliver <role> [--force]`, never a hand-rolled `send-keys "You have new messages" Enter`; genuinely frozen TUI → kill the OS process (`-TERM`, then `-KILL`) and let the daemon restart it.
- **Agent wake-up**: `checkIdleAgents()` (every 5s) notifies idle agents holding actionable (request-type) messages; `LaunchSession()` wakes agents with pre-populated inboxes at startup.
- **Delivery acknowledgement (default ON)**: per-message receipts replace pane-scrape inference. On consume `bus/inbox.go` writes a receipt: a true `acked` when the agent's own runtime read it (Claude's `muxcode inbox --poll --loop` listener kept alive by the Stop hook, the codex hook road in `hook stop`/`prompt-submit`, the harness in-process), or a weaker `delivered` for verified pane injection. `checkPollHealth()` detects a receipt gap, re-drives delivery and alerts `delivery-gap`. `hasReceipt()` is the single definition: `AckedAt > 0` **or** `Status == responded` — a reply implies receipt (load-bearing: `MarkResponded()` sets no `AckedAt`); `MarkResponded()` also drains the answered row via `ConsumeByID`. Rollback valves: `MUXCODE_DELIVERY_ACK_DISABLE=1` (needs a daemon restart), `MUXCODE_DELIVERY_ACK=off`, `muxcode delivery-ack off` (restart-free). See [Architecture](docs/architecture.md#delivery-tracking).
- **Auto-clear between tasks**: off by default. `MUXCODE_AUTO_CLEAR_ROLES` enrolls episodic Claude roles (`edit`/`auto` are hard-excluded), `MUXCODE_AUTO_CLEAR_QUIET_SECS` (60) sets the quiet window; `checkAutoClear()` issues a guarded `/clear` (idle, no actionable inbox, no live task, no reload marker, Claude only) at most once per task. Manual: `muxcode clear <role>`.
- **Daemon identity**: the daemon's bus identity is `daemon`; `NormalizeBusRole("daemon")` maps to `edit` so reply instructions route somewhere valid, and `daemon` is excluded from loop detection, as `isSystemAction()` excludes `loop-detected`, `compact-recommended`, `proc-`/`spawn-complete`, `ollama-*` and `agent-*` events.
- **Graph orchestration**: `muxcode graph run|validate|list|status|cancel|retry|approve` — a DAG control plane over the bus adding `map` fan-out, `join` barriers (`all`/`any`/`quorum`), `condition` branches (reusing `EvaluateConditions()`), capped loops (uncapped cycles fail validation) and `wait_human` gates. The daemon executes edges as ordinary bus messages, so profiles, receipts, scrub and watchdogs still apply. **Authority gates**: a commit or Atlassian node must sit downstream of a `wait_human` gate; `CheckGateApprovalAuthority` runs in the executor; default authority is the user alone, `MUXCODE_GATE_AUTHORITY_ROLES` is read from the config file only (env ignored) and sealed at daemon startup, so a mid-session edit can narrow but never widen; no agent may approve a gate on a run it created; approval is single-use, so a retry or loop re-entry needs a fresh `graph approve`. Run state lives under `BusDir()/graphs/<run-id>/`, and the first tick after a daemon restart is the resume. Templates resolve `project > user > builtin`. See [Architecture](docs/architecture.md#graph-orchestration-control-plane), [CLI](docs/agent-bus.md#muxcode-graph).
- **Agent diagnostics**: `muxcode diagnose <role>` root-causes an unresponsive agent against 15 failure modes (`--json`, `--all`; non-zero on critical). A version mismatch is a warning, fixed by `muxcode upgrade-daemons`. Daemon-wake checks are gated on `bus.AckDeliveryActive()`; `checkUnexplainedEvidence` runs last so a stuck actionable inbox is never reported clean. Any agent may diagnose a peer.
- **Lifecycle logging**: JSONL at `~/.config/muxcode/logs/{session}.log`; `muxcode lifecycle show [session]` (`--source`/`--level`/`--event`/`--since`); rotates at `MUXCODE_LIFECYCLE_LOG_MAX` (1000); `lifecycle purge --days 30`.
- **PII scrubbing**: `api`, `run` and `watch` output is redacted (emails, SSN, cards, phones, AWS keys, JWTs, secrets) before it enters the conversation; `ScrubPIIWithNotice()` prepends a banner so placeholders are never mistaken for data or measured.
- **`--wait` auto-degrade**: after `MUXCODE_WAIT_DEGRADE_SECS` (90; `0` = block to the poll timeout) the send becomes a tracked task and returns — not a failure; never scrape a pane for the answer.
- **Relay-loop suppression**: `bus.Send` drops identical non-edit request relays once a `(from,to,action)` tuple fires ≥ `MUXCODE_RELAY_SUPPRESS_THRESHOLD` (4) within `MUXCODE_RELAY_SUPPRESS_WINDOW` (300s). Response ping-pong is only reported (`DetectMessageLoop`).
- **Daemon watchdogs** (each opt-out by env, each logging lifecycle events): long-active (`MUXCODE_ACTIVE_WATCHDOG_SECS`, 600); stuck-provider reload (`MUXCODE_STUCK_RELOAD_DISABLE=1`; cap 3/role); in-flight task expiry (`task-timeout`, 600s — expired tasks no longer block the dedup guard); permission-block (`MUXCODE_PERMBLOCK_WATCHDOG_DISABLE=1`; alert-only, stops re-waking a Claude agent wedged at a rejected permission prompt). Code: `bus/stuck.go`, `daemon/daemon.go`.
- **Daemon self-monitoring**: `daemon.keepalive` is stamped each poll; `muxcode watch --monitor` relaunches a daemon stale for >30s.
- **Local LLM harness** (`tools/muxcode-llm-harness/`): three-layer circuit breaker (turn filter, `MaxAllBlockedTurns=2`, `MaxConsecutiveFailures=3` → 30s cooldown; 5-minute batch timeout); build and test are single-shot roles; definitions resolve `agents/harness/` > `.claude/agents/` > `~/.config/muxcode/agents/harness/` > `~/.config/muxcode/agents/`. See [Agents](docs/agents.md#circuit-breaker).

## Code reference

### Go bus binary (`tools/muxcode/`)

Build: `cd tools/muxcode && go build .`
Test: `cd tools/muxcode && go test ./...`

| Area | Files |
|------|-------|
| Paths, roles, actors | `bus/config.go` (`BusDir`, `PaneTarget`, `NormalizeBusRole`, `WindowForRole`, `BusActorVerified`, active-spec helpers) |
| Messaging | `bus/message.go`, `bus/inbox.go` (`Send`, `SendNoCC`), `bus/dedup.go`, `bus/delivery.go` (receipts), `bus/task.go`, `bus/subscribe.go` |
| Authority | `bus/commit_authority.go`, `bus/atlassian_authority.go`, `bus/gate_authority.go` |
| Chains & conditions | `bus/profile.go` (`ToolProfile`, `EventChain`, `ResolveChain`), `bus/conditions.go` (11 condition types), `bus/hook.go`, `bus/evidence_guard.go` (`CheckEvidenceGuard`, `parseShellStatements`) |
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
- Integration scripts assert their binary precondition through `scripts/lib/muxcode-version.sh` — `require_muxcode_version "$MUX" vX.Y.Z <spec-id>` wraps `muxcode version --at-least`: an older binary fails (exit 1), while an untagged dev build has no semver rank (exit 2) and continues with a note, so the tree-built binary developers run between tags stays testable. A binary predating `muxcode version` routes it to the launcher as a project path and exits 1 — read as "older", correctly

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
