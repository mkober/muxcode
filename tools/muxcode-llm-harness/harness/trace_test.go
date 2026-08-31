package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readTraceEntries parses every JSONL row of a turn-trace file.
func readTraceEntries(t *testing.T, path string) []TraceEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace file: %v", err)
	}
	var entries []TraceEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e TraceEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad trace line %q: %v", line, err)
		}
		entries = append(entries, e)
	}
	return entries
}

// findOutcome returns the entries with the given outcome.
func findOutcome(entries []TraceEntry, outcome string) []TraceEntry {
	var out []TraceEntry
	for _, e := range entries {
		if e.Outcome == outcome {
			out = append(out, e)
		}
	}
	return out
}

// TestProcessBatch_TraceDistinguishesRejectedFromAccepted drives the real
// batch loop: turn 1 issues a profile-rejected command, turn 2 an accepted
// one, turn 3 a text response. The trace must name the two tool outcomes
// differently — the exact distinction four MUX-115 fix attempts could not
// observe.
func TestProcessBatch_TraceDistinguishesRejectedFromAccepted(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var resp ChatResponse
		switch callCount {
		case 1:
			resp = toolCallResponse("call_1", `{"command":"muxcode graph list"}`)
		case 2:
			resp = toolCallResponse("call_2", `{"command":"echo hello"}`)
		default:
			resp = ChatResponse{Choices: []ChatChoice{
				{Message: ChatMessage{Role: "assistant", Content: "Done: task complete"}},
			}}
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	dir := t.TempDir()
	cfg := Config{Role: "review", Session: "test", BusDir: dir, MaxTurns: 10}

	ollama := NewOllamaClient(server.URL, "test-model")
	executor := NewExecutor([]string{"Bash(echo *)"})
	tools := BuildToolDefs([]string{"Bash(echo *)"})
	filter := NewFilter("review")
	bus := &BusClient{BusDir: dir, Role: "review", BinPath: "echo"}
	msgs := []Message{{ID: "m1", From: "edit", To: "review", Action: "review", Payload: "run the task"}}

	tracePath := filepath.Join(dir, "review-turn-trace.jsonl")
	tracer := NewTurnTracer(tracePath)

	processBatch(context.Background(), cfg, bus, ollama, executor, tools, "system prompt", filter, msgs, &mockEventSink{}, tracer)

	entries := readTraceEntries(t, tracePath)

	rejected := findOutcome(entries, TraceOutcomeRejectedProfile)
	if len(rejected) != 1 {
		t.Fatalf("want 1 rejected-profile entry, got %d (entries: %+v)", len(rejected), entries)
	}
	if rejected[0].Turn != 1 || rejected[0].Tool != "bash" {
		t.Errorf("rejected entry = %+v, want turn 1 bash", rejected[0])
	}
	if !strings.Contains(rejected[0].Args, "muxcode graph list") {
		t.Errorf("rejected entry args = %q, want the rejected command", rejected[0].Args)
	}

	accepted := findOutcome(entries, TraceOutcomeAccepted)
	if len(accepted) != 1 {
		t.Fatalf("want 1 accepted entry, got %d", len(accepted))
	}
	if accepted[0].Turn != 2 {
		t.Errorf("accepted entry turn = %d, want 2", accepted[0].Turn)
	}

	if rejected[0].Outcome == accepted[0].Outcome {
		t.Error("rejected and accepted entries must be distinguishable by outcome")
	}

	if len(findOutcome(entries, TraceOutcomeText)) != 1 {
		t.Error("want a text-response entry for the final turn")
	}
	if len(findOutcome(entries, TraceBatchStart)) != 1 || len(findOutcome(entries, TraceBatchEnd)) != 1 {
		t.Error("want batch-start and batch-end entries")
	}
	for _, e := range entries {
		if e.Batch != "m1" {
			t.Errorf("entry %+v missing batch id m1", e)
		}
	}
}

// TestProcessBatch_TraceNamesExhaustion runs a batch whose every turn is a
// profile-rejected probe until the budget runs out, and asserts the trace
// attributes every turn AND names the exhaustion itself.
func TestProcessBatch_TraceNamesExhaustion(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req ChatRequest
		json.NewDecoder(r.Body).Decode(&req)

		var resp ChatResponse
		if len(req.Tools) == 0 {
			resp = ChatResponse{Choices: []ChatChoice{
				{Message: ChatMessage{Role: "assistant", Content: "Failed: every command was rejected"}},
			}}
		} else {
			// Distinct command per turn so the repetition filter never fires
			// and every turn's cause stays the profile rejection.
			resp = toolCallResponse(fmt.Sprintf("call_%d", callCount), fmt.Sprintf(`{"command":"muxcode graph status run-%d"}`, callCount))
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	dir := t.TempDir()
	cfg := Config{Role: "review", Session: "test", BusDir: dir, MaxTurns: 3}

	ollama := NewOllamaClient(server.URL, "test-model")
	executor := NewExecutor([]string{"Bash(echo *)"})
	tools := BuildToolDefs([]string{"Bash(echo *)"})
	filter := NewFilter("review")
	bus := &BusClient{BusDir: dir, Role: "review", BinPath: "echo"}
	msgs := []Message{{ID: "m1", From: "edit", To: "review", Action: "review", Payload: "run the task"}}

	tracePath := filepath.Join(dir, "review-turn-trace.jsonl")
	tracer := NewTurnTracer(tracePath)

	processBatch(context.Background(), cfg, bus, ollama, executor, tools, "system prompt", filter, msgs, &mockEventSink{}, tracer)

	entries := readTraceEntries(t, tracePath)

	rejected := findOutcome(entries, TraceOutcomeRejectedProfile)
	if len(rejected) != 3 {
		t.Fatalf("want 3 rejected-profile entries (one per turn), got %d", len(rejected))
	}
	for i, e := range rejected {
		if e.Turn != i+1 {
			t.Errorf("rejected entry %d has turn %d, want %d", i, e.Turn, i+1)
		}
	}

	exhausted := findOutcome(entries, TraceOutcomeExhausted)
	if len(exhausted) != 1 {
		t.Fatalf("want 1 exhausted entry, got %d (entries: %+v)", len(exhausted), entries)
	}
	if exhausted[0].Turn != 3 {
		t.Errorf("exhausted entry turn = %d, want maxTurns 3", exhausted[0].Turn)
	}
}

// TestProcessBatch_TracingOffNoFile is the negative control: the same
// batch flow with a nil tracer produces no trace file and the same
// number of model calls.
func TestProcessBatch_TracingOffNoFile(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var resp ChatResponse
		if callCount == 1 {
			resp = toolCallResponse("call_1", `{"command":"echo hello"}`)
		} else {
			resp = ChatResponse{Choices: []ChatChoice{
				{Message: ChatMessage{Role: "assistant", Content: "Done: task complete"}},
			}}
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	dir := t.TempDir()
	cfg := Config{Role: "review", Session: "test", BusDir: dir, MaxTurns: 10}

	ollama := NewOllamaClient(server.URL, "test-model")
	executor := NewExecutor([]string{"Bash(echo *)"})
	tools := BuildToolDefs([]string{"Bash(echo *)"})
	filter := NewFilter("review")
	bus := &BusClient{BusDir: dir, Role: "review", BinPath: "echo"}
	msgs := []Message{{ID: "m1", From: "edit", To: "review", Action: "review", Payload: "run the task"}}

	processBatch(context.Background(), cfg, bus, ollama, executor, tools, "system prompt", filter, msgs, &mockEventSink{}, nil)

	matches, _ := filepath.Glob(filepath.Join(dir, "*turn-trace*"))
	if len(matches) != 0 {
		t.Errorf("tracing off must produce no trace file, found %v", matches)
	}
	if callCount != 2 {
		t.Errorf("loop behaviour changed with tracing off: %d model calls, want 2", callCount)
	}
}

// TestTurnTracer_NilSafe pins the disabled state: every method no-ops on
// a nil receiver.
func TestTurnTracer_NilSafe(t *testing.T) {
	var tracer *TurnTracer
	tracer.BatchStart("id", "action", 10)
	tracer.ToolCall(1, "bash", `{"command":"echo hi"}`, TraceOutcomeAccepted, "")
	tracer.TurnEvent(1, TraceOutcomeText, "done")
	tracer.BatchEnd(true, "")
}

// TestTurnTracer_ScrubsPII asserts trace args and detail route through
// the PII scrub — tool arguments can carry user prompt text.
func TestTurnTracer_ScrubsPII(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")
	tracer := NewTurnTracer(path)
	tracer.BatchStart("m1", "prompt", 10)
	tracer.ToolCall(1, "bash",
		`{"command":"muxcode send edit chat 'mail bob@example.com the report'"}`,
		TraceOutcomeAccepted,
		"sent to bob@example.com with api_key=sk-abcdef1234567890")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "bob@example.com") {
		t.Error("trace file contains an unscrubbed email address")
	}
	if !strings.Contains(content, "[EMAIL_REDACTED]") {
		t.Error("trace file missing email redaction placeholder")
	}
	if strings.Contains(content, "sk-abcdef1234567890") {
		t.Error("trace file contains an unscrubbed secret")
	}
}

func TestClassifyToolOutcome(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{"profile rejection", "Error: command not allowed by tool profile: muxcode graph list", TraceOutcomeRejectedProfile},
		{"tool profile rejection non-bash", "Error: read_file not allowed by tool profile", TraceOutcomeRejectedProfile},
		{"non-zero exit", "fatal: not a git repo\nExit code: 128", TraceOutcomeError},
		{"timeout", "partial output\nError: command timed out after 60 seconds", TraceOutcomeError},
		{"error prefix", "Error: path is required", TraceOutcomeError},
		{"success", "hello\n", TraceOutcomeAccepted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyToolOutcome(tt.output); got != tt.want {
				t.Errorf("classifyToolOutcome(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

func TestConfig_TurnTraceEnvGate(t *testing.T) {
	t.Setenv("MUXCODE_HARNESS_TURN_TRACE", "")
	if DefaultConfig().TurnTrace {
		t.Error("TurnTrace must default off")
	}
	t.Setenv("MUXCODE_HARNESS_TURN_TRACE", "1")
	if !DefaultConfig().TurnTrace {
		t.Error("MUXCODE_HARNESS_TURN_TRACE=1 must enable TurnTrace")
	}
}

func TestConfig_TurnTracePath(t *testing.T) {
	cfg := Config{Role: "prompt", BusDir: "/tmp/bus"}
	if got := cfg.TurnTracePath(); got != "/tmp/bus/prompt-turn-trace.jsonl" {
		t.Errorf("default path = %q", got)
	}
	cfg.TurnTraceFile = "/tmp/custom.jsonl"
	if got := cfg.TurnTracePath(); got != "/tmp/custom.jsonl" {
		t.Errorf("override path = %q", got)
	}
	cfg = Config{Role: "prompt", BusRole: "prompt2", BusDir: "/tmp/bus"}
	if got := cfg.TurnTracePath(); got != "/tmp/bus/prompt2-turn-trace.jsonl" {
		t.Errorf("bus-role path = %q", got)
	}
}

// toolCallResponse builds a single-tool-call chat response for mock servers.
func toolCallResponse(id, argsJSON string) ChatResponse {
	return ChatResponse{Choices: []ChatChoice{
		{Message: ChatMessage{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:   id,
				Type: "function",
				Function: FunctionCall{
					Name:      "bash",
					Arguments: json.RawMessage(argsJSON),
				},
			}},
		}},
	}}
}
