package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// mockEventSink is a no-op EventSink for testing
type mockEventSink struct{}

func (m *mockEventSink) Emit(e Event) {}
func (m *mockEventSink) Close()       {}

func TestProcessBatch_SimpleResponse(t *testing.T) {
	// Set up mock Ollama that returns a text response (no tool calls)
	server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatResponse{
			Choices: []ChatChoice{
				{Message: ChatMessage{Role: "assistant", Content: "Task completed successfully"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	dir := t.TempDir()
	cfg := Config{
		Role:     "commit",
		Session:  "test",
		BusDir:   dir,
		MaxTurns: 10,
	}

	ollama := NewOllamaClient(server.URL, "test-model")
	ollama.HTTP = server.Client()
	executor := NewExecutor([]string{"Bash(git *)", "Read"})
	tools := BuildToolDefs([]string{"Bash(git *)", "Read"})
	filter := NewFilter("commit")

	// Create a fake bus client that captures send calls
	bus := &BusClient{BusDir: dir, Role: "commit", BinPath: "echo"} // echo as a no-op

	msgs := []Message{
		{ID: "1", From: "edit", To: "commit", Action: "status", Payload: "Show git status"},
	}

	// This will fail on bus.Send because echo isn't the real bus binary,
	// but we can verify the conversation logic works
	processBatch(context.Background(), cfg, bus, ollama, executor, tools, "system prompt", filter, msgs, &mockEventSink{}, nil)

	// If we got here without panic, the conversation loop worked
}

// recordingBus is a BusClient whose binary is a script appending each call's
// argv, one argument per line, to a log; calls(verb) returns the calls whose
// first argument is verb ("send", "lifecycle").
func recordingBus(t *testing.T, role string) (*BusClient, func(verb string) [][]string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	bin := filepath.Join(dir, "muxcode")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> '" + log + "'; done\nprintf -- '--end--\\n' >> '" + log + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := func(verb string) [][]string {
		data, _ := os.ReadFile(log)
		var calls [][]string
		for _, call := range strings.Split(strings.TrimSuffix(string(data), "--end--\n"), "--end--\n") {
			if args := strings.Split(strings.TrimSuffix(call, "\n"), "\n"); len(args) > 0 && args[0] == verb {
				calls = append(calls, args)
			}
		}
		return calls
	}
	return &BusClient{BusDir: dir, Role: role, BinPath: bin}, calls
}

// toolLoopModel is a stub model that never stops calling tools — a distinct
// `echo` each call, so the filter's repeat guard never steps in — counting
// its calls.
func toolLoopModel(t *testing.T) (*OllamaClient, *int) {
	t.Helper()
	n := 0
	server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		json.NewEncoder(w).Encode(ChatResponse{Choices: []ChatChoice{{Message: ChatMessage{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "call", Type: "function",
			Function: FunctionCall{Name: "bash", Arguments: json.RawMessage(`{"command":"echo turn ` + strconv.Itoa(n) + `"}`)},
		}}}}}})
	}))
	t.Cleanup(server.Close)
	ollama := NewOllamaClient(server.URL, "test-model")
	ollama.HTTP = server.Client()
	return ollama, &n
}

// MUX-110 Phase 1: a batch whose model never stops calling tools is terminal
// and visible — its request is answered once with the exhaustion text under
// its own id (which marks it responded, so nothing re-drives it), exactly one
// harness-tool-loop-exhausted row names the role, the request and the turn
// budget, the model is called a bounded number of times, and processBatch
// reports failure — the empty-answer case once read as a success and reset
// the circuit breaker. Negative control: a batch answered within budget
// writes no exhaustion row, is answered as before and reports success.
func TestProcessBatch_ExhaustionIsTerminalAndLogged(t *testing.T) {
	request := Message{ID: "R1", From: "edit", To: "commit", Type: "request", Action: "commit", Payload: "Review your context"}
	for _, c := range []struct {
		name      string
		exhausted bool
		model     string // "loop": never stops calling tools; "empty": answers empty text; "text": answers
		turns     string // the row's turns-used wording
	}{
		{"exhausted", true, "loop", "after 3 of its 3 turns"},
		{"empty answers: names the turns used, not the budget", true, "empty", "after 2 of its 3 turns"}, // turn 1 is the forced tool-use retry
		{"control: answered within budget", false, "text", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			bus, calls := recordingBus(t, "commit")
			cfg := Config{Role: "commit", Session: "test", BusDir: bus.BusDir, MaxTurns: 3}
			model, n := textModel(t), new(int)
			switch c.model {
			case "loop":
				model, n = toolLoopModel(t)
			case "empty":
				model = replyModel(t, "")
			}
			tools := []string{"Bash(echo *)"}
			success := processBatch(context.Background(), cfg, bus, model, NewExecutor(tools), BuildToolDefs(tools), "system prompt", NewFilter("commit"), []Message{request}, &mockEventSink{}, nil)
			if success == c.exhausted {
				t.Errorf("processBatch success = %v, want %v — an exhausted batch must count as failed for the breaker", success, !c.exhausted)
			}

			sends, rows := calls("send"), calls("lifecycle")
			if len(sends) != 1 {
				t.Fatalf("sends = %v, want exactly one reply", sends)
			}
			reply := sends[0]
			if n := len(reply); n < 7 || reply[1] != "edit" || reply[n-1] != "R1" || reply[n-2] != "--reply-to" {
				t.Errorf("reply = %q, want one to edit under R1", reply)
			}
			if !c.exhausted {
				if len(rows) != 0 || reply[3] == toolLoopExhausted {
					t.Errorf("an answered batch: lifecycle calls %v, reply %q — want none and the model's answer", rows, reply[3])
				}
				return
			}
			if reply[3] != toolLoopExhausted {
				t.Errorf("reply payload = %q, want the exhaustion text", reply[3])
			}
			if len(rows) != 1 || rows[0][5] != "harness-tool-loop-exhausted" || !strings.Contains(rows[0][7], "commit: request R1 answered with the exhaustion text "+c.turns) {
				t.Errorf("lifecycle calls = %q, want one harness-tool-loop-exhausted row naming commit, R1 and %q", rows, c.turns)
			}
			if *n > cfg.MaxTurns+1 {
				t.Errorf("model called %d times for a %d-turn budget — the loop is not bounded", *n, cfg.MaxTurns)
			}
		})
	}
}

// textModel is a stub model endpoint answering every turn with plain text.
func textModel(t *testing.T) *OllamaClient {
	t.Helper()
	return replyModel(t, "Done. EXIT=0")
}

// replyModel is a stub model endpoint answering every turn with content.
func replyModel(t *testing.T, content string) *OllamaClient {
	t.Helper()
	server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ChatResponse{Choices: []ChatChoice{{Message: ChatMessage{Role: "assistant", Content: content}}}})
	}))
	t.Cleanup(server.Close)
	ollama := NewOllamaClient(server.URL, "test-model")
	ollama.HTTP = server.Client()
	return ollama
}

// MUX-111: every reply's recipient, action and correlation id come from one
// request, never from whichever message arrived last (Phase 2), and each
// request in the batch gets its own reply (Phase 4) — from two senders, from
// one sender twice (MUX-222's case), and beside the startup bootstrap, which
// as the batch's last request once took the only reply. A request's
// ReplyRole still names the recipient (MUX-213); a batch holding no request
// gets no reply. Negative control: a lone request is answered exactly as
// before — the fix is not "never reply".
func TestProcessBatch_ReplyCorrelatesToTheRequest(t *testing.T) {
	request := Message{ID: "R1", From: "edit", To: "commit", Type: "request", Action: "commit", Payload: "Commit the change"}
	response := Message{ID: "P1", From: "build", To: "commit", Type: "response", Action: "build", Payload: "Build OK"}
	for _, c := range []struct {
		name string
		msgs []Message
		want [][3]string // per reply, in order: to, action, reply-to id; empty = no reply
	}{
		{"control: a lone request", []Message{request}, [][3]string{{"edit", "commit", "R1"}}},
		{"[request, response]: answers the request", []Message{request, response}, [][3]string{{"edit", "commit", "R1"}}},
		{"[response, request]: answers the request", []Message{response, request}, [][3]string{{"edit", "commit", "R1"}}},
		{"a request's ReplyRole names the recipient",
			[]Message{{ID: "R2", From: "test", To: "commit", Type: "request", Action: "review", ReplyRole: "edit", Payload: "Review"}, response},
			[][3]string{{"edit", "review", "R2"}}},
		{"no request in the batch: no reply", []Message{response, {ID: "E1", From: "daemon", To: "commit", Type: "event", Action: "notify", Payload: "fyi"}}, nil},
		{"two requests from two senders: one reply each",
			[]Message{request, response, {ID: "R3", From: "plan", To: "commit", Type: "request", Action: "pr-read", Payload: "Read the PR"}},
			[][3]string{{"edit", "commit", "R1"}, {"plan", "pr-read", "R3"}}},
		{"two requests from one sender: each answered once",
			[]Message{request, {ID: "R4", From: "edit", To: "commit", Type: "request", Action: "commit", Payload: "And push"}},
			[][3]string{{"edit", "commit", "R1"}, {"edit", "commit", "R4"}}},
		{"[request, startup bootstrap]: the request is still answered",
			[]Message{request, {ID: "B1", From: "commit", To: "commit", Type: "request", Action: "startup", Payload: "bootstrap"}},
			[][3]string{{"edit", "commit", "R1"}, {"commit", "startup", "B1"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			bus, recorded := recordingBus(t, "commit")
			cfg := Config{Role: "commit", Session: "test", BusDir: bus.BusDir, MaxTurns: 3}
			processBatch(context.Background(), cfg, bus, textModel(t), NewExecutor(nil), BuildToolDefs(nil), "system prompt", NewFilter("commit"), c.msgs, &mockEventSink{}, nil)

			calls := recorded("send")
			if len(calls) != len(c.want) {
				t.Fatalf("send calls = %v, want %d", calls, len(c.want))
			}
			for i, got := range calls {
				w := c.want[i]
				if n := len(got); n < 7 || got[1] != w[0] || got[2] != w[1] || got[n-2] != "--reply-to" || got[n-1] != w[2] {
					t.Errorf("reply %d = %q, want to=%s action=%s --reply-to %s", i, got, w[0], w[1], w[2])
				}
			}
		})
	}
}

// MUX-111 Phase 3: an inbox the CLI did not filter — an older binary's,
// played here by a bus script printing the raw rows — holds the agent's own
// response; ConsumeInbox drops it, and the batch's reply goes to edit, never
// to the agent itself. Control: the launch-time request:startup bootstrap is
// self-addressed by design and is kept, so it can still be answered.
func TestConsumeInbox_SelfAddressedNeverAnsweredToSelf(t *testing.T) {
	dir := t.TempDir()
	rows := `{"id":"R1","from":"edit","to":"commit","type":"request","action":"commit","payload":"Commit it"}
{"id":"S1","from":"commit","to":"commit","type":"response","action":"commit","payload":"my own earlier reply"}
{"id":"B1","from":"commit","to":"commit","type":"request","action":"startup","payload":"bootstrap"}
`
	inbox := filepath.Join(dir, "rows.jsonl")
	if err := os.WriteFile(inbox, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "calls.log")
	bin := filepath.Join(dir, "muxcode")
	script := "#!/bin/sh\nif [ \"$1\" = inbox ]; then cat '" + inbox + "'; exit 0; fi\nprintf '%s\\n' \"$@\" >> '" + log + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	bus := &BusClient{BusDir: dir, Role: "commit", BinPath: bin}

	msgs, err := bus.ConsumeInbox()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "R1,B1" {
		t.Fatalf("consumed %v, want R1 and the startup bootstrap B1 — the self-addressed response S1 dropped", ids)
	}

	cfg := Config{Role: "commit", Session: "test", BusDir: dir, MaxTurns: 3}
	processBatch(context.Background(), cfg, bus, textModel(t), NewExecutor(nil), BuildToolDefs(nil), "system prompt", NewFilter("commit"), msgs[:1], &mockEventSink{}, nil)
	sent, _ := os.ReadFile(log)
	if lines := strings.Split(string(sent), "\n"); len(lines) < 2 || lines[0] != "send" || lines[1] != "edit" {
		t.Errorf("reply argv = %q, want a send to edit", sent)
	}
	if strings.Contains(string(sent), "S1") {
		t.Errorf("the reply references the agent's own response: %q", sent)
	}
}

func TestProcessBatch_WithToolCall(t *testing.T) {
	callCount := 0
	server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req ChatRequest
		json.NewDecoder(r.Body).Decode(&req)

		if callCount == 1 {
			// First call: return a tool call
			resp := ChatResponse{
				Choices: []ChatChoice{
					{
						Message: ChatMessage{
							Role: "assistant",
							ToolCalls: []ToolCall{
								{
									ID:   "call_1",
									Type: "function",
									Function: FunctionCall{
										Name:      "bash",
										Arguments: json.RawMessage(`{"command":"echo hello"}`),
									},
								},
							},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		} else {
			// Second call: return text response
			resp := ChatResponse{
				Choices: []ChatChoice{
					{Message: ChatMessage{Role: "assistant", Content: "Done: hello"}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	cfg := Config{
		Role:     "commit",
		Session:  "test",
		BusDir:   dir,
		MaxTurns: 10,
	}

	ollama := NewOllamaClient(server.URL, "test-model")
	ollama.HTTP = server.Client()
	executor := NewExecutor([]string{"Bash(echo *)"})
	tools := BuildToolDefs([]string{"Bash(echo *)"})
	filter := NewFilter("commit")
	bus := &BusClient{BusDir: dir, Role: "commit", BinPath: "echo"}

	msgs := []Message{
		{ID: "1", From: "edit", To: "commit", Action: "test", Payload: "Run echo hello"},
	}

	processBatch(context.Background(), cfg, bus, ollama, executor, tools, "system prompt", filter, msgs, &mockEventSink{}, nil)

	if callCount != 2 {
		t.Errorf("expected 2 Ollama calls, got %d", callCount)
	}
}

func TestProcessBatch_FilterBlocksInbox(t *testing.T) {
	callCount := 0
	server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		if callCount == 1 {
			// LLM tries to check inbox
			resp := ChatResponse{
				Choices: []ChatChoice{
					{
						Message: ChatMessage{
							Role: "assistant",
							ToolCalls: []ToolCall{
								{
									ID:   "call_1",
									Type: "function",
									Function: FunctionCall{
										Name:      "bash",
										Arguments: json.RawMessage(`{"command":"muxcode inbox"}`),
									},
								},
							},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		} else {
			// After blocked, LLM should provide text response
			resp := ChatResponse{
				Choices: []ChatChoice{
					{Message: ChatMessage{Role: "assistant", Content: "I see, executing the task..."}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	cfg := Config{
		Role:     "commit",
		Session:  "test",
		BusDir:   dir,
		MaxTurns: 10,
	}

	ollama := NewOllamaClient(server.URL, "test-model")
	ollama.HTTP = server.Client()
	executor := NewExecutor([]string{"Bash(muxcode *)"})
	tools := BuildToolDefs([]string{"Bash(muxcode *)"})
	filter := NewFilter("commit")
	bus := &BusClient{BusDir: dir, Role: "commit", BinPath: "echo"}

	msgs := []Message{
		{ID: "1", From: "edit", To: "commit", Action: "status", Payload: "Show status"},
	}

	processBatch(context.Background(), cfg, bus, ollama, executor, tools, "system prompt", filter, msgs, &mockEventSink{}, nil)

	// Should have called Ollama twice: once with tool call, once after block
	if callCount < 2 {
		t.Errorf("expected at least 2 Ollama calls, got %d", callCount)
	}
}

func TestLogToolToHistory(t *testing.T) {
	dir := t.TempDir()
	bus := &BusClient{BusDir: dir, Role: "commit"}

	tc := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"git status"}`),
		},
	}

	logToolToHistory(bus, tc, "On branch main\nnothing to commit")

	data, err := os.ReadFile(filepath.Join(dir, "commit-history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	var entry map[string]interface{}
	json.Unmarshal(data[:len(data)-1], &entry)

	if entry["command"] != "git status" {
		t.Errorf("command = %q", entry["command"])
	}
	if entry["outcome"] != "success" {
		t.Errorf("outcome = %q", entry["outcome"])
	}
}

func TestLogToolToHistory_Failure(t *testing.T) {
	dir := t.TempDir()
	bus := &BusClient{BusDir: dir, Role: "commit"}

	tc := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"git push"}`),
		},
	}

	logToolToHistory(bus, tc, "error: failed to push\nExit code: 1")

	data, _ := os.ReadFile(filepath.Join(dir, "commit-history.jsonl"))
	var entry map[string]interface{}
	json.Unmarshal(data[:len(data)-1], &entry)

	if entry["outcome"] != "failure" {
		t.Errorf("outcome = %q, want failure", entry["outcome"])
	}
	if entry["exit_code"] != "1" {
		t.Errorf("exit_code = %q, want 1", entry["exit_code"])
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	// Verify Run exits cleanly when context is cancelled
	server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[{"name":"test-model"}]}`))
			return
		}
	}))
	defer server.Close()

	cfg := Config{
		Role:        "commit",
		Session:     "test-cancel",
		OllamaURL:   server.URL,
		HTTPClient:  server.Client(),
		OllamaModel: "test-model",
		MaxTurns:    10,
		BusDir:      t.TempDir(),
		BusBin:      "echo", // no-op
	}

	// Create inbox dir so HasMessages works
	os.MkdirAll(filepath.Join(cfg.BusDir, "inbox"), 0755)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := Run(ctx, cfg, &mockEventSink{})
	if err != nil {
		t.Errorf("Run should return nil on context cancel, got: %v", err)
	}
}

func TestRun_HarnessMarkerLifecycle(t *testing.T) {
	// Verify harness writes marker on startup and removes it on exit
	server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[{"name":"test-model"}]}`))
			return
		}
	}))
	defer server.Close()

	busDir := t.TempDir()
	cfg := Config{
		Role:        "build",
		Session:     "test-marker",
		OllamaURL:   server.URL,
		HTTPClient:  server.Client(),
		OllamaModel: "test-model",
		MaxTurns:    10,
		BusDir:      busDir,
		BusBin:      "echo",
	}

	// Create inbox dir
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	markerPath := filepath.Join(busDir, "harness-build.pid")

	// Run with short timeout so it starts, writes marker, then exits
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Use a goroutine so we can check the marker while Run is active
	markerSeen := false
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, &mockEventSink{})
	}()

	// Poll for marker creation
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(markerPath); err == nil {
			markerSeen = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for Run to finish
	<-done

	if !markerSeen {
		t.Error("harness marker was never created during Run")
	}

	// After Run returns, marker should be cleaned up via defer
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("harness marker should be removed after Run exits")
	}
}

func TestLooksLikeNarration(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"empty", "", false},
		{"clean success", "Build succeeded: compiled muxcode binary", false},
		{"clean failure", "Build FAILED: missing dependency in bus/agent.go", false},
		{"lets narration", "Let's try running ./build.sh directly and capture its output.", true},
		{"let me narration", "Let me execute the build command now.", true},
		{"i will narration", "I will now run the build script.", true},
		{"ill narration", "I'll run ./build.sh to build the project.", true},
		{"code block narration", "Here's the command:\n```bash\n./build.sh 2>&1\n```\nThis will build the project.", true},
		{"code block with succeeded", "Build succeeded:\n```\nok muxcode\n```", false},
		{"code block with failed", "Build failed:\n```\nerror in main.go\n```", false},
		{"now i need to", "Now I need to run the build command.", true},
		{"going to", "I'm going to execute the build.", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksLikeNarration(tt.input)
			if got != tt.expected {
				t.Errorf("looksLikeNarration(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestProcessBatch_NarrationRecovery(t *testing.T) {
	callCount := 0
	server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		switch callCount {
		case 1:
			// First call: tool call to run a command
			resp := ChatResponse{
				Choices: []ChatChoice{
					{
						Message: ChatMessage{
							Role: "assistant",
							ToolCalls: []ToolCall{
								{
									ID:   "call_1",
									Type: "function",
									Function: FunctionCall{
										Name:      "bash",
										Arguments: json.RawMessage(`{"command":"echo build ok"}`),
									},
								},
							},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case 2:
			// Second call: narration response (the bug)
			resp := ChatResponse{
				Choices: []ChatChoice{
					{Message: ChatMessage{Role: "assistant", Content: "Let's try running the build again."}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case 3:
			// Third call: summarization recovery (no tools passed)
			resp := ChatResponse{
				Choices: []ChatChoice{
					{Message: ChatMessage{Role: "assistant", Content: "Build succeeded: compiled successfully"}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	cfg := Config{
		Role:     "build",
		Session:  "test",
		BusDir:   dir,
		MaxTurns: 10,
	}

	ollama := NewOllamaClient(server.URL, "test-model")
	ollama.HTTP = server.Client()
	executor := NewExecutor([]string{"Bash(echo *)"})
	tools := BuildToolDefs([]string{"Bash(echo *)"})
	filter := NewFilter("build")
	bus := &BusClient{BusDir: dir, Role: "build", BinPath: "echo"}

	msgs := []Message{
		{ID: "1", From: "edit", To: "build", Action: "build", Payload: "Run build"},
	}

	processBatch(context.Background(), cfg, bus, ollama, executor, tools, "system prompt", filter, msgs, &mockEventSink{}, nil)

	// Build is a single-shot role: after successful tool execution, the loop
	// breaks and fires one summary call. That's 2 calls total (tool + summary).
	if callCount != 2 {
		t.Errorf("expected 2 Ollama calls (tool + single-shot summary), got %d", callCount)
	}
}

func TestProcessBatch_NarrationRecovery_NonSingleShot(t *testing.T) {
	callCount := 0
	server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		switch callCount {
		case 1:
			// First call: tool call to run a command
			resp := ChatResponse{
				Choices: []ChatChoice{
					{
						Message: ChatMessage{
							Role: "assistant",
							ToolCalls: []ToolCall{
								{
									ID:   "call_1",
									Type: "function",
									Function: FunctionCall{
										Name:      "bash",
										Arguments: json.RawMessage(`{"command":"echo review ok"}`),
									},
								},
							},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case 2:
			// Second call: narration response (the bug)
			resp := ChatResponse{
				Choices: []ChatChoice{
					{Message: ChatMessage{Role: "assistant", Content: "Let's try running the review again."}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case 3:
			// Third call: summarization recovery (no tools passed)
			resp := ChatResponse{
				Choices: []ChatChoice{
					{Message: ChatMessage{Role: "assistant", Content: "Review completed: no issues found"}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	// Use "review" role — not single-shot, so narration recovery still applies
	cfg := Config{
		Role:     "review",
		Session:  "test",
		BusDir:   dir,
		MaxTurns: 10,
	}

	ollama := NewOllamaClient(server.URL, "test-model")
	ollama.HTTP = server.Client()
	executor := NewExecutor([]string{"Bash(echo *)"})
	tools := BuildToolDefs([]string{"Bash(echo *)"})
	filter := NewFilter("review")
	bus := &BusClient{BusDir: dir, Role: "review", BinPath: "echo"}

	msgs := []Message{
		{ID: "1", From: "edit", To: "review", Action: "review", Payload: "Run review"},
	}

	processBatch(context.Background(), cfg, bus, ollama, executor, tools, "system prompt", filter, msgs, &mockEventSink{}, nil)

	// Non-single-shot role: 3 calls — tool + narration + summary recovery
	if callCount != 3 {
		t.Errorf("expected 3 Ollama calls (tool + narration + recovery), got %d", callCount)
	}
}

func TestFormatTask_Integration(t *testing.T) {
	msgs := []Message{
		{
			ID:      "123",
			From:    "edit",
			To:      "commit",
			Action:  "commit",
			Payload: "Stage and commit all current changes with message 'Add feature X'",
		},
	}

	result := FormatTask(msgs)
	if !strings.Contains(result, "commit") {
		t.Error("should contain action")
	}
	if !strings.Contains(result, "edit") {
		t.Error("should contain from")
	}
	if !strings.Contains(result, "Stage and commit") {
		t.Error("should contain payload")
	}
	if !strings.Contains(result, "Do NOT run") {
		t.Error("should contain inbox warning")
	}
}
