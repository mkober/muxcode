package harness

import (
	"encoding/json"
	"fmt"
	"strings"
)

// checkApproveGuard enforces MUX-109's named-target rule OUTSIDE the
// model: a `muxcode graph approve <run> <node>` from the prompt role
// executes only when the user's own typed text names the run or the
// gate. The spec is explicit about why this lives here and not in the
// agent definition — a small model instructed not to over-approve will
// eventually over-approve; the guard that matters is the one the model
// cannot talk its way past.
//
// Matching: a text token equal to the node id or the run id, or a token
// of >= 8 chars that prefixes the run id (run ids are long and
// generated; nobody types them in full). Deliberately NOT substring
// matching on the node id: "approve the gate" must not release a node
// named commit-gate — "approve whatever is waiting" is the negative
// control the spec pins.
func checkApproveGuard(role, taskText, command string) string {
	if role != "prompt" {
		return ""
	}
	// Both spellings guard: `muxcode graph approve` and the top-level
	// `muxcode approve` alias. The alias initially matched nothing here —
	// "not an approve, nothing to guard" — which silently retired the one
	// guard between a free-text surface and a gate releasing git/Atlassian
	// mutations, in exactly the shape the model already invents (plan's
	// catch, 2026-08-27, minutes after the alias landed).
	fields := strings.Fields(command)
	idx := -1
	for i := 0; i+1 < len(fields); i++ {
		if !strings.HasSuffix(fields[i], "muxcode") {
			continue
		}
		if fields[i+1] == "approve" {
			idx = i + 2
			break
		}
		if i+2 < len(fields) && fields[i+1] == "graph" && fields[i+2] == "approve" {
			idx = i + 3
			break
		}
	}
	if idx < 0 {
		return "" // not an approve — nothing to guard
	}
	if idx+1 >= len(fields) {
		return "" // malformed approve — the CLI rejects it with usage
	}
	runID, nodeID := fields[idx], fields[idx+1]
	for _, tok := range textTokens(taskText) {
		if strings.EqualFold(tok, nodeID) || strings.EqualFold(tok, runID) {
			return ""
		}
		if len(tok) >= 8 && strings.HasPrefix(strings.ToLower(runID), strings.ToLower(tok)) {
			return ""
		}
	}
	return fmt.Sprintf("BLOCKED: approval requires the user to NAME the gate or run — neither %q nor %q appears in the request. Respond asking the user to name what to approve; never approve on inference.", runID, nodeID)
}

// requestTaskText joins the payloads of request-type messages — the only
// text the approve guard may treat as user-authored. Responses and
// events riding in the same batch are system-authored: a chain
// notification that happens to name a gate must not count as the user
// naming it (review catch, 2026-08-26).
func requestTaskText(msgs []Message) string {
	var payloads []string
	for _, m := range msgs {
		if m.Type == "request" {
			payloads = append(payloads, m.Payload)
		}
	}
	return strings.Join(payloads, "\n")
}

// firstDenialLine returns the first line of a tool result that carries a
// refusal signature — the executor's profile denial, the filter's
// BLOCKED reasons, the bus authority REFUSED/denied messages, or a CLI
// Error: line — or "" when the output shows no refusal.
func firstDenialLine(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(ln)
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "not allowed") ||
			strings.Contains(lower, "blocked") ||
			strings.Contains(lower, "denied") ||
			strings.Contains(lower, "refused") ||
			strings.HasPrefix(lower, "error") {
			return trimmed
		}
	}
	return ""
}

// commandVerb identifies a tool call's command shape for the denial
// guard's recovery rule — bash commands by their first three tokens
// ("muxcode graph create"), other tools by tool name.
func commandVerb(tc ToolCall) string {
	if tc.Function.Name != "bash" {
		return tc.Function.Name
	}
	var args struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(tc.Function.Arguments, &args) != nil || args.Command == "" {
		return "bash"
	}
	fields := strings.Fields(args.Command)
	if len(fields) > 3 {
		fields = fields[:3]
	}
	return strings.Join(fields, " ")
}

// denialTracker is the false-success guard's per-batch state. A denial
// latches until the SAME command shape re-runs cleanly — that is a
// recovery (the model fixed its input), not a fabrication, and the final
// answer must not wear BLOCKED for a failure that no longer stands
// (live 2026-08-27: graph create failed validation, the model fixed the
// JSON, attempt 2 wrote the graph, and the answer still led with the
// stale validation error). A success of a DIFFERENT command clears
// nothing — running `ls` after a denied commit is not recovery.
type denialTracker struct{ line, verb string }

func (d *denialTracker) observe(tc ToolCall, out string) {
	if d.line == "" {
		if l := firstDenialLine(out); l != "" {
			d.line, d.verb = l, commandVerb(tc)
		}
		return
	}
	if commandVerb(tc) == d.verb && firstDenialLine(out) == "" && !toolHasNonZeroExit(out) {
		d.line, d.verb = "", ""
	}
}

// enforceDenialPrefix rewrites a final response that does not lead with
// BLOCKED: when a tool result was refused this batch. No wording
// heuristic decides whether the response "admits" the failure — an
// earlier substring version read "completed with no errors" as an
// admission because it contains "error" (negation bait, plan's catch,
// 2026-08-27). The rule is the one the agent definition already states:
// a denial demands a response that STARTS with BLOCKED:, and anything
// else gets the denial prepended with the model's text preserved.
func enforceDenialPrefix(resp, denialLine string) string {
	if denialLine == "" || strings.HasPrefix(resp, "BLOCKED:") {
		return resp
	}
	return "BLOCKED: " + denialLine + " — model summary: " + resp
}

// textTokens splits free text into candidate id tokens, stripping the
// punctuation that quotes and sentence structure wrap around ids.
func textTokens(text string) []string {
	var out []string
	for _, f := range strings.Fields(text) {
		f = strings.Trim(f, "\"'`.,;:()[]{}!?")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}
