package bus

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// stubCensus answers every list-panes with the given census and captures
// all tmux calls (run and output) into one ordered slice.
func stubCensus(t *testing.T, census string, outErr error) *[][]string {
	t.Helper()
	origRun := tmuxRunner
	origOut := tmuxOutputRunner
	var calls [][]string
	tmuxRunner = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	tmuxOutputRunner = func(args ...string) (string, error) {
		calls = append(calls, args)
		return census, outErr
	}
	t.Cleanup(func() {
		tmuxRunner = origRun
		tmuxOutputRunner = origOut
	})
	return &calls
}

// fakeTmuxWindow emulates one window's pane state behind the tmux
// runners: set-option -p writes tags (failing where told to), set-option
// -w stamps the window marker, list-panes renders the live census. It
// exists so TagWindowPanes and ResolvePane can be exercised against the
// SAME evolving state — the adversarial spec case is a sequence, not a
// single stubbed reply.
type fakeTmuxWindow struct {
	ids        []string          // pane ids in index order
	tags       map[string]string // pane id → @muxcode_pane value
	marker     bool              // window-level @muxcode_tagged
	failTag    map[string]bool   // index-target suffix (".0", ".1") whose tag write fails
	failMarker bool              // the set-option -w marker write itself fails
	calls      [][]string
}

func installFakeWindow(t *testing.T, w *fakeTmuxWindow) {
	t.Helper()
	if w.tags == nil {
		w.tags = map[string]string{}
	}
	origRun := tmuxRunner
	origOut := tmuxOutputRunner
	tmuxRunner = func(args ...string) error {
		w.calls = append(w.calls, args)
		if len(args) >= 6 && args[0] == "set-option" && args[1] == "-p" && args[4] == paneTagOption {
			dot := strings.LastIndex(args[3], ".")
			if dot < 0 {
				return errors.New("fake window only understands index targets")
			}
			suffix := args[3][dot:]
			if w.failTag[suffix] {
				return errors.New("set-option -p rejected")
			}
			idx := 0
			fmt.Sscanf(suffix, ".%d", &idx)
			if idx < len(w.ids) {
				w.tags[w.ids[idx]] = args[5]
			}
			return nil
		}
		if len(args) >= 6 && args[0] == "set-option" && args[1] == "-w" && args[4] == windowTaggedOption {
			if w.failMarker {
				return errors.New("set-option -w failed")
			}
			w.marker = true
		}
		return nil
	}
	tmuxOutputRunner = func(args ...string) (string, error) {
		w.calls = append(w.calls, args)
		var lines []string
		for _, id := range w.ids {
			m := ""
			if w.marker {
				m = "1"
			}
			lines = append(lines, id+":"+w.tags[id]+":"+m)
		}
		return strings.Join(lines, "\n"), nil
	}
	t.Cleanup(func() {
		tmuxRunner = origRun
		tmuxOutputRunner = origOut
	})
}

func countLifecycleEvents(t *testing.T, session, event string) int {
	t.Helper()
	entries, err := FilterLifecycleLog(session, LifecycleFilterOpts{Event: event})
	if err != nil {
		return 0
	}
	return len(entries)
}

// Resolution is by identity, not position: the agent pane is listed
// FIRST in the census (as after a split inserted before it), and the
// resolver must return its pane id — never an index target.
func TestResolvePane_ByTag(t *testing.T) {
	stubCensus(t, "%5:agent:1\n%0:left:1\n%7:control:1", nil)

	target, err := ResolvePane("s", "edit", PaneTagAgent)
	if err != nil {
		t.Fatalf("ResolvePane: %v", err)
	}
	if target != "%5" {
		t.Errorf("agent target = %q, want pane id %%5", target)
	}
	if strings.Contains(target, ".1") {
		t.Errorf("resolution must not produce an index target: %q", target)
	}

	if target, _ := ResolvePane("s", "edit", PaneTagLeft); target != "%0" {
		t.Errorf("left target = %q, want %%0", target)
	}
	if target, _ := ResolvePane("s", "edit", PaneTagControl); target != "%7" {
		t.Errorf("control target = %q, want %%7", target)
	}
}

// An unmarked window (older binary — no @muxcode_tagged) falls back to
// the creation-order index convention and logs pane-fallback exactly
// once per window per session, not once per resolution.
func TestResolvePane_LegacyFallbackLogsOnce(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-pane-legacy"
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}
	stubCensus(t, "%0::\n%1::", nil)

	target, err := ResolvePane(session, "build", PaneTagAgent)
	if err != nil {
		t.Fatalf("legacy fallback must not error: %v", err)
	}
	if want := session + ":build.1"; target != want {
		t.Errorf("agent fallback = %q, want %q", target, want)
	}
	if target, _ := ResolvePane(session, "build", PaneTagLeft); target != session+":build.0" {
		t.Errorf("left fallback = %q, want %s:build.0", target, session)
	}
	if n := countLifecycleEvents(t, session, "pane-fallback"); n != 1 {
		t.Errorf("pane-fallback logged %d times across two resolutions, want exactly 1", n)
	}
}

// A marked window whose requested tag is absent is a broken contract:
// the resolver must error, and must never hand back an index that may
// host an editor or a git TUI.
func TestResolvePane_MissingTagFailsLoud(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-pane-missing"
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}
	stubCensus(t, "%0:left:1\n%1::1", nil)

	target, err := ResolvePane(session, "edit", PaneTagAgent)
	if err == nil {
		t.Fatal("missing tag on a marked window must error")
	}
	if target != "" {
		t.Errorf("failed resolution returned a target: %q", target)
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should name the missing tag: %v", err)
	}
	if n := countLifecycleEvents(t, session, "pane-resolve-failed"); n != 1 {
		t.Errorf("pane-resolve-failed logged %d times, want 1", n)
	}
}

// Two panes claiming the same tag is equally a broken contract —
// convergence is the control-pane sweep's job, never the resolver's.
func TestResolvePane_DuplicateTagFailsLoud(t *testing.T) {
	stubCensus(t, "%0:agent:1\n%1:agent:1", nil)

	target, err := ResolvePane("s", "edit", PaneTagAgent)
	if err == nil || target != "" {
		t.Fatalf("duplicate tag must error with no target, got %q, %v", target, err)
	}
	if !strings.Contains(err.Error(), "2 panes") {
		t.Errorf("error should count the claimants: %v", err)
	}
}

// A window with no readable census — no server, no such window, or
// output that is not pane rows (another test's capture-pane stub) —
// resolves to the legacy index silently: there is no evidence to branch
// on, and the follow-up tmux command fails loudly on its own.
func TestResolvePane_NoCensusIsSilentLegacy(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-pane-nocensus"
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}

	stubCensus(t, "", errors.New("no server running"))
	target, err := ResolvePane(session, "edit", PaneTagAgent)
	if err != nil || target != session+":edit.1" {
		t.Errorf("query error: target = %q, err = %v, want legacy index and nil", target, err)
	}

	stubCensus(t, "  Ran 1 shell command\n❯ commit: the:thing", nil)
	target, err = ResolvePane(session, "edit", PaneTagAgent)
	if err != nil || target != session+":edit.1" {
		t.Errorf("garbage census: target = %q, err = %v, want legacy index and nil", target, err)
	}

	if n := countLifecycleEvents(t, session, "pane-fallback"); n != 0 {
		t.Errorf("no-census fallback must not log pane-fallback, got %d events", n)
	}
}

// PaneTarget cannot return an error, so a failed resolution substitutes
// a sentinel pane part tmux rejects — the command fails instead of
// typing into whatever pane occupies an index.
func TestPaneTarget_SentinelNeverAnIndex(t *testing.T) {
	stubCensus(t, "%0:left:1\n%1::1", nil)

	target := PaneTarget("s", "edit")
	if want := "s:edit." + unresolvedPaneSentinel; target != want {
		t.Errorf("PaneTarget = %q, want %q", target, want)
	}
	for _, idx := range []string{".0", ".1", ".2"} {
		if strings.HasSuffix(target, idx) {
			t.Errorf("failed resolution fell back to index %s: %q", idx, target)
		}
	}
}

// PaneTargetForWindow is the window+tag variant the Phase 3 conversions
// lean on: tag match yields the pane id, a failed resolution yields the
// same non-index sentinel as PaneTarget, and an unmarked window falls
// back to the legacy index for the requested tag.
func TestPaneTargetForWindow(t *testing.T) {
	stubCensus(t, "%0:left:1\n%1:agent:1", nil)
	if target := PaneTargetForWindow("s", "edit", PaneTagLeft); target != "%0" {
		t.Errorf("left target = %q, want %%0", target)
	}
	if target := PaneTargetForWindow("s", "edit", PaneTagControl); target != "s:edit."+unresolvedPaneSentinel {
		t.Errorf("missing tag on tagged window = %q, want sentinel target", target)
	}

	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-pane-target-window"
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}
	stubCensus(t, "%0::\n%1::", nil)
	if target := PaneTargetForWindow(session, "build", PaneTagLeft); target != session+":build.0" {
		t.Errorf("legacy left target = %q, want %s:build.0", target, session)
	}
}

// The marker-write double failure: tags land but the tmux marker write
// fails, so the window would read as pre-tagging legacy forever. The
// on-disk broken record makes later delivery fail closed while the
// creation-instant path still launches by index — and a subsequent
// successful tagging clears the record (negative control).
func TestTagWindowPanes_MarkerWriteFailureFailsClosed(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-pane-marker-fail"
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}
	w := &fakeTmuxWindow{ids: []string{"%0", "%1"}, failMarker: true}
	installFakeWindow(t, w)

	if err := TagWindowPanes(session, "edit"); err == nil {
		t.Fatal("marker write failure must surface an error")
	}
	if w.marker {
		t.Fatal("fake stamped the marker despite failMarker")
	}

	// Delivery fails closed — never legacy .0/.1.
	target, rerr := ResolvePane(session, "edit", PaneTagAgent)
	if rerr == nil {
		t.Fatalf("delivery on disk-broken window resolved to %q, want error", target)
	}
	// Creation-instant path still launches by index.
	if target := CreationPaneTarget(session, "edit", PaneTagAgent); target != session+":edit.1" {
		t.Errorf("creation target = %q, want %s:edit.1", target, session)
	}

	// Recovery: marker write succeeds on a later pass — broken record
	// clears and tag resolution works normally.
	w.failMarker = false
	if err := TagWindowPanes(session, "edit"); err != nil {
		t.Fatalf("recovered tagging pass: %v", err)
	}
	if windowMarkedBroken(session, "edit") {
		t.Error("broken record not cleared after successful marker write")
	}
	if target, err := ResolvePane(session, "edit", PaneTagAgent); err != nil || target != "%1" {
		t.Errorf("post-recovery resolution = %q, %v, want %%1", target, err)
	}
}

// The broken record must guard the no-census road too: a broken window
// whose census also fails must error, not legacy-fall-back — while the
// same census failure without a record keeps the documented silent
// legacy behavior (negative control).
func TestResolvePane_CensusErrorHonorsBrokenRecord(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-pane-census-broken"
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}
	if err := markWindowBroken(session, "edit"); err != nil {
		t.Fatalf("markWindowBroken: %v", err)
	}
	stubCensus(t, "", errors.New("no server running"))

	if target, err := ResolvePane(session, "edit", PaneTagAgent); err == nil {
		t.Fatalf("census-error on disk-broken window resolved to %q, want error", target)
	}

	clearWindowBroken(session, "edit")
	if target, err := ResolvePane(session, "edit", PaneTagAgent); err != nil || target != session+":edit.1" {
		t.Errorf("census-error without record = %q, %v, want legacy index", target, err)
	}
}

// When the broken record itself cannot persist (no bus dir), the
// failure must ride the returned error — with neither tmux marker nor
// disk record, the caller's error is the only trace of the exposure.
func TestTagWindowPanes_BrokenRecordPersistFailureSurfaced(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-pane-persist-fail"
	// BusDir(session) deliberately not created — the record write fails.
	w := &fakeTmuxWindow{ids: []string{"%0", "%1"}, failMarker: true}
	installFakeWindow(t, w)

	err := TagWindowPanes(session, "edit")
	if err == nil || !strings.Contains(err.Error(), "broken-record persist") {
		t.Fatalf("persist failure not surfaced in error: %v", err)
	}
}

// A broken window (marked, tags missing) must not cost the launch: the
// creation-instant resolver falls back to the just-created index where
// the delivery resolver returns the sentinel — the contrast is the
// review must-fix (createWindowContent silently launched nothing).
func TestCreationPaneTarget_IndexFallbackOnBrokenWindow(t *testing.T) {
	stubCensus(t, "%0::1\n%1::1", nil)

	if target := CreationPaneTarget("s", "edit", PaneTagAgent); target != "s:edit.1" {
		t.Errorf("creation target on broken window = %q, want s:edit.1", target)
	}
	if target := PaneTargetForWindow("s", "edit", PaneTagAgent); target != "s:edit."+unresolvedPaneSentinel {
		t.Errorf("delivery target on broken window = %q, want sentinel", target)
	}
}

// The happy path stamps left on pane 0, agent on pane 1, and the window
// marker only AFTER the read-back census confirms a tag landed.
func TestTagWindowPanes_StampsTagsThenMarker(t *testing.T) {
	w := &fakeTmuxWindow{ids: []string{"%0", "%1"}}
	installFakeWindow(t, w)

	if err := TagWindowPanes("s", "build"); err != nil {
		t.Fatalf("TagWindowPanes: %v", err)
	}
	if w.tags["%0"] != PaneTagLeft || w.tags["%1"] != PaneTagAgent {
		t.Errorf("tags = %v, want %%0=left %%1=agent", w.tags)
	}
	if !w.marker {
		t.Error("window marker not stamped after successful read-back")
	}

	var seq []string
	for _, c := range w.calls {
		seq = append(seq, c[0]+" "+c[1])
	}
	joined := strings.Join(seq, " | ")
	readBack := strings.Index(joined, "list-panes")
	marker := strings.Index(joined, "set-option -w")
	if readBack == -1 || marker == -1 || marker < readBack {
		t.Errorf("marker must be stamped after read-back, calls: %s", joined)
	}

	target, err := ResolvePane("s", "build", PaneTagAgent)
	if err != nil || target != "%1" {
		t.Errorf("resolution after tagging = %q, %v, want %%1", target, err)
	}
}

// The adversarial spec case: panes present, the agent tag write
// deliberately fails while the left tag lands, so the marker is
// legitimately stamped ("at least one read back"). Resolving the agent
// must then be a LOUD error — never an index fallback — and the launch
// failure itself is on the lifecycle record.
func TestTagWindowPanes_AdversarialFailureIsLoudNotFallback(t *testing.T) {
	session := "test-pane-adversarial"
	w := &fakeTmuxWindow{ids: []string{"%0", "%1"}, failTag: map[string]bool{".1": true}}
	installFakeWindow(t, w)

	err := TagWindowPanes(session, "edit")
	if err == nil {
		t.Fatal("failed agent tag write must surface an error")
	}
	if !w.marker {
		t.Fatal("marker must still stamp — the left tag read back")
	}
	if n := countLifecycleEvents(t, session, "pane-tag-failed"); n != 1 {
		t.Errorf("pane-tag-failed logged %d times, want 1", n)
	}

	target, rerr := ResolvePane(session, "edit", PaneTagAgent)
	if rerr == nil {
		t.Fatal("resolution on the marked, agent-less window must error")
	}
	if target != "" || strings.HasSuffix(target, ".1") {
		t.Errorf("adversarial case fell back toward an index: %q", target)
	}
}

// Total tag failure on a per-pane-capable tmux marks the window broken:
// the marker stamps anyway, so resolution errors loudly instead of the
// freshly created window impersonating a pre-tagging one (review
// must-fix, 2026-08-31 — legacy fallback here reintroduces the exact
// index misdelivery this mechanism removes). The fake window's
// show-options probe succeeds, so the failure classifies as unexpected,
// not as missing capability.
func TestTagWindowPanes_TotalFailureMarksBroken(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-pane-broken"
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}
	w := &fakeTmuxWindow{ids: []string{"%0", "%1"}, failTag: map[string]bool{".0": true, ".1": true}}
	installFakeWindow(t, w)

	err := TagWindowPanes(session, "test")
	if err == nil {
		t.Fatal("total tag failure must surface an error")
	}
	if errors.Is(err, ErrPaneTagUnsupported) {
		t.Fatal("capable-tmux failure must not classify as unsupported")
	}
	if !w.marker {
		t.Fatal("marker must stamp — broken window must not resolve as legacy")
	}

	target, rerr := ResolvePane(session, "test", PaneTagAgent)
	if rerr == nil {
		t.Fatalf("broken window resolved without error to %q", target)
	}
	if target != "" {
		t.Errorf("broken window produced a target: %q", target)
	}
}

// A tmux that rejects per-pane options is the documented degradation:
// ErrPaneTagUnsupported, no marker, and legacy index resolution — the
// one world where fallback is legitimate.
func TestTagWindowPanes_UnsupportedTmuxDegradesLegacy(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-pane-unsupported"
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}

	origRun, origOut := tmuxRunner, tmuxOutputRunner
	t.Cleanup(func() { tmuxRunner, tmuxOutputRunner = origRun, origOut })
	var calls [][]string
	tmuxRunner = func(args ...string) error {
		calls = append(calls, args)
		if args[0] == "set-option" && args[1] == "-p" {
			return errors.New("exit status 1")
		}
		return nil
	}
	tmuxOutputRunner = func(args ...string) (string, error) {
		calls = append(calls, args)
		if args[0] == "show-options" && args[1] == "-p" {
			return "", errors.New("usage: show-options [-gHpqsvw] [-t target-pane] [option]")
		}
		return "%0::\n%1::", nil
	}

	err := TagWindowPanes(session, "test")
	if !errors.Is(err, ErrPaneTagUnsupported) {
		t.Fatalf("want ErrPaneTagUnsupported, got %v", err)
	}
	for _, c := range calls {
		if c[0] == "set-option" && c[1] == "-w" {
			t.Fatalf("marker must not stamp on unsupported tmux: %v", c)
		}
	}
	if n := countLifecycleEvents(t, session, "pane-tag-unsupported"); n != 1 {
		t.Errorf("pane-tag-unsupported logged %d times, want 1", n)
	}

	target, rerr := ResolvePane(session, "test", PaneTagAgent)
	if rerr != nil || target != session+":test.1" {
		t.Errorf("unsupported tmux must legacy-fallback, got %q, %v", target, rerr)
	}
}
