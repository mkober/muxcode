# Codex CLI compatibility

Enable swapping Claude Code with [Codex CLI](https://github.com/openai/codex) (OpenAI's open-source coding agent) on a per-agent basis. Each tmux window/tab runs one agent, and each agent can independently use Claude Code, OpenCode, or Codex CLI as its AI CLI. A single muxcode session can mix all three providers. Provider assignment is per-agent at launch time via environment variables (`MUXCODE_{ROLE}_CLI=codex`). This extends the existing Provider interface with a new `CodexProvider` implementation.

**Status**: Phases 0–4 complete. Codex runs in TUI mode (`--no-alt-screen`) with `tmux send-keys` interaction — the same pattern as OpenCode, not the exec-mode wrapper originally proposed. Default model: **gpt-5.3-codex**. Recommended roles: review and analyze (reasoning-heavy, read-only).

## Context

### What is Codex CLI

Codex CLI is OpenAI's open-source terminal-based coding agent (GitHub: openai/codex, MIT licensed). Originally built in TypeScript/Node.js, it has been rewritten in **Rust** with an Ink-based TUI. It connects to OpenAI models (gpt-5.3-codex default, plus o4-mini, o3, gpt-4.1, gpt-5.4, etc.) to perform coding tasks in the terminal.

Key capabilities relevant to muxcode integration:

| Feature | Codex CLI | Claude Code | OpenCode |
|---------|-----------|-------------|----------|
| Runtime | Rust binary + Ink TUI | Node.js | TypeScript (Bun) |
| Interface | Full alt-screen TUI + `exec` mode | Line-based CLI with inline rendering | Full TUI |
| Providers | OpenAI models (+ OpenAI-compatible via config) | Anthropic only | Multi-provider |
| Non-interactive | `codex exec "prompt"` | `claude -p "prompt"` | `opencode run "prompt"` |
| Approval modes | `--ask-for-approval never` / `--full-auto` / `--yolo` | `--dangerously-skip-permissions` | Per-agent `permission` block |
| Custom instructions | `AGENTS.md` (project + user) | `.claude/agents/` markdown | `.opencode/agents/` markdown |
| System prompts | `AGENTS.md` file hierarchy (no CLI flag) | `--append-system-prompt` flag | Per-agent `prompt` field |
| Hooks | Experimental (`hooks.json`, requires feature flag) | Full PreToolUse/PostToolUse system | None |
| Sandbox | macOS Seatbelt, Linux Bubblewrap/Landlock | settings.json allow/deny | Per-agent permission block |
| Compact | Not applicable (`exec` mode is stateless) | `/compact` slash command | Auto-compact at 95% |
| Memory file | `AGENTS.md` | `CLAUDE.md` | `AGENTS.md` |
| Session persistence | Interactive TUI only, session logs in `~/.codex/sessions/` | Full conversation context | `--session`, `--continue` flags |
| Install | `npm install -g @openai/codex` or Homebrew | `npm install -g @anthropic-ai/claude-code` | `npm install -g @opencode-ai/opencode` |
| Config format | TOML (`~/.codex/config.toml`, `.codex/config.toml`) | JSON (`settings.json`) | JSON (`opencode.json`) |
| Output modes | stdout (plain text) or `--json` (JSONL events) | Inline rendering | TUI only |

### Why add Codex CLI support

1. **OpenAI model access** — Codex CLI natively supports OpenAI's reasoning models (o4-mini, o3, codex-mini) which have different strengths for certain coding tasks. The review agent benefits from reasoning model capabilities for code analysis.
2. **Cost optimization** — Mix cheaper models for simple roles (build, test) while using Claude for complex roles (edit). OpenAI's gpt-5.4 is optimized for coding tasks at lower cost.
3. **Provider diversity** — Reduce single-vendor dependency. If one provider has an outage, agents on other providers keep working.
4. **Clean exec mode** — `codex exec` outputs the final assistant message to stdout and progress to stderr, making response capture trivial compared to TUI-based providers.

### Current provider landscape

| Provider | Status | Hook support | Idle detection | Integration pattern |
|----------|--------|-------------|----------------|-------------------|
| Claude Code | Production | Full hooks | `❯` prompt | Persistent agent, hook-driven chains |
| OpenCode | Production | None | None (TUI) | Persistent TUI, best-effort notifications |
| Local LLM | Production | None | None | Harness-managed, run-to-completion batches |
| **Codex CLI** | **Production** | **None** | **Heuristic (TUI prompt)** | **TUI mode (`--no-alt-screen`), `tmux send-keys` interaction** |

### Key challenges (and resolutions)

1. ~~**No persistent agent**~~ — **Resolved**: TUI mode (`--no-alt-screen`) runs a persistent Codex process. Multi-turn context is maintained within the TUI session. The originally proposed exec-mode wrapper was not needed.
2. **No hook system** — Codex has experimental `hooks.json` behind a feature flag, but it's not stable. Build/test/review chains use graceful degradation: `chainInstructionForRole()` appends manual `muxcode send` commands to the injected prompt. `CheckSendPolicy()` bypasses deny rules for non-hook providers.
3. ~~**Sandbox blocks /tmp on macOS**~~ — **Resolved**: TUI mode doesn't use the sandbox flag directly. `--full-auto` (which implies `sandbox=workspace-write`) is used for command-execution roles. Read-only roles omit `--full-auto` so Codex prompts before writes.
4. **No --instructions flag** — `WriteAgentConfig()` generates `.codex/AGENTS.md` with bus protocol and role context. Task-specific instructions are injected via `SendWakeUp()` prompt.
5. ~~**Alt-screen TUI**~~ — **Resolved**: `--no-alt-screen` flag disables alt-screen, preserving scrollback for `tmux capture-pane` and enabling `tmux send-keys` interaction.
6. **OpenAI API key required** — `install.sh` validates auth: `OPENAI_API_KEY`, subscription login (`codex login`), `.codexrc`, or `auth.json`.

### Opportunities

1. **Clean stdout/stderr separation** — `codex exec` writes progress to stderr and the final answer to stdout. Capturing the response is `output=$(codex exec "..." 2>/dev/null)` or redirect stderr to a log file.
2. **JSONL event stream** — `codex exec --json` outputs structured events (thread.started, item.completed, turn.completed). Enables rich task-completion detection without pane scraping.
3. **Full-auto shortcut** — `--full-auto` sets approval=never + sandbox=workspace-write in one flag. `--yolo` disables both sandbox and approvals entirely.
4. **Session resume** — `codex exec resume --last` can continue a previous session. Could enable multi-turn reviews if needed.
5. **Named profiles** — `config.toml` supports `[profiles.review]` with role-specific model/sandbox/approval settings. `codex --profile review exec "..."`.
6. **Structured output** — `--output-schema schema.json` validates the response against a JSON schema. Could enforce structured review output format.
7. **Additional directory access** — `--add-dir /tmp/muxcode-bus-*` grants write access to specific directories without fully disabling the sandbox.

## Design

### Integration mode: TUI (`--no-alt-screen`)

Codex runs as a persistent TUI process in each agent's tmux pane, using `--no-alt-screen` to preserve scrollback for `tmux capture-pane` inspection. This is the same interaction pattern as OpenCode — tasks are injected via `tmux send-keys` and completion is detected heuristically from pane content.

**Why TUI mode instead of exec mode?**
- `--no-alt-screen` preserves scrollback, enabling pane capture for health checks and task detection
- Persistent process avoids cold-start overhead per task
- `tmux send-keys` works reliably for text injection into the Codex input prompt
- Matches the established OpenCode integration pattern, reducing maintenance burden
- `exec` mode is still available programmatically via `RunCodexExec()` in `codex_events.go`

**Command template:**

```bash
codex \
  --full-auto \        # omitted for read-only roles (review, analyze)
  --no-alt-screen \
  -m "${model}"
```

**Read-only role handling:** `isReadOnlyCodexRole()` returns true for review and analyze roles. These agents omit `--full-auto` to enforce Codex's permission prompts as a guardrail against unintended writes.

### Review agent integration (Phase 0)

The review agent is the ideal first target because:
- **Read-only workload** — reviews don't modify files, reducing sandbox risk
- **Self-contained tasks** — each review is a single request/response cycle
- **No chain origination** — the review agent is a chain terminus (receives from test, doesn't trigger next steps)
- **Measurable quality** — review output can be compared side-by-side between Claude Code and Codex

**Review agent flow with Codex:**

```
edit sends "review latest changes" → bus → review inbox
  ↓
daemon detects idle agent with pending inbox (checkIdleAgents)
  ↓
daemon calls Notify() → CodexProvider.SendWakeUp()
  ↓
SendWakeUp:
  1. Peek inbox (non-destructive read)
  2. Filter out self-addressed messages (prevents loops)
  3. Combine pending messages into single prompt
  4. Append reply instruction + chainInstructionForRole() suffix
  5. Inject via tmux send-keys (text + Enter)
  6. Consume inbox on success
  ↓
Codex TUI processes the prompt, runs git diff, reads files
  ↓
Codex sends reply via muxcode send (instructed in prompt)
  ↓
DetectTaskCompletion detects bus reply ("Sent") or TUI prompt reappear
  ↓
edit receives review summary
```

**Key design choices:**

1. **Let Codex run its own commands** — Codex runs `git diff`, reads files via its bash tool, same as Claude Code. The injected prompt just describes the task; Codex figures out the commands.
2. **Self-message filtering** — `SendWakeUp()` filters out messages where `From` matches the agent's role, preventing infinite reply loops where the agent wakes itself.
3. **Chain instructions** — `chainInstructionForRole()` appends manual chaining commands (e.g., build→test, test→review) since Codex has no hook system.

### Provider interface implementation

File: `bus/provider_codex.go`

```go
type CodexProvider struct{}

func (p *CodexProvider) Name() string { return "codex" }

// ConfigureLaunch resolves agent file and shared prompt via BuildSharedPrompt(role).
// Model resolved from MUXCODE_{ROLE}_CODEX_MODEL or MUXCODE_CODEX_MODEL, default gpt-5.3-codex.
func (p *CodexProvider) ConfigureLaunch(cfg *LaunchConfig, role string)

// BuildExecArgs returns ["codex", "--no-alt-screen"] plus model flag.
// Read-only roles (review, analyze) omit --full-auto.
// No -C flag — preserves repo root as working directory.
func (p *CodexProvider) BuildExecArgs(cfg *LaunchConfig) (string, []string)

// IsIdle always returns false — TUI mode has no stable idle prompt character.
// Task completion is detected via DetectTaskCompletion instead.
func (p *CodexProvider) IsIdle(session, role string) bool

// IsAlive captures pane and checks for Codex TUI indicators
// (box-drawing characters, "codex" text, prompt symbols ›/>).
func (p *CodexProvider) IsAlive(session, role string) bool

// ClassifyPane returns PaneIdle if TUI renders (box-drawing or "codex" text),
// PaneNotReady on error markers, PaneUnknown otherwise.
func (p *CodexProvider) ClassifyPane(content string) PaneState

// AcceptStartup returns true when state == PaneIdle (TUI has rendered).
func (p *CodexProvider) AcceptStartup(session, pane string, state PaneState) bool

// SendWakeUp peeks inbox (non-destructive), filters self-addressed messages,
// combines pending messages with reply instruction + chainInstructionForRole(),
// injects via tmux send-keys, then consumes inbox on success.
func (p *CodexProvider) SendWakeUp(session, role string) error

// Compact is a no-op — TUI manages its own context.
func (p *CodexProvider) Compact(session, role, target string) error

// SupportsHooks returns false — uses graceful degradation via prompt instructions.
func (p *CodexProvider) SupportsHooks() bool { return false }

// IdlePromptChar returns "›" (Codex TUI prompt symbol).
func (p *CodexProvider) IdlePromptChar() string

// WriteAgentConfig delegates to writeCodexAgentConfig(role) which writes
// .codex/AGENTS.md with bus protocol + role-specific instructions.
func (p *CodexProvider) WriteAgentConfig(role string) error

// DetectTaskCompletion uses heuristic detection:
// 1. Active spinner (⠋⠙...) → still running
// 2. Bus reply output ("Sent") in last 10 lines → completed
// 3. TUI prompt (›/>) in last 3 lines → completed, preceding line as summary
func (p *CodexProvider) DetectTaskCompletion(session, role, content string) (bool, bool, string)
```

Helper: `isReadOnlyCodexRole(role)` returns true for review and analyze — these omit `--full-auto` from launch args.

### Agent launch (no wrapper script)

Unlike the originally proposed wrapper script, Codex agents launch directly as a TUI process — no bash wrapper needed. `BuildExecArgs()` returns the `codex` command with flags:

```bash
# Standard role (build, test, deploy, etc.)
codex --full-auto --no-alt-screen -m gpt-5.3-codex

# Read-only role (review, analyze) — omits --full-auto
codex --no-alt-screen -m gpt-5.3-codex
```

The daemon's `checkIdleAgents()` handles inbox polling and `SendWakeUp()` handles task injection — the same lifecycle as OpenCode agents. No dedicated wrapper script is needed because the TUI process is persistent and receives tasks via `tmux send-keys`.

### AGENTS.md generation

Unlike Claude Code (which reads `.claude/agents/<role>.md`) or OpenCode (`.opencode/agents/<role>.md`), Codex reads `AGENTS.md` from the project root downward.

**Implementation**: Single shared `.codex/AGENTS.md` at repo root, written by `writeCodexAgentConfig(role)`. When multiple Codex roles launch concurrently, the last writer wins — a known tradeoff acknowledged in tests (`TestLastWriterWinsCollision`). The content includes bus protocol instructions, role-specific task description from the agent definition, and `adaptBodyForNonHookProvider()` rewrites that replace hook-chain references with manual `muxcode send` commands.

No `-C` flag is used — Codex runs in the repo root directory so it can access source files directly.

```markdown
# MuxCode Agent Instructions

You are an agent in a multi-agent coding environment coordinated via a message bus.

## Bus Commands

- Send messages: `muxcode send <target> <action> "<message>"`
- Read memory: `muxcode memory context`
- Log results: `muxcode log <role> "<summary>" --exit-code <0|1>`

## Targets

- `edit` — orchestrator, code editor
- `build` — build runner
- `test` — test runner
- `review` — code reviewer (you, if review role)
- `commit` — git operations

## Rules

- Process the task immediately, do not ask for confirmation
- Reply to the requesting agent when done
- Do not run commands outside your role's scope
```

### Provider resolution

Extend `ResolveProvider()` in `bus/provider.go`:

```go
func ResolveProvider(role string) Provider {
    cli := ResolveProviderCLI(role)
    switch cli {
    case "opencode":
        return &OpenCodeProvider{}
    case "codex":
        return &CodexProvider{}
    case "local":
        return &LocalProvider{}
    default:
        return &ClaudeCodeProvider{}
    }
}
```

### Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODE_{ROLE}_CLI=codex` | — | Use Codex CLI for a specific role |
| `MUXCODE_AGENT_CLI=codex` | — | Use Codex CLI for all agents |
| `MUXCODE_{ROLE}_CODEX_MODEL` | `gpt-5.3-codex` | Model for a specific Codex role |
| `MUXCODE_CODEX_MODEL` | `gpt-5.3-codex` | Default model for all Codex agents |
| `OPENAI_API_KEY` | — | Optional if logged in via `codex login` subscription |

Config file (`.muxcode/config` or `~/.config/muxcode/config`):

```bash
# Codex CLI settings — recommended for reasoning-heavy roles
MUXCODE_REVIEW_CLI=codex
MUXCODE_ANALYZE_CLI=codex
MUXCODE_CODEX_MODEL=gpt-5.3-codex
# Auth: use subscription login (codex login) or API key:
# OPENAI_API_KEY=sk-...
```

### Codex CLI flags reference

Flags used in the integration (✦ = used in TUI mode, ✧ = exec mode only):

| Flag | Short | Values | Usage |
|------|-------|--------|-------|
| `--no-alt-screen` | — | boolean | ✦ Disable alt-screen, preserve scrollback for tmux capture |
| `--full-auto` | — | boolean | ✦ Sets approval=never + sandbox=workspace-write (omitted for read-only roles) |
| `--model` | `-m` | string | ✦ Model override (e.g. `gpt-5.3-codex`, `o4-mini`) |
| `exec` | `e` | subcommand | ✧ Non-interactive run-to-completion mode (used by `RunCodexExec()`) |
| `--json` | — | boolean | ✧ JSONL event stream to stdout (used with exec mode) |
| `--sandbox` | `-s` | `read-only`, `workspace-write`, `danger-full-access` | ✧ Filesystem access level (exec mode) |
| `--add-dir` | — | path | ✧ Grant additional directory write access (exec mode) |
| `--cd` | `-C` | path | ✧ Set working directory (not used — agents run from repo root) |
| `-o` | — | path | ✧ Write final message to file |
| `--skip-git-repo-check` | — | boolean | Allow running outside a git repo |
| `--yolo` | — | boolean | Disable both sandbox and approvals |
| `--profile` | `-p` | string | Load named config profile |
| `--config` | `-c` | key=value | Inline config override |

### Graceful degradation

Same three-layer strategy as OpenCode:

| Feature | Claude Code | Codex CLI | Fallback |
|---------|-------------|-----------|----------|
| Build/test/review chain | Hook-driven | None | `AGENTS.md` instructions: "after build succeeds, run `muxcode send test test ...`" |
| Edit guard | PreToolUse hook blocks | None | `AGENTS.md` instructions: "do NOT edit source files" (soft enforcement) |
| File-change routing | Analyze hook | None | Not available — edit agent manually triggers review |
| Idle notifications | send-keys at `❯` | None | `SendWakeUp()` injects task via `tmux send-keys` into TUI |
| Compact | `/compact` injection | None | TUI manages own context — Compact() is a no-op |
| Workflow state | Hook transitions | None | `DetectTaskCompletion()` heuristic from pane content |

### Sandbox considerations

Codex CLI sandbox behavior by platform:

| Platform | Engine | Network | Notes |
|----------|--------|---------|-------|
| macOS | Seatbelt (`sandbox-exec`) | Always disabled | Cannot enable network even with config |
| Linux | Bubblewrap / Landlock+seccomp | Configurable | `network_access = true` in config.toml |

MuxCode agents need access to:
- `/tmp/muxcode-bus-{session}/` — bus files (inbox, triggers, locks)
- `~/.config/muxcode/` — memory, skills, config
- Project directory — source code (read for review, write for build/edit)
- `~/.codex/` — Codex config and session logs

**Resolution**: In TUI mode, sandbox behavior is controlled by `--full-auto` (which implies `sandbox=workspace-write`). Read-only roles (review, analyze) omit `--full-auto`, so Codex prompts before writes — providing natural protection. MuxCode's own tool profiles and prompt-based restrictions are the primary guardrails. The `--sandbox` and `--add-dir` flags from exec mode are not used in TUI mode.

### Task completion detection

Detection uses heuristic pane content analysis to avoid false positives from intermediate Codex output (tokens like "Done", "completed", "Applied", "✓" can appear mid-task and must not trigger premature completion).

**Three-step detection in `DetectTaskCompletion()`:**

1. **Active spinner check** — Scans for braille spinner characters (⠋, ⠙, ⠹, etc.) or "thinking" indicators. If detected, the agent is still running — returns not completed.

2. **Bus reply detection** — Scans last 10 lines for "Sent" (output from `muxcode send`). If found, the agent has replied via the bus — returns completed with error flag based on surrounding context.

3. **TUI prompt detection** — Checks last 3 lines for the Codex input prompt (`›` or `>`). If found, the preceding non-empty line is extracted as the task summary.

All detection is from `tmux capture-pane` content in `--no-alt-screen` mode — no wrapper markers needed.

### JSONL event stream (future enhancement)

`codex exec --json` outputs structured JSONL events:

```jsonl
{"type":"thread.started","thread_id":"...","model":"gpt-5.4"}
{"type":"turn.started"}
{"type":"item.completed","item":{"type":"message","content":"Review complete..."}}
{"type":"turn.completed"}
```

This could be parsed for richer task tracking (progress updates, token usage, tool calls). Deferred to Phase 3 — plain text stdout is sufficient for initial integration.

## Implementation phases

### Phase 0: Manual validation ✅

Manually tested `codex exec` and interactive TUI mode. Validated that `--no-alt-screen` preserves scrollback for `tmux capture-pane`. Discovered that TUI mode is more practical than exec mode for persistent agents — led to the design pivot from exec-mode wrapper to TUI-mode integration.

### Phase 1: Provider implementation ✅

`CodexProvider` struct with all Provider interface methods, wired into `ResolveProvider()`.

**Key files**:

| File | Change |
|------|--------|
| `bus/provider.go` | `"codex"` case in `ResolveProvider()`, `chainInstructionForRole()` shared helper |
| `bus/provider_codex.go` | Full `CodexProvider` — TUI mode, `SendWakeUp` with self-message filtering, `DetectTaskCompletion`, `isReadOnlyCodexRole()` |
| `bus/provider_codex_test.go` | Interface conformance, resolution, BuildExecArgs, ClassifyPane, DetectTaskCompletion, model resolution, WriteAgentConfig, coexistence, last-writer-wins |
| `bus/launch.go` | `RoleCodexModelEnvVar()`, `RoleCodexModelDefault()` (gpt-5.3-codex), `resolveCodexModel()` |

### Phase 2: Review agent end-to-end ✅

Review and analyze agents working with Codex in live muxcode sessions.

**Verified**:
1. `MUXCODE_REVIEW_CLI=codex` in config launches Codex TUI in review window
2. `muxcode send review review "Review latest changes"` triggers review via send-keys
3. Review results delivered to edit inbox via bus
4. `muxcode inspect` shows provider=codex for review agent
5. Daemon detects dead Codex TUI and restarts via `StartAgent()`

### Phase 3: JSONL parsing and structured output ✅

JSONL event parsing infrastructure for `codex exec --json`. Adapted from original exec-mode design to support both TUI mode (primary) and programmatic exec-mode via `RunCodexExec()`.

**Key files**:

| File | Change |
|------|--------|
| `bus/codex_events.go` | Event types, JSONL parser, `AnalyzeCodexEvents()`, `FormatCodexResult()`, `RunCodexExec()` |
| `bus/codex_events_test.go` | 30+ tests: parsing, analysis, formatting, end-to-end with real JSONL |
| `bus/provider_codex.go` | Rewritten for TUI mode (`codex --full-auto --no-alt-screen`) |

**Success criteria**:
1. ✅ JSONL events parsed from `codex exec --json` output (7 event types)
2. ✅ `AnalyzeCodexEvents()` extracts messages, commands, errors, token usage
3. ✅ `FormatCodexResult()` produces concise bus-friendly summaries
4. ✅ `RunCodexExec()` programmatic entry point for non-interactive use
5. ✅ Error events detected: `turn.failed`, `error`, non-zero exit codes

### Phase 4: Multi-role expansion ✅

All command-execution roles support Codex. Chains work end-to-end via prompt-instructed `muxcode send` commands.

**Key implementation details**:
- `adaptBodyForNonHookProvider()` shared between Codex and OpenCode (defined in `provider_opencode.go`)
- `CheckSendPolicy()` bypasses deny rules for non-hook providers (`SupportsHooks() == false`)
- `chainInstructionForRole()` in `provider.go` appends manual chain commands to SendWakeUp prompts
- Default `ResolveProviderCLI()` routes build/test/deploy/run/watch/commit to OpenCode; review/analyze recommended for Codex

**Verified**:
1. Build→test→review chain completes via bus messages with mixed providers
2. Mixed session: Claude Code edit + OpenCode build/test + Codex review works
3. `isReadOnlyCodexRole()` omits `--full-auto` for review and analyze

### Phase 5: Mixed-provider testing and hardening ✅

**Verified**:
1. Session with Claude Code edit + OpenCode build/test + Codex review runs reliably
2. `muxcode inspect` shows correct provider for each agent
3. Agent health monitoring detects and restarts crashed Codex TUI
4. `install.sh` validates Codex availability and offers installation (npm or Homebrew)
5. Auth check: detects `OPENAI_API_KEY`, subscription login, `.codexrc`, or auth.json

### Phase 6: ~~Interactive TUI mode~~ (superseded)

TUI mode (`--no-alt-screen`) became the primary integration mode in Phase 1, replacing the originally proposed exec-mode wrapper. All Codex agents now run as persistent TUI processes. This phase is complete — it was absorbed into the main implementation.

### Phase 7 (deferred): Session resume

Use `codex exec resume --last` to continue previous sessions for multi-turn workflows. Could enable persistent review context across multiple review requests. Deferred until basic integration is stable.

## Resolved questions

### Q1: `exec` mode vs interactive TUI?

**Decision**: TUI mode (`--no-alt-screen`) for all roles. Exec mode available programmatically via `RunCodexExec()`.

**Rationale**: `--no-alt-screen` preserves scrollback for `tmux capture-pane`, enabling health checks and task completion detection. The persistent TUI avoids cold-start overhead per task. `tmux send-keys` works reliably for injecting prompts into the Codex input field. This matches the established OpenCode integration pattern, reducing maintenance burden. The original concern about alt-screen breaking send-keys was solved by `--no-alt-screen`.

### Q2: Wrapper script vs Go harness vs direct TUI?

**Decision**: Direct TUI process (no wrapper, no harness).

**Rationale**: Codex CLI is a complete agent with its own tool execution. The Go harness is for Ollama's raw chat completion API. A bash wrapper was originally proposed but proved unnecessary — the daemon's `checkIdleAgents()` + `SendWakeUp()` handles inbox polling and task injection for the persistent TUI, the same pattern used for OpenCode.

### Q3: How to provide role-specific instructions?

**Decision**: Shared `.codex/AGENTS.md` for bus protocol + role-specific instructions injected via `SendWakeUp()` prompt.

**Rationale**: Codex reads `AGENTS.md` from project root — a single file, not per-role like Claude Code or OpenCode. `WriteAgentConfig()` writes `.codex/AGENTS.md` with bus protocol and role context. Role-specific behavior comes from `SendWakeUp()` which combines the task message, reply instruction, and `chainInstructionForRole()` suffix. When multiple Codex roles run concurrently, the last `WriteAgentConfig()` call wins — an acceptable tradeoff since bus protocol is common to all roles and task-specific instructions come from the injected prompt.

### Q4: Which sandbox mode?

**Decision**: No sandbox flag in TUI mode. Read-only roles omit `--full-auto` instead.

**Rationale**: In TUI mode (not exec mode), the sandbox flag is less relevant — `--full-auto` controls whether Codex auto-approves tool execution. For read-only roles (review, analyze), `isReadOnlyCodexRole()` omits `--full-auto`, meaning Codex prompts for approval before writes — a natural guardrail. For command-execution roles (build, test), `--full-auto` is included since they need to run builds/tests autonomously.

### Q5: Should Codex be available for the edit role?

**Decision**: Not supported for initial release.

**Rationale**: The edit agent requires persistent conversation, complex tool orchestration, hook support for the guard system, and `/compact` for context management. `codex exec` is stateless. The interactive TUI mode could theoretically work but loses the edit guard — a significant security regression. The edit role stays Claude Code only.

### Q6: What about Codex's experimental hooks.json?

**Decision**: Ignore for now, revisit when stable.

**Rationale**: Codex has `hooks.json` behind `features.codex_hooks = true`, but it's undocumented and experimental. If it stabilizes and provides PreToolUse/PostToolUse equivalents, it could enable hook-driven chains like Claude Code. For now, treat Codex as a non-hook provider with the same graceful degradation as OpenCode.

### Q7: Plain text vs JSONL output?

**Decision**: TUI mode for agents (pane content heuristics), JSONL parsing available for programmatic use.

**Rationale**: Agents run in TUI mode where task completion is detected via pane content heuristics (`DetectTaskCompletion`). JSONL event parsing (`codex_events.go`) is implemented for `RunCodexExec()` — a programmatic entry point for non-interactive exec-mode use. Both paths coexist: TUI for persistent agents, exec+JSONL for one-shot programmatic invocations.

## Status

Complete
