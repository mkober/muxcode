package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// BusClient wraps the muxcode-agent-bus CLI for bus operations.
type BusClient struct {
	BinPath   string
	Session   string
	Role      string // bus identity role (for inbox, lock, send, history)
	AgentRole string // agent definition role (for tools, skills, context)
	BusDir    string
}

// NewBusClient creates a bus client from harness config.
func NewBusClient(cfg Config) *BusClient {
	busRole := cfg.BusRole
	if busRole == "" {
		busRole = cfg.Role
	}
	return &BusClient{
		BinPath:   cfg.BusBin,
		Session:   cfg.Session,
		Role:      busRole,
		AgentRole: cfg.Role,
		BusDir:    cfg.BusDir,
	}
}

// HasMessages checks if the inbox file has content (non-empty).
func (b *BusClient) HasMessages(inboxPath string) bool {
	info, err := os.Stat(inboxPath)
	if err != nil {
		return false
	}
	return info.Size() > 0
}

// ConsumeInbox reads and consumes all pending inbox messages via the bus CLI.
// Returns parsed messages. The CLI atomically consumes the inbox.
func (b *BusClient) ConsumeInbox() ([]Message, error) {
	out, err := b.run("inbox", "--raw")
	if err != nil {
		// No messages is not an error
		if strings.Contains(out, "No messages") || strings.TrimSpace(out) == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("inbox: %w: %s", err, out)
	}
	out = strings.TrimSpace(out)
	if out == "" || strings.Contains(out, "No messages") {
		return nil, nil
	}
	return ParseMessages(out)
}

// Send sends a message via the bus CLI.
func (b *BusClient) Send(to, action, payload, msgType, replyTo string) error {
	args := []string{"send", to, action, payload}
	if msgType != "" {
		args = append(args, "--type", msgType)
	}
	if replyTo != "" {
		args = append(args, "--reply-to", replyTo)
	}
	out, err := b.run(args...)
	if err != nil {
		return fmt.Errorf("send: %w: %s", err, out)
	}
	return nil
}

// Lock marks this role as busy.
func (b *BusClient) Lock() error {
	out, err := b.run("lock", b.Role)
	if err != nil {
		return fmt.Errorf("lock: %w: %s", err, out)
	}
	return nil
}

// Unlock marks this role as idle.
func (b *BusClient) Unlock() error {
	out, err := b.run("unlock", b.Role)
	if err != nil {
		return fmt.Errorf("unlock: %w: %s", err, out)
	}
	return nil
}

// ResolveTools gets the allowed tool patterns for the agent definition role.
func (b *BusClient) ResolveTools() ([]string, error) {
	out, err := b.run("tools", b.AgentRole)
	if err != nil {
		return nil, fmt.Errorf("tools: %w: %s", err, out)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	var patterns []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			patterns = append(patterns, line)
		}
	}
	return patterns, nil
}

// SkillPrompt returns the skills prompt for the agent definition role.
func (b *BusClient) SkillPrompt() (string, error) {
	out, err := b.run("skill", "prompt", b.AgentRole)
	if err != nil {
		return "", nil // skills are optional
	}
	return strings.TrimSpace(out), nil
}

// ContextPrompt returns the context.d prompt for the agent definition role.
func (b *BusClient) ContextPrompt() (string, error) {
	out, err := b.run("context", "prompt", b.AgentRole)
	if err != nil {
		return "", nil // context is optional
	}
	return strings.TrimSpace(out), nil
}

// LogHistory appends a bash command execution to the role's history JSONL.
func (b *BusClient) LogHistory(command, output, exitCode, outcome string) error {
	historyPath := b.BusDir + "/" + b.Role + "-history.jsonl"

	// Truncate output for history
	if len(output) > 2000 {
		output = output[:2000] + "..."
	}

	entry := map[string]interface{}{
		"ts":        time.Now().Unix(),
		"summary":   command,
		"exit_code": exitCode,
		"command":   command,
		"output":    output,
		"outcome":   outcome,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(historyPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// run executes a bus CLI command and returns stdout.
func (b *BusClient) run(args ...string) (string, error) {
	cmd := exec.Command(b.BinPath, args...)
	cmd.Env = append(os.Environ(),
		"BUS_SESSION="+b.Session,
		"AGENT_ROLE="+b.Role,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
