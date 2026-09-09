package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
