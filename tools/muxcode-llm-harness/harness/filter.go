package harness

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// Filter checks tool calls before execution, blocking inbox checks, self-sends,
// and repetitive commands. This is the core harness logic that prevents small
// LLMs from getting stuck in loops.
type Filter struct {
	Role       string
	CallCounts map[string]int // command hash → count per batch
	MaxRepeat  int            // max times same command can repeat (default 3)
}

// NewFilter creates a new filter for the given role.
func NewFilter(role string) *Filter {
	return &Filter{
		Role:       role,
		CallCounts: make(map[string]int),
		MaxRepeat:  3,
	}
}

// Reset clears the call counts for a new batch of messages.
func (f *Filter) Reset() {
	f.CallCounts = make(map[string]int)
}

// FilterResult holds the result of a filter check.
type FilterResult struct {
	Blocked bool
	Reason  string
}

// Check examines a tool call and returns whether it should be blocked.
// Returns (blocked, reason). If blocked, the reason is a corrective message
// that should be returned to the LLM as the tool result.
func (f *Filter) Check(tc ToolCall) FilterResult {
	name := tc.Function.Name

	// Only filter bash commands — other tools don't cause loops
	if name != "bash" {
		return FilterResult{Blocked: false}
	}

	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(tc.Function.Arguments, &args); err != nil {
		return FilterResult{Blocked: false} // let executor handle bad JSON
	}

	command := strings.TrimSpace(args.Command)

	// Rule 1: Block inbox checks
	if isInboxCommand(command) {
		return FilterResult{
			Blocked: true,
			Reason:  "BLOCKED: Messages already delivered in this conversation. Your task is in the user message above. Execute it now — do NOT check the inbox.",
		}
	}

	// Rule 2: Block self-send
	if isSelfSend(command, f.Role) {
		return FilterResult{
			Blocked: true,
			Reason:  fmt.Sprintf("BLOCKED: Cannot send messages to yourself (%s). Send to the requesting agent instead.", f.Role),
		}
	}

	// Rule 3: Block repetition (same command hash 3+ times)
	hash := commandHash(command)
	f.CallCounts[hash]++
	if f.CallCounts[hash] >= f.MaxRepeat {
		return FilterResult{
			Blocked: true,
			Reason:  fmt.Sprintf("BLOCKED: Command executed %d times already — you are stuck in a loop. Try a completely different approach or provide your final response.", f.CallCounts[hash]),
		}
	}

	return FilterResult{Blocked: false}
}

// isInboxCommand detects attempts to check the bus inbox.
func isInboxCommand(command string) bool {
	// Exact match or prefix match
	if command == "muxcode-agent-bus inbox" {
		return true
	}
	if strings.HasPrefix(command, "muxcode-agent-bus inbox ") {
		return true
	}
	// Also catch common variations
	if strings.Contains(command, "agent-bus inbox") {
		return true
	}
	return false
}

// isSelfSend detects attempts to send messages to the agent's own role.
func isSelfSend(command, role string) bool {
	// Check for "muxcode-agent-bus send <role>"
	prefix := "muxcode-agent-bus send " + role
	if strings.HasPrefix(command, prefix+" ") || command == prefix {
		return true
	}
	// Also match with path prefix
	if strings.Contains(command, "agent-bus send "+role+" ") {
		return true
	}
	return false
}

// commandHash returns a short hash of a command for dedup tracking.
func commandHash(command string) string {
	// Normalize: trim whitespace, collapse multiple spaces
	normalized := strings.Join(strings.Fields(command), " ")
	h := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", h[:8])
}
