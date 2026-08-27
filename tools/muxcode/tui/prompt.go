package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// The Prompt surface (MUX-109) is the control pane's natural-language
// input: interpret mode routes the typed text to the prompt-agent as an
// ordinary bus request, inject mode (wired in Phase 6) forwards it to the
// window's main agent. The transcript is derived from the session bus
// log — requests to and responses from the prompt role — so it is
// session-global with no second write path: ask on one window, walk to
// another, the answer is there.

// PromptExchange is one question/answer pair, paired from the bus log.
type PromptExchange struct {
	Question string
	Answer   string
	From     string // window role that asked
	AskedAt  int64
	Answered bool
}

// PromptSurfaceState is the snapshot RenderPromptFrame consumes. It is
// gathered by refresh() — the renderer does no I/O.
type PromptSurfaceState struct {
	Exchanges   []PromptExchange
	Input       string
	Cursor      int      // rune index into Input; at len(Input) = trailing block
	Suggest     string   // ghost completion after the cursor (→ accepts)
	Inject      bool     // inject mode selected (vs interpret)
	Destination string   // where the text goes — always visible in the input line
	Working     bool     // newest question has no answer yet
	Unreachable string   // non-empty: why the prompt path cannot serve right now
	Activity    []string // tail of the headless agent's log — live detail under the working marker
	Scroll      int      // left-column rows scrolled up from the newest (0 = pinned to the tail)
	ActScroll   int      // right-column (output/log) scroll, independent of the left
}

// promptTranscriptLimit caps how many exchanges refresh loads — the
// renderer clamps further to the pane, this only bounds the read.
const promptTranscriptLimit = 50

// LoadPromptExchanges reads the session bus log and pairs prompt-role
// requests with their responses.
func LoadPromptExchanges(session string, limit int) []PromptExchange {
	return pairPromptExchanges(bus.ReadLogHistory(session, "prompt", 0), limit)
}

// pairPromptExchanges is the pure pairing pass, split from the log read
// for testability. Correlation is by ReplyTo; a response that correlates
// to nothing attaches to the newest unanswered question rather than being
// dropped — the harness under a small model has been observed replying to
// the wrong id (MUX-111), and losing the answer punishes the user for the
// model's bookkeeping.
func pairPromptExchanges(msgs []bus.Message, limit int) []PromptExchange {
	byID := make(map[string]int)
	var out []PromptExchange
	for _, m := range msgs {
		switch {
		case m.To == "prompt" && m.Type == "request":
			byID[m.ID] = len(out)
			out = append(out, PromptExchange{Question: m.Payload, From: m.From, AskedAt: m.TS})
		case m.From == "prompt" && m.Type == "response":
			if idx, ok := byID[m.ReplyTo]; ok {
				out[idx].Answer = m.Payload
				out[idx].Answered = true
			} else if n := len(out) - 1; n >= 0 && !out[n].Answered {
				out[n].Answer = m.Payload
				out[n].Answered = true
			}
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// promptActivityLimit caps how many log-tail lines refresh loads. The
// right column scrolls independently, so the cap bounds the load, not
// the visible window.
const promptActivityLimit = 100

// LoadPromptActivity tails the headless prompt-agent's log — the
// `[model] ...` lines its LogSink writes (Ollama call timing, tool
// calls, profile denials, single-shot states). While an answer is being
// made, the surface shows these instead of a bare working marker, so a
// multi-minute thinking model reads as alive rather than stuck.
func LoadPromptActivity(session string, limit int) []string {
	data, err := os.ReadFile(bus.PromptAgentLogPath(session))
	if err != nil {
		return nil
	}
	var lines []string
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(ln) != "" {
			lines = append(lines, ln)
		}
	}
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines
}

// PromptUnreachable reports why the prompt path cannot serve, or "" when
// it can. Every signal is a file read — this runs from refresh(), never
// from the renderer, and must not probe the network.
func PromptUnreachable(session string) string {
	if !bus.PromptAgentEnabled() {
		return "prompt-agent disabled (MUXCODE_PROMPT_AGENT_DISABLE=1)"
	}
	// A keyless gateway backend is the sharpest failure now that opencode
	// is the DEFAULT: the harness launches, every request 401s, and
	// without this line nothing tells the user why (review catch,
	// 2026-08-27). Checked before liveness — the running-ness of an
	// agent that cannot authenticate is not the user's problem. The
	// surface defaults to inject in this state (user-requested), so the
	// line names what still works and what the key would unlock.
	if PromptKeyless(session) {
		return "no gateway key — interpret disabled, inject still works; add MUXCODE_OPENCODE_API_KEY to ~/.config/muxcode/config to use interpret (or set MUXCODE_PROMPT_BACKEND=ollama)"
	}
	if !bus.PromptAgentAlive(session) {
		return "prompt-agent not running — the daemon starts it within a minute"
	}
	if bus.PromptBackend(session) == "ollama" && bus.HasOllamaFailSentinel(session) {
		return "model unreachable — Ollama failure sentinel present, recovery in progress"
	}
	return ""
}

// PromptKeyless reports the interpret-cannot-work state: gateway backend
// selected with no API key configured. The surface defaults the toggle
// to inject here — the only mode that can actually deliver.
func PromptKeyless(session string) bool {
	return bus.PromptBackend(session) == "opencode" && bus.OpencodeAPIKey() == ""
}

// promptChromeLines reserves the rows around the transcript body: tab
// bar (2), rule (1), input (1), the outer render()'s notice line (1),
// its rule (1), its footer (1), and one spare. The body pads to the full
// remaining budget (the separator column runs the pane height), so an
// under-reservation here pushes the outer lines past the pane clamp —
// exactly how the injection receipt vanished when this was 6
// (integration run 3, 2026-08-27).
const promptChromeLines = 8

// promptSplitMinWidth is the narrowest pane that gets the 50/50 layout;
// below it the surface degrades to the stacked single-column form rather
// than rendering two unusable slivers.
const promptSplitMinWidth = 60

// RenderPromptFrame renders the Prompt surface: tab bar, transcript, and
// the input line naming its destination. At usable widths the body is a
// 50/50 split — the left column is the user's prompt area (questions,
// input line), the right column the model's output, wrapped inside its
// column so long answers stay readable. Pure — state in, string out.
func RenderPromptFrame(st PromptSurfaceState, width, height int) string {
	var b strings.Builder
	b.WriteString(renderSurfaceTabs("Prompt", width))
	fmt.Fprintf(&b, "%s%s%s\n", Comment, HLine('─', width), RST)

	// The input area wraps (a long prompt must never truncate off the
	// pane edge), so its extra rows come out of the transcript budget —
	// the chrome reservation covers one input line.
	mode := "→ interpret"
	if st.Inject {
		mode = "⇒ inject"
	}
	inputLines := renderInputLines(mode, st.Destination, st.Input, st.Cursor, st.Suggest, width)

	bodyBudget := height - promptChromeLines - (len(inputLines) - 1)
	if st.Unreachable != "" {
		bodyBudget--
	}
	if bodyBudget < 0 {
		bodyBudget = 0
	}
	if width >= promptSplitMinWidth {
		renderPromptSplit(&b, st, width, bodyBudget)
	} else {
		renderPromptStacked(&b, st, width, bodyBudget)
	}

	// State line: unreachable outranks working — a dead model must never
	// render as merely slow. Both carry a glyph so the state reads without
	// color.
	if st.Unreachable != "" {
		b.WriteString(fitWidth(fmt.Sprintf("  %s✗ %s%s", Red+Bold, st.Unreachable, RST), width) + "\n")
	}

	// Input area — the destination is always visible, and the two modes
	// differ by glyph and word, not color alone.
	for _, ln := range inputLines {
		b.WriteString(ln + "\n")
	}
	return b.String()
}

// renderInputLines renders the destination-labeled input wrapped to the
// pane: continuation rows indent to the text column and the cursor is
// drawn in place on whichever row it falls. Long prompts must never
// truncate off the pane edge (user catch, 2026-08-27).
func renderInputLines(mode, destination, input string, cursor int, suggest string, width int) []string {
	prefixPlain := fmt.Sprintf("  [%s: %s] ", mode, destination)
	prefix := fmt.Sprintf("  %s[%s: %s]%s ", Purple+Bold, mode, destination, RST)
	avail := width - len([]rune(prefixPlain))
	if avail < 8 {
		// Degenerate pane: one truncated line beats zero usable columns.
		return []string{fitWidth(prefix+FG+renderInputCursor(input, cursor)+RST, width)}
	}

	in := []rune(input)
	if cursor < 0 || cursor > len(in) {
		cursor = len(in)
	}
	var rows []string
	var row strings.Builder
	col := 0
	flush := func() { rows = append(rows, row.String()); row.Reset(); col = 0 }
	for i := 0; i <= len(in); i++ {
		if col == avail {
			flush()
		}
		switch {
		case i == len(in):
			if cursor == len(in) {
				row.WriteString("█")
			}
		case i == cursor:
			row.WriteString(Reverse + string(in[i]) + NoReverse)
			col++
		default:
			row.WriteRune(in[i])
			col++
		}
	}
	flush()
	if cursor == len(in) && suggest != "" {
		rows[len(rows)-1] += Comment + suggest + RST + FG
	}

	indent := strings.Repeat(" ", len([]rune(prefixPlain)))
	lines := make([]string, 0, len(rows))
	for i, r := range rows {
		if i == 0 {
			lines = append(lines, fitWidth(prefix+FG+r+RST, width))
			continue
		}
		lines = append(lines, fitWidth(indent+FG+r+RST, width))
	}
	return lines
}

// renderPromptSplit renders the two-column body: questions left, model
// output right, each column an independent tail-anchored scroll window
// (Scroll left, ActScroll right) — a long log must not force the
// question column to move with it. Oldest rows leave the window first.
func renderPromptSplit(b *strings.Builder, st PromptSurfaceState, width, budget int) {
	colW := (width - 3) / 2
	textW := colW - 3 // leading space + 2-char glyph prefix
	if textW < 8 {
		textW = 8
	}

	// No placeholder text in the empty state (user-requested): the tab
	// bar, the full-height separator column, and the destination-labeled
	// input line are the explicit frame — the body itself stays clean.
	var left, right []string
	var marker string
	for i, ex := range st.Exchanges {
		for j, l := range wrapPlain(ex.Question, textW) {
			prefix := Comment + "❯ " + RST
			if j > 0 {
				prefix = "  "
			}
			left = append(left, prefix+FG+l+RST)
		}
		left = append(left, "") // spacer between exchanges

		answered := ex.Answered
		working := !answered && st.Working && i == len(st.Exchanges)-1
		switch {
		case answered:
			// A refused outcome must never wear the success glyph:
			// enforceDenialPrefix (harness) GUARANTEES blocked answers
			// lead with BLOCKED:, so the prefix is a reliable key —
			// and the one moment glyph identity matters most is a
			// denial that the model narrated as success (plan's
			// catch, 2026-08-27).
			blocked := strings.HasPrefix(ex.Answer, "BLOCKED:")
			for j, l := range wrapPlain(ex.Answer, textW) {
				switch {
				case j == 0 && blocked:
					right = append(right, Red+Bold+"✗ "+RST+FG+l+RST)
				case j == 0:
					right = append(right, Green+"✓ "+RST+FG+l+RST)
				default:
					right = append(right, "  "+FG+l+RST)
				}
			}
			right = append(right, "")
		case working:
			// Only the newest open question works — older unanswered
			// ones are abandoned, and marking them all would make one
			// slow answer look like several. Under the marker, the
			// agent's live log tail shows WHAT the model is doing —
			// a minute-long thinking call must read as alive, not stuck.
			for j, l := range wrapPlain("working — the model is thinking; the pane stays live", textW) {
				glyph := Yellow + "⋯ "
				if j > 0 {
					glyph = Yellow + "  "
				}
				right = append(right, glyph+l+RST)
				if j == 0 {
					marker = right[len(right)-1]
				}
			}
			for _, a := range st.Activity {
				for _, l := range wrapPlain(a, textW) {
					right = append(right, "  "+Comment+l+RST)
				}
			}
			right = append(right, "")
		}
	}

	leftRows, leftScrolled := scrollTail(left, budget, st.Scroll)
	rightRows, rightScrolled := scrollTail(right, budget, st.ActScroll)
	// A long log window must never scroll the working marker away — a
	// pane without it reads as stuck (2026-08-27). If the tail window
	// dropped it, it takes the window's first row.
	if marker != "" && !containsLine(rightRows, marker) && len(rightRows) > 0 {
		rightRows[0] = marker
	}
	// Pad to the full body budget so the separator column runs down to
	// the input line instead of stopping where the transcript does.
	n := budget
	if len(leftRows) > n {
		n = len(leftRows)
	}
	if len(rightRows) > n {
		n = len(rightRows)
	}
	for len(leftRows) < n {
		leftRows = append(leftRows, "")
	}
	for len(rightRows) < n {
		rightRows = append(rightRows, "")
	}
	// Off the tail, say so — new answers land below the window and a
	// silent freeze reads as stuck. Each column names its own key.
	if n > 0 {
		if leftScrolled {
			leftRows[n-1] = Comment + "▼ scrolled — ↓ for newer" + RST
		}
		if rightScrolled {
			rightRows[n-1] = Comment + "▼ scrolled — Shift↓ for newer" + RST
		}
	}

	sep := Comment + "│" + RST
	for i := 0; i < n; i++ {
		b.WriteString(padVisible(" "+leftRows[i], colW+1) + sep + padVisible(" "+rightRows[i], colW+1) + "\n")
	}
}

// containsLine reports whether lines holds s exactly.
func containsLine(lines []string, s string) bool {
	for _, l := range lines {
		if l == s {
			return true
		}
	}
	return false
}

// scrollTail windows lines to at most budget rows anchored at the tail,
// lifted by scroll toward older content. Over-scroll clamps rather than
// blanking; budget <= 0 leaves the lines unwindowed.
func scrollTail(lines []string, budget, scroll int) ([]string, bool) {
	if budget <= 0 || len(lines) <= budget {
		return lines, false
	}
	maxScroll := len(lines) - budget
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	end := len(lines) - scroll
	return lines[end-budget : end], scroll > 0
}

// renderPromptStacked is the narrow-pane fallback: the original
// single-column transcript, one line per item, width-truncated.
func renderPromptStacked(b *strings.Builder, st PromptSurfaceState, width, budget int) {
	// No placeholder text (user-requested, matching the split layout) —
	// an empty transcript renders an empty body between the frame chrome.
	var lines []string
	for i, ex := range st.Exchanges {
		lines = append(lines, fmt.Sprintf("  %s❯ %s%s%s%s", Comment, RST, FG, ex.Question, RST))
		if ex.Answered {
			glyph := Green + "✓" + RST
			if strings.HasPrefix(ex.Answer, "BLOCKED:") {
				glyph = Red + Bold + "✗" + RST
			}
			lines = append(lines, fmt.Sprintf("    %s %s", glyph, ex.Answer))
		} else if st.Working && i == len(st.Exchanges)-1 {
			lines = append(lines, fmt.Sprintf("    %s⋯ working — the model is thinking; the pane stays live%s", Yellow, RST))
			// Same trim as the split layout — the question and marker
			// must survive the newest-lines clamp below.
			act := st.Activity
			if max := budget - 3; len(act) > max {
				if max < 0 {
					max = 0
				}
				act = act[len(act)-max:]
			}
			for _, a := range act {
				lines = append(lines, fmt.Sprintf("      %s%s%s", Comment, a, RST))
			}
		}
	}
	if budget > 0 && len(lines) > budget {
		maxScroll := len(lines) - budget
		scroll := st.Scroll
		if scroll > maxScroll {
			scroll = maxScroll
		}
		if scroll < 0 {
			scroll = 0
		}
		end := len(lines) - scroll
		lines = lines[end-budget : end]
		if scroll > 0 && len(lines) > 0 {
			lines[len(lines)-1] = "  " + Comment + "▼ scrolled — ↓ for newer" + RST
		}
	}
	for _, ln := range lines {
		b.WriteString(fitWidth(ln, width) + "\n")
	}
}

// wrapPlain wraps plain (uncolored) text into lines of at most w runes,
// preferring space breaks in the back half of the line.
func wrapPlain(s string, w int) []string {
	if w < 1 {
		return []string{s}
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		r := []rune(para)
		for len(r) > w {
			cut := w
			for i := w; i > w/2; i-- {
				if r[i-1] == ' ' {
					cut = i
					break
				}
			}
			out = append(out, strings.TrimRight(string(r[:cut]), " "))
			r = r[cut:]
		}
		out = append(out, string(r))
	}
	return out
}

// padVisible pads (or ANSI-truncates) a string to exactly w visible
// columns, so split cells align regardless of color codes.
func padVisible(s string, w int) string {
	vis := VisibleWidth(s)
	if vis > w {
		return TruncateAnsi(s, w)
	}
	return s + strings.Repeat(" ", w-vis)
}

// renderInputCursor places the cursor inside the input text: at the end
// it is the familiar trailing block; mid-string the rune under the
// cursor renders reverse-video, so ←/→ editing has a visible position.
func renderInputCursor(input string, cursor int) string {
	in := []rune(input)
	if cursor < 0 || cursor >= len(in) {
		return string(in) + "█"
	}
	return string(in[:cursor]) + Reverse + string(in[cursor]) + NoReverse + string(in[cursor+1:])
}

// promptVerbs are the completion vocabulary beyond template names.
var promptVerbs = []string{"run", "launch", "status", "gates", "approve", "create", "cancel", "retry"}

// PromptSuggest completes the input's last word against the vocabulary
// (template names, then verbs), returning the ghost remainder or "".
func PromptSuggest(input string, templates []string) string {
	fields := strings.Fields(input)
	if len(fields) == 0 || strings.HasSuffix(input, " ") {
		return ""
	}
	last := strings.ToLower(fields[len(fields)-1])
	for _, vocab := range [][]string{templates, promptVerbs} {
		for _, w := range vocab {
			if len(last) < len(w) && strings.HasPrefix(strings.ToLower(w), last) {
				return w[len(last):]
			}
		}
	}
	return ""
}

// PromptFooterStatus right-aligns "backend · model" onto the Prompt
// footer, mirroring the agent panes' provider·model badge. Returns the
// footer unchanged when the width can't fit the badge — the keys win.
func PromptFooterStatus(footer, backend, model string, width int) string {
	status := backend + " · " + model
	pad := width - VisibleWidth(footer) - len([]rune(status)) - 2
	if pad <= 0 {
		return footer
	}
	return footer + strings.Repeat(" ", pad) +
		Comment + backend + " · " + RST + Cyan + model + RST
}

// PromptRenderOnce renders a single Prompt-surface frame — the
// scriptable seam for integration tests.
func PromptRenderOnce(session string, width int) (string, error) {
	if width <= 0 {
		width = termWidth()
	}
	st := PromptSurfaceState{
		Exchanges:   LoadPromptExchanges(session, promptTranscriptLimit),
		Inject:      PromptKeyless(session), // keyless gateway defaults to inject
		Destination: promptDestinationLabel(PromptKeyless(session), ""),
		Unreachable: PromptUnreachable(session),
		Activity:    LoadPromptActivity(session, promptActivityLimit),
	}
	if n := len(st.Exchanges); n > 0 && !st.Exchanges[n-1].Answered {
		st.Working = true
	}
	return RenderPromptFrame(st, width, termHeight()), nil
}

// promptDestinationLabel names where a submitted prompt goes. Pure
// formatting — the active role is resolved by refresh() (mode-cycle
// aware), never here.
func promptDestinationLabel(inject bool, activeRole string) string {
	if !inject {
		return "prompt-agent"
	}
	if activeRole == "" {
		activeRole = "edit"
	}
	return activeRole + " agent"
}
