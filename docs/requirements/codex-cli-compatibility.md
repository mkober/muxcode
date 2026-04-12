# Codex CLI compatibility

Enable swapping Claude Code with [Codex CLI](https://github.com/openai/codex) (OpenAI's open-source coding agent) on a per-agent basis. Each tmux window/tab runs one agent, and each agent can independently use Claude Code, OpenCode, or Codex CLI as its AI CLI. A single muxcode session can mix all three providers. Provider assignment is per-agent at launch time via environment variables (`MUXCODE_{ROLE}_CLI=codex`). This extends the existing Provider interface with a new `CodexProvider` implementation.

**Initial target role**: review agent — validate the integration pattern with a read-only, run-to-completion workload before expanding to other roles.

## Context

### What is Codex CLI

Codex CLI is OpenAI's open-source terminal-based coding agent (GitHub: openai/codex, MIT licensed). Originally built in TypeScript/Node.js, it has been rewritten in **Rust** with an Ink-based TUI. It connects to OpenAI models (codex-mini-latest default, plus o4-mini, o3, gpt-4.1, gpt-5.4, etc.) to perform coding tasks in the terminal.

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
2. **Cost optimization** — Mix cheaper models for simple roles (build, test) while using Claude for complex roles (edit). OpenAI's codex-mini-latest is optimized for coding tasks at lower cost.
3. **Provider diversity** — Reduce single-vendor dependency. If one provider has an outage, agents on other providers keep working.
4. **Clean exec mode** — `codex exec` outputs the final assistant message to stdout and progress to stderr, making response capture trivial compared to TUI-based providers.

### Current provider landscape

| Provider | Status | Hook support | Idle detection | Integration pattern |
|----------|--------|-------------|----------------|-------------------|
| Claude Code | Production | Full hooks | `❯` prompt | Persistent agent, hook-driven chains |
| OpenCode | Production | None | None (TUI) | Persistent TUI, best-effort notifications |
| Local LLM | Production | None | None | Harness-managed, run-to-completion batches |
| **Codex CLI** | **Proposed** | **None** | **Shell prompt** | **`exec` mode, run-to-completion via wrapper** |

### Key challenges

1. **No persistent agent** — `codex exec` is run-to-completion: each invocation starts fresh with no conversation history. The wrapper loop pattern (similar to LocalProvider) handles this, but it means no multi-turn context within a single review.
2. **No hook system** — Codex has experimental `hooks.json` behind a feature flag, but it's not stable. Build/test/review chains require the same graceful degradation strategy as OpenCode (system prompt instructions for manual `muxcode send` commands).
3. **Sandbox blocks /tmp on macOS** — Seatbelt unconditionally disables network and restricts filesystem. MuxCode agents need `/tmp/muxcode-bus-{session}/` access. Must use `--sandbox danger-full-access` or `--yolo` to bypass.
4. **No --instructions flag** — Custom instructions are only loaded from `AGENTS.md` files in the filesystem hierarchy. The wrapper must ensure the right `AGENTS.md` is in scope (project root or `~/.codex/`).
5. **Alt-screen TUI** — The interactive TUI uses alternate screen with a React-based (Ink) input box. `tmux send-keys` does not work reliably. The `exec` mode avoids this entirely.
6. **OpenAI API key required** — Codex requires `OPENAI_API_KEY` (or `codex login` auth). Mixed sessions need both Anthropic and OpenAI credentials available.

### Opportunities

1. **Clean stdout/stderr separation** — `codex exec` writes progress to stderr and the final answer to stdout. Capturing the response is `output=$(codex exec "..." 2>/dev/null)` or redirect stderr to a log file.
2. **JSONL event stream** — `codex exec --json` outputs structured events (thread.started, item.completed, turn.completed). Enables rich task-completion detection without pane scraping.
3. **Full-auto shortcut** — `--full-auto` sets approval=never + sandbox=workspace-write in one flag. `--yolo` disables both sandbox and approvals entirely.
4. **Session resume** — `codex exec resume --last` can continue a previous session. Could enable multi-turn reviews if needed.
5. **Named profiles** — `config.toml` supports `[profiles.review]` with role-specific model/sandbox/approval settings. `codex --profile review exec "..."`.
6. **Structured output** — `--output-schema schema.json` validates the response against a JSON schema. Could enforce structured review output format.
7. **Additional directory access** — `--add-dir /tmp/muxcode-bus-*` grants write access to specific directories without fully disabling the sandbox.

## Design

### Integration mode: `exec` (run-to-completion)

`codex exec` is the right fit for MuxCode's non-edit agents. Each bus message spawns a new Codex invocation with the task prompt, captures stdout, and replies via the bus. This is the same pattern as the LocalProvider/harness but using Codex's own tool execution engine.

**Why not interactive TUI?**
- Alt-screen TUI breaks `tmux send-keys` interaction
- No reliable idle detection (no prompt character, React-based rendering)
- `exec` mode provides cleaner output separation (stdout vs stderr)
- Run-to-completion matches the review agent's workload pattern

**Command template:**

```bash
codex exec \
  --full-auto \
  --sandbox danger-full-access \
  --add-dir "/tmp/muxcode-bus-${session}" \
  -m "${model}" \
  -C "${project_dir}" \
  "${prompt}"
```

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
wrapper detects inbox message
  ↓
wrapper builds prompt:
  - Role instructions from AGENTS.md
  - Task: "Review the changes in this project"
  - Context: git diff output (pre-captured, passed as prompt)
  ↓
codex exec --full-auto -m o4-mini "..." 2>>/tmp/codex-review.log
  ↓
stdout captured as review result
  ↓
wrapper parses result, sends reply via bus
  ↓
edit receives review summary
```

**Key design choice: pre-capture diff vs let Codex run git**

Option A: Wrapper pre-captures `git diff` and includes it in the prompt. Codex doesn't need git access.
Option B: Codex runs `git diff` itself via its bash tool. Requires git in PATH and repo access.

**Decision: Option B** — Let Codex run its own commands. This matches how the Claude Code review agent works (runs `git status`, `git diff`, reads files for context). Codex's bash tool handles this natively. The wrapper just passes the task description; Codex figures out the commands.

### Provider interface implementation

New file: `bus/provider_codex.go`

```go
type CodexProvider struct{}

func (p *CodexProvider) Name() string { return "codex" }

// ConfigureLaunch sets exec-mode configuration.
// Resolves model from MUXCODE_{ROLE}_CODEX_MODEL or MUXCODE_CODEX_MODEL.
func (p *CodexProvider) ConfigureLaunch(cfg *LaunchConfig, role string)

// BuildExecArgs returns the wrapper script path and arguments.
// The wrapper handles inbox polling and codex exec invocation.
func (p *CodexProvider) BuildExecArgs(cfg *LaunchConfig) (string, []string)

// IsIdle checks if the wrapper's pane shows a "waiting for messages" state.
// The wrapper prints a marker line when idle between invocations.
func (p *CodexProvider) IsIdle(session, role string) bool

// IsAlive checks if the wrapper process is running in the pane.
// Shell prompt ($, %) without wrapper marker = dead.
func (p *CodexProvider) IsAlive(session, role string) bool

// ClassifyPane detects wrapper state vs bare shell.
func (p *CodexProvider) ClassifyPane(content string) PaneState

// AcceptStartup — no startup prompts needed (wrapper handles everything).
func (p *CodexProvider) AcceptStartup(session, pane string, state PaneState) bool

// SendWakeUp — writes trigger file to wake the wrapper's poll loop.
func (p *CodexProvider) SendWakeUp(session, role string) error

// Compact — no-op (exec mode is stateless).
func (p *CodexProvider) Compact(session, role, target string) error

// SupportsHooks — false.
func (p *CodexProvider) SupportsHooks() bool { return false }

// IdlePromptChar — returns wrapper's idle marker string.
func (p *CodexProvider) IdlePromptChar() string

// WriteAgentConfig writes AGENTS.md with role-specific instructions.
// Uses .codex/AGENTS.md or a role-specific file referenced from config.toml.
func (p *CodexProvider) WriteAgentConfig(role string) error

// DetectTaskCompletion checks wrapper output for completion markers.
// The wrapper prints "[CODEX-DONE]" or "[CODEX-ERROR]" after each invocation.
func (p *CodexProvider) DetectTaskCompletion(session, role, content string) (bool, bool, string)
```

### Wrapper script design

`scripts/muxcode-codex-agent.sh` — the pane process for Codex agents:

```bash
#!/usr/bin/env bash
set -euo pipefail

ROLE="$1"
SESSION="$MUXCODE_SESSION"
IDLE_MARKER="[codex-agent:${ROLE}] waiting for messages..."

# Validate codex is installed
if ! command -v codex &>/dev/null; then
  echo "ERROR: codex CLI not found in PATH. Install: npm install -g @openai/codex"
  exit 1
fi

# Validate API key
if [ -z "${OPENAI_API_KEY:-}" ]; then
  echo "ERROR: OPENAI_API_KEY not set"
  exit 1
fi

# Resolve model
model_var="MUXCODE_$(echo "$ROLE" | tr '[:lower:]' '[:upper:]')_CODEX_MODEL"
model="${!model_var:-${MUXCODE_CODEX_MODEL:-codex-mini-latest}}"

# Resolve sandbox mode
sandbox="${MUXCODE_CODEX_SANDBOX:-danger-full-access}"

# Log startup
muxcode log "$ROLE" "Codex agent started (model: $model, sandbox: $sandbox)"

while true; do
  echo "$IDLE_MARKER"

  # Poll for inbox messages (blocks until message arrives or timeout)
  muxcode inbox --poll --role "$ROLE" --timeout 600

  # Read and consume inbox
  raw=$(muxcode inbox --role "$ROLE" --consume --raw 2>/dev/null) || continue
  [ -z "$raw" ] && continue

  # Extract message fields
  msg_id=$(echo "$raw" | jq -r '.[0].id // empty')
  from=$(echo "$raw" | jq -r '.[0].from // empty')
  action=$(echo "$raw" | jq -r '.[0].action // empty')
  content=$(echo "$raw" | jq -r '.[0].content // empty')

  echo "[codex-agent:${ROLE}] processing: ${action} from ${from}"

  # Build the prompt with task context
  prompt="${content}"

  # Run codex exec, capture stdout (final answer), stderr to log
  log_file="/tmp/muxcode-codex-${SESSION}-${ROLE}.log"
  output=$(codex exec \
    --full-auto \
    --sandbox "$sandbox" \
    --add-dir "/tmp/muxcode-bus-${SESSION}" \
    -m "$model" \
    "$prompt" 2>>"$log_file") && exit_code=0 || exit_code=$?

  if [ $exit_code -eq 0 ] && [ -n "$output" ]; then
    echo "[CODEX-DONE] success"

    # Write output to temp file for bus reply (avoid newline issues in args)
    tmpfile=$(mktemp /tmp/muxcode-codex-reply-XXXXXX.txt)
    echo "$output" > "$tmpfile"

    # Send truncated summary via bus, full output via log
    summary=$(echo "$output" | head -20)
    muxcode send "$from" response "$summary" --type response --reply-to "$msg_id"
    muxcode log "$ROLE" "Codex task completed" --exit-code 0 --output-file "$tmpfile"
    rm -f "$tmpfile"
  else
    echo "[CODEX-ERROR] exit code: $exit_code"
    error_msg="Codex exec failed (exit $exit_code)"
    [ -n "$output" ] && error_msg="$error_msg: $(echo "$output" | tail -5)"
    muxcode send "$from" response "$error_msg" --type response --reply-to "$msg_id"
    muxcode log "$ROLE" "Codex task failed" --exit-code "$exit_code"
  fi
done
```

### AGENTS.md generation

Unlike Claude Code (which reads `.claude/agents/<role>.md`) or OpenCode (`.opencode/agents/<role>.md`), Codex reads `AGENTS.md` from the project root downward. MuxCode cannot create per-role `AGENTS.md` files without conflicts.

**Solution**: Use Codex's `--cd` flag to set a role-specific working directory that contains a tailored `AGENTS.md`:

```
/tmp/muxcode-codex-{session}/{role}/AGENTS.md    # Role-specific instructions
```

The wrapper script creates this directory, writes `AGENTS.md`, and passes `--cd` to point at the project dir while the instructions file is in a parent scope. Alternatively, use `~/.codex/AGENTS.override.md` for global instructions and rely on the project's own `AGENTS.md` for project context.

**Preferred approach**: Write role instructions to `.codex/AGENTS.md` in the project directory. This file is role-agnostic (contains shared instructions about the bus protocol). Role-specific behavior comes from the prompt passed to `codex exec`.

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
| `MUXCODE_{ROLE}_CODEX_MODEL` | `codex-mini-latest` | Model for a specific Codex role |
| `MUXCODE_CODEX_MODEL` | `codex-mini-latest` | Default model for all Codex agents |
| `OPENAI_API_KEY` | — | Required for Codex CLI |
| `MUXCODE_CODEX_SANDBOX` | `danger-full-access` | Sandbox mode: `read-only`, `workspace-write`, `danger-full-access` |

Config file (`.muxcode/config` or `~/.config/muxcode/config`):

```bash
# Codex CLI settings
MUXCODE_REVIEW_CLI=codex
MUXCODE_CODEX_MODEL=o4-mini
OPENAI_API_KEY=sk-...
```

### Codex CLI flags reference

Flags used in the integration:

| Flag | Short | Values | Usage |
|------|-------|--------|-------|
| `exec` | `e` | subcommand | Non-interactive run-to-completion mode |
| `--full-auto` | — | boolean | Sets approval=never + sandbox=workspace-write |
| `--sandbox` | `-s` | `read-only`, `workspace-write`, `danger-full-access` | Filesystem access level |
| `--add-dir` | — | path | Grant additional directory write access |
| `--model` | `-m` | string | Model override (e.g. `o4-mini`, `codex-mini-latest`) |
| `--cd` | `-C` | path | Set working directory |
| `--json` | — | boolean | JSONL event stream to stdout |
| `-o` | — | path | Write final message to file |
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
| Idle notifications | send-keys at `❯` | None | Trigger file — wrapper's poll loop detects and processes |
| Compact | `/compact` injection | None | Not needed — each `exec` invocation is stateless |
| Workflow state | Hook transitions | None | Wrapper script logs via `muxcode log` after each invocation |

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

**Resolution**: Use `--sandbox danger-full-access` for initial implementation. The `--add-dir` flag could be used for finer control (`--add-dir /tmp/muxcode-bus-*`), but for the review agent (read-only workload) the sandbox mode matters less. MuxCode's own tool profile / prompt-based restrictions are the primary guardrails.

### Task completion detection

The wrapper script prints explicit markers, making detection simple:

```go
func (p *CodexProvider) DetectTaskCompletion(session, role, content string) (bool, bool, string) {
    lines := strings.Split(strings.TrimSpace(content), "\n")
    for i := len(lines) - 1; i >= 0 && i >= len(lines)-10; i-- {
        line := strings.TrimSpace(lines[i])
        if strings.Contains(line, "[CODEX-DONE]") {
            return true, false, "codex task completed successfully"
        }
        if strings.Contains(line, "[CODEX-ERROR]") {
            return true, true, "codex task failed"
        }
    }
    // Check for wrapper idle marker (between tasks)
    for i := len(lines) - 1; i >= 0 && i >= len(lines)-5; i-- {
        if strings.Contains(lines[i], "waiting for messages") {
            return true, false, "codex agent idle"
        }
    }
    return false, false, ""
}
```

### JSONL event stream (future enhancement)

`codex exec --json` outputs structured JSONL events:

```jsonl
{"type":"thread.started","thread_id":"...","model":"codex-mini-latest"}
{"type":"turn.started"}
{"type":"item.completed","item":{"type":"message","content":"Review complete..."}}
{"type":"turn.completed"}
```

This could be parsed for richer task tracking (progress updates, token usage, tool calls). Deferred to Phase 3 — plain text stdout is sufficient for initial integration.

## Implementation phases

### Phase 0: Manual validation

Before writing any Go code, manually test `codex exec` as a review agent replacement to validate the approach.

**Steps**:
1. Install Codex CLI: `npm install -g @openai/codex`
2. Set `OPENAI_API_KEY`
3. From the muxcode project root, run:
   ```bash
   codex exec --full-auto --sandbox danger-full-access -m o4-mini \
     "You are a code review agent. Run git diff to see recent changes, then review them for correctness, security, performance, and maintainability. Organize findings by severity: must-fix, should-fix, nit. Format each item as file:line, issue, suggested fix."
   ```
4. Compare output quality and latency against the Claude Code review agent
5. Test with `--json` flag to understand event stream format
6. Test sandbox modes to find the minimum permission level needed

**Success criteria**:
1. `codex exec` produces a useful code review from the same diff
2. Latency is acceptable (under 60 seconds for typical diffs)
3. Output is clean enough to parse and send via bus
4. No sandbox errors when accessing project files

### Phase 1: Provider stub and wrapper script

Add `CodexProvider` struct, wrapper script, and wire into `ResolveProvider()`.

**Key files**:

| File | Change |
|------|--------|
| `bus/provider.go` | Add `"codex"` case to `ResolveProvider()` |
| `bus/provider_codex.go` | New file — `CodexProvider` struct, all interface methods |
| `bus/provider_codex_test.go` | New file — interface conformance, provider resolution |
| `scripts/muxcode-codex-agent.sh` | New file — wrapper loop script |
| `bus/launch.go` | Add `RoleCodexModelEnvVar()`, `RoleCodexModelDefault()` |
| `Makefile` | Install wrapper script to `~/.config/muxcode/scripts/` |

**Success criteria**:
1. `MUXCODE_REVIEW_CLI=codex` resolves to `CodexProvider`
2. `CodexProvider` implements all `Provider` interface methods
3. `SupportsHooks()` returns `false`
4. Wrapper script starts, polls inbox, invokes codex, sends reply
5. All existing tests pass

### Phase 2: Review agent end-to-end

Get the review agent working with Codex in a live muxcode session.

**Key files**:

| File | Change |
|------|--------|
| `bus/provider_codex.go` | Implement `WriteAgentConfig()` (AGENTS.md generation) |
| `bus/provider_codex.go` | Implement pane detection methods (IsIdle, IsAlive, ClassifyPane) |
| `bus/agent_health.go` | Handle codex agent restart (re-launch wrapper) |
| `bus/notify.go` | Handle codex notification (trigger file for wrapper poll) |

**Success criteria**:
1. `MUXCODE_REVIEW_CLI=codex` in config, launch muxcode session
2. Review window shows wrapper script running
3. `muxcode send review review "Review latest changes"` triggers codex exec
4. Review results appear in edit inbox
5. `muxcode inspect` shows review agent with provider=codex
6. Watcher detects dead wrapper and restarts it

### Phase 3: JSONL parsing and structured output

Upgrade from plain text capture to JSONL event parsing for richer task tracking.

**Key files**:

| File | Change |
|------|--------|
| `bus/provider_codex.go` | Add JSONL event parser, structured output schema |
| `bus/provider_codex_test.go` | JSONL parsing tests |
| `scripts/muxcode-codex-agent.sh` | Switch to `--json` mode, parse events |

**Success criteria**:
1. Wrapper parses JSONL events from codex exec
2. Progress events logged to console
3. Final message extracted cleanly from event stream
4. Error events detected and reported

### Phase 4: Multi-role expansion

Extend to build, test, and deploy agents. Validate chains work end-to-end.

**Key files**:

| File | Change |
|------|--------|
| `bus/provider_codex.go` | Role-specific AGENTS.md content for build/test/deploy |
| `bus/provider_opencode.go` | Extract `adaptBodyForNonHookProvider()` to shared util if needed |
| `bus/profile.go` | Ensure `CheckSendPolicy()` bypass works for codex provider |
| `scripts/muxcode-codex-agent.sh` | Role-specific prompt construction |

**Success criteria**:
1. Build agent with Codex runs `./build.sh` and reports results
2. Test agent with Codex runs tests and reports results
3. Build→test→review chain completes via bus messages (all on Codex)
4. Mixed session: Claude Code edit + Codex review + Codex build works

### Phase 5: Mixed-provider testing and hardening

Comprehensive testing of mixed Claude Code + Codex sessions.

**Key files**:

| File | Change |
|------|--------|
| `bus/provider_codex_test.go` | Mixed-provider coexistence tests |
| `bus/console.go` | Codex agent display in console |
| `bus/inspect.go` | Provider field in agent status for codex |
| `install.sh` | Codex availability check when selected as provider |

**Success criteria**:
1. Session with Claude Code edit + Codex review + Claude Code commit runs reliably
2. `muxcode inspect` shows correct provider for each agent
3. Console displays codex agent output correctly
4. Agent health monitoring detects and restarts crashed codex agents
5. `install.sh` validates codex installation when configured

### Phase 6 (deferred): Interactive TUI mode

Support Codex CLI's interactive TUI for roles needing conversation persistence. Would require `--no-alt-screen` flag and send-keys interaction. Deferred unless a concrete use case emerges — exec mode covers all current non-edit roles.

### Phase 7 (deferred): Session resume

Use `codex exec resume --last` to continue previous sessions for multi-turn workflows. Could enable persistent review context across multiple review requests. Deferred until basic integration is stable.

## Resolved questions

### Q1: `exec` mode vs interactive TUI?

**Decision**: `exec` mode (run-to-completion) for all roles.

**Rationale**: `codex exec` outputs the final answer to stdout and progress to stderr — clean separation that's trivial to capture. The interactive TUI uses alt-screen and a React-based input box that doesn't work with `tmux send-keys`. Exec mode matches the review agent's "receive task, analyze, report" workflow perfectly.

### Q2: Wrapper script vs Go harness integration?

**Decision**: Wrapper script (bash).

**Rationale**: Codex CLI is a complete agent with its own tool execution (bash, file read/write, glob, grep). Wrapping it in the Go harness would be redundant — the harness is designed for Ollama's raw chat completion API where MuxCode must provide tool execution. The wrapper just handles inbox polling and codex invocation.

### Q3: How to provide role-specific instructions?

**Decision**: Shared `.codex/AGENTS.md` for bus protocol + role-specific prompt in `codex exec` argument.

**Rationale**: Codex reads `AGENTS.md` from project root. Unlike Claude Code (per-role files in `.claude/agents/`) or OpenCode (per-role files in `.opencode/agents/`), Codex has a single `AGENTS.md` per directory scope. Role-specific behavior comes from the prompt passed to each `codex exec` invocation, which includes the full agent definition and task description. The shared `AGENTS.md` contains bus protocol instructions common to all roles.

### Q4: Which sandbox mode?

**Decision**: `danger-full-access` for initial implementation, with `--add-dir` refinement later.

**Rationale**: The review agent only reads files (git diff, file content), so sandbox risk is low. But it needs `/tmp/muxcode-bus-*/` access for bus communication and potentially `~/.config/muxcode/` for memory. On macOS, Seatbelt unconditionally blocks network regardless of config — but MuxCode bus is filesystem-based, so this doesn't matter. `danger-full-access` is the pragmatic starting point; we can tighten to `workspace-write` + `--add-dir` paths once the integration is stable.

### Q5: Should Codex be available for the edit role?

**Decision**: Not supported for initial release.

**Rationale**: The edit agent requires persistent conversation, complex tool orchestration, hook support for the guard system, and `/compact` for context management. `codex exec` is stateless. The interactive TUI mode could theoretically work but loses the edit guard — a significant security regression. The edit role stays Claude Code only.

### Q6: What about Codex's experimental hooks.json?

**Decision**: Ignore for now, revisit when stable.

**Rationale**: Codex has `hooks.json` behind `features.codex_hooks = true`, but it's undocumented and experimental. If it stabilizes and provides PreToolUse/PostToolUse equivalents, it could enable hook-driven chains like Claude Code. For now, treat Codex as a non-hook provider with the same graceful degradation as OpenCode.

### Q7: Plain text vs JSONL output?

**Decision**: Plain text for Phase 0-2, JSONL for Phase 3+.

**Rationale**: Plain text (`codex exec` default) is simpler — final message goes to stdout, progress to stderr. Good enough for initial integration. JSONL (`--json`) adds structured events (thread.started, item.completed, turn.completed) that enable progress tracking and richer error detection. Worth adding once the basic integration works.
