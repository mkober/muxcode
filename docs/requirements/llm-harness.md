# Local LLM Harness

## Context

The local LLM agent (qwen2.5-coder:7b via Ollama) running the commit/git role gets stuck in a loop calling `muxcode-agent-bus inbox` 20 times instead of executing the actual task. Root causes:

1. **SharedPrompt** says "Check Messages: `muxcode-agent-bus inbox`" but inbox is already consumed before Ollama sees it
2. **Agent definition** (git-manager.md) also has inbox instructions
3. **No tool call filtering** — `Bash(muxcode-agent-bus *)` pattern allows inbox calls
4. **No loop detection** within the tool-calling loop
5. **Small LLMs need clearer, more structured prompts**

## Solution: Standalone Go application at `tools/muxcode-llm-harness/`

A new binary that replaces `muxcode-agent-bus agent run` for local LLM roles. Uses the bus CLI for all bus operations (inbox, send, lock, tools), has its own Ollama client, tool executor, and harness logic.

## Project structure

```
tools/muxcode-llm-harness/
├── go.mod                    # go 1.22, stdlib only
├── main.go                   # CLI entry, signal handling
├── harness/
│   ├── config.go             # Session/role/paths, env vars
│   ├── ollama.go             # Ollama HTTP client (chat, health)
│   ├── ollama_test.go
│   ├── bus.go                # Bus CLI wrapper (inbox, send, lock, tools)
│   ├── bus_test.go
│   ├── message.go            # Message struct, JSONL parsing
│   ├── message_test.go
│   ├── executor.go           # Tool execution (bash, file ops)
│   ├── executor_test.go
│   ├── tools.go              # Tool defs for Ollama + pattern matching
│   ├── tools_test.go
│   ├── prompt.go             # Local LLM system prompt generation
│   ├── prompt_test.go
│   ├── filter.go             # Tool call filtering + loop detection
│   ├── filter_test.go
│   ├── loop.go               # Main polling loop + conversation mgmt
│   └── loop_test.go
```

## Bus integration strategy

The harness uses the bus CLI as its interface — no direct file access needed:

| Operation | Bus CLI command |
|-----------|----------------|
| Check for messages | `stat /tmp/muxcode-bus-{SESSION}/inbox/{ROLE}.jsonl` (file size > 0) |
| Consume messages | `muxcode-agent-bus inbox --raw` (atomic consume, JSONL output) |
| Send response | `muxcode-agent-bus send <to> <action> "<payload>" --type response --reply-to <id>` |
| Lock (busy) | `muxcode-agent-bus lock <role>` |
| Unlock (idle) | `muxcode-agent-bus unlock <role>` |
| Get tool patterns | `muxcode-agent-bus tools <role>` (once at startup, cached) |
| Get skills prompt | `muxcode-agent-bus skill prompt <role>` (once at startup) |
| Get context prompt | `muxcode-agent-bus context prompt <role>` (once at startup) |
| Log bash history | Append JSONL to `/tmp/muxcode-bus-{SESSION}/{role}-history.jsonl` (direct file write — simple append, no protocol) |

## File-by-file design

### `main.go` — Entry point

```
Usage: muxcode-llm-harness run <role> [--model MODEL] [--url URL] [--max-turns N]
```

- Parse args, set up signal handling (SIGINT/SIGTERM → context cancel)
- Call `harness.Run(ctx, cfg)` — blocks until cancelled
- Exit 0 on clean shutdown, 1 on error

### `harness/config.go` — Configuration

```go
type Config struct {
    Role       string  // agent role (commit, build, etc.)
    Session    string  // bus session name
    OllamaURL  string  // default http://localhost:11434
    OllamaModel string // default qwen2.5-coder:7b
    MaxTurns   int     // default 10
    BusDir     string  // /tmp/muxcode-bus-{session}/
    BusBin     string  // path to muxcode-agent-bus binary
}

func DefaultConfig() Config
func (c Config) InboxPath() string
func (c Config) HistoryPath() string
```

Session detection: `$MUXCODE_SESSION` → `$SESSION` → tmux session name.
Role detection: `$AGENT_ROLE` → `$BUS_ROLE` → first CLI arg.

### `harness/ollama.go` — Ollama client

Minimal OpenAI-compatible client (rewrite, not import — separate module):

```go
type OllamaClient struct { ... }
type ChatMessage struct { Role, Content string; ToolCalls []ToolCall; ToolCallID string }
type ToolCall struct { ID string; Function FunctionCall }
type ToolDef struct { ... }
type ChatResponse struct { ... }

func NewOllamaClient(url, model string) *OllamaClient
func (c *OllamaClient) ChatComplete(ctx, messages, tools) (*ChatResponse, error)
func (c *OllamaClient) CheckHealth(ctx) error
```

Same retry logic (3 attempts, 1s/2s/4s backoff). Temperature 0.1, max_tokens 4096.

### `harness/bus.go` — Bus CLI wrapper

```go
type BusClient struct { BinPath string; Session string; Role string }

func NewBusClient(cfg Config) *BusClient
func (b *BusClient) HasMessages() bool           // stat inbox file
func (b *BusClient) ConsumeInbox() ([]Message, error)  // muxcode-agent-bus inbox --raw
func (b *BusClient) Send(to, action, payload, msgType, replyTo string) error
func (b *BusClient) Lock() error
func (b *BusClient) Unlock() error
func (b *BusClient) ResolveTools() ([]string, error)   // muxcode-agent-bus tools <role>
func (b *BusClient) SkillPrompt() (string, error)      // muxcode-agent-bus skill prompt <role>
func (b *BusClient) ContextPrompt() (string, error)    // muxcode-agent-bus context prompt <role>
func (b *BusClient) LogHistory(command, output, exitCode, outcome string) error  // direct JSONL append
```

### `harness/message.go` — Message types

```go
type Message struct {
    ID      string `json:"id"`
    TS      int64  `json:"ts"`
    From    string `json:"from"`
    To      string `json:"to"`
    Type    string `json:"type"`
    Action  string `json:"action"`
    Payload string `json:"payload"`
    ReplyTo string `json:"reply_to"`
}

func ParseMessages(jsonlOutput string) ([]Message, error)
```

### `harness/executor.go` — Tool execution

```go
type Executor struct {
    Patterns []string  // allowed tool patterns from bus
    WorkDir  string
}

func NewExecutor(patterns []string) *Executor
func (e *Executor) Execute(ctx, call ToolCall) string
```

Same tools as bus executor: bash (60s timeout, 10K truncation, pattern enforcement), read_file, glob, grep, write_file, edit_file.

### `harness/tools.go` — Tool definitions + pattern matching

```go
func BuildToolDefs(patterns []string) []ToolDef
func IsToolAllowed(toolName, command string, patterns []string) bool
func GlobMatch(pattern, text string) bool  // exported for testing
```

Same glob-match DP algorithm. Tool defs generated from patterns (check which categories exist).

### `harness/prompt.go` — Local LLM prompt ⭐

The key differentiation from the bus agent. Generates a system prompt optimized for small LLMs:

```go
func BuildSystemPrompt(role string, skills, context, agentDef string) string
func LocalLLMInstructions(role string) string
func RoleExamples(role string) string
func ReadAgentDefinition(role string) string
func StripFrontmatter(content string) string
```

**LocalLLMInstructions** generates:

```markdown
## How You Work

You are an autonomous agent. Tasks are delivered in the user message below.
Your inbox has already been read — do NOT run `muxcode-agent-bus inbox`.

### Rules
1. Read the task below and execute it immediately using your tools
2. Do NOT check the inbox — your task is already here
3. Do NOT ask for confirmation — execute autonomously
4. After completing, provide a short summary

### Sending Results
muxcode-agent-bus send <target> <action> "<short single-line result>"

### Memory
muxcode-agent-bus memory write "<section>" "<text>"
```

**RoleExamples** returns concrete tool call examples per role:
- `git`/`commit`: git status, git add, git commit, git log examples
- `build`: ./build.sh
- `test`: ./test.sh
- etc.

**BuildSystemPrompt** assembles: agent definition + LocalLLMInstructions + role examples + skills + context.

### `harness/filter.go` — Tool call filtering + loop detection ⭐

The core harness logic:

```go
type Filter struct {
    Role       string
    CallCounts map[string]int  // command hash → count per batch
}

func NewFilter(role string) *Filter
func (f *Filter) Reset()
func (f *Filter) Check(tc ToolCall) (blocked bool, reason string)
```

**Filter rules** (in order):

1. **Block inbox**: `muxcode-agent-bus inbox*` → "Messages already delivered. Execute the task."
2. **Block self-send**: `muxcode-agent-bus send <own-role>` → "Cannot send to yourself."
3. **Block repetition**: same command hash 3+ times → "Stuck in a loop. Try different approach."
4. **Pass**: everything else goes to executor.

### `harness/loop.go` — Main polling loop + conversation management

```go
func Run(ctx context.Context, cfg Config) error
```

**Main loop** (3s poll interval):

```
1. stat inbox file → if empty, sleep 3s, continue
2. bus.Lock()
3. msgs = bus.ConsumeInbox()
4. conversation = buildConversation(systemPrompt, msgs)
5. tool loop (max 10 turns):
   a. resp = ollama.ChatComplete(conversation)
   b. if no tool calls → final response, break
   c. for each tool call:
      - filter.Check(tc) → if blocked, use corrective as tool result
      - if not blocked: executor.Execute(tc) → tool result
      - if bash and not blocked: bus.LogHistory(...)
      - append tool result to conversation
      - if blocked: also inject corrective user message
6. bus.Send(response)
7. bus.Unlock()
```

**buildConversation** transforms messages into structured task format:

```markdown
## Task

- **Action**: commit
- **From**: edit
- **Instructions**: Stage and commit all current changes...

Execute this task now using your available tools. Do NOT run `muxcode-agent-bus inbox`.
```

## Changes to existing files

| File | Change |
|------|--------|
| `scripts/muxcode-agent.sh` | Change local LLM launch from `muxcode-agent-bus agent run` to `muxcode-llm-harness run` |
| `Makefile` | Add `build-harness` target, add to `install` target |
| `CLAUDE.md` | Add "Local LLM harness" section documenting new binary |

### `bus/agent.go` — Keep unchanged

The existing `agent run` command in the bus binary remains for backward compatibility. The harness is the preferred path for local LLM roles.

## Verification

1. `cd tools/muxcode-llm-harness && go build .` — builds cleanly
2. `cd tools/muxcode-llm-harness && go test ./...` — all tests pass
3. `cd tools/muxcode-agent-bus && go test ./...` — existing tests still pass (nothing changed)
4. `make install` — both binaries installed
5. Manual test: start Ollama, send `muxcode-agent-bus send commit status "Show git status"` → harness should run `git status` and reply (not loop on inbox)
