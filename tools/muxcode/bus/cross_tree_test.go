package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stubReceiverDir(t *testing.T, dir string) {
	t.Helper()
	prev := receiverDirFn
	receiverDirFn = func(string, string) string { return dir }
	t.Cleanup(func() { receiverDirFn = prev })
}

func TestCheckCrossTree_RefusesWorktreeToMainCheckout(t *testing.T) {
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	stubReceiverDir(t, repo)

	for _, role := range []string{"build", "test", "review"} {
		deny := CheckCrossTree("s", role, wt)
		if deny == "" {
			t.Fatalf("%s request from a worktree to the main checkout was allowed", role)
		}
		if !strings.Contains(deny, gitTreeRoot(wt)) || !strings.Contains(deny, gitTreeRoot(repo)) {
			t.Errorf("%s refusal does not name both trees: %s", role, deny)
		}
	}
}

func TestCheckCrossTree_AllowsSameTreeFromSubdirectory(t *testing.T) {
	repo := initPortRepo(t)
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	stubReceiverDir(t, repo)

	if deny := CheckCrossTree("s", "build", sub); deny != "" {
		t.Fatalf("same-tree request refused: %s", deny)
	}
}

func TestCheckCrossTree_AllowsNonTreeScopedPeer(t *testing.T) {
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	stubReceiverDir(t, repo)

	for _, role := range []string{"plan", "research", "run", "watch"} {
		if deny := CheckCrossTree("s", role, wt); deny != "" {
			t.Errorf("non-tree-scoped %s refused: %s", role, deny)
		}
	}
}

func TestCheckCrossTree_FailsOpenWhenTreeUnresolvable(t *testing.T) {
	repo := initPortRepo(t)
	stubReceiverDir(t, repo)
	if deny := CheckCrossTree("s", "build", t.TempDir()); deny != "" {
		t.Errorf("non-git sender refused: %s", deny)
	}

	stubReceiverDir(t, "")
	if deny := CheckCrossTree("s", "build", repo); deny != "" {
		t.Errorf("unresolvable receiver refused: %s", deny)
	}
}

func TestCheckCrossTree_DisabledByEnv(t *testing.T) {
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	stubReceiverDir(t, repo)
	t.Setenv("MUXCODE_CROSS_TREE_GUARD", "0")

	if deny := CheckCrossTree("s", "build", wt); deny != "" {
		t.Errorf("guard disabled but refused: %s", deny)
	}
}
