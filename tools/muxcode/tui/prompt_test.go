package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// ── Renderer frames ────────────────────────────────────────

func TestRenderPromptFrame_EmptyState(t *testing.T) {
	st := PromptSurfaceState{Destination: "prompt-agent"}
	frame := RenderPromptFrame(st, 100, 20)

	for _, want := range []string{"Prompt", "Launch Graph", "Graph Runs", "Pending Gates"} {
		if !strings.Contains(frame, want) {
			t.Errorf("empty state lost the tab bar entry %q", want)
		}
	}
	if !strings.Contains(frame, "No prompts yet") {
		t.Error("empty state must be explicit, not a blank body")
	}
	if !strings.Contains(frame, "prompt-agent") {
		t.Error("input line must name its destination even when empty")
	}
	if !strings.Contains(frame, "interpret") {
		t.Error("mode must be readable as text in the empty state")
	}
}

func TestRenderPromptFrame_ClampsHeight(t *testing.T) {
	// A fixture that genuinely overflows: 30 answered exchanges are 60
	// body lines against a 12-row pane.
	var ex []PromptExchange
	for i := 0; i < 30; i++ {
		ex = append(ex, PromptExchange{
			Question: fmt.Sprintf("question-%02d", i),
			Answer:   fmt.Sprintf("answer-%02d", i),
			Answered: true,
		})
	}
	st := PromptSurfaceState{Exchanges: ex, Destination: "prompt-agent"}
	frame := RenderPromptFrame(st, 100, 12)

	if n := len(strings.Split(strings.TrimRight(frame, "\n"), "\n")); n > 12 {
		t.Errorf("frame is %d lines for a 12-row pane", n)
	}
	if !strings.Contains(frame, "question-29") {
		t.Error("newest exchange must survive the clamp")
	}
	if strings.Contains(frame, "question-00") {
		t.Error("oldest exchange must be dropped, not the newest")
	}

	// Negative control: a fixture that fits is not truncated.
	small := PromptSurfaceState{
		Exchanges:   []PromptExchange{{Question: "only-question", Answer: "only-answer", Answered: true}},
		Destination: "prompt-agent",
	}
	frame = RenderPromptFrame(small, 100, 40)
	if !strings.Contains(frame, "only-question") || !strings.Contains(frame, "only-answer") {
		t.Error("a fitting transcript must render in full")
	}
}

func TestRenderPromptFrame_ClampsWidth(t *testing.T) {
	long := strings.Repeat("x", 300)
	st := PromptSurfaceState{
		Exchanges:   []PromptExchange{{Question: long, Answer: long, Answered: true}},
		Input:       long,
		Destination: "prompt-agent",
	}
	frame := RenderPromptFrame(st, 40, 20)
	for i, ln := range strings.Split(strings.TrimRight(frame, "\n"), "\n") {
		if w := VisibleWidth(ln); w > 40 {
			t.Errorf("line %d is %d cols wide for a 40-col pane", i, w)
		}
	}
}

func TestRenderPromptFrame_StatesDistinctWithoutColor(t *testing.T) {
	base := []PromptExchange{{Question: "run the build graph"}}

	working := RenderPromptFrame(PromptSurfaceState{
		Exchanges: base, Working: true, Destination: "prompt-agent"}, 100, 20)
	if !strings.Contains(working, "working") {
		t.Error("working state needs a text marker, not just color")
	}

	finished := RenderPromptFrame(PromptSurfaceState{
		Exchanges:   []PromptExchange{{Question: "run it", Answer: "started run abc", Answered: true}},
		Destination: "prompt-agent"}, 100, 20)
	if !strings.Contains(finished, "✓") || !strings.Contains(finished, "started run abc") {
		t.Error("finished state must show the answer with its glyph")
	}
	if strings.Contains(finished, "working") {
		t.Error("an answered exchange must not read as working")
	}

	unreachable := RenderPromptFrame(PromptSurfaceState{
		Exchanges: base, Working: true, Destination: "prompt-agent",
		Unreachable: "prompt-agent not running"}, 100, 20)
	if !strings.Contains(unreachable, "✗") || !strings.Contains(unreachable, "not running") {
		t.Error("unreachable state must carry its glyph and reason")
	}
}

func TestRenderPromptFrame_InjectModeLabeled(t *testing.T) {
	st := PromptSurfaceState{Inject: true, Destination: promptDestinationLabel(true, "edit")}
	frame := RenderPromptFrame(st, 100, 20)
	if !strings.Contains(frame, "inject") || !strings.Contains(frame, "edit agent") {
		t.Error("inject mode must name its destination in the input line")
	}
	if strings.Contains(frame, "→ interpret") {
		t.Error("inject mode must not still read as interpret")
	}
}

// ── Surface registration ───────────────────────────────────

func TestPromptSurfaceInCycle(t *testing.T) {
	found := false
	for _, s := range graphSurfaces {
		if s == viewGraphPrompt {
			found = true
		}
	}
	if !found {
		t.Fatal("viewGraphPrompt missing from graphSurfaces — Tab can never reach it")
	}
	if surfaceName(viewGraphPrompt) != "Prompt" {
		t.Errorf("surfaceName = %q", surfaceName(viewGraphPrompt))
	}
	if surfaceKey(viewGraphPrompt) != "prompt" {
		t.Errorf("surfaceKey = %q", surfaceKey(viewGraphPrompt))
	}
	if v, ok := surfaceForKey("prompt"); !ok || v != viewGraphPrompt {
		t.Errorf("surfaceForKey(prompt) = %v, %v", v, ok)
	}
	if !strings.Contains(renderSurfaceTabs("Prompt", 100), "Prompt") {
		t.Error("tab bar must name the Prompt surface")
	}
}

// ── Transcript pairing ─────────────────────────────────────

func TestPairPromptExchanges(t *testing.T) {
	msgs := []bus.Message{
		{ID: "q1", To: "prompt", From: "edit", Type: "request", Payload: "launch build-test-review", TS: 1},
		{ID: "a1", From: "prompt", To: "edit", Type: "response", ReplyTo: "q1", Payload: "started", TS: 2},
		{ID: "q2", To: "prompt", From: "build", Type: "request", Payload: "status", TS: 3},
	}
	ex := pairPromptExchanges(msgs, 0)
	if len(ex) != 2 {
		t.Fatalf("got %d exchanges, want 2", len(ex))
	}
	if !ex[0].Answered || ex[0].Answer != "started" {
		t.Errorf("correlated reply not paired: %+v", ex[0])
	}
	if ex[1].Answered {
		t.Error("open question must stay unanswered")
	}

	// MUX-111 fallback: a response correlated to nothing attaches to the
	// newest unanswered question rather than vanishing.
	msgs = append(msgs, bus.Message{ID: "a2", From: "prompt", Type: "response", ReplyTo: "bogus", Payload: "42 runs", TS: 4})
	ex = pairPromptExchanges(msgs, 0)
	if !ex[1].Answered || ex[1].Answer != "42 runs" {
		t.Errorf("mis-correlated reply must attach to the open question: %+v", ex[1])
	}

	// Limit keeps the newest.
	ex = pairPromptExchanges(msgs, 1)
	if len(ex) != 1 || ex[0].Question != "status" {
		t.Errorf("limit must keep the newest exchange: %+v", ex)
	}
}

// ── Key handling ───────────────────────────────────────────

func TestEditLine(t *testing.T) {
	buf, ok := editLine(nil, 'a')
	if !ok || string(buf) != "a" {
		t.Errorf("printable append failed: %q", string(buf))
	}
	buf, ok = editLine(buf, 127)
	if !ok || len(buf) != 0 {
		t.Errorf("backspace failed: %q", string(buf))
	}
	if _, ok = editLine(buf, 9); ok {
		t.Error("Tab must not be consumed by the line editor")
	}
}

func TestHandlePromptKey(t *testing.T) {
	ui := NewGraphPromptUI("test-prompt-ui")

	// Printables — including q, j, k — type, never navigate or quit.
	for _, k := range []byte{'q', 'j', 'k', ' ', 'x'} {
		if out := ui.handlePromptKey(k); out != "" {
			t.Fatalf("key %q must type, got %q", k, out)
		}
	}
	if got := string(ui.promptInput); got != "qjk x" {
		t.Errorf("input = %q, want %q", got, "qjk x")
	}

	// Ctrl-T toggles inject; the buffer is untouched.
	ui.handlePromptKey(20)
	if !ui.promptInject {
		t.Error("Ctrl-T must enable inject mode")
	}
	ui.handlePromptKey(20)
	if ui.promptInject {
		t.Error("Ctrl-T must toggle back to interpret")
	}

	// Tab cycles away and the input buffer survives the round trip.
	ui.handlePromptKey(9)
	if ui.view == viewGraphPrompt {
		t.Fatal("Tab must cycle to the next surface")
	}
	for ui.view != viewGraphPrompt {
		ui.cycleSurface(1)
	}
	if got := string(ui.promptInput); got != "qjk x" {
		t.Errorf("input lost across a surface round trip: %q", got)
	}

	// Bare Escape (timeout path with no key channel): first clears the
	// input, then — empty, in direct mode — quits.
	if out := ui.handlePromptKey(27); out != "" || len(ui.promptInput) != 0 {
		t.Errorf("Escape with input must clear it, got out=%q input=%q", out, string(ui.promptInput))
	}
	if out := ui.handlePromptKey(27); out != "quit" {
		t.Errorf("Escape on empty input in direct mode must quit, got %q", out)
	}

	// Enter with an empty buffer is a no-op, not a send.
	if out := ui.handlePromptKey(13); out != "" {
		t.Errorf("empty Enter must be inert, got %q", out)
	}

	// Inject-mode Enter dispatches for real (Phase 6). In a unit test the
	// tmux target cannot exist, so the failure path runs: a notice must
	// surface, and the input must survive — retyping is the one cost a
	// failed inject must not add.
	ui.promptInput = []rune("do the thing")
	ui.promptInject = true
	ui.handlePromptKey(13)
	if ui.notice == "" {
		t.Error("inject-mode Enter must surface an outcome notice")
	}
	if len(ui.promptInput) == 0 {
		t.Error("a failed inject must not consume the input")
	}
}
