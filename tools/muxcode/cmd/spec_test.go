package cmd

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// lastSpecRow returns the detail of the session's last lifecycle row
// carrying event.
func lastSpecRow(t *testing.T, session, event string) (string, bool) {
	t.Helper()
	entries, err := bus.ReadLifecycleLog(session)
	if err != nil {
		t.Fatalf("read lifecycle: %v", err)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Event == event {
			return entries[i].Detail, true
		}
	}
	return "", false
}

// TestSpecSetClearWriteLifecycleRows drives the real `muxcode spec set` and
// `spec clear` handlers, so dropping either logging call in Spec fails here
// — a test of LogSpecPointerChange alone would stay green (MUX-168 review).
// Clear must name the path it removed, not "none".
func TestSpecSetClearWriteLifecycleRows(t *testing.T) {
	busBase := t.TempDir()
	bus.SetBusDirBase(busBase)
	t.Cleanup(bus.ResetBusDirBase)

	session := fmt.Sprintf("spec-rows-%d", rand.Int())
	t.Setenv("BUS_SESSION", session)
	if err := bus.Init(session, filepath.Join(busBase, "memory")); err != nil {
		t.Fatalf("Init: %v", err)
	}

	repo := t.TempDir()
	const spec = "docs/requirements/drafts/MUX-168-example.md"
	if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(spec)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, spec), []byte("# MUX-168\n"), 0644); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AGENT_ROLE", "")
	t.Setenv("BUS_ROLE", "plan")
	Spec([]string{"set", spec})

	if got := bus.ReadActiveSpec(session); got != spec {
		t.Fatalf("pointer after spec set = %q, want %q", got, spec)
	}
	if detail, ok := lastSpecRow(t, session, "spec-set"); !ok || detail != "actor=plan path="+spec {
		t.Errorf("spec-set row = %q (found %v), want %q", detail, ok, "actor=plan path="+spec)
	}

	t.Setenv("BUS_ROLE", "")
	Spec([]string{"clear"})

	if got := bus.ReadActiveSpec(session); got != "" {
		t.Errorf("pointer after spec clear = %q, want empty", got)
	}
	if detail, ok := lastSpecRow(t, session, "spec-clear"); !ok || detail != "actor=user path="+spec {
		t.Errorf("spec-clear row = %q (found %v), want %q", detail, ok, "actor=user path="+spec)
	}
}
