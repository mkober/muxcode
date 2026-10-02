package bus

import (
	"fmt"
	"os"
	"strings"
)

// treeScopedRoles answer about whatever git tree their own pane sits in. A
// request to one of them carries no tree (Message has no such field), so it is
// answered about the receiver's checkout regardless of where it was asked.
var treeScopedRoles = map[string]bool{"build": true, "test": true, "review": true}

// receiverDirFn resolves the directory a role's agent pane is working in.
// Replaced in tests; production reads tmux.
var receiverDirFn = func(session, role string) string {
	out, err := TmuxOutput("display-message", "-p", "-t", PaneTarget(session, role), "#{pane_current_path}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// CheckCrossTree refuses a request to a tree-scoped role (build, test, review)
// when the requester's git tree differs from the tree the receiving agent is
// working in, and returns "" to allow. senderDir is the requester's working
// directory — only meaningful for a shell-issued `muxcode send`, which is why
// the CLI is the sole caller: daemon-side senders run in the daemon's cwd,
// which `upgrade-daemons` can re-parent into an unrelated repo.
//
// Without this, a request from a worktree was answered about the session's
// main checkout, silently: during MUX-136 review re-reported a finding already
// fixed in a spawn worktree, and a MUX-142 worker was told "tests green" about
// a tree containing none of its code. Trees are compared by git toplevel, so a
// requester in a subdirectory of the same checkout is the same tree.
//
// Fails open when either tree cannot be resolved (not a git repo, no pane):
// a mismatch that cannot be shown is not refused. MUXCODE_CROSS_TREE_GUARD=0
// disables the check for a deliberate cross-tree delegation.
func CheckCrossTree(session, to, senderDir string) string {
	if !treeScopedRoles[to] || os.Getenv("MUXCODE_CROSS_TREE_GUARD") == "0" {
		return ""
	}
	senderTree := gitTreeRoot(senderDir)
	receiverTree := gitTreeRoot(receiverDirFn(session, to))
	if senderTree == "" || receiverTree == "" || senderTree == receiverTree {
		return ""
	}
	return fmt.Sprintf("cross-tree request refused: the %s agent works in %s but you are in %s, so its answer would describe a tree without your changes — run the check in your own tree, or set MUXCODE_CROSS_TREE_GUARD=0 to delegate anyway",
		to, receiverTree, senderTree)
}

// gitTreeRoot returns the git toplevel containing dir, or "" when dir is empty
// or not inside a work tree.
func gitTreeRoot(dir string) string {
	if dir == "" {
		return ""
	}
	return gitOutputIn(dir, "rev-parse", "--show-toplevel")
}
