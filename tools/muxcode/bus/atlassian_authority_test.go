package bus

import (
	"strings"
	"testing"
)

func TestIsAtlassianMutatingAction_ReadsAreOpen(t *testing.T) {
	readOnly := []struct{ service, action string }{
		{"jira", "read"},
		{"jira", "comments"},
		{"jira", "link-types"},
		{"jira", "transitions"},
		{"jira", "search"},
		{"confluence", "read"},
		{"confluence", "search"},
	}
	for _, c := range readOnly {
		if IsAtlassianMutatingAction(c.service, c.action) {
			t.Errorf("%s %s: expected read-only, got mutating", c.service, c.action)
		}
	}
}

func TestIsAtlassianMutatingAction_WritesAreGated(t *testing.T) {
	writes := []struct{ service, action string }{
		{"jira", "update"},
		{"jira", "comment"},
		{"jira", "link"},
		{"jira", "transition"},
		{"jira", "create-subtask"},
		{"jira", "worklog"},
		{"jira", "attach"},
		{"confluence", "update"},
		{"confluence", "attach"},
	}
	for _, c := range writes {
		if !IsAtlassianMutatingAction(c.service, c.action) {
			t.Errorf("%s %s: expected mutating, got read-only", c.service, c.action)
		}
	}
}

// The three writes that landed on PBP1-4849 as a side effect of a docs request.
// If any of these ever reads as non-mutating again, the incident can recur.
func TestIsAtlassianMutatingAction_RegressionPBP1_4849(t *testing.T) {
	for _, action := range []string{"update", "comment", "link"} {
		if !IsAtlassianMutatingAction("jira", action) {
			t.Errorf("jira %s must be gated: it is one of the writes that triggered this gate", action)
		}
	}
}

// "comments" reads but "comment" writes; "transitions" lists but "transition"
// executes. A prefix match would collapse these pairs and either block reads or
// wave writes through.
func TestIsAtlassianMutatingAction_NearCollisions(t *testing.T) {
	if IsAtlassianMutatingAction("jira", "comments") {
		t.Error("jira comments (read) must not be gated as a write")
	}
	if !IsAtlassianMutatingAction("jira", "comment") {
		t.Error("jira comment (write) must be gated")
	}
	if IsAtlassianMutatingAction("jira", "transitions") {
		t.Error("jira transitions (list) must not be gated as a write")
	}
	if !IsAtlassianMutatingAction("jira", "transition") {
		t.Error("jira transition (execute) must be gated")
	}
}

// A subcommand added later must land closed, not open.
func TestIsAtlassianMutatingAction_UnknownDefaultsToMutating(t *testing.T) {
	if !IsAtlassianMutatingAction("jira", "assign") {
		t.Error("unknown jira action must default to mutating")
	}
	if !IsAtlassianMutatingAction("bitbucket", "read") {
		t.Error("unknown service must default to mutating")
	}
}

func TestCheckAtlassianAuthority_EditAuthorized(t *testing.T) {
	t.Setenv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES", "edit")
	if deny := CheckAtlassianAuthority("edit", "jira", "update"); deny != "" {
		t.Errorf("edit must be authorized to write, got deny: %s", deny)
	}
}

func TestCheckAtlassianAuthority_PlanDenied(t *testing.T) {
	t.Setenv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES", "edit")
	deny := CheckAtlassianAuthority("plan", "jira", "update")
	if deny == "" {
		t.Fatal("plan must be denied Jira writes")
	}
	if !strings.Contains(deny, "plan") {
		t.Errorf("deny message should name the refused role, got: %s", deny)
	}
}

// The deny message must not hand the refused agent the env var that lifts the
// block — it is read at call time, so an agent could prefix it to the very
// command it was just refused.
func TestCheckAtlassianAuthority_DenyDoesNotLeakOptIn(t *testing.T) {
	t.Setenv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES", "edit")
	deny := CheckAtlassianAuthority("plan", "jira", "comment")
	if strings.Contains(deny, "MUXCODE_ATLASSIAN_AUTHORITY_ROLES") {
		t.Errorf("deny message must not name the opt-in env var, got: %s", deny)
	}
}

func TestCheckAtlassianAuthority_ReadsAllowedForEveryRole(t *testing.T) {
	t.Setenv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES", "edit")
	for _, role := range []string{"plan", "review", "build", "research", "auto"} {
		if deny := CheckAtlassianAuthority(role, "jira", "read"); deny != "" {
			t.Errorf("%s must be able to read Jira, got deny: %s", role, deny)
		}
		if deny := CheckAtlassianAuthority(role, "confluence", "read"); deny != "" {
			t.Errorf("%s must be able to read Confluence, got deny: %s", role, deny)
		}
	}
}

// The user at their own shell has no AGENT_ROLE and no tmux window name.
// Denying them would lock the owner out of their own tracker.
func TestCheckAtlassianAuthority_HumanShellAllowed(t *testing.T) {
	t.Setenv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES", "edit")
	for _, role := range []string{"", "unknown", "   "} {
		if deny := CheckAtlassianAuthority(role, "jira", "update"); deny != "" {
			t.Errorf("role %q (human shell) must be allowed, got deny: %s", role, deny)
		}
	}
}

func TestCheckAtlassianAuthority_OptInRole(t *testing.T) {
	t.Setenv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES", "edit,auto")
	if deny := CheckAtlassianAuthority("auto", "jira", "transition"); deny != "" {
		t.Errorf("auto opted in must be authorized, got deny: %s", deny)
	}
	if deny := CheckAtlassianAuthority("plan", "jira", "transition"); deny == "" {
		t.Error("plan must still be denied when only edit,auto are opted in")
	}
}

// An empty authority list is a legitimate configuration: nobody writes.
func TestCheckAtlassianAuthority_EmptyListDeniesAll(t *testing.T) {
	t.Setenv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES", "")
	if deny := CheckAtlassianAuthority("edit", "jira", "update"); deny == "" {
		t.Error("empty authority list must deny even edit")
	}
	if deny := CheckAtlassianAuthority("edit", "jira", "read"); deny != "" {
		t.Errorf("reads must stay open even with an empty authority list, got: %s", deny)
	}
}

// "planner" normalizes to "plan"; a role alias must not slip past the gate.
func TestCheckAtlassianAuthority_NormalizesRoleAliases(t *testing.T) {
	t.Setenv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES", "edit")
	if deny := CheckAtlassianAuthority("planner", "jira", "update"); deny == "" {
		t.Error("planner (alias of plan) must be denied")
	}
}

func TestHasAtlassianAuthorityLimit(t *testing.T) {
	t.Setenv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES", "edit")
	if HasAtlassianAuthorityLimit("edit") {
		t.Error("edit is authorized, so it is not limited")
	}
	if !HasAtlassianAuthorityLimit("plan") {
		t.Error("plan is limited and must be intercepted by the guard hook")
	}
}

func TestAtlassianCommandTarget(t *testing.T) {
	cases := []struct {
		command         string
		service, action string
	}{
		{"muxcode atlassian jira update PBP1-4849 /tmp/x.json", "jira", "update"},
		{"cd /repo && muxcode atlassian jira comment PBP1-4849 /tmp/c.json", "jira", "comment"},
		{"MUXCODE_CONFIG=/tmp/cfg muxcode atlassian jira link Blocks A B", "jira", "link"},
		{"./bin/muxcode atlassian confluence update 123 /tmp/p.json", "confluence", "update"},
		{"/usr/local/bin/muxcode atlassian jira read PBP1-4849", "jira", "read"},
		{"muxcode send edit jira-suggest \"stale\"", "", ""},
		{"echo atlassian jira update", "", ""},
		{"muxcode atlassian jira", "", ""},
	}
	for _, c := range cases {
		service, action := atlassianCommandTarget(c.command)
		if service != c.service || action != c.action {
			t.Errorf("%q: got (%q,%q), want (%q,%q)", c.command, service, action, c.service, c.action)
		}
	}
}

func TestIsAtlassianMCPTool(t *testing.T) {
	cases := []struct {
		tool                 string
		isAtlassian, mutates bool
	}{
		{"mcp__claude_ai_Atlassian__editJiraIssue", true, true},
		{"mcp__claude_ai_Atlassian__addCommentToJiraIssue", true, true},
		{"mcp__claude_ai_Atlassian__createIssueLink", true, true},
		{"mcp__claude_ai_Atlassian__transitionJiraIssue", true, true},
		{"mcp__claude_ai_Atlassian__updateConfluencePage", true, true},
		{"mcp__claude_ai_Atlassian__getJiraIssue", true, false},
		{"mcp__claude_ai_Atlassian__searchJiraIssuesUsingJql", true, false},
		{"mcp__claude_ai_Atlassian__lookupJiraAccountId", true, false},
		{"mcp__claude_ai_Atlassian__fetch", true, false},
		{"mcp__plugin_atlassian_atlassian__authenticate", true, true},
		// Unrelated tools must not be intercepted.
		{"Bash", false, false},
		{"Edit", false, false},
		{"mcp__plugin_firebase_firebase__firebase_deploy", false, false},
	}
	for _, c := range cases {
		isAtlassian, mutates := IsAtlassianMCPTool(c.tool)
		if isAtlassian != c.isAtlassian || mutates != c.mutates {
			t.Errorf("%s: got (atlassian=%v,mutates=%v), want (%v,%v)",
				c.tool, isAtlassian, mutates, c.isAtlassian, c.mutates)
		}
	}
}

// An MCP tool this repo has never heard of must land closed.
func TestIsAtlassianMCPTool_UnknownVerbIsAWrite(t *testing.T) {
	_, mutates := IsAtlassianMCPTool("mcp__claude_ai_Atlassian__bulkArchiveEverything")
	if !mutates {
		t.Error("unknown Atlassian MCP operation must default to mutating")
	}
}

func TestCheckAtlassianMCPGuard(t *testing.T) {
	t.Setenv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES", "edit")

	if d := CheckAtlassianMCPGuard("plan", "mcp__claude_ai_Atlassian__editJiraIssue"); d == nil || !d.Blocked {
		t.Error("plan must be blocked from editing a Jira issue via MCP")
	}
	if d := CheckAtlassianMCPGuard("plan", "mcp__claude_ai_Atlassian__getJiraIssue"); d != nil {
		t.Errorf("plan must be able to read via MCP, got block: %s", d.Reason)
	}
	if d := CheckAtlassianMCPGuard("edit", "mcp__claude_ai_Atlassian__editJiraIssue"); d != nil {
		t.Errorf("edit is authorized, got block: %s", d.Reason)
	}
	if d := CheckAtlassianMCPGuard("plan", "Bash"); d != nil {
		t.Error("non-MCP tool must not be intercepted")
	}
}

func TestCheckAtlassianCommandGuard(t *testing.T) {
	t.Setenv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES", "edit")

	if d := CheckAtlassianCommandGuard("plan", "muxcode atlassian jira update PBP1-4849 /tmp/x.json"); d == nil || !d.Blocked {
		t.Error("plan writing a Jira description must be blocked at the tool layer")
	}
	if d := CheckAtlassianCommandGuard("plan", "muxcode atlassian jira read PBP1-4849"); d != nil {
		t.Errorf("plan reading Jira must be allowed, got block: %s", d.Reason)
	}
	if d := CheckAtlassianCommandGuard("edit", "muxcode atlassian jira update PBP1-4849 /tmp/x.json"); d != nil {
		t.Errorf("edit is authorized, got block: %s", d.Reason)
	}
	if d := CheckAtlassianCommandGuard("plan", "git status"); d != nil {
		t.Error("non-atlassian command must not be intercepted")
	}
	if d := CheckAtlassianCommandGuard("plan", "muxcode atlassian jira update X /tmp/x.json"); d != nil {
		if !strings.HasPrefix(d.Reason, "BLOCKED:") {
			t.Errorf("guard reason must start with BLOCKED: for hook rendering, got: %s", d.Reason)
		}
	}
}
