package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Config holds configuration for the LLM harness.
type Config struct {
	Role        string        // agent definition role (git, build, etc.) — for tools, skills, agent def
	BusRole     string        // bus identity role (commit, build, etc.) — for inbox, lock, send, history
	Session     string        // bus session name
	OllamaURL   string        // default http://localhost:11434
	OllamaModel string        // default qwen3:4b (must support tool calling)
	MaxTurns    int           // max tool-calling turns per batch (default 10)
	BusDir      string        // /tmp/muxcode-bus-{session}/
	BusBin      string        // path to muxcode binary
	NoThink     bool          // disable the model's thinking phase (MUXCODE_HARNESS_NOTHINK=1)
	APIKey      string        // bearer key for a hosted OpenAI-compatible endpoint (MUXCODE_HARNESS_API_KEY)
	TUI         bool          // enable TUI mode (live activity display)
	UserInput   <-chan string // user-submitted chat messages (TUI mode only)

	// TurnTrace enables the opt-in per-turn trace of the batch loop
	// (MUX-115, MUXCODE_HARNESS_TURN_TRACE=1); default off. TurnTraceFile
	// overrides the trace path (MUXCODE_HARNESS_TURN_TRACE_FILE), which
	// defaults to {BusDir}/{role}-turn-trace.jsonl next to the history file.
	TurnTrace     bool
	TurnTraceFile string
}

// DefaultConfig returns a Config with sensible defaults, reading from env vars.
func DefaultConfig() Config {
	cfg := Config{
		OllamaURL:   "http://localhost:11434",
		OllamaModel: "qwen3:4b",
		MaxTurns:    10,
	}

	// Session detection — matches bus.BusSession() resolution order
	if v := os.Getenv("MUXCODE_SESSION"); v != "" {
		cfg.Session = v
	} else if v := os.Getenv("BUS_SESSION"); v != "" {
		cfg.Session = v
	} else if v := os.Getenv("SESSION"); v != "" {
		cfg.Session = v
	} else if v := tmuxVar("#S"); v != "" {
		cfg.Session = v
	} else {
		cfg.Session = "default"
	}

	// BusRole detection — bus identity from env or tmux window name.
	// This determines the inbox, lock, send identity, and history path.
	// Follows the same fallback chain as bus.BusRole() in the bus binary.
	if v := os.Getenv("AGENT_ROLE"); v != "" {
		cfg.BusRole = v
	} else if v := os.Getenv("BUS_ROLE"); v != "" {
		cfg.BusRole = v
	} else if v := tmuxVar("#W"); v != "" {
		cfg.BusRole = v
	}

	// Ollama overrides from env
	if v := os.Getenv("MUXCODE_OLLAMA_URL"); v != "" {
		cfg.OllamaURL = v
	}
	if v := os.Getenv("MUXCODE_OLLAMA_MODEL"); v != "" {
		cfg.OllamaModel = v
	}

	// Thinking off: qwen3-class models spend 30-90s reasoning before their
	// first token — latency the harness's structured tasks rarely need.
	cfg.NoThink = os.Getenv("MUXCODE_HARNESS_NOTHINK") == "1"

	// A bearer key switches the endpoint from local Ollama to a hosted
	// OpenAI-compatible gateway (MUX-109: the prompt-agent on OpenCode's
	// Zen gateway) — same client, same dialect, plus Authorization.
	cfg.APIKey = os.Getenv("MUXCODE_HARNESS_API_KEY")

	cfg.TurnTrace = os.Getenv("MUXCODE_HARNESS_TURN_TRACE") == "1"
	cfg.TurnTraceFile = os.Getenv("MUXCODE_HARNESS_TURN_TRACE_FILE")

	cfg.BusDir = "/tmp/muxcode-bus-" + cfg.Session
	cfg.BusBin = findBusBin()

	return cfg
}

// RoleModel returns the Ollama model for a specific role, checking
// per-role env vars before falling back to the global default.
// Resolution order: MUXCODE_{ROLE}_MODEL → MUXCODE_OLLAMA_MODEL → default.
//
// MUXCODE_{ROLE}_MODEL is overloaded: OpenCode reads the same variable
// for its catalog models (provider/model form, e.g. opencode-go/foo), so
// a role pinned for OpenCode and later flipped to CLI=local would hand
// Ollama a model name it can never pull. Catalog-form values are
// therefore skipped — but ONLY while the endpoint is local Ollama. A
// remote gateway defines its own model namespace, so the guard stands
// down and pins pass through VERBATIM: some gateways use slashed ids
// (OpenRouter's deepseek/deepseek-chat), others bare ids (OpenCode Zen —
// opencode-go/deepseek-v4-flash 401'd there, live 2026-08-27). Stripping
// or rewriting would silently mangle ids valid elsewhere; a wrong pin
// fails loudly with the gateway's own error, which is the better
// teacher. Mirrored in bus/ollama.go.
func RoleModel(role string) string {
	envVar := roleModelEnvVar(role)
	if v := os.Getenv(envVar); v != "" && (!strings.Contains(v, "/") || remoteEndpointConfigured()) {
		return v
	}
	// Fall through to global env / default
	cfg := DefaultConfig()
	return cfg.OllamaModel
}

// remoteEndpointConfigured reports whether the inference endpoint is a
// non-local gateway rather than the default local Ollama.
func remoteEndpointConfigured() bool {
	u := os.Getenv("MUXCODE_OLLAMA_URL")
	return u != "" && u != "http://localhost:11434" && !strings.Contains(u, "localhost") && !strings.Contains(u, "127.0.0.1")
}

// roleModelEnvVar returns the per-role model env var name.
func roleModelEnvVar(role string) string {
	switch role {
	case "commit", "git":
		return "MUXCODE_GIT_MODEL"
	case "build":
		return "MUXCODE_BUILD_MODEL"
	case "test":
		return "MUXCODE_TEST_MODEL"
	case "review":
		return "MUXCODE_REVIEW_MODEL"
	case "deploy":
		return "MUXCODE_DEPLOY_MODEL"
	case "edit":
		return "MUXCODE_EDIT_MODEL"
	case "analyze", "analyst":
		return "MUXCODE_ANALYZE_MODEL"
	case "docs":
		return "MUXCODE_DOCS_MODEL"
	case "research":
		return "MUXCODE_RESEARCH_MODEL"
	case "watch":
		return "MUXCODE_WATCH_MODEL"
	default:
		return "MUXCODE_" + strings.ToUpper(role) + "_MODEL"
	}
}

// busRole returns the bus identity role, falling back to Role if BusRole is empty.
func (c Config) busRole() string {
	if c.BusRole != "" {
		return c.BusRole
	}
	return c.Role
}

// InboxPath returns the inbox file path for this role's bus identity.
func (c Config) InboxPath() string {
	return filepath.Join(c.BusDir, "inbox", c.busRole()+".jsonl")
}

// HistoryPath returns the history JSONL file path for this role's bus identity.
func (c Config) HistoryPath() string {
	return filepath.Join(c.BusDir, c.busRole()+"-history.jsonl")
}

// TurnTracePath returns the turn-trace JSONL file path, honoring the
// TurnTraceFile override.
func (c Config) TurnTracePath() string {
	if c.TurnTraceFile != "" {
		return c.TurnTraceFile
	}
	return filepath.Join(c.BusDir, c.busRole()+"-turn-trace.jsonl")
}

// findBusBin locates the muxcode binary.
func findBusBin() string {
	if p, err := exec.LookPath("muxcode"); err == nil {
		return p
	}
	// Fallback to common locations
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local", "bin", "muxcode"),
		"/usr/local/bin/muxcode",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "muxcode" // let PATH resolve it at runtime
}

// tmuxVar runs tmux display-message to get a variable value.
func tmuxVar(format string) string {
	args := []string{"display-message"}
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		args = append(args, "-t", pane)
	}
	args = append(args, "-p", format)
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
