package harness

import (
	"fmt"
	"io"
	"os"
	"time"
)

// EventKind identifies the type of activity event emitted by the harness loop.
type EventKind int

const (
	EventStartup         EventKind = iota // harness connected, ready
	EventMessageReceived                  // incoming bus message
	EventBatchStart                       // processing N messages
	EventOllamaCall                       // calling Ollama (turn N/M)
	EventOllamaResponse                   // Ollama responded (duration, tool count)
	EventToolStart                        // executing tool
	EventToolComplete                     // tool finished (duration, exit info)
	EventToolOutput                       // truncated tool output preview
	EventToolBlocked                      // tool call blocked by filter
	EventTextResponse                     // LLM text (no tool calls)
	EventBatchComplete                    // response sent
	EventCooldown                         // consecutive failure cooldown
	EventError                            // error condition
	EventForceToolUse                     // injected corrective prompt
	EventNarrationRetry                   // requesting factual summary
	EventAllBlocked                       // all tool calls blocked in turn
	EventUserInput                        // user submitted chat message
	EventChatResponse                     // model responded to user chat
)

// Event represents a single harness activity event.
type Event struct {
	Kind    EventKind
	Time    time.Time
	Message string // pre-formatted one-line description
}

// EventSink receives events from the harness loop. Implementations render
// them to stderr (headless) or the TUI display.
type EventSink interface {
	Emit(Event)
	Close()
}

// --- LogSink: headless mode (writes to stderr, same as current behavior) ---

// LogSink writes events to an io.Writer with a [tag] prefix, matching the
// existing harness stderr log format.
type LogSink struct {
	W   io.Writer
	Tag string // log prefix (typically the model name)
}

// NewLogSink creates a LogSink that writes to stderr.
func NewLogSink(tag string) *LogSink {
	return &LogSink{W: os.Stderr, Tag: tag}
}

func (s *LogSink) Emit(e Event) {
	switch e.Kind {
	case EventMessageReceived:
		// Message-received lines use a different format (no [tag] prefix)
		fmt.Fprintf(s.W, "\n%s\n", e.Message)
	default:
		fmt.Fprintf(s.W, "[%s] %s\n", s.Tag, e.Message)
	}
}

func (s *LogSink) Close() {}
