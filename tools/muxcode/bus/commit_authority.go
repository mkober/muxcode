package bus

import (
	"fmt"
	"os"
	"strings"
)

// Git mutations are USER-INITIATED. Nothing in the fleet may commit, push, or
// open a PR on its own initiative — the user asks, and only then does it happen.
//
// Prose alone does not hold this line. Every agent definition already said
// "user-initiated only", and the plan agent still produced eight unauthorized
// commits in a live session: its own definition handed it the delegation
// pattern (`muxcode send commit commit "... push"`), the story-lifecycle loop
// drove it, and the commit agent — which has no notion of who is allowed to ask
// — obeyed every request. Instructions that contradict each other resolve in
// favour of whichever one the model read last. So the rule is enforced here, at
// the bus, where it cannot be argued with.
//
// The gate is authority, not intent: we cannot verify "the user asked for this"
// from inside a send. What we CAN verify is that the request came from the one
// agent that is actually in conversation with the user. Routing every git
// mutation through the user-facing orchestrator puts a human in the loop, which
// is the property that was missing.

// commitAuthorityDefault is the role permitted to request git mutations: the
// edit agent, the only agent the user talks to directly.
var commitAuthorityDefault = []string{"edit"}

// CommitAuthorityRoles returns the roles allowed to request git mutations.
//
// Override with MUXCODE_COMMIT_AUTHORITY_ROLES (comma-separated) to opt a role
// in — e.g. the autonomous story-lifecycle agent, which commits by design:
//
//	MUXCODE_COMMIT_AUTHORITY_ROLES=edit,auto
//
// Setting it to the empty string denies every role, including edit. That is a
// legitimate configuration: a session where no agent may touch git at all.
func CommitAuthorityRoles() []string {
	if v, ok := os.LookupEnv("MUXCODE_COMMIT_AUTHORITY_ROLES"); ok {
		return splitTrimmed(v)
	}
	return commitAuthorityDefault
}

// gitMutatingActions are the action labels that mutate git state. This is the
// single source of truth: cmd's isCommitAction delegates here.
//
// Gating only "commit" was a hole wide enough to drive through — a denied agent
// simply retries `muxcode send commit push "..."` (or stage/merge/rebase/tag),
// the daemon delivers it like any other request, and the commit agent obeys the
// payload. Every label that reaches git has to be on this list, or the gate is
// theatre.
var gitMutatingActions = map[string]bool{
	"commit": true,
	"stage":  true,
	"push":   true,
	"merge":  true,
	"rebase": true,
	"tag":    true,
}

// IsGitMutatingAction reports whether an action sent to the commit agent mutates
// git state.
//
// The commit agent's read-only action — "pr-read", used to fetch PR data, CI
// status, and review comments — stays open to every role: reading is not the
// problem, and gating it would break the PR review flow.
func IsGitMutatingAction(to, action string) bool {
	return NormalizeBusRole(to) == "commit" && gitMutatingActions[action]
}

// CheckCommitAuthority returns a deny message if `from` may not request a git
// mutation, or "" if the send is allowed.
//
// Deliberately NOT bypassable by --force. --force exists to skip the pre-commit
// agent-idle check, which is a convenience guard; this is a safety guard, and a
// safety guard that any caller can wave away is decoration.
// The deny message deliberately does NOT name the env var that would lift the
// block. MUXCODE_COMMIT_AUTHORITY_ROLES is process-scoped and read at send time,
// so an agent handed that string can self-authorize by prefixing it to the very
// command it was just refused — printing it here would be handing the lock-picks
// to the person we just locked out. The opt-in is documented for the USER, in
// docs/configuration.md, where it belongs.
func CheckCommitAuthority(from, to, action string) string {
	if !IsGitMutatingAction(to, action) {
		return ""
	}
	from = NormalizeBusRole(from)
	authorized := CommitAuthorityRoles()
	for _, allowed := range authorized {
		if NormalizeBusRole(allowed) == from {
			return ""
		}
	}
	who := "no role is authorized"
	if len(authorized) > 0 {
		who = "only " + strings.Join(authorized, ", ") + " is authorized"
	}
	return fmt.Sprintf(
		"git mutations are user-initiated: %s may not request a commit/stage/push/merge/rebase/tag (%s). "+
			"Report what is ready to commit and let the user decide.",
		from, who)
}
