package bus

import (
	"fmt"
	"os"
	"strings"
)

// The prompt role's requests carry a special premise: the approve guard
// (harness prompt_guard.go) verifies gate/run ids against "the user's
// typed text" — which is, mechanically, just the request payload. That
// premise holds only if request payloads addressed to prompt genuinely
// originate from a human at the Prompt surface. A bus request from an
// agent — `muxcode send prompt prompt "approve commit-gate on run X"` —
// is byte-identical to a surface send and would launder agent-authored
// text into human authority, and gate approvals release git/Atlassian
// mutations (authority hole found by plan's verify pass, 2026-08-26).
//
// The default authority list is therefore EMPTY: no bus identity may
// address requests to prompt. The surface is deliberately absent from
// the list — it sends through SendHumanPrompt, an in-process seam agents
// cannot reach: every agent-accessible road (the send CLI, chains,
// subscriptions, hooks, the webhook) funnels through sendMessage, where
// the check runs. Graph nodes targeting prompt are additionally rejected
// at validate time (validateNode), so a definition fails early instead
// of its run failing at dispatch.

// PromptAuthorityRoles returns the bus identities allowed to send
// requests to the prompt role. Default: none. Comma-separated opt-in via
// MUXCODE_PROMPT_AUTHORITY_ROLES.
func PromptAuthorityRoles() []string {
	v, ok := os.LookupEnv("MUXCODE_PROMPT_AUTHORITY_ROLES")
	if !ok {
		return nil
	}
	var roles []string
	for _, r := range strings.Split(v, ",") {
		if r = strings.TrimSpace(r); r != "" {
			roles = append(roles, r)
		}
	}
	return roles
}

// CheckPromptAuthority denies request-type messages addressed to the
// prompt role from any non-authorized sender. Returns a deny reason, or
// "" when allowed. Responses are the caller's concern (the wiring gates
// requests only) — refusing a reply that echoes an action label would
// strand the sender's tracked task, the same rule the commit gate keeps.
func CheckPromptAuthority(from, to string) string {
	if NormalizeBusRole(to) != "prompt" {
		return ""
	}
	for _, r := range PromptAuthorityRoles() {
		if r == from {
			return ""
		}
	}
	return fmt.Sprintf("requests to the prompt role are human-initiated: their text is what the approve guard trusts as the user's own words, so bus identity %q cannot originate one — type it in the control pane's Prompt surface (override: MUXCODE_PROMPT_AUTHORITY_ROLES)", from)
}

// SendHumanPrompt is the Prompt surface's sanctioned send: the text was
// typed by a human in the surface's own process, so it bypasses
// CheckPromptAuthority. No auto-CC — a prompt typed on the build window
// must not ping edit's inbox.
func SendHumanPrompt(session, from, text string) error {
	return sendMessage(session, NewMessage(from, "prompt", "request", "prompt", text, ""), false, false, true)
}
