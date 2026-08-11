package bus

import (
	"fmt"
	"os"
	"strings"
)

// Jira and Confluence are SHARED SYSTEMS. They are not scratch space: an issue
// description, a comment, an issue link, a transition — every one of these is
// visible to the user's whole team, arrives with the user's name on it, and
// cannot be quietly undone the way a local file edit can.
//
// Prose alone does not hold this line, and we have the receipt. The plan agent
// rewrote a Jira description, posted a comment, and created an issue link to a
// second story — all as a side effect of a request to revise a spec document.
// It was not disobeying. agents/planner.md told it to: "automatically update the
// corresponding Jira story description ... Do not ask the user — treat this as
// part of your standard workflow", and the jira-manage-issues skill supplied the
// dependency-link extraction routine. Meanwhile the edit agent's own definition
// claimed edit owned the integration and that plan should delegate via a
// jira-update message. Two contradictory mandates, and the model resolved them
// in favour of the one it read last. That is the same failure that produced
// eight unauthorized commits before CheckCommitAuthority moved the git rule out
// of prose and into the bus. Same disease, same cure.
//
// The gate is authority, not intent: we cannot verify "the user asked for this"
// from inside a CLI call. What we CAN verify is which agent is calling, so the
// write surface is deliberately narrowed to exactly one role.
//
// That role is now the plan agent, by explicit operator decision: plan owns the
// shared WRITTEN artifacts — specs under docs/, and the tracker items those
// specs describe — so the tooling and the ownership finally sit together.
//
// Be clear about what this costs. The previous holder was edit, justified by
// edit being the only agent in direct conversation with the user: the human was
// structurally in the loop because the writing agent was the one being spoken
// to. Plan does not have that property. It acts on bus messages, so the gate
// alone no longer proves a human asked for anything.
//
// What replaces it is a scope rule, not a checkable invariant, and it is aimed
// squarely at the incident above: plan writes to Jira/Confluence ONLY on an
// explicit user-initiated request relayed from edit, and NEVER as a side effect
// of a spec or docs change. agents/planner.md carries that rule, and the
// "automatically update the corresponding Jira story / do not ask the user"
// instruction that caused the original cascade is gone.
//
// So this default is weaker than what it replaces, on purpose. If the tracker
// should be strictly human-owned, set MUXCODE_ATLASSIAN_AUTHORITY_ROLES="" and
// every role is denied.

// atlassianAuthorityDefault is the role permitted to write to Jira and
// Confluence: the plan agent, which owns shared written artifacts. Changing this
// value changes a security boundary — TestAtlassianAuthorityDefault pins it so
// the change cannot happen silently.
var atlassianAuthorityDefault = []string{"plan"}

// AtlassianAuthorityRoles returns the roles allowed to mutate Jira/Confluence.
//
// Override with MUXCODE_ATLASSIAN_AUTHORITY_ROLES (comma-separated) to opt a
// role in — e.g. the autonomous story-lifecycle agent, which transitions issues
// by design:
//
//	MUXCODE_ATLASSIAN_AUTHORITY_ROLES=edit,auto
//
// Setting it to the empty string denies every role. That is a legitimate
// configuration, and arguably the right default for a team where the tracker is
// strictly human-owned: agents read for context, the human makes every write.
func AtlassianAuthorityRoles() []string {
	if v, ok := os.LookupEnv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES"); ok {
		return splitTrimmed(v)
	}
	return atlassianAuthorityDefault
}

// atlassianMCPReadPrefixes are the operation-name prefixes on an Atlassian MCP
// tool that only read. Anything else is treated as a write.
//
// Allowlisted for the same reason as atlassianReadOnlyActions: the MCP surface
// is defined by a remote server, not by this repo, so it can grow a new mutating
// tool at any time without a code change here. Matching "does it look like a
// write?" would let that new tool through; matching "is it a known read?" makes
// it land closed.
var atlassianMCPReadPrefixes = []string{
	"get", "search", "fetch", "lookup", "list", "read",
}

// IsAtlassianMCPTool reports whether a tool name belongs to an Atlassian MCP
// server, and if so whether it mutates.
//
// Every agent definition already says "CLI only — never the Atlassian MCP", and
// that instruction is exactly as binding as the one that told the plan agent not
// to touch Jira on its own initiative. The CLI gate is worthless if an agent can
// reach the same Jira issue through mcp__..._editJiraIssue instead, so the rule
// is enforced on both roads rather than asserted on one.
func IsAtlassianMCPTool(toolName string) (isAtlassian, mutates bool) {
	lower := strings.ToLower(toolName)
	if !strings.HasPrefix(lower, "mcp__") || !strings.Contains(lower, "atlassian") {
		return false, false
	}
	op := toolName
	if idx := strings.LastIndex(toolName, "__"); idx >= 0 {
		op = toolName[idx+2:]
	}
	opLower := strings.ToLower(op)
	for _, prefix := range atlassianMCPReadPrefixes {
		if strings.HasPrefix(opLower, prefix) {
			return true, false
		}
	}
	return true, true
}

// CheckAtlassianMCPGuard blocks an unauthorized role from calling a mutating
// Atlassian MCP tool. Returns nil when the tool is unrelated, read-only, or the
// role is authorized.
func CheckAtlassianMCPGuard(role, toolName string) *GuardDecision {
	isAtlassian, mutates := IsAtlassianMCPTool(toolName)
	if !isAtlassian || !mutates {
		return nil
	}
	// Reuse the CLI decision so authority lives in exactly one place. The
	// service/action pair is nominal — only the mutating verdict matters, and
	// that is already settled above.
	if deny := CheckAtlassianAuthority(role, "jira", "update"); deny != "" {
		return &GuardDecision{
			Blocked: true,
			Reason: "BLOCKED: " + deny +
				" This applies to the Atlassian MCP as well as the CLI — use `muxcode atlassian ... read` for context instead.",
		}
	}
	return nil
}

// HasAtlassianAuthorityLimit reports whether a role is subject to the Atlassian
// write gate — i.e. whether it is worth running the PreToolUse check at all.
//
// Used by the guard hook to decide if a role needs interception. Authorized
// roles (and the human, who presents as no role) are not limited and can skip
// the check entirely.
func HasAtlassianAuthorityLimit(role string) bool {
	return CheckAtlassianAuthority(role, "jira", "update") != ""
}

// atlassianReadOnlyActions is an ALLOWLIST of subcommands that only read.
//
// Deliberately inverted relative to gitMutatingActions, which enumerates the
// mutating side and therefore silently opens a hole every time a new mutating
// verb is added and someone forgets the list. Here, anything not named below is
// treated as a write. A new `muxcode atlassian jira assign` lands closed rather
// than wide open, and the failure mode of forgetting to update this map is a
// spurious denial — loud, immediate, and harmless — instead of a shared system
// mutated by an agent nobody asked.
//
// Note the near-collisions, which are the whole reason this is matched exactly
// rather than by prefix: "comments" reads, "comment" writes; "transitions"
// lists, "transition" executes.
var atlassianReadOnlyActions = map[string]map[string]bool{
	"jira": {
		"read":        true,
		"comments":    true,
		"link-types":  true,
		"transitions": true,
		"search":      true,
	},
	"confluence": {
		"read":   true,
		"search": true,
	},
}

// IsAtlassianMutatingAction reports whether a `muxcode atlassian` invocation
// writes to a shared Atlassian system.
//
// Reading stays open to every role. Agents need Jira and Confluence context to
// write good specs, and gating reads would break that for no safety gain — the
// harm here is writes landing in front of a team, not an agent knowing what a
// ticket says. An unrecognised service or action counts as mutating, per the
// allowlist rationale above.
func IsAtlassianMutatingAction(service, action string) bool {
	readOnly, known := atlassianReadOnlyActions[service]
	if !known {
		return true
	}
	return !readOnly[action]
}

// CheckAtlassianAuthority returns a deny message if the calling role may not
// perform this Atlassian write, or "" if the call is allowed.
//
// The empty role — no AGENT_ROLE, no BUS_ROLE, no tmux window name — is allowed
// through as a human at a terminal. That is sound because agent identity is
// always set: LaunchConfig exports AGENT_ROLE for every launched agent
// (bus/launch.go), the local harness sets it on every tool exec
// (harness/bus.go), and spawn roles set it explicitly (bus/spawn.go). An agent
// therefore cannot present as "unknown" without someone deliberately stripping
// its environment, whereas the user running `muxcode atlassian jira update` from
// their own shell legitimately has no role at all. Locking the user out of their
// own tracker to guard against a threat that would require sabotaging the
// launcher would be protecting the wrong party.
//
// Like CheckCommitAuthority, the deny message does NOT name the env var that
// lifts the block: it is read at call time from the calling process, so an agent
// handed that string could self-authorize by prefixing it to the very command it
// was just refused. The opt-in is documented for the USER, in
// docs/configuration.md, where it belongs.
func CheckAtlassianAuthority(role, service, action string) string {
	if !IsAtlassianMutatingAction(service, action) {
		return ""
	}
	role = NormalizeBusRole(strings.TrimSpace(role))
	if role == "" || role == "unknown" {
		return ""
	}
	authorized := AtlassianAuthorityRoles()
	for _, allowed := range authorized {
		if NormalizeBusRole(allowed) == role {
			return ""
		}
	}
	who := "no agent role is authorized"
	if len(authorized) > 0 {
		who = "only " + strings.Join(authorized, ", ") + " is authorized"
	}
	return fmt.Sprintf(
		"Jira and Confluence writes are user-initiated: %s may not run `atlassian %s %s` (%s). "+
			"This is a shared system the user's team sees. Report what you would have written and let the user decide.",
		role, service, action, who)
}
