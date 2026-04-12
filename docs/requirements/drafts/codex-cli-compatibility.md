# Codex CLI compatibility

Enable swapping Claude Code with [Codex CLI](https://github.com/openai/codex) (OpenAI's open-source coding agent) on a per-agent basis. Each tmux window/tab runs one agent, and each agent can independently use Claude Code, OpenCode, or Codex CLI as its AI CLI. A single muxcode session can mix all three providers. Provider assignment is per-agent at launch time via environment variables (`MUXCODE_{ROLE}_CLI=codex`). This extends the existing Provider interface with a new `CodexProvider` implementation.

## Context

### What is Codex CLI

Codex CLI is OpenAI's open-source terminal-based coding agent (GitHub: openai/codex, MIT licensed) built with TypeScript/Node.js. It connects to OpenAI models (o4-mini default, plus o3, gpt-4.1, etc.) to perform coding tasks in the terminal.

Key capabilities relevant to muxcode integration:

| Feature | Codex CLI | Claude Code | OpenCode |
|---------|-----------|-------------|----------|
| Runtime | TypeScript (Node.js) | Node.js | TypeScript (Bun) |
| Interface | Interactive TUI + quiet mode | Line-based CLI with inline rendering | Full TUI |
| Providers | OpenAI models (+ OpenAI-compatible via `OPENAI_BASE_URL`) | Anthropic only | Multi-provider |
| Non-interactive | `codex --quiet --prompt "..."` | `claude -p "..."` | `opencode run "..."` |
| Approval modes | `suggest`, `auto-edit`, `full-auto` | `--dangerously-skip-permissions` | Per-agent `permission` block |
| Agent config | `AGENTS.md` (project), `~/.codex/instructions.md` (user) | `.claude/agents/` markdown | `.opencode/agents/` markdown |
| System prompts | `--instructions` flag or instructions file | `--append-system-prompt` flag | Per-agent `prompt` field |
| Hooks | None | Full PreToolUse/PostToolUse system | None |
| Sandbox | macOS `sandbox-exec`, Linux Docker/bubblewrap | settings.json allow/deny | Per-agent permission block |
| Compact | Not available (stateless per invocation in quiet mode) | `/compact` slash command | Auto-compact at 95% |
| Memory file | `AGENTS.md` | `CLAUDE.md` | `AGENTS.md` |
| Session persistence | Interactive mode only (no `--continue` equivalent in quiet) | Full conversation context | `--session`, `--continue` flags |
| Install | `npm install -g @openai/codex` | `npm install -g @anthropic-ai/claude-code` | `npm install -g @opencode-ai/opencode` |

### Why add Codex CLI support

1. **OpenAI model access** — Codex CLI natively supports OpenAI's reasoning models (o4-mini, o3) which have different strengths for certain coding tasks (e.g. complex debugging, multi-file refactoring).
2. **Cost optimization** — Mix cheaper models for simple roles (build, test) while using Claude for complex roles (edit, review).
3. **Provider diversity** — Reduce single-vendor dependency. If one provider has an outage, agents on other providers keep working.
4. **Quiet mode** — Codex's `--quiet` flag provides clean stdout output, making it easier to capture structured responses compared to TUI-based providers.

### Current provider landscape

| Provider | Status | Hook support | Idle detection | Integration pattern |
|----------|--------|-------------|----------------|-------------------|
| Claude Code | Production | Full hooks | `❯` prompt | Persistent agent, hook-driven chains |
| OpenCode | Production | None | None (TUI) | Persistent TUI, best-effort notifications |
| Local LLM | Production | None | None | Harness-managed, run-to-completion batches |
| **Codex CLI** | **Proposed** | **None** | **See design** | **Dual-mode: persistent interactive or run-to-completion** |

### Key challenges

1. **Dual personality** — Codex has both an interactive TUI mode and a headless `--quiet` mode. The integration must choose one (or support both). The quiet mode is more predictable but stateless; the interactive mode has session persistence but harder pane interaction.
2. **No hook system** — Like OpenCode, Codex has no PreToolUse/PostToolUse hooks. Build/test/review chains require the same graceful degradation strategy (system prompt instructions for manual `muxcode send` commands).
3. **Stateless quiet mode** — Each `codex --quiet` invocation starts fresh with no conversation history. Long multi-turn tasks require the full context to be passed each time, or the interactive mode must be used instead.
4. **Sandbox conflicts** — Codex's default sandbox restricts network and filesystem access. MuxCode agents need to run `muxcode send`, read/write bus files in `/tmp/`, and access project files. The sandbox must be disabled (`--no-sandbox`) or configured to allow these paths.
5. **No compact** — In quiet mode, there's no session to compact. In interactive mode, there's no `/compact` equivalent. Context management is handled differently.
6. **OpenAI API key required** — Codex requires `OPENAI_API_KEY`. Mixed sessions need both Anthropic and OpenAI credentials available.

### Opportunities

1. **Clean stdout capture** — `--quiet` mode outputs results to stdout without TUI chrome, making response parsing trivial compared to OpenCode's TUI capture.
2. **Full-auto approval** — `--approval-mode full-auto` eliminates permission prompts entirely, no startup acceptance needed.
3. **Run-to-completion semantics** — Quiet mode naturally fits the "receive task, execute, reply" pattern used by non-edit agents.
4. **OpenAI-compatible endpoint** — `OPENAI_BASE_URL` override enables pointing at any OpenAI-compatible API (local models, Azure OpenAI, etc.).
5. **Lightweight agents** — Codex quiet-mode agents have no TUI overhead, lower memory footprint, faster startup than persistent TUI agents.

## Design

### Integration mode decision

**Recommended: quiet mode (run-to-completion)**

Codex CLI's `--quiet --approval-mode full-auto` mode is the best fit for MuxCode's non-edit agents. Each bus message spawns a new Codex invocation with the full context, captures stdout, and replies. This is similar to the LocalProvider/harness pattern but using Codex instead of Ollama.

Rationale:
- Non-edit agents (build, test, review, deploy) process one task at a time — no need for session persistence
- Quiet mode provides clean, parseable output
- No TUI interaction complexity (no send-keys timing, no box-drawing detection)
- No idle detection needed — process exits when done
- Sandbox can be disabled at launch (no runtime permission prompts)

**Alternative: interactive mode (persistent TUI)**

For roles that benefit from conversation continuity (e.g. a Codex-powered review agent maintaining context across multiple file reviews), the interactive TUI mode could be used. This would follow the OpenCode provider pattern (send-keys interaction, best-effort notifications). Deferred to a later phase if needed.

### Provider interface implementation

New file: `bus/provider_codex.go`

```go
type CodexProvider struct{}

func (p *CodexProvider) Name() string { return "codex" }

// ConfigureLaunch sets quiet mode and full-auto approval.
func (p *CodexProvider) ConfigureLaunch(cfg *LaunchConfig, role string)

// BuildExecArgs constructs: codex --quiet --approval-mode full-auto
//   --model <model> --instructions <agent-prompt-file>
//   --prompt <task-message>
func (p *CodexProvider) BuildExecArgs(cfg *LaunchConfig) (string, []string)

// IsIdle — in quiet mode, the process either is running or has exited.
// Check if the pane shows a shell prompt (process exited = idle).
func (p *CodexProvider) IsIdle(session, role string) bool

// IsAlive — check if codex process is running in the pane.
// If pane shows shell prompt ($, %), the process has exited.
func (p *CodexProvider) IsAlive(session, role string) bool

// ClassifyPane — detect codex running state vs shell prompt.
func (p *CodexProvider) ClassifyPane(content string) PaneState

// AcceptStartup — no startup prompts in quiet+full-auto mode.
func (p *CodexProvider) AcceptStartup(session, pane string, state PaneState) bool

// SendWakeUp — no-op for quiet mode (process is either running or exited).
func (p *CodexProvider) SendWakeUp(session, role string) error

// Compact — no-op (stateless invocations, no session to compact).
func (p *CodexProvider) Compact(session, role, target string) error

// SupportsHooks — false (no hook system).
func (p *CodexProvider) SupportsHooks() bool

// IdlePromptChar — empty (no interactive prompt to detect).
func (p *CodexProvider) IdlePromptChar() string

// WriteAgentConfig — write instructions file for the role.
func (p *CodexProvider) WriteAgentConfig(role string) error

// DetectTaskCompletion — check if codex process has exited (shell prompt visible).
// Parse last output lines for error indicators.
func (p *CodexProvider) DetectTaskCompletion(session, role, paneContent string) (bool, bool, string)
```

### Agent launch pattern

Unlike Claude Code (persistent) and OpenCode (persistent TUI), Codex quiet-mode agents use a **wrapper loop** pattern:

```bash
# Pane runs a loop that:
# 1. Waits for a trigger file (bus message notification)
# 2. Reads the latest inbox message
# 3. Invokes codex --quiet with the message as prompt
# 4. Captures output and sends reply via bus
# 5. Returns to waiting

while true; do
  # Wait for trigger file mtime change
  muxcode inbox --poll --timeout 600

  # Read and consume inbox
  messages=$(muxcode inbox --consume)

  # Build prompt from message + agent instructions
  prompt="$messages"

  # Run codex
  output=$(codex --quiet \
    --approval-mode full-auto \
    --no-sandbox \
    --model "$CODEX_MODEL" \
    --instructions "$AGENT_INSTRUCTIONS_FILE" \
    --prompt "$prompt" 2>&1)

  # Reply via bus
  muxcode send edit response "$output" --reply-to "$msg_id"
done
```

This wrapper script replaces the direct `claude` / `opencode` binary launch. The `CodexProvider.BuildExecArgs()` returns the wrapper script path and arguments.

### Agent instructions file

Each role gets an instructions file at `.codex/agents/<role>.md` (or a temp file), containing:

1. Role description (from the agent definition)
2. Shared prompt (bus commands, memory access, etc.)
3. Graceful degradation instructions (manual chain commands)
4. Tool restrictions (as prose instructions — Codex has no tool profile system)

Example for the build role:

```markdown
# Build Agent

You are the build agent in a multi-agent coding environment. Your role is to
run builds and report results.

## Instructions

- Run `./build.sh` to build the project
- Report build output (success/failure, errors, warnings)
- After a successful build, notify the test agent:
  `muxcode send test test "Build succeeded, run tests"`
- After a failed build, notify the edit agent:
  `muxcode send edit build-failed "Build failed: <error summary>"`

## Restrictions

- Do NOT modify source code files
- Do NOT run git commands
- Do NOT run tests (that's the test agent's job)
- Only run build-related commands: ./build.sh, make, pnpm build
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
| `MUXCODE_{ROLE}_CODEX_MODEL` | `o4-mini` | Model for a specific Codex role |
| `MUXCODE_CODEX_MODEL` | `o4-mini` | Default model for all Codex agents |
| `OPENAI_API_KEY` | — | Required for Codex CLI |
| `OPENAI_BASE_URL` | — | Optional: custom OpenAI-compatible endpoint |
| `MUXCODE_CODEX_SANDBOX` | `off` | Sandbox mode: `off`, `seatbelt`, `docker` |

### Graceful degradation

Same strategy as OpenCode — three-layer approach:

| Feature | Claude Code | Codex CLI | Fallback |
|---------|-------------|-----------|----------|
| Build/test/review chain | Hook-driven | None | Instructions in agent prompt: "after build succeeds, run `muxcode send test test ...`" |
| Edit guard | PreToolUse hook blocks | None | Instructions: "do NOT edit source files" (soft enforcement) |
| File-change routing | Analyze hook | None | Not available — edit agent manually triggers review |
| Idle notifications | send-keys at `❯` | None | Process-exit detection — wrapper loop handles next message automatically |
| Compact | `/compact` injection | None | Not needed — each invocation is stateless |
| Workflow state | Hook transitions | None | Agent prompt instructions for `muxcode send` state updates |

### Sandbox considerations

Codex CLI's default sandbox restricts:
- Network access (disabled)
- Filesystem (scoped to working directory)

MuxCode agents need access to:
- `/tmp/muxcode-bus-{session}/` — bus files (inbox, triggers, locks)
- `~/.config/muxcode/` — memory, skills, config
- Project directory — source code
- Network — for `muxcode send` (local filesystem, no network needed)

**Resolution**: Launch with `--no-sandbox` for MuxCode agents. The multi-agent permission model (tool profiles, role restrictions in agent prompts) provides the guardrails. Codex's sandbox is designed for untrusted single-user use; MuxCode's agents operate in a trusted, controlled environment.

### Task completion detection

In quiet mode, Codex exits when done. Detection is straightforward:

```go
func (p *CodexProvider) DetectTaskCompletion(session, role, content string) (bool, bool, string) {
    lines := strings.Split(strings.TrimSpace(content), "\n")
    if len(lines) == 0 {
        return false, false, ""
    }

    lastLine := strings.TrimSpace(lines[len(lines)-1])

    // Shell prompt means codex exited
    if strings.HasSuffix(lastLine, "$") || strings.HasSuffix(lastLine, "%") {
        // Check for error indicators in recent output
        for _, line := range lines {
            lower := strings.ToLower(line)
            if strings.Contains(lower, "error") ||
               strings.Contains(lower, "fatal") ||
               strings.Contains(lower, "failed") {
                return true, true, "codex exited with errors"
            }
        }
        return true, false, "codex completed successfully"
    }

    return false, false, ""
}
```

## Implementation phases

### Phase 1: Provider stub and configuration

Add the `CodexProvider` struct with minimal implementation. Wire into `ResolveProvider()`. Add env var support.

**Key files**:

| File | Change |
|------|--------|
| `bus/provider.go` | Add `"codex"` case to `ResolveProvider()` |
| `bus/provider_codex.go` | New file — `CodexProvider` struct, all interface methods |
| `bus/provider_codex_test.go` | New file — interface conformance, provider resolution |

**Success criteria**:
1. `MUXCODE_BUILD_CLI=codex` resolves to `CodexProvider`
2. `CodexProvider` implements all `Provider` interface methods
3. `SupportsHooks()` returns `false`
4. All existing tests pass

### Phase 2: Wrapper script and launch integration

Create the wrapper script that runs the inbox-poll + codex-invoke loop. Implement `BuildExecArgs()` and `ConfigureLaunch()`.

**Key files**:

| File | Change |
|------|--------|
| `bus/provider_codex.go` | Implement `BuildExecArgs()`, `ConfigureLaunch()` |
| `scripts/muxcode-codex-agent.sh` | New file — wrapper loop script |
| `bus/launch.go` | Add codex model env var resolution (`RoleCodexModelEnvVar()`, `RoleCodexModelDefault()`) |
| `Makefile` | Install wrapper script to `~/.config/muxcode/scripts/` |

**Success criteria**:
1. `BuildExecArgs()` returns wrapper script path with correct arguments
2. Wrapper script polls inbox, invokes codex, sends reply
3. Agent launches in tmux pane and processes a test message
4. Reply appears in edit agent's inbox

### Phase 3: Agent instructions and graceful degradation

Generate role-specific instructions files. Implement `WriteAgentConfig()`. Add chain instructions via `adaptBodyForNonHookProvider()` (reuse OpenCode's approach).

**Key files**:

| File | Change |
|------|--------|
| `bus/provider_codex.go` | Implement `WriteAgentConfig()`, instructions file generation |
| `bus/provider_opencode.go` | Extract `adaptBodyForNonHookProvider()` to shared helper if not already |
| `bus/profile.go` | Ensure `CheckSendPolicy()` bypass works for codex provider |

**Success criteria**:
1. Instructions file generated at `.codex/agents/<role>.md` with role-specific content
2. Chain instructions included (build→test→review)
3. Tool restrictions expressed as prose instructions
4. `CheckSendPolicy()` allows non-hook chain messages for codex agents

### Phase 4: Pane detection, health, and notifications

Implement `IsIdle()`, `IsAlive()`, `ClassifyPane()`, `DetectTaskCompletion()`. Integrate with watcher health checks.

**Key files**:

| File | Change |
|------|--------|
| `bus/provider_codex.go` | Implement pane detection methods |
| `bus/agent_health.go` | Handle codex agent restart (re-launch wrapper script) |
| `bus/notify.go` | Handle codex notification (trigger file sufficient — wrapper polls) |

**Success criteria**:
1. `IsAlive()` correctly detects running codex process vs exited shell
2. `DetectTaskCompletion()` identifies success and error states
3. Watcher detects stale codex agents and triggers restart
4. Messages delivered via trigger file (wrapper's `--poll` detects)

### Phase 5: Mixed-provider testing

Test mixed sessions with Claude Code + Codex CLI agents. Verify chains, health monitoring, and console display.

**Key files**:

| File | Change |
|------|--------|
| `bus/provider_codex_test.go` | Mixed-provider coexistence tests |
| `bus/console.go` | Add codex agent display (if console formatting needed) |
| `bus/inspect.go` | Include provider field in agent status for codex |

**Success criteria**:
1. Session with Claude Code edit + Codex CLI build + Codex CLI test runs end-to-end
2. Build→test→review chain completes via bus messages
3. `muxcode inspect` shows correct provider for each agent
4. Console displays codex agent output correctly
5. Agent health monitoring works for codex agents

### Phase 6 (deferred): Interactive mode

Support Codex CLI's interactive TUI mode for roles that need conversation persistence. Would follow OpenCode's TUI integration pattern (send-keys, box-drawing detection). Deferred unless a concrete use case emerges — quiet mode should cover all non-edit roles.

## Resolved questions

### Q1: Quiet mode vs interactive mode?

**Decision**: Quiet mode (run-to-completion) for initial implementation.

**Rationale**: Non-edit agents process discrete tasks — build this, test that, review these files. Each task is self-contained and doesn't benefit from conversation history. Quiet mode is simpler to integrate (no TUI interaction, no idle detection complexity, predictable stdout capture). Interactive mode can be added later if needed.

### Q2: Wrapper script vs harness integration?

**Decision**: Wrapper script (bash) rather than extending the Go harness.

**Rationale**: The Go LLM harness (`muxcode-llm-harness`) is designed for Ollama's chat completion API, managing tool execution and conversation turns in Go. Codex CLI is a complete agent with its own tool execution — wrapping it in the harness would be redundant. A simple bash wrapper that handles inbox polling and codex invocation is sufficient and easier to maintain.

### Q3: How to pass context without session persistence?

**Decision**: Each invocation gets the full context via instructions file + prompt.

**Rationale**: The instructions file contains the role definition, restrictions, and chain commands (static per session). The prompt contains the specific task message. For build/test agents, this is sufficient — they don't need to remember previous builds. For review agents that might benefit from context, the instructions file can include recent memory entries via `muxcode memory context`.

### Q4: Sandbox on or off?

**Decision**: Off by default (`--no-sandbox`) for MuxCode agents.

**Rationale**: MuxCode agents need filesystem access to bus files (`/tmp/`), config (`~/.config/muxcode/`), and the project directory. They also run arbitrary build/test commands. The sandbox would need so many exceptions that it provides minimal security value. MuxCode's own permission model (tool profiles, role-restricted prompts) is the appropriate security layer.

### Q5: Should Codex be available for the edit role?

**Decision**: Technically supported but not recommended for initial release.

**Rationale**: The edit agent requires persistent conversation, complex tool orchestration, and hook support for the guard system. Codex's quiet mode doesn't support these. Interactive mode could work but would lose the edit guard hook — a significant security regression. For now, recommend Claude Code for the edit role and Codex for worker roles (build, test, review, deploy).

### Q6: How to handle Codex CLI not installed?

**Decision**: Fail fast at agent launch with a clear error message.

**Rationale**: Same pattern as OpenCode — if `MUXCODE_BUILD_CLI=codex` is set but `codex` is not in PATH, the wrapper script should exit with a clear error message. The watcher will detect the dead agent and report it. `install.sh` should check for `codex` availability when the user selects it as a provider.
