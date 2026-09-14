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
	if isGraphDispatch(from) {
		return denyGraphDispatch("it carries no graph provenance")
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

// isGraphDispatch reports whether a sender is the graph executor's identity,
// read BEFORE normalization.
//
// NormalizeBusRole("daemon") returns "edit", the default commit authority, so a
// check that normalizes first cannot tell a graph node's request from the
// request of the one agent actually talking to the user. That is the whole of
// MUX-144 Defect C, and reading the raw sender is what closes it. The
// normalization itself stays — replies to a daemon send must still reach edit.
func isGraphDispatch(from string) bool {
	return strings.EqualFold(strings.TrimSpace(from), graphSender)
}

// denyGraphDispatch words a graph refusal. Like CheckCommitAuthority's own
// message it names no lever that would lift the block, and it points at the
// gate, because an approval by an authorized person is the only thing that
// legitimately releases one of these.
func denyGraphDispatch(why string) string {
	return fmt.Sprintf(
		"git mutations are user-initiated: this graph dispatch may not request a "+
			"commit/stage/push/merge/rebase/tag because %s. A wait_human gate "+
			"approved by an authorized person is what releases it.", why)
}

// CheckCommitAuthorityForMessage returns a deny message if m may not request a
// git mutation, or "" if the send is allowed.
//
// This is the road every non-CLI sender crosses, and the only one that can
// judge a graph dispatch on the gate that released it rather than on its
// sender: the run id and node id travel on the message, and the approval
// they lead to is the evidence a human consented.
func CheckCommitAuthorityForMessage(session string, m Message) string {
	if !IsGitMutatingAction(m.To, m.Action) {
		return ""
	}
	if isGraphDispatch(m.From) {
		return checkGraphCommitDispatch(session, m)
	}
	return CheckCommitAuthority(m.From, m.To, m.Action)
}

// checkGraphCommitDispatch judges a graph-dispatched git mutation on the
// wait_human gate that released its node.
//
// Two questions, and both must hold. First: is this message actually the work
// the frozen graph defines for the node it names — same kind, same target, same
// action, same interpolated payload? A gate approves ONE action, so without that
// binding any audited approval anywhere in the run would authorize an arbitrary
// commit; gateTerritory contains the gate itself and every downstream node of
// any kind, so a dispatch could name a read-only successor, or keep a real
// node's id and substitute a different payload. Second: did a wait_human gate
// whose territory contains that node carry an approval that is attributable,
// authorized and corroborated?
//
// Anything else is refused, including a dispatch with no provenance and a run
// that cannot be read. This is the backstop, so it fails closed: absence of
// evidence that a human approved is not evidence of approval.
func checkGraphCommitDispatch(session string, m Message) string {
	if m.GraphRun == "" || m.GraphNode == "" {
		return denyGraphDispatch("it carries no graph provenance")
	}
	run, err := ReadGraphRun(session, m.GraphRun)
	if err != nil {
		return denyGraphDispatch(fmt.Sprintf("its run %q cannot be read", m.GraphRun))
	}
	g, err := ReadGraphRunGraph(session, m.GraphRun)
	if err != nil {
		return denyGraphDispatch(fmt.Sprintf("the frozen graph for run %q cannot be read", m.GraphRun))
	}
	byID := make(map[string]*Node, len(g.Nodes))
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
	}
	n, ok := byID[m.GraphNode]
	if !ok {
		return denyGraphDispatch(fmt.Sprintf("node %q is not in the frozen graph for run %s", m.GraphNode, m.GraphRun))
	}
	if why := dispatchMatchesNode(session, run, n, m); why != "" {
		return denyGraphDispatch(why)
	}
	for i := range g.Nodes {
		gate := &g.Nodes[i]
		if gate.Type != NodeWaitHuman || !g.gateTerritory(byID, gate.ID)[n.ID] {
			continue
		}
		if graphGateApprovalAuthorizes(session, run, gate.ID) {
			return ""
		}
	}
	return denyGraphDispatch(fmt.Sprintf(
		"no wait_human gate released node %q on an audited, authorized approval", n.ID))
}

// dispatchMatchesNode reports why a message is not the work its named node
// defines, or "" when it matches.
//
// The payload is re-interpolated exactly as dispatchNode built it, with an empty
// item: validateGates refuses a commit-role map or spawn node, so a git mutation
// only ever reaches the bus from a plain send.
func dispatchMatchesNode(session string, run *GraphRun, n *Node, m Message) string {
	if n.Type != NodeSend {
		return fmt.Sprintf("node %q is a %s node, not a send", n.ID, n.Type)
	}
	if NormalizeBusRole(n.Role) != NormalizeBusRole(m.To) {
		return fmt.Sprintf("node %q dispatches to %q, not to %q", n.ID, n.Role, m.To)
	}
	if n.Action != m.Action {
		return fmt.Sprintf("node %q dispatches action %q, not %q", n.ID, n.Action, m.Action)
	}
	if interpolateGraphMessage(session, run, n.Message, "") != m.Payload {
		return fmt.Sprintf("the payload is not what node %q defines", n.ID)
	}
	return ""
}

// graphGateApprovalAuthorizes reports whether a gate's approval marker holds.
func graphGateApprovalAuthorizes(session string, run *GraphRun, gateID string) bool {
	marker, err := os.ReadFile(graphApprovalPath(session, run.ID, gateID, "approved"))
	if err != nil {
		return false
	}
	_, deny := approvalDenial(session, run, gateID, marker)
	return deny == ""
}
