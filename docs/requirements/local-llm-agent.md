# Local LLM Agent for Commit Role via Ollama

## Context

The commit agent (git-manager) currently runs Claude Code CLI, which is overkill for git operations (status, diff, commit, push, branch, PR). Replace it with a local LLM via Ollama to reduce API costs and latency. Other agents continue using Claude Code. The solution must be configurable per-role so any agent can optionally use a local LLM.

## Architecture: Go subcommand

New `muxcode-agent-bus agent run <role>` subcommand — a self-contained agentic loop that:
1. Builds system prompt from agent definition + shared prompt + skills + context + session resume
2. Polls inbox for messages
3. Sends messages to Ollama's OpenAI-compatible API with tool definitions
4. Executes tool calls (bash, read, glob, grep) with allowedTools enforcement
5. Feeds results back to LLM for multi-turn conversation
6. Sends final response back via bus

Ollama serves models locally at `http://localhost:11434/v1/chat/completions`.

## New files (all in `tools/muxcode-agent-bus/`)

| File | Purpose |
|------|---------|
| `bus/ollama.go` | HTTP client for Ollama API — request/response types, `ChatComplete()` function |
| `bus/ollama_test.go` | Tests with `httptest.NewServer` mock (same pattern as `bus/webhook_test.go`) |
| `bus/tools.go` | Build Ollama tool definitions from allowedTools patterns — `BuildToolDefs()`, `IsToolAllowed()` |
| `bus/tools_test.go` | AllowedTools glob matching tests |
| `bus/executor.go` | Execute tool calls — bash (with timeout/truncation), read, glob, grep |
| `bus/executor_test.go` | Executor tests |
| `bus/agent.go` | Core agentic loop — inbox polling, conversation state, tool-calling loop, history logging |
| `bus/agent_test.go` | Agent loop tests |
| `cmd/agent.go` | CLI entry point — `muxcode-agent-bus agent run <role> [--model MODEL] [--url URL]` |

## Modified files

| File | Change |
|------|--------|
| `main.go` | Add `case "agent": cmd.Agent(args)` to dispatch |
| `bus/profile.go` | Add `AgentCLI string` field to `ToolProfile`, add `OllamaConfig` to `MuxcodeConfig` |
| `scripts/muxcode-agent.sh` | Check per-role CLI override, route to `muxcode-agent-bus agent run` when set to `"local"` |

## Configuration

Per-role CLI override in `.muxcode/config` or `~/.config/muxcode/config`:

```bash
MUXCODE_GIT_CLI=local              # commit agent uses local LLM
MUXCODE_OLLAMA_MODEL=qwen2.5-coder:7b  # default model (configurable)
MUXCODE_OLLAMA_URL=http://localhost:11434  # default URL
```

Model is fully configurable — no hard-coded preference. Default `qwen2.5-coder:7b` as the lightest option that fits any Mac.

Launcher integration (`muxcode-agent.sh`): Before `exec claude ...`, check `MUXCODE_${ROLE^^}_CLI`. If `"local"`, run `exec muxcode-agent-bus agent run "$ROLE"` instead. Fallback to Claude Code if Ollama is unreachable.

## Ollama API integration (`bus/ollama.go`)

Key types:

```go
type OllamaConfig struct {
    BaseURL     string  // default "http://localhost:11434"
    Model       string  // default "qwen2.5-coder:7b"
    Temperature float64 // default 0.1 (low for git ops)
    Timeout     int     // seconds, default 120
    MaxTokens   int     // default 4096
}

type ChatMessage struct {
    Role       string     `json:"role"`
    Content    string     `json:"content"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`
}
```

- `Stream: false` for simplicity (no streaming initially)
- `POST /v1/chat/completions` with tool definitions
- `GET /api/tags` on startup to verify Ollama + model available
- Retry: 3 attempts with 1s/2s/4s backoff on connection errors

## Tool execution model (`bus/executor.go`, `bus/tools.go`)

| Tool | Ollama function name | Implementation |
|------|---------------------|----------------|
| Bash | `bash` | `exec.Command("bash", "-c", cmd)`, capture stdout+stderr. 60s timeout. 10K char output truncation. |
| Read | `read_file` | `os.ReadFile(path)`, return content |
| Glob | `glob` | `filepath.Glob(pattern)`, return matched paths |
| Grep | `grep` | Shell out to `grep -rn` |

AllowedTools enforcement: `IsToolAllowed(toolName, command, patterns)` matches `Bash(command)` against resolved patterns from `ResolveTools(role)`. Uses custom glob matcher (not `filepath.Match`) since `*` must match spaces and special chars within the parentheses.

## Agent loop (`bus/agent.go`)

```
START -> Build system prompt -> Build tool defs -> Verify Ollama connection
  |
MAIN LOOP:
  Lock -> Check inbox -> No messages? -> Unlock -> Sleep 3s -> repeat
  | (has messages)
  Format inbox messages as user content
  |
  TOOL LOOP (max 20 turns):
    Send conversation to Ollama
    |
    Has tool_calls? -> Execute each -> Append results -> continue tool loop
    No tool_calls? -> Extract text response
  |
  Send response via bus -> Log to history -> Unlock -> continue main loop
```

System prompt assembly — reuses existing bus functions:
1. Read agent definition (`agents/git-manager.md`) — parse frontmatter + body
2. `bus.SharedPrompt(role)` — coordination text
3. `bus.SkillPrompt(role)` — injected skills
4. `bus.ContextPrompt(role)` — context.d files
5. `bus.ResumeContext(role)` — session memory

Conversation state: Reset between inbox checks (keep system prompt, drop history). Prevents unbounded context growth.

History logging: After bash execution, append to `commit-history.jsonl` directly via bus library (replaces PostToolUse hook which only fires for Claude Code).

## Fallback to Claude Code

In `muxcode-agent.sh`, if `MUXCODE_GIT_CLI=local` but Ollama is unreachable:

```bash
if curl -s --max-time 2 "http://localhost:11434/api/tags" >/dev/null 2>&1; then
  exec muxcode-agent-bus agent run "$ROLE"
else
  echo "Ollama not running, falling back to Claude Code" >&2
  # Fall through to normal claude launch
fi
```

Remove `MUXCODE_GIT_CLI=local` from config to permanently revert.

## Implementation sequence

1. **Ollama client** — `bus/ollama.go` + tests (HTTP types, ChatComplete, health check)
2. **Tool definitions & matching** — `bus/tools.go` + tests (BuildToolDefs, IsToolAllowed)
3. **Tool executor** — `bus/executor.go` + tests (bash exec, read, glob, grep, timeout, truncation)
4. **Agent loop** — `bus/agent.go` + tests (prompt assembly, inbox polling, tool-call loop, history logging)
5. **CLI + config** — `cmd/agent.go`, `main.go` dispatch, `bus/profile.go` extensions
6. **Launcher integration** — `muxcode-agent.sh` per-role routing with Ollama fallback
7. **Docs** — CLAUDE.md section, backlog update

## Verification

1. `go test ./...` — all unit tests pass (mocked Ollama via httptest)
2. Start Ollama: `ollama serve && ollama pull qwen2.5-coder:7b`
3. Set `MUXCODE_GIT_CLI=local` in `.muxcode/config`
4. Start MUXcode session, send: `muxcode-agent-bus send commit status "Show git status"`
5. Verify commit agent reads inbox, calls Ollama, executes `git status`, sends response to edit
6. Full flow: `muxcode-agent-bus send commit commit "Commit current changes"` — verify git add + commit + response
7. Kill Ollama, restart session — verify fallback to Claude Code
