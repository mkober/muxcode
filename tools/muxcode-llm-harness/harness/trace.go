package harness

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"
)

// Turn-trace outcomes (MUX-115). The split that matters is
// TraceOutcomeRejectedProfile versus everything else: four fix attempts
// could not observe whether the prompt-agent's turn budget went to
// profile-rejected probes or to some other cause, and these constants are
// the vocabulary that makes the distinction readable after a failed run.
const (
	TraceBatchStart             = "batch-start"
	TraceBatchEnd               = "batch-end"
	TraceOutcomeAccepted        = "accepted"          // tool executed, no error signature
	TraceOutcomeRejectedProfile = "rejected-profile"  // executor denied by tool profile (the probe rejection)
	TraceOutcomeBlockedFilter   = "blocked-filter"    // harness Filter block (inbox/self-send/approve-guard/repeat)
	TraceOutcomeError           = "error"             // tool ran but failed (non-zero exit, timeout, Error:)
	TraceOutcomeText            = "text-response"     // turn produced text, no tool calls
	TraceOutcomeForced          = "forced-tool-use"   // corrective prompt injected on first no-tool turn
	TraceOutcomeModelError      = "model-error"       // inference call failed or returned nothing
	TraceOutcomeAllBlocked      = "all-blocked-break" // consecutive all-blocked turns broke the loop
	TraceOutcomeSingleShot      = "single-shot-complete"
	TraceOutcomeExhausted       = "exhausted" // turn budget consumed without a final response
)

// traceMaxArgs and traceMaxDetail cap entry field sizes. Scrubbing runs
// BEFORE truncation — truncating first could split a secret mid-token so
// the redaction pattern no longer matches it.
const (
	traceMaxArgs   = 500
	traceMaxDetail = 300
)

// TraceEntry is one JSONL row in the turn-trace file. Turn is 1-based;
// batch-level rows (batch-start/batch-end) carry Turn 0.
type TraceEntry struct {
	Time    string `json:"time"`
	Batch   string `json:"batch,omitempty"`
	Action  string `json:"action,omitempty"`
	Turn    int    `json:"turn,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Args    string `json:"args,omitempty"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
}

// TurnTracer appends per-turn trace entries to a JSONL file. A nil
// tracer is the disabled state: every method no-ops on a nil receiver, so
// the batch loop is instrumented unconditionally and behaves identically
// with tracing off. Args and Detail are scrubbed through ScrubPII on
// every write — tool arguments can carry user prompt text.
//
// Each entry is written with its own open/append/close so the trace is
// on disk the moment the turn happens — readable after a failed or
// killed run without any flush or close path having run.
type TurnTracer struct {
	Path    string
	batchID string
	action  string
}

// NewTurnTracer creates a tracer writing to path.
func NewTurnTracer(path string) *TurnTracer {
	return &TurnTracer{Path: path}
}

// BatchStart records the start of a message batch and pins the batch
// identity (message ID + action) onto every subsequent entry, so a trace
// file spanning multiple intents attributes each turn to the request
// that consumed it.
func (t *TurnTracer) BatchStart(batchID, action string, maxTurns int) {
	if t == nil {
		return
	}
	t.batchID = batchID
	t.action = action
	t.write(TraceEntry{
		Outcome: TraceBatchStart,
		Detail:  "max_turns=" + strconv.Itoa(maxTurns),
	})
}

// BatchEnd records the batch outcome.
func (t *TurnTracer) BatchEnd(success bool, detail string) {
	if t == nil {
		return
	}
	t.write(TraceEntry{
		Outcome: TraceBatchEnd,
		Detail:  "success=" + strconv.FormatBool(success) + " " + detail,
	})
}

// ToolCall records one tool invocation within a turn.
func (t *TurnTracer) ToolCall(turn int, tool, args, outcome, detail string) {
	if t == nil {
		return
	}
	t.write(TraceEntry{
		Turn:    turn,
		Tool:    tool,
		Args:    args,
		Outcome: outcome,
		Detail:  detail,
	})
}

// TurnEvent records a turn-level outcome that consumed a turn without a
// tool call (text response, model error, forced tool use, exhaustion).
func (t *TurnTracer) TurnEvent(turn int, outcome, detail string) {
	if t == nil {
		return
	}
	t.write(TraceEntry{
		Turn:    turn,
		Outcome: outcome,
		Detail:  detail,
	})
}

func (t *TurnTracer) write(e TraceEntry) {
	e.Time = time.Now().Format(time.RFC3339)
	e.Batch = t.batchID
	e.Action = t.action
	e.Args = truncateTrace(scrubTrace(e.Args), traceMaxArgs)
	e.Detail = truncateTrace(scrubTrace(e.Detail), traceMaxDetail)

	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.OpenFile(t.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

func scrubTrace(s string) string {
	if s == "" {
		return s
	}
	out, _ := ScrubPII(s)
	return out
}

func truncateTrace(s string, max int) string {
	if runes := []rune(s); len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return s
}

// classifyToolOutcome names an executed tool call's outcome from its
// output text. The rejected-profile check must come first: a profile
// denial's output ("Error: command not allowed by tool profile: …") also
// matches the generic error signatures, and collapsing it into "error"
// would erase exactly the probe-vs-other split this trace exists to
// observe.
func classifyToolOutcome(output string) string {
	switch {
	case strings.Contains(output, "not allowed by tool profile"):
		return TraceOutcomeRejectedProfile
	case strings.Contains(output, "timed out") || toolHasNonZeroExit(output) || strings.HasPrefix(output, "Error:"):
		return TraceOutcomeError
	default:
		return TraceOutcomeAccepted
	}
}
