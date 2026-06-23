package bus

import "strings"

// providerLoopSignatures are substrings that, when present in a non-hook
// agent's pane, strongly indicate the agent is wedged inside a provider-side
// loop or a fatal request-validation error that it cannot recover from on its
// own. These come from observed OpenCode/Codex failures where the underlying
// model API (e.g. DashScope/Qwen) rejects repeated identical tool calls or a
// malformed request, and the agent process stays alive+active — so neither the
// dead-process restart (checkAgentHealth) nor the idle wake-up (checkIdleAgents)
// ever fires. Matching is case-insensitive.
var providerLoopSignatures = []string{
	"internalerror.algo",
	"repeated across multiple consecutive rounds",
	"adjust the tool call arguments to avoid infinite loops",
	"no matching discriminator",
	"type validation failed",
}

// PaneShowsProviderLoop reports whether the captured pane content contains a
// known provider-loop / fatal-validation signature. Used by the daemon's
// stuck-provider watchdog to decide whether a non-hook agent needs an
// automatic reload. A single match is enough for this function; the caller is
// responsible for any debounce (e.g. requiring the signature across two
// consecutive checks) before acting.
func PaneShowsProviderLoop(content string) bool {
	if content == "" {
		return false
	}
	lower := strings.ToLower(content)
	for _, sig := range providerLoopSignatures {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

// permissionBlockSignatures are substrings that, when present in a hook-provider
// (Claude Code) agent's pane, indicate the agent is wedged at a REJECTED
// permission prompt it cannot satisfy autonomously — e.g. `./build.sh` denied
// with no human present to approve. The agent never sends a response, so the
// pending request stays actionable and the idle-delivery safety net re-wakes it
// endlessly while the requester hangs. These are the phrases the agent emits
// after a permission denial (observed across Claude models). Matching is
// case-insensitive. Kept deliberately narrow to avoid false positives on agents
// that merely mention permissions in normal output; the daemon's
// checkStuckPermissions further gates on a pending unanswered request plus a
// multi-sighting debounce before acting.
var permissionBlockSignatures = []string{
	"blocked by permission system",
	"without explicit user authorization",
	"rejected. unable to proceed",
	"blocked by the permission system",
}

// PaneShowsPermissionBlock reports whether the captured pane content contains a
// known permission-denial signature. Used by the daemon's permission-block
// watchdog to decide whether a hook-provider agent is stuck at a rejected
// prompt. A single match is enough for this function; the caller is responsible
// for any debounce and for gating on a pending unanswered request.
func PaneShowsPermissionBlock(content string) bool {
	if content == "" {
		return false
	}
	lower := strings.ToLower(content)
	for _, sig := range permissionBlockSignatures {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}
