package bus

import "fmt"

// CheckTrackerWriteForMessage returns a deny message if a graph dispatch of a
// tracker write may not reach plan, or "" if the send is allowed (MUX-181).
//
// A graph tracker write — a node sending any action in gatedTrackerWriteActions,
// the list the validator's gate rule keys on — is handed to plan, which holds
// the Atlassian and GitHub-issue authority itself, so CheckAtlassianAuthority
// never sees the graph. Without this the only thing between the dispatch and a
// tracker entry other people read was plan's reading of a template sentence
// ("The user approved the tracker update"). The dispatch is now judged as a
// graph commit is: bound to the frozen node it names and released by a
// wait_human gate whose approval is attributable, authorized and audited
// (checkGraphGatedDispatch). It fails closed.
//
// Keyed on the RAW sender: only the graph executor's own sends are judged here.
// A request from edit relays the user, and one from any other agent is plan's
// rule to decline. Reads (jira-read, issue-read, confluence-read) are not in
// the list and pass.
func CheckTrackerWriteForMessage(session string, m Message) string {
	if !gatedTrackerWriteActions[m.Action] || !isGraphDispatch(m.From) {
		return ""
	}
	deny := func(why string) string {
		return fmt.Sprintf(
			"tracker writes are user-approved: this graph dispatch may not request %s because %s. "+
				"A wait_human gate approved by an authorized person is what releases it.", m.Action, why)
	}
	return checkGraphGatedDispatch(session, m, deny)
}
