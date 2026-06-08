package bus

import (
	"strings"
	"testing"
)

// resizeStub installs tmux runner stubs for resize tests. listOut is returned for
// the listAllWindows query, fitOut for the windowSize display-message read
// (distinguished by the "window_width" format token). fitOut is "width\theight".
// All resize-window invocations are captured into the returned slice pointer.
func resizeStub(t *testing.T, listOut, fitOut string) *[][]string {
	t.Helper()
	origRun := tmuxRunner
	origOutput := tmuxOutputRunner
	var calls [][]string
	tmuxRunner = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	tmuxOutputRunner = func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "window_width") {
			return fitOut, nil
		}
		return listOut, nil
	}
	t.Cleanup(func() {
		tmuxRunner = origRun
		tmuxOutputRunner = origOutput
	})
	return &calls
}

// resizeCalls filters captured tmux calls down to resize-window invocations.
func resizeCalls(calls [][]string) [][]string {
	var out [][]string
	for _, c := range calls {
		if len(c) > 0 && c[0] == "resize-window" {
			out = append(out, c)
		}
	}
	return out
}

func TestResizeAllWindows_AttachedUsesAuto(t *testing.T) {
	// Two windows in one attached session — both should get -A.
	list := "main\t0\t1\nmain\t1\t1"
	fit := "200\t48"
	calls := resizeStub(t, list, fit)

	if err := ResizeAllWindows(); err != nil {
		t.Fatalf("ResizeAllWindows: %v", err)
	}

	resizes := resizeCalls(*calls)
	if len(resizes) != 2 {
		t.Fatalf("expected 2 resize calls, got %d: %v", len(resizes), resizes)
	}
	for _, r := range resizes {
		joined := strings.Join(r, " ")
		if !strings.Contains(joined, "-A") {
			t.Errorf("attached window should use -A, got %v", r)
		}
		if strings.Contains(joined, "-x") || strings.Contains(joined, "-y") {
			t.Errorf("attached window should not use explicit size, got %v", r)
		}
	}
}

func TestResizeAllWindows_DetachedGetsExplicitSize(t *testing.T) {
	// main is attached, sub is a detached subsession. sub's windows must be
	// resized explicitly to the fit size copied from the attached window.
	list := "main\t0\t1\nsub\t0\t0\nsub\t1\t0"
	fit := "200\t48"
	calls := resizeStub(t, list, fit)

	if err := ResizeAllWindows(); err != nil {
		t.Fatalf("ResizeAllWindows: %v", err)
	}

	resizes := resizeCalls(*calls)
	if len(resizes) != 3 {
		t.Fatalf("expected 3 resize calls, got %d: %v", len(resizes), resizes)
	}

	// main:0 -> -A
	if joined := strings.Join(resizes[0], " "); !strings.Contains(joined, "main:0") || !strings.Contains(joined, "-A") {
		t.Errorf("expected attached main:0 with -A, got %v", resizes[0])
	}

	// sub:0 and sub:1 -> explicit -x 200 -y 48
	for _, target := range []string{"sub:0", "sub:1"} {
		found := false
		for _, r := range resizes[1:] {
			joined := strings.Join(r, " ")
			if strings.Contains(joined, target) {
				found = true
				if !strings.Contains(joined, "-x 200") || !strings.Contains(joined, "-y 48") {
					t.Errorf("%s should be resized to 200x48, got %v", target, r)
				}
				if strings.Contains(joined, "-A") {
					t.Errorf("%s should not use -A, got %v", target, r)
				}
			}
		}
		if !found {
			t.Errorf("expected a resize for detached window %s, got %v", target, resizes)
		}
	}
}

func TestResizeAllWindows_NoAttachedClientSkipsDetached(t *testing.T) {
	// No session attached (session_attached==0 for all). With no fit size to
	// copy, detached windows must be left untouched (no explicit resize).
	list := "sub\t0\t0\nsub\t1\t0"
	fit := "" // windowSize finds nothing
	calls := resizeStub(t, list, fit)

	if err := ResizeAllWindows(); err != nil {
		t.Fatalf("ResizeAllWindows: %v", err)
	}

	if got := resizeCalls(*calls); len(got) != 0 {
		t.Errorf("expected no resize calls when nothing is attached, got %v", got)
	}
}

func TestResizeAllWindows_SessionNameWithColon(t *testing.T) {
	// A session name containing ':' must still target correctly — the inline
	// cut -d: form could not handle this; tab-delimited parsing can.
	list := "main\t0\t1\nfoo:bar\t2\t0"
	fit := "180\t50"
	calls := resizeStub(t, list, fit)

	if err := ResizeAllWindows(); err != nil {
		t.Fatalf("ResizeAllWindows: %v", err)
	}

	// Assert the exact -t target value is "foo:bar:2" — a substring check could
	// pass on a malformed target, which is exactly the bug the tab-delimited
	// parsing prevents.
	found := false
	for _, r := range resizeCalls(*calls) {
		target, ok := flagValue(r, "-t")
		if ok && target == "foo:bar:2" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a resize with -t foo:bar:2, got %v", resizeCalls(*calls))
	}
}

// flagValue returns the argument immediately following flag in args.
func flagValue(args []string, flag string) (string, bool) {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1], true
		}
	}
	return "", false
}

func TestListAllWindows_ParsesAttachedFlag(t *testing.T) {
	origOutput := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		return "main\t0\t1\nsub\t3\t0", nil
	}
	t.Cleanup(func() { tmuxOutputRunner = origOutput })

	entries, err := listAllWindows()
	if err != nil {
		t.Fatalf("listAllWindows: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if !entries[0].attached || entries[0].target() != "main:0" {
		t.Errorf("entry 0 wrong: %+v", entries[0])
	}
	if entries[1].attached || entries[1].target() != "sub:3" {
		t.Errorf("entry 1 wrong: %+v", entries[1])
	}
}
