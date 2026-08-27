package tui

import (
	"fmt"
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
	Inject      bool   // inject mode selected (vs interpret)
	Destination string // where the text goes — always visible in the input line
	Working     bool   // newest question has no answer yet
	Unreachable string // non-empty: why the prompt path cannot serve right now
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

// PromptUnreachable reports why the prompt path cannot serve, or "" when
// it can. Every signal is a file read — this runs from refresh(), never
// from the renderer, and must not probe the network.
func PromptUnreachable(session string) string {
	if !bus.PromptAgentEnabled() {
		return "prompt-agent disabled (MUXCODE_PROMPT_AGENT_DISABLE=1)"
	}
	if !bus.PromptAgentAlive(session) {
		return "prompt-agent not running — the daemon starts it within a minute"
	}
	if bus.HasOllamaFailSentinel(session) {
		return "model unreachable — Ollama failure sentinel present, recovery in progress"
	}
	return ""
}

// promptChromeLines is the fixed rows RenderPromptFrame always emits
// around the transcript: tab bar (2), rule (1), status/blank (1), input
// (1), plus one spare so the outer footer never pushes the input off.
const promptChromeLines = 6

// RenderPromptFrame renders the Prompt surface: tab bar, transcript
// (newest exchanges kept when the pane is short), state line, and the
// input line naming its destination. Pure — state in, string out.
func RenderPromptFrame(st PromptSurfaceState, width, height int) string {
	var b strings.Builder
	b.WriteString(renderSurfaceTabs("Prompt", width))
	fmt.Fprintf(&b, "%s%s%s\n", Comment, HLine('─', width), RST)

	// Transcript body, oldest-clamped: build newest-first, keep what fits,
	// then restore chronological order.
	bodyBudget := height - promptChromeLines
	if bodyBudget < 0 {
		bodyBudget = 0
	}
	var body []string
	if len(st.Exchanges) == 0 && st.Unreachable == "" {
		body = append(body,
			fmt.Sprintf("  %sNo prompts yet this session.%s", Comment, RST),
			fmt.Sprintf("  %sType below — launch a graph, ask about a run, approve a named gate, or draft a new graph.%s", Comment, RST),
		)
	} else {
		var lines []string
		for i, ex := range st.Exchanges {
			lines = append(lines, fmt.Sprintf("  %s❯ %s%s%s%s", Comment, RST, FG, ex.Question, RST))
			if ex.Answered {
				lines = append(lines, fmt.Sprintf("    %s✓%s %s", Green, RST, ex.Answer))
			} else if st.Working && i == len(st.Exchanges)-1 {
				// Only the newest open question is "working" — older
				// unanswered ones are abandoned, and marking them all
				// would make one slow answer look like several.
				lines = append(lines, fmt.Sprintf("    %s⋯ working — the model is thinking; the pane stays live%s", Yellow, RST))
			}
		}
		if len(lines) > bodyBudget && bodyBudget > 0 {
			lines = lines[len(lines)-bodyBudget:]
		}
		body = lines
	}
	for _, ln := range body {
		b.WriteString(fitWidth(ln, width) + "\n")
	}

	// State line: unreachable outranks working — a dead model must never
	// render as merely slow. Both carry a glyph so the state reads without
	// color.
	if st.Unreachable != "" {
		b.WriteString(fitWidth(fmt.Sprintf("  %s✗ %s%s", Red+Bold, st.Unreachable, RST), width) + "\n")
	}

	// Input line — the destination is always visible, and the two modes
	// differ by glyph and word, not color alone.
	mode := "→ interpret"
	if st.Inject {
		mode = "⇒ inject"
	}
	b.WriteString(fitWidth(fmt.Sprintf("  %s[%s: %s]%s %s%s█%s",
		Purple+Bold, mode, st.Destination, RST, FG, st.Input, RST), width) + "\n")
	return b.String()
}

// PromptRenderOnce renders a single Prompt-surface frame — the
// scriptable seam for integration tests.
func PromptRenderOnce(session string, width int) (string, error) {
	if width <= 0 {
		width = termWidth()
	}
	st := PromptSurfaceState{
		Exchanges:   LoadPromptExchanges(session, promptTranscriptLimit),
		Destination: promptDestinationLabel(false, ""),
		Unreachable: PromptUnreachable(session),
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
