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
	// Placeholder text was removed by request — the empty frame
	// self-describes through structure instead: the separator column
	// runs the full body height down to the input line.
	if strings.Contains(frame, "No prompts yet") || strings.Contains(frame, "appears here") {
		t.Error("placeholder instructional text must not return")
	}
	if n := strings.Count(StripAnsi(frame), "│"); n < 10 {
		t.Errorf("separator column must run the full body height, got %d rows", n)
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

// TestRenderPromptFrame_SplitLayout pins the 50/50 body: the user's
// prompt area on the left, the model's output on the right, aligned
// row-wise per exchange with answers wrapped inside their column — and
// the narrow-pane negative control proving the split degrades to the
// stacked form instead of rendering two slivers.
func TestRenderPromptFrame_SplitLayout(t *testing.T) {
	longAnswer := strings.Repeat("output word ", 12) // wraps at ~45-col cells
	st := PromptSurfaceState{
		Exchanges: []PromptExchange{
			{Question: "run the build graph", Answer: "started run abc", Answered: true},
			{Question: "explain", Answer: longAnswer, Answered: true},
		},
		Destination: "prompt-agent",
	}
	frame := StripAnsi(RenderPromptFrame(st, 100, 30))

	if !strings.Contains(frame, "│") {
		t.Fatal("wide frame must carry the column separator")
	}
	aligned := false
	answerRows := 0
	for _, ln := range strings.Split(frame, "\n") {
		l, r, found := strings.Cut(ln, "│")
		if !found {
			continue
		}
		if strings.Contains(l, "run the build graph") && strings.Contains(r, "started run abc") {
			aligned = true
		}
		if strings.Contains(l, "run the build graph") && strings.Contains(l, "started run abc") {
			t.Fatal("the answer leaked into the left column")
		}
		if strings.Contains(r, "output word") {
			answerRows++
		}
	}
	if !aligned {
		t.Error("question and its answer must share a row across the split")
	}
	if answerRows < 2 {
		t.Errorf("a long answer must wrap inside the right column, got %d rows", answerRows)
	}

	// Negative control: a narrow pane degrades to the stacked layout.
	narrow := StripAnsi(RenderPromptFrame(st, 40, 30))
	if strings.Contains(narrow, "│") {
		t.Error("narrow frame must fall back to the stacked layout, not render slivers")
	}
	if !strings.Contains(narrow, "run the build graph") {
		t.Error("stacked fallback must still show the transcript")
	}
}

// TestRenderPromptFrame_ActivityUnderWorking pins the live log tail: a
// working question shows the agent's activity lines in the right column
// so a minute-long thinking call reads as alive — and an answered
// exchange shows only its answer, never stale activity.
func TestRenderPromptFrame_ActivityUnderWorking(t *testing.T) {
	activity := []string{
		"[qwen3:4b] Prompt 1/10 — calling Ollama...",
		"[qwen3:4b] Error: command not allowed by tool profile: date",
	}
	working := StripAnsi(RenderPromptFrame(PromptSurfaceState{
		Exchanges:   []PromptExchange{{Question: "what time is it"}},
		Working:     true,
		Activity:    activity,
		Destination: "prompt-agent",
	}, 120, 24))
	if !strings.Contains(working, "calling Ollama") || !strings.Contains(working, "not allowed by tool profile") {
		t.Error("working state must show the agent's live activity tail")
	}
	if !strings.Contains(working, "working") {
		t.Error("the working marker must still lead the activity")
	}
	// Activity lines belong to the right column, past the separator.
	for _, ln := range strings.Split(working, "\n") {
		if l, _, found := strings.Cut(ln, "│"); found && strings.Contains(l, "calling Ollama") {
			t.Error("activity leaked into the left (prompt) column")
		}
	}

	// The bug shape this exists for: a 20-line activity tail in a short
	// pane must not clamp away the question and marker at the top of the
	// block — the blank left column that read as stuck (2026-08-27).
	var bigActivity []string
	for i := 0; i < 20; i++ {
		bigActivity = append(bigActivity, fmt.Sprintf("[qwen3:4b] log line %02d", i))
	}
	short := StripAnsi(RenderPromptFrame(PromptSurfaceState{
		Exchanges:   []PromptExchange{{Question: "run build test review"}},
		Working:     true,
		Activity:    bigActivity,
		Destination: "prompt-agent",
	}, 120, 14))
	if !strings.Contains(short, "run build test review") {
		t.Error("the question must survive an oversized activity tail")
	}
	if !strings.Contains(short, "working") {
		t.Error("the working marker must survive an oversized activity tail")
	}
	if !strings.Contains(short, "log line 19") {
		t.Error("the NEWEST activity lines are the ones kept")
	}

	// Negative control: an answered exchange shows its answer, not the log.
	answered := StripAnsi(RenderPromptFrame(PromptSurfaceState{
		Exchanges:   []PromptExchange{{Question: "what time is it", Answer: "succeeded", Answered: true}},
		Activity:    activity,
		Destination: "prompt-agent",
	}, 120, 24))
	if strings.Contains(answered, "calling Ollama") {
		t.Error("stale activity must not render once the answer arrived")
	}
}

// TestRenderPromptFrame_BlockedAnswerNeverWearsSuccessGlyph pins plan's
// catch: a BLOCKED answer (the prefix enforceDenialPrefix guarantees)
// must render the red ✗, never the green ✓ — the one moment glyph
// identity matters most is a denial the model narrated as success.
func TestRenderPromptFrame_BlockedAnswerNeverWearsSuccessGlyph(t *testing.T) {
	blocked := "BLOCKED: command not allowed by tool profile: make build — model summary: succeeded"
	for _, width := range []int{120, 40} { // split and stacked layouts
		frame := RenderPromptFrame(PromptSurfaceState{
			Exchanges:   []PromptExchange{{Question: "run build", Answer: blocked, Answered: true}},
			Destination: "prompt-agent",
		}, width, 24)
		plain := StripAnsi(frame)
		if !strings.Contains(plain, "✗") || !strings.Contains(plain, "BLOCKED:") {
			t.Errorf("width %d: blocked answer must carry the refusal glyph", width)
		}
		if strings.Contains(plain, "✓") {
			t.Errorf("width %d: blocked answer must not wear the success glyph", width)
		}

		// Positive control: a clean answer still earns the ✓.
		frame = RenderPromptFrame(PromptSurfaceState{
			Exchanges:   []PromptExchange{{Question: "run build", Answer: "started run wf-1", Answered: true}},
			Destination: "prompt-agent",
		}, width, 24)
		if !strings.Contains(StripAnsi(frame), "✓") {
			t.Errorf("width %d: a clean answer must keep the success glyph", width)
		}
	}
}

// TestPromptUnreachable_MissingGatewayKey pins the review catch: on the
// default opencode backend a keyless install must say WHY prompts cannot
// work — and a configured key clears that reason (negative control).
func TestPromptUnreachable_MissingGatewayKey(t *testing.T) {
	t.Setenv("MUXCODE_CONFIG", "/dev/null") // hermetic: ignore the user's real config
	t.Setenv("HOME", t.TempDir())           // and the home-config fallback
	t.Setenv("MUXCODE_PROMPT_AGENT_DISABLE", "")
	t.Setenv("MUXCODE_PROMPT_BACKEND", "")
	t.Setenv("MUXCODE_OPENCODE_API_KEY", "")
	reason := PromptUnreachable("no-such-session")
	if !strings.Contains(reason, "MUXCODE_OPENCODE_API_KEY") {
		t.Errorf("keyless default backend must name the missing key, got %q", reason)
	}
	if !strings.Contains(reason, "inject still works") {
		t.Errorf("the keyless line must say inject still works, got %q", reason)
	}
	if !PromptKeyless("no-such-session") {
		t.Error("keyless gateway state must report PromptKeyless")
	}

	t.Setenv("MUXCODE_OPENCODE_API_KEY", "sk-test")
	reason = PromptUnreachable("no-such-session")
	if strings.Contains(reason, "MUXCODE_OPENCODE_API_KEY") {
		t.Errorf("a configured key must clear the missing-key reason, got %q", reason)
	}
	if PromptKeyless("no-such-session") {
		t.Error("a configured key must clear PromptKeyless (negative control)")
	}
}

// TestPromptRenderOnce_KeylessDefaultsToInject pins the user-requested
// keyless behavior end-to-end at the render seam: with no gateway key
// the input line labels INJECT (the only mode that can deliver) and the
// frame carries the key advisory; with a key the default is interpret
// (negative control).
func TestPromptRenderOnce_KeylessDefaultsToInject(t *testing.T) {
	t.Setenv("MUXCODE_CONFIG", "/dev/null")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MUXCODE_PROMPT_AGENT_DISABLE", "")
	t.Setenv("MUXCODE_PROMPT_BACKEND", "")
	t.Setenv("MUXCODE_OPENCODE_API_KEY", "")

	frame, err := PromptRenderOnce("no-such-session", 120)
	if err != nil {
		t.Fatal(err)
	}
	plain := StripAnsi(frame)
	if !strings.Contains(plain, "⇒ inject") {
		t.Errorf("keyless surface must default the input to inject:\n%s", plain)
	}
	if !strings.Contains(plain, "MUXCODE_OPENCODE_API_KEY") {
		t.Errorf("keyless surface must tell the user which key unlocks interpret:\n%s", plain)
	}

	t.Setenv("MUXCODE_OPENCODE_API_KEY", "sk-test")
	frame, err = PromptRenderOnce("no-such-session", 120)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(StripAnsi(frame), "→ interpret") {
		t.Error("with a key configured the default must stay interpret (negative control)")
	}
}

// TestRenderPromptFrame_Scroll pins the scrollback: ↑ lifts the window
// to older rows with an off-tail indicator, over-scroll clamps at the
// oldest row, and Scroll=0 stays pinned to the newest (negative control).
func TestRenderPromptFrame_Scroll(t *testing.T) {
	var ex []PromptExchange
	for i := 0; i < 30; i++ {
		ex = append(ex, PromptExchange{
			Question: fmt.Sprintf("q-%02d", i),
			Answer:   fmt.Sprintf("a-%02d", i),
			Answered: true,
		})
	}
	base := PromptSurfaceState{Exchanges: ex, Destination: "prompt-agent"}

	// Pinned to the tail: newest visible, oldest not, no indicator.
	pinned := StripAnsi(RenderPromptFrame(base, 100, 14))
	if !strings.Contains(pinned, "q-29") || strings.Contains(pinned, "q-00") {
		t.Error("Scroll=0 must pin the newest rows")
	}
	if strings.Contains(pinned, "scrolled") {
		t.Error("the off-tail indicator must not show at the tail")
	}

	// Scrolled up: older rows appear, the newest leaves, indicator shows.
	up := base
	up.Scroll = 20
	scrolled := StripAnsi(RenderPromptFrame(up, 100, 14))
	if strings.Contains(scrolled, "q-29") {
		t.Error("scrolling up must move the newest row out of the window")
	}
	if !strings.Contains(scrolled, "▼ scrolled") {
		t.Error("off the tail, the indicator must show")
	}

	// Over-scroll clamps at the oldest rather than blanking.
	over := base
	over.Scroll = 10000
	clamped := StripAnsi(RenderPromptFrame(over, 100, 14))
	if !strings.Contains(clamped, "q-00") {
		t.Error("over-scroll must clamp at the oldest row, not blank the view")
	}
}

// TestRenderPromptFrame_IndependentScroll pins the two-column contract:
// Scroll lifts only the left (questions) window, ActScroll only the
// right (output/log), and each indicator names its own key.
func TestRenderPromptFrame_IndependentScroll(t *testing.T) {
	var ex []PromptExchange
	for i := 0; i < 30; i++ {
		ex = append(ex, PromptExchange{
			Question: fmt.Sprintf("q-%02d", i),
			Answer:   fmt.Sprintf("a-%02d", i),
			Answered: true,
		})
	}
	base := PromptSurfaceState{Exchanges: ex, Destination: "prompt-agent"}

	leftUp := base
	leftUp.Scroll = 20
	frame := StripAnsi(RenderPromptFrame(leftUp, 100, 14))
	if strings.Contains(frame, "q-29") || !strings.Contains(frame, "a-29") {
		t.Errorf("Scroll must lift only the left column:\n%s", frame)
	}
	if !strings.Contains(frame, "▼ scrolled — ↓ for newer") {
		t.Errorf("left indicator must name ↓:\n%s", frame)
	}

	rightUp := base
	rightUp.ActScroll = 20
	frame = StripAnsi(RenderPromptFrame(rightUp, 100, 14))
	if strings.Contains(frame, "a-29") || !strings.Contains(frame, "q-29") {
		t.Errorf("ActScroll must lift only the right column:\n%s", frame)
	}
	if !strings.Contains(frame, "▼ scrolled — PgDn for newer") {
		t.Errorf("right indicator must name PgDn:\n%s", frame)
	}
}

// TestPromptFooterStatus pins the provider·model badge: right-aligned
// two columns off the edge when it fits, dropped entirely when the
// width can't hold it (the keys win — negative control).
func TestPromptFooterStatus(t *testing.T) {
	keys := "  Enter Send  Esc Back"

	wide := PromptFooterStatus(keys, "opencode", "deepseek-v4-flash", 120)
	plain := StripAnsi(wide)
	if !strings.HasSuffix(plain, "opencode · deepseek-v4-flash") {
		t.Errorf("badge missing or not at the end: %q", plain)
	}
	if len([]rune(plain)) != 118 {
		t.Errorf("badge must end 2 columns off the edge, line is %d cols", len([]rune(plain)))
	}

	narrow := PromptFooterStatus(keys, "opencode", "deepseek-v4-flash", 40)
	if narrow != keys {
		t.Errorf("too narrow for the badge — footer must be unchanged, got %q", StripAnsi(narrow))
	}
}

// TestRenderPromptFrame_InputWraps pins the wrapping input area: a long
// typed prompt flows onto indented continuation rows instead of
// truncating off the pane edge, and the extra rows are paid for by the
// transcript budget — the frame's total line count must not grow.
// Short input stays a single line (negative control).
func TestRenderPromptFrame_InputWraps(t *testing.T) {
	// width 80, prefix "  [→ interpret: prompt-agent] " = 30 cols → 50
	// input cols per row; 124 runes = rows of 50/50/24, tail intact.
	long := strings.Repeat("x", 120) + "TAIL"
	st := PromptSurfaceState{Input: long, Cursor: len([]rune(long)), Destination: "prompt-agent"}
	frame := StripAnsi(RenderPromptFrame(st, 80, 24))
	if !strings.Contains(frame, "TAIL") {
		t.Errorf("the end of a long input must stay visible — wrap, not truncate:\n%s", frame)
	}
	if !strings.Contains(frame, "TAIL█") {
		t.Errorf("the cursor must render on the wrapped row it falls on:\n%s", frame)
	}

	short := StripAnsi(RenderPromptFrame(PromptSurfaceState{Input: "hi", Destination: "prompt-agent"}, 80, 24))
	if strings.Count(frame, "\n") != strings.Count(short, "\n") {
		t.Errorf("wrapped input rows must come out of the transcript budget, not grow the frame: %d vs %d lines",
			strings.Count(frame, "\n"), strings.Count(short, "\n"))
	}
}

// TestPromptSuggestAndTypeahead pins the two completion helpers: the
// ghost completes the last word (templates before verbs, none after a
// trailing space or full match), and TypeaheadIndex jumps to the first
// case-insensitive prefix match.
func TestPromptSuggestAndTypeahead(t *testing.T) {
	tmpl := []string{"build-test-review", "story-lifecycle", "story-to-spec"}
	if got := PromptSuggest("run sto", tmpl); got != "ry-lifecycle" {
		t.Errorf("suggest = %q", got)
	}
	if got := PromptSuggest("run story-lifecycle", tmpl); got != "" {
		t.Errorf("a full match must suggest nothing, got %q", got)
	}
	if got := PromptSuggest("run ", tmpl); got != "" {
		t.Errorf("trailing space must suggest nothing, got %q", got)
	}
	if got := PromptSuggest("app", nil); got != "rove" {
		t.Errorf("verbs complete too, got %q", got)
	}

	if i := TypeaheadIndex(tmpl, "story"); i != 1 {
		t.Errorf("typeahead jump = %d, want 1", i)
	}
	if i := TypeaheadIndex(tmpl, "zzz"); i != -1 {
		t.Errorf("no match must be -1, got %d", i)
	}
}

// TestRenderPromptFrame_GhostSuggestion pins the ghost render: it shows
// dim after the cursor block at end-of-input, and never mid-string
// (negative control).
func TestRenderPromptFrame_GhostSuggestion(t *testing.T) {
	st := PromptSurfaceState{Input: "run sto", Cursor: 7, Suggest: "ry-lifecycle", Destination: "prompt-agent"}
	frame := StripAnsi(RenderPromptFrame(st, 100, 20))
	if !strings.Contains(frame, "run sto█ry-lifecycle") {
		t.Errorf("ghost must render after the cursor block:\n%s", frame)
	}
	st.Cursor = 2
	frame = StripAnsi(RenderPromptFrame(st, 100, 20))
	if strings.Contains(frame, "ry-lifecycle") {
		t.Error("a mid-string cursor must render no ghost (negative control)")
	}
}

// TestEditLineAt pins the cursor-aware editor: insert and backspace act
// at the cursor, an out-of-range cursor clamps to the end (which keeps
// editLine's end-anchored contract intact).
func TestEditLineAt(t *testing.T) {
	buf, cur, _ := editLineAt([]rune("acd"), 1, 'b')
	if string(buf) != "abcd" || cur != 2 {
		t.Errorf("mid-insert = %q cur=%d, want abcd cur=2", string(buf), cur)
	}
	buf, cur, _ = editLineAt([]rune("abXcd"), 3, 127)
	if string(buf) != "abcd" || cur != 2 {
		t.Errorf("mid-backspace = %q cur=%d, want abcd cur=2", string(buf), cur)
	}
	buf, cur, _ = editLineAt([]rune("ab"), 99, 'c')
	if string(buf) != "abc" || cur != 3 {
		t.Errorf("out-of-range cursor must clamp to end, got %q cur=%d", string(buf), cur)
	}
	if buf, cur, _ = editLineAt(nil, 0, 127); len(buf) != 0 || cur != 0 {
		t.Error("backspace at the start must be a no-op")
	}
}

// TestRenderInputCursor pins the visible cursor: mid-string the rune
// under it renders reverse-video; at the end the trailing block shows
// (negative control — no reverse-video sequence).
func TestRenderInputCursor(t *testing.T) {
	mid := renderInputCursor("abc", 1)
	if !strings.Contains(mid, Reverse+"b"+NoReverse) {
		t.Errorf("cursor on 'b' must reverse-video it, got %q", mid)
	}
	if strings.Contains(mid, "█") {
		t.Error("mid-string cursor must not also draw the trailing block")
	}
	end := renderInputCursor("abc", 3)
	if !strings.HasSuffix(end, "█") || strings.Contains(end, Reverse) {
		t.Errorf("end cursor is the trailing block, no reverse-video, got %q", end)
	}
}

// TestSummarizeRunResults pins the results cell: issues first (failed
// node + why), else the completed node chain — node IDS, never node
// output, whose mid-sentence first lines read as prose flowing across
// run rows. An inferred completion carries "?" on ITS node and the cell
// explains the mark; a fully proven chain carries no explainer
// (negative control).
func TestSummarizeRunResults(t *testing.T) {
	if got := SummarizeRunResults(bus.GraphRunFailed, []string{"review", "deploy"}, "tests failed: 2\nnoise", []string{"build"}); got != "✗ review +1: tests failed: 2" {
		t.Errorf("failure summary = %q", got)
	}
	if got := SummarizeRunResults(bus.GraphRunComplete, nil, "", []string{"build", "test", "review?"}); got != "✓ build → test → review?" {
		t.Errorf("the inferred node keeps its ? mark, explainer lives in the list legend — got %q", got)
	}
	if got := SummarizeRunResults(bus.GraphRunComplete, nil, "", []string{"build", "test", "review"}); got != "✓ build → test → review" {
		t.Errorf("a fully proven chain must carry no explainer, got %q", got)
	}
	if got := SummarizeRunResults(bus.GraphRunComplete, nil, "", nil); got != "✓ complete" {
		t.Errorf("complete-with-no-done-nodes = %q", got)
	}
	if got := SummarizeRunResults(bus.GraphRunRunning, nil, "", []string{"build"}); got != "build ⋯" {
		t.Errorf("in-flight shows the done chain with the working glyph, got %q", got)
	}
}

// TestRenderRunListFrame_ResultsColumn pins the column end-to-end: the
// header names it, a failed run's cell renders red with its reason, and
// a clean run's cell carries the ✓ line.
func TestRenderRunListFrame_ResultsColumn(t *testing.T) {
	rows := []RunListRow{
		{ID: "r1", Template: "bt", State: bus.GraphRunFailed, Results: "✗ review: tests failed"},
		{ID: "r2", Template: "bt", State: bus.GraphRunComplete, Results: "✓ LGTM"},
	}
	frame := RenderRunListFrame(rows, 200, 0)
	plain := StripAnsi(frame)
	if !strings.Contains(plain, "RESULTS") {
		t.Error("header must name the RESULTS column")
	}
	if !strings.Contains(plain, "✗ review: tests failed") || !strings.Contains(plain, "✓ LGTM") {
		t.Errorf("both results cells must render, got:\n%s", plain)
	}
	if !strings.Contains(frame, Red+"✗ review: tests failed") {
		t.Error("a failure cell must render red")
	}
	if strings.Contains(plain, "completion inferred") {
		t.Error("no ? marks — the legend must not render (negative control)")
	}

	marked := []RunListRow{{ID: "r3", Template: "bt", State: bus.GraphRunComplete, Results: "✓ build → test?"}}
	withLegend := StripAnsi(RenderRunListFrame(marked, 200, 0))
	if !strings.Contains(withLegend, "? = completion inferred") {
		t.Error("a ? mark anywhere must render the one-line legend")
	}
	if strings.Count(withLegend, "completion inferred") != 1 {
		t.Error("the legend renders exactly once, never per row")
	}
}

// TestRenderRunListFrameH_Scrolls pins the vertical scroll window: the
// selection stays visible with ↑/↓ overflow indicators, and a list that
// fits renders whole with no indicators (negative control).
func TestRenderRunListFrameH_Scrolls(t *testing.T) {
	var rows []RunListRow
	for i := 0; i < 20; i++ {
		rows = append(rows, RunListRow{ID: fmt.Sprintf("run-%02d", i), Template: "t", State: bus.GraphRunComplete, Results: "✓ done"})
	}

	frame := StripAnsi(RenderRunListFrameH(rows, 200, 16, 15))
	if !strings.Contains(frame, "run-15") {
		t.Errorf("the selected row must be visible:\n%s", frame)
	}
	if !strings.Contains(frame, "↑ ") || !strings.Contains(frame, "↓ ") {
		t.Errorf("a mid-list window must show both overflow indicators:\n%s", frame)
	}
	if strings.Contains(frame, "run-00") {
		t.Error("rows above the window must be hidden")
	}

	all := StripAnsi(RenderRunListFrameH(rows[:3], 200, 40, 0))
	if strings.Contains(all, "more") {
		t.Error("a list that fits must render whole with no indicators")
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
	if surfaceKey(viewGraphPrompt, "") != "prompt" {
		t.Errorf("surfaceKey = %q", surfaceKey(viewGraphPrompt, ""))
	}
	if v, _, ok := surfaceForKey("prompt"); !ok || v != viewGraphPrompt {
		t.Errorf("surfaceForKey(prompt) = %v, %v", v, ok)
	}
	// Drill-ins share as run:<id>; confirm/intent never share.
	if surfaceKey(viewGraphDAG, "r-1") != "run:r-1" {
		t.Errorf("DAG surfaceKey = %q", surfaceKey(viewGraphDAG, "r-1"))
	}
	if v, id, ok := surfaceForKey("run:r-1"); !ok || v != viewGraphDAG || id != "r-1" {
		t.Errorf("surfaceForKey(run:r-1) = %v, %q, %v", v, id, ok)
	}
	if surfaceKey(viewGraphConfirm, "r-1") != "" {
		t.Error("confirm must never share — it is an active input flow")
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
