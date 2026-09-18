package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
	"github.com/mkober/muxcode/tools/muxcode/tui"
)

// The banner renders inside a fixed-width display-popup, so a long
// project path must fit rather than wrap outside the popup border —
// the live failure this fitting exists to prevent.
func TestLaunchBannerFitsWidth(t *testing.T) {
	const width = 40
	long := "/Users/someone/Repos/org/a-very-long-project-name-here"
	banner := tui.StripAnsi(launchBanner(long, "a-very-long-project-name-here", width))
	for _, line := range strings.Split(banner, "\n") {
		if len([]rune(line)) > width {
			t.Errorf("line overflows width %d (%d cols): %q", width, len([]rune(line)), line)
		}
	}
	if !strings.Contains(banner, "Project:") || !strings.Contains(banner, "Session:") {
		t.Errorf("both labels must render:\n%s", banner)
	}
}

// Negative control for the fitting: a path that fits must render whole.
// Without this, a banner that always elided would pass the test above.
func TestLaunchBannerKeepsShortPathWhole(t *testing.T) {
	banner := tui.StripAnsi(launchBanner("/tmp/proj", "proj", 100))
	if !strings.Contains(banner, "/tmp/proj") {
		t.Errorf("short path must render unabbreviated:\n%s", banner)
	}
	if strings.Contains(banner, "…") {
		t.Errorf("short path must not be elided:\n%s", banner)
	}
}

// The tail of a path identifies the project, so fitting drops leading
// components. A right truncation would keep only "/Users/..." and lose
// the only part worth reading.
func TestFitPathKeepsTail(t *testing.T) {
	got := fitPath("/Users/someone/Repos/org/my-project", 20)
	if len([]rune(got)) > 20 {
		t.Errorf("fitPath exceeded width: %q", got)
	}
	if !strings.HasSuffix(got, "my-project") {
		t.Errorf("tail must survive fitting, got %q", got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("elision must be marked, got %q", got)
	}
}

func TestAbbrevHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}
	if got := abbrevHome(filepath.Join(home, "Repos", "x")); got != "~/Repos/x" {
		t.Errorf("abbrevHome = %q, want ~/Repos/x", got)
	}
	if got := abbrevHome("/opt/elsewhere"); got != "/opt/elsewhere" {
		t.Errorf("path outside home must be untouched, got %q", got)
	}
	if got := abbrevHome(home); got != "~" {
		t.Errorf("home itself = %q, want ~", got)
	}
}

// A bare string prefix also matches a sibling directory, which would
// render /Users/alice-backup/proj as ~-backup/proj. The prefix has to
// end on a separator.
func TestAbbrevHomeIgnoresSiblingPrefix(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}
	sibling := strings.TrimSuffix(home, string(filepath.Separator)) + "-backup/proj"
	if got := abbrevHome(sibling); got != sibling {
		t.Errorf("sibling of home must be untouched, got %q want %q", got, sibling)
	}
}

// stubEnsureDaemonCurrent replaces the launcher's daemon-check seam for one
// test and counts the calls it received.
func stubEnsureDaemonCurrent(t *testing.T, res bus.UpgradeResult, upgraded bool, err error) *int {
	t.Helper()
	calls := new(int)
	prev := ensureDaemonCurrent
	ensureDaemonCurrent = func(string) (bus.UpgradeResult, bool, error) {
		*calls++
		return res, upgraded, err
	}
	t.Cleanup(func() { ensureDaemonCurrent = prev })
	return calls
}

// TestRefreshNoticeSilentWhenCurrent is the negative control for the two
// notices below: the overwhelmingly common attach finds a current daemon, and
// printing anything then would put noise in front of every session.
func TestRefreshNoticeSilentWhenCurrent(t *testing.T) {
	out, warn := refreshNotice(bus.UpgradeResult{}, false, nil)
	if out != "" || warn != "" {
		t.Errorf("a current daemon must print nothing, got out=%q warn=%q", out, warn)
	}
}

func TestRefreshNoticeNamesVersionDelta(t *testing.T) {
	res := bus.UpgradeResult{UpgradePlan: bus.UpgradePlan{
		Session:     "mine",
		DaemonBuild: bus.Info{Version: "v0.1.0"},
		Installed:   bus.Info{Version: "v0.2.0"},
	}}

	out, warn := refreshNotice(res, true, nil)
	if warn != "" {
		t.Errorf("an upgrade is not a warning, got %q", warn)
	}
	plain := tui.StripAnsi(out)
	if !strings.Contains(plain, "v0.1.0") || !strings.Contains(plain, "v0.2.0") {
		t.Errorf("notice must name the build it left and the one it joined, got %q", plain)
	}
}

func TestRefreshNoticeWarnsOnFailure(t *testing.T) {
	out, warn := refreshNotice(bus.UpgradeResult{}, false, fmt.Errorf("ps: operation not permitted"))
	if out != "" {
		t.Errorf("a failed check prints nothing to stdout, got %q", out)
	}
	if !strings.Contains(warn, "operation not permitted") {
		t.Errorf("the warning must carry the cause, got %q", warn)
	}
}

// TestRefreshSessionDaemonReturnsWhenCheckFails is the load-bearing one: the
// attach runs immediately after this call, so a refresh failure that aborted
// would lock the user out of a running session over a version check. `ps` is
// denied under some sandboxes, which makes that failure routine, not exotic.
func TestRefreshSessionDaemonReturnsWhenCheckFails(t *testing.T) {
	calls := stubEnsureDaemonCurrent(t, bus.UpgradeResult{}, false, fmt.Errorf("ps: operation not permitted"))

	refreshSessionDaemon("mine") // reaching the next line is the assertion

	if *calls != 1 {
		t.Errorf("expected exactly 1 daemon check, got %d", *calls)
	}
}

// stubSessionSeams replaces runSession's three bus seams for one test and
// records the order they fired in. Order is the point: a test that only counted
// calls would still pass with the refresh moved after the attach, where it can
// never run.
func stubSessionSeams(t *testing.T, running bool, refreshErr, attachErr error) *[]string {
	t.Helper()
	order := new([]string)

	prevEnsure, prevHas := ensureDaemonCurrent, hasSession
	prevAttach, prevLaunch := attachSession, launchSession
	ensureDaemonCurrent = func(s string) (bus.UpgradeResult, bool, error) {
		*order = append(*order, "refresh:"+s)
		return bus.UpgradeResult{}, false, refreshErr
	}
	hasSession = func(string) bool { return running }
	attachSession = func(s string) error {
		*order = append(*order, "attach:"+s)
		return attachErr
	}
	launchSession = func(_ *bus.LauncherConfig, _, s string) error {
		*order = append(*order, "launch:"+s)
		return nil
	}
	t.Cleanup(func() {
		ensureDaemonCurrent, hasSession = prevEnsure, prevHas
		attachSession, launchSession = prevAttach, prevLaunch
	})
	return order
}

// TestRunSessionRefreshesBeforeAttaching pins the wiring, not the helpers:
// deleting the refresh call from the attach branch leaves every other test in
// this file green, so this is the one that notices. Attaching is the road that
// survives MUX-161 — `ps` is denied inside the build agent's sandbox, so
// build.sh's own upgrade cannot run — which makes a silent regression here the
// difference between a fleet on the installed binary and one still executing
// the code it loaded at launch.
func TestRunSessionRefreshesBeforeAttaching(t *testing.T) {
	order := stubSessionSeams(t, true, nil, nil)

	if err := runSession(nil, "/proj", "mine"); err != nil {
		t.Fatalf("attach must succeed, got %v", err)
	}

	want := []string{"refresh:mine", "attach:mine"}
	if !slices.Equal(*order, want) {
		t.Errorf("refresh must run, name the resolved session and precede the attach\n got %v\nwant %v", *order, want)
	}
}

// TestRunSessionAttachesWhenRefreshFails is the consequence of the guarantee
// above: refreshing first only stays safe while a failed check cannot strand
// the user outside a session that is running fine.
func TestRunSessionAttachesWhenRefreshFails(t *testing.T) {
	order := stubSessionSeams(t, true, fmt.Errorf("ps: operation not permitted"), nil)

	if err := runSession(nil, "/proj", "mine"); err != nil {
		t.Fatalf("a failed version check must not fail the attach, got %v", err)
	}

	if !slices.Contains(*order, "attach:mine") {
		t.Errorf("attach must still be reached after a failed refresh, got %v", *order)
	}
}

// TestRunSessionFreshLaunchSkipsRefresh is the negative control: a renderer
// that always refreshed would pass both tests above.
func TestRunSessionFreshLaunchSkipsRefresh(t *testing.T) {
	order := stubSessionSeams(t, false, nil, nil)

	if err := runSession(nil, "/proj", "mine"); err != nil {
		t.Fatalf("fresh launch must succeed, got %v", err)
	}

	want := []string{"launch:mine"}
	if !slices.Equal(*order, want) {
		t.Errorf("a fresh launch starts its daemon from PATH and has nothing to refresh\n got %v\nwant %v", *order, want)
	}
}

func TestRunSessionPropagatesAttachError(t *testing.T) {
	stubSessionSeams(t, true, nil, fmt.Errorf("no server running"))

	if err := runSession(nil, "/proj", "mine"); err == nil {
		t.Error("a failed attach must reach the caller, which exits nonzero")
	}
}
