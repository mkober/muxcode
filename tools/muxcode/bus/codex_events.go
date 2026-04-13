package bus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// --- Codex JSONL event types ---
// Parsed from `codex exec --json` output.
// See: https://github.com/openai/codex

// CodexEventType enumerates the known Codex JSONL event types.
type CodexEventType string

const (
	CodexEventThreadStarted CodexEventType = "thread.started"
	CodexEventTurnStarted   CodexEventType = "turn.started"
	CodexEventTurnCompleted CodexEventType = "turn.completed"
	CodexEventTurnFailed    CodexEventType = "turn.failed"
	CodexEventItemStarted   CodexEventType = "item.started"
	CodexEventItemCompleted CodexEventType = "item.completed"
	CodexEventError         CodexEventType = "error"
)

// CodexEvent is the top-level JSONL event envelope.
type CodexEvent struct {
	Type     CodexEventType `json:"type"`
	ThreadID string         `json:"thread_id,omitempty"`
	Message  string         `json:"message,omitempty"` // for error events
	Item     *CodexItem     `json:"item,omitempty"`
	Usage    *CodexUsage    `json:"usage,omitempty"`
	Error    *CodexError    `json:"error,omitempty"` // for turn.failed
}

// CodexItem represents an item in a Codex event stream.
// Items can be agent messages or command executions.
type CodexItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"` // "agent_message" or "command_execution"
	Text             string `json:"text,omitempty"`
	Command          string `json:"command,omitempty"`
	AggregatedOutput string `json:"aggregated_output,omitempty"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	Status           string `json:"status,omitempty"` // "in_progress", "completed"
}

// CodexUsage tracks token consumption for a turn.
type CodexUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

// CodexError holds error details from a failed turn or error event.
type CodexError struct {
	Message string `json:"message,omitempty"`
}

// --- JSONL parsing ---

// ParseCodexEvent parses a single JSONL line into a CodexEvent.
// Returns nil if the line is empty or not valid JSON.
func ParseCodexEvent(line string) *CodexEvent {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	var ev CodexEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return nil
	}
	return &ev
}

// ParseCodexEvents parses multiple JSONL lines into a slice of CodexEvents.
// Skips invalid or empty lines.
func ParseCodexEvents(data string) []CodexEvent {
	lines := strings.Split(data, "\n")
	events := make([]CodexEvent, 0, len(lines))
	for _, line := range lines {
		if ev := ParseCodexEvent(line); ev != nil {
			events = append(events, *ev)
		}
	}
	return events
}

// --- Event stream analysis ---

// CodexTaskResult summarizes the outcome of a Codex exec session
// extracted from its JSONL event stream.
type CodexTaskResult struct {
	ThreadID     string      // session thread ID
	Completed    bool        // true if turn.completed received
	Errored      bool        // true if errors detected
	Messages     []string    // agent message texts in order
	Commands     []CodexCmd  // commands executed
	ErrorMessage string      // first error message
	Usage        *CodexUsage // token usage from turn.completed
}

// CodexCmd represents a single command execution from the event stream.
type CodexCmd struct {
	Command  string
	Output   string
	ExitCode int
	Success  bool
}

// AnalyzeCodexEvents processes a stream of Codex events and produces
// a structured task result. This is the primary entry point for
// extracting meaning from a codex exec --json run.
func AnalyzeCodexEvents(events []CodexEvent) CodexTaskResult {
	var result CodexTaskResult

	for _, ev := range events {
		switch ev.Type {
		case CodexEventThreadStarted:
			result.ThreadID = ev.ThreadID

		case CodexEventItemCompleted:
			if ev.Item == nil {
				continue
			}
			switch ev.Item.Type {
			case "agent_message":
				if ev.Item.Text != "" {
					result.Messages = append(result.Messages, ev.Item.Text)
				}
			case "command_execution":
				cmd := CodexCmd{
					Command: ev.Item.Command,
					Output:  ev.Item.AggregatedOutput,
				}
				if ev.Item.ExitCode != nil {
					cmd.ExitCode = *ev.Item.ExitCode
					cmd.Success = *ev.Item.ExitCode == 0
				}
				result.Commands = append(result.Commands, cmd)
				// Non-zero exit code = error
				if ev.Item.ExitCode != nil && *ev.Item.ExitCode != 0 {
					result.Errored = true
					if result.ErrorMessage == "" {
						result.ErrorMessage = ev.Item.Command + " exited with code " + strings.TrimSpace(ev.Item.AggregatedOutput)
					}
				}
			}

		case CodexEventTurnCompleted:
			result.Completed = true
			if ev.Usage != nil {
				result.Usage = ev.Usage
			}

		case CodexEventTurnFailed:
			result.Completed = true
			result.Errored = true
			if ev.Error != nil && result.ErrorMessage == "" {
				result.ErrorMessage = ev.Error.Message
			}

		case CodexEventError:
			result.Errored = true
			if ev.Message != "" && result.ErrorMessage == "" {
				result.ErrorMessage = ev.Message
			}
		}
	}

	return result
}

// FormatCodexResult produces a concise human-readable summary of a
// CodexTaskResult suitable for bus message replies.
func FormatCodexResult(r CodexTaskResult) string {
	var b strings.Builder

	// Status line
	if r.Errored {
		b.WriteString("❌ Codex task failed")
		if r.ErrorMessage != "" {
			b.WriteString(": ")
			// Truncate long error messages
			msg := r.ErrorMessage
			if len(msg) > 200 {
				msg = msg[:200] + "..."
			}
			b.WriteString(msg)
		}
		b.WriteString("\n")
	} else if r.Completed {
		b.WriteString("✅ Codex task completed\n")
	}

	// Commands summary
	if len(r.Commands) > 0 {
		successCount := 0
		failCount := 0
		for _, cmd := range r.Commands {
			if cmd.Success {
				successCount++
			} else {
				failCount++
			}
		}
		b.WriteString("Commands: ")
		if failCount == 0 {
			b.WriteString(fmt.Sprintf("%d executed, all succeeded\n", len(r.Commands)))
		} else {
			b.WriteString(fmt.Sprintf("%d executed, %d failed\n", len(r.Commands), failCount))
		}
	}

	// Last agent message (the final answer)
	if len(r.Messages) > 0 {
		lastMsg := r.Messages[len(r.Messages)-1]
		// Truncate for bus reply
		if len(lastMsg) > 500 {
			lastMsg = lastMsg[:500] + "..."
		}
		b.WriteString(lastMsg)
		b.WriteString("\n")
	}

	// Token usage
	if r.Usage != nil {
		b.WriteString(fmt.Sprintf("Tokens: %d in (%d cached), %d out\n",
			r.Usage.InputTokens, r.Usage.CachedInputTokens, r.Usage.OutputTokens))
	}

	return strings.TrimRight(b.String(), "\n")
}

// --- Exec mode runner ---

// RunCodexExec runs `codex exec --json --full-auto` with the given prompt
// and model, parses the JSONL output, and returns a structured result.
// This is the programmatic entry point for non-interactive Codex usage.
func RunCodexExec(prompt, model string) (CodexTaskResult, error) {
	args := []string{"exec", "--json", "--full-auto"}
	if model != "" {
		args = append(args, "-m", model)
	}
	args = append(args, prompt)

	cmd := exec.Command("codex", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Parse JSONL regardless of exit code — codex may output events before failing
	events := ParseCodexEvents(stdout.String())
	result := AnalyzeCodexEvents(events)

	// If no events parsed but command failed, create an error result
	if len(events) == 0 && err != nil {
		result.Errored = true
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		result.ErrorMessage = strings.TrimSpace(errMsg)
	}

	return result, err
}
