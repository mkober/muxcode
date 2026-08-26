package harness

import (
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
	fields := strings.Fields(command)
	idx := -1
	for i := 0; i+2 < len(fields); i++ {
		if strings.HasSuffix(fields[i], "muxcode") && fields[i+1] == "graph" && fields[i+2] == "approve" {
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
