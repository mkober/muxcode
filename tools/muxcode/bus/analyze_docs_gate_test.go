package bus

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gateCheckout is a scratch git checkout holding a spec, a root CLAUDE.md, a
// code file, and a code directory that is merely named docs deeper in the
// tree. Paths are returned resolved, as a hook sees them on macOS.
func gateCheckout(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"docs/requirements/drafts/MUX-150-x.md", "CLAUDE.md", "bus/x.go", "tools/gen/docs/render.go"} {
		if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(f)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, f), []byte("x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return repo
}

// analyzeWrite runs the hook for a write to path from window, on a workflow
// that has just reached reviewed. TriggerFile ignores the bus-dir override and
// names the real /tmp, so the session is unique and its trigger is removed.
func analyzeWrite(t *testing.T, window, path string) (AnalyzeHookResult, WorkflowStateEntry, string) {
	t.Helper()
	useTempBusDir(t)
	session := fmt.Sprintf("test-mux150-gate-%d", rand.Int())
	t.Cleanup(func() { os.Remove(TriggerFile(session)) })
	if !TransitionWorkflow(session, StateReviewed, "daemon:review-complete") {
		t.Fatal("fixture: the workflow did not reach reviewed")
	}
	ev, err := ParseToolEvent([]byte(`{"tool_input":{"file_path":"` + path + `"}}`))
	if err != nil {
		t.Fatal(err)
	}
	result := ProcessAnalyzeHook(session, window, ev)
	trigger, _ := os.ReadFile(TriggerFile(session))
	return result, ReadWorkflowState(session), string(trigger)
}

// TestProcessAnalyzeHook_DocsWriteLeavesTheWorkflowAlone is MUX-150's Phase 1
// characterization, flipped: plan's own spec write regressed the workflow
// reviewed → editing, became LastFiles and wrote the analyze trigger, so every
// verification pass rewrote the state the next one read. A write under the
// checkout's docs/ now does none of that, from any window.
func TestProcessAnalyzeHook_DocsWriteLeavesTheWorkflowAlone(t *testing.T) {
	repo := gateCheckout(t)
	for _, tc := range []struct{ window, path string }{
		{"plan", filepath.Join(repo, "docs/requirements/drafts/MUX-150-x.md")},
		{"build", filepath.Join(repo, "docs/requirements/drafts/MUX-150-x.md")},
		{"plan", "./docs/requirements/drafts/MUX-150-x.md"},
	} {
		result, wf, trigger := analyzeWrite(t, tc.window, tc.path)
		if !result.DocsSkipped || result.TriggerWritten || trigger != "" {
			t.Errorf("%s %s: docs write skipped=%v trigger=%v %q, want skipped and no trigger", tc.window, tc.path, result.DocsSkipped, result.TriggerWritten, trigger)
		}
		if wf.State != StateReviewed || len(wf.LastFiles) != 0 {
			t.Errorf("%s %s: workflow %q with LastFiles %v after a docs write, want reviewed and untouched", tc.window, tc.path, wf.State, wf.LastFiles)
		}
	}
}

// Negative controls: a code write, a root CLAUDE.md (not under docs/), a code
// directory merely named docs deeper in the tree, and a relative spelling that
// only passes through docs/ still route and still regress the workflow.
func TestProcessAnalyzeHook_NonDocsWritesStillRoute(t *testing.T) {
	repo := gateCheckout(t)
	paths := []string{"docs/../bus/x.go"}
	for _, rel := range []string{"bus/x.go", "CLAUDE.md", "tools/gen/docs/render.go"} {
		paths = append(paths, filepath.Join(repo, rel))
	}
	for _, path := range paths {
		result, wf, trigger := analyzeWrite(t, "build", path)
		if result.DocsSkipped || !result.TriggerWritten || !strings.Contains(trigger, path) {
			t.Errorf("%s: skipped=%v trigger=%v %q, want routed", path, result.DocsSkipped, result.TriggerWritten, trigger)
		}
		if wf.State != StateEditing || wf.PrevState != StateReviewed {
			t.Errorf("%s: workflow %q from %q, want the regression reviewed → editing", path, wf.State, wf.PrevState)
		}
	}
}

// The edit window's code write is unchanged, its nvim diff cleanup included.
// TMUX_TMPDIR points the cleanup's tmux calls at a server that does not exist,
// never the caller's.
func TestProcessAnalyzeHook_EditWindowCodeWriteStillCleansTheDiff(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("TMUX", "")
	repo := gateCheckout(t)
	result, wf, _ := analyzeWrite(t, "edit", filepath.Join(repo, "bus/x.go"))
	if !result.TriggerWritten || !result.DiffCleaned || wf.State != StateEditing {
		t.Errorf("edit-window code write: trigger=%v diff=%v state=%q, want all as before", result.TriggerWritten, result.DiffCleaned, wf.State)
	}
}
