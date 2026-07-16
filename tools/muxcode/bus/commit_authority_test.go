package bus

import (
	"strings"
	"testing"
)

// The regression this exists to prevent: the plan agent sent
// `plan → commit [request:commit] "... Push. Report sha."` and the commit agent
// obeyed, producing eight commits the user never asked for.
func TestCheckCommitAuthority_DeniesNonEditRoles(t *testing.T) {
	for _, from := range []string{"plan", "test", "review", "deploy", "run", "build"} {
		deny := CheckCommitAuthority(from, "commit", "commit")
		if deny == "" {
			t.Errorf("%s must not be allowed to request a git mutation", from)
		}
		if !strings.Contains(deny, "user-initiated") {
			t.Errorf("deny message for %s should explain the rule, got: %q", from, deny)
		}
	}
}

// edit is the agent the user actually talks to, so it is the one role that may
// relay a commit request.
func TestCheckCommitAuthority_AllowsEdit(t *testing.T) {
	if deny := CheckCommitAuthority("edit", "commit", "commit"); deny != "" {
		t.Errorf("edit must be allowed to request a commit, got deny: %q", deny)
	}
}

// Reading PR data is not a mutation and must stay open to every role — gating it
// would break the PR review flow (commit agent fetches, review agent analyzes).
func TestCheckCommitAuthority_AllowsPRReadFromAnyRole(t *testing.T) {
	for _, from := range []string{"plan", "review", "edit"} {
		if deny := CheckCommitAuthority(from, "commit", "pr-read"); deny != "" {
			t.Errorf("pr-read from %s is read-only and must be allowed, got deny: %q", from, deny)
		}
	}
}

// Sends to other roles are untouched by this gate.
func TestCheckCommitAuthority_IgnoresNonCommitTargets(t *testing.T) {
	if deny := CheckCommitAuthority("plan", "build", "build"); deny != "" {
		t.Errorf("non-commit target must be unaffected, got deny: %q", deny)
	}
}

// The autonomous story-lifecycle agent commits by design; it opts in explicitly
// rather than the gate being loose by default.
func TestCommitAuthorityRoles_EnvOptIn(t *testing.T) {
	t.Setenv("MUXCODE_COMMIT_AUTHORITY_ROLES", "edit,auto")
	if deny := CheckCommitAuthority("auto", "commit", "commit"); deny != "" {
		t.Errorf("auto should be authorized when opted in, got deny: %q", deny)
	}
	if deny := CheckCommitAuthority("plan", "commit", "commit"); deny == "" {
		t.Error("plan must still be denied when only edit,auto are authorized")
	}
}

// Set-to-empty denies every role, including edit — a session where no agent may
// touch git at all is a legitimate configuration.
func TestCommitAuthorityRoles_EmptyDeniesAll(t *testing.T) {
	t.Setenv("MUXCODE_COMMIT_AUTHORITY_ROLES", "")
	if deny := CheckCommitAuthority("edit", "commit", "commit"); deny == "" {
		t.Error("an empty authority list must deny every role, including edit")
	}
}

// The default must be locked down: if the env var is unset, only edit passes.
func TestCommitAuthorityRoles_DefaultIsEditOnly(t *testing.T) {
	roles := CommitAuthorityRoles()
	if len(roles) != 1 || roles[0] != "edit" {
		t.Errorf("default commit authority = %v, want [edit]", roles)
	}
}

// The gate must live at the bus, not only at the `muxcode send` CLI. The CLI is
// one of 30+ callers — daemon chains, subscriptions, hooks, and the webhook HTTP
// endpoint all reach the inbox via sendMessage() and would sail past a CLI-only
// check. The webhook is the sharpest: an HTTP POST could otherwise order a
// commit and a push.
func TestSend_RefusesUnauthorizedCommitRequest(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-authority"
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Programmatic Send() — the path daemon chains and the webhook use.
	msg := NewMessage("plan", "commit", "request", "commit", "Stage, commit, push. Report sha.", "")
	if err := Send(session, msg); err == nil {
		t.Fatal("Send() must refuse an unauthorized commit request, not just the CLI")
	}

	// And it must not reach the commit agent's inbox.
	msgs, _ := Peek(session, "commit")
	for _, m := range msgs {
		if m.From == "plan" && m.Action == "commit" {
			t.Error("refused commit request must not be delivered to the commit agent's inbox")
		}
	}
}

// The webhook identity is subject to the same gate.
func TestCheckCommitAuthority_DeniesWebhook(t *testing.T) {
	if deny := CheckCommitAuthority("webhook", "commit", "commit"); deny == "" {
		t.Error("an HTTP webhook must not be able to order a commit/push")
	}
}

// edit's authorized requests still flow through the bus untouched.
func TestSend_AllowsEditCommitRequest(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-authority-edit"
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	msg := NewMessage("edit", "commit", "request", "commit", "Stage and commit, then push.", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("edit's commit request must be allowed: %v", err)
	}
	msgs, _ := Peek(session, "commit")
	if len(msgs) == 0 {
		t.Error("edit's commit request must be delivered to the commit agent")
	}
}

// The bypass the gate originally had: it checked only action "commit", so a
// denied agent could simply retry with `push` (or stage/merge/rebase/tag) and
// walk straight through. Every label that reaches git must be gated.
func TestCheckCommitAuthority_DeniesAllGitMutatingActions(t *testing.T) {
	for _, action := range []string{"commit", "stage", "push", "merge", "rebase", "tag"} {
		if deny := CheckCommitAuthority("plan", "commit", action); deny == "" {
			t.Errorf("action %q mutates git and must be denied to plan — retrying with it would bypass the gate", action)
		}
	}
}

// The deny message must not hand the blocked agent the env var that lifts the
// block: it is process-scoped and read at send time, so an agent given that
// string can self-authorize by prefixing it to the command it was just refused.
func TestCheckCommitAuthority_DenyMessageLeaksNoBypass(t *testing.T) {
	deny := CheckCommitAuthority("plan", "commit", "commit")
	if strings.Contains(deny, "MUXCODE_COMMIT_AUTHORITY_ROLES") {
		t.Errorf("deny message must not disclose the self-authorization env var, got: %q", deny)
	}
}

// Responses are not actionable. The inbox reply template echoes the action label
// back to the commit agent, so gating responses would strand commit's tracked
// task until timeout.
func TestSend_AllowsResponseToCommitAgent(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-authority-response"
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	msg := NewMessage("plan", "commit", "response", "commit", "Done — report received.", "abc123")
	if err := Send(session, msg); err != nil {
		t.Fatalf("a response to the commit agent must not be refused: %v", err)
	}
}
