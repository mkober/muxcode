package bus

import (
	"encoding/json"
	"runtime/debug"
	"strings"
	"testing"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.1.0", "v0.1.0", 0},
		{"0.1.0", "v0.1.0", 0},
		{"v0.1.0", "v0.2.0", -1},
		{"v0.2.0", "v0.1.0", 1},
		{"v0.1.9", "v0.1.10", -1},
		{"v1.0.0", "v0.9.9", 1},
		{"v1.0.0+build.5", "v1.0.0", 0},

		// A describe suffix names commits past the tag: after the tag,
		// before the next one, ordered by distance.
		{"v0.1.0-3-gabc1234", "v0.1.0", 1},
		{"v0.1.0", "v0.1.0-3-gabc1234", -1},
		{"v0.1.0-3-gabc1234", "v0.1.0-5-gdef5678", -1},
		{"v0.1.0-3-gabc1234", "v0.1.1", -1},
		{"v0.1.0-3-gabc1234-dirty", "v0.1.0-3-gabc1234", 0},
		{"v0.1.0-dirty", "v0.1.0", 0},

		// Pre-release ordering (SemVer §11).
		{"v1.0.0-rc1", "v1.0.0", -1},
		{"v1.0.0", "v1.0.0-rc1", 1},
		{"v1.0.0-rc1", "v1.0.0-rc2", -1},
		{"v1.0.0-alpha", "v1.0.0-alpha.1", -1},
		{"v1.0.0-alpha.1", "v1.0.0-alpha.beta", -1},
		{"v1.0.0-beta.2", "v1.0.0-beta.11", -1},
		{"v1.0.0-rc1-3-gabc1234", "v1.0.0-rc1", 1},
		{"v1.0.0-rc1-3-gabc1234", "v1.0.0", -1},
	}
	for _, c := range cases {
		got, err := CompareSemver(c.a, c.b)
		if err != nil {
			t.Errorf("CompareSemver(%q, %q): unexpected error: %v", c.a, c.b, err)
			continue
		}
		if got != c.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		back, _ := CompareSemver(c.b, c.a)
		if back != -c.want {
			t.Errorf("CompareSemver(%q, %q) = %d, not antisymmetric with %d", c.b, c.a, back, c.want)
		}
	}
}

func TestCompareSemverMalformed(t *testing.T) {
	for _, s := range []string{"", "devel", "2f55e13", "2f55e13-dirty", "v1.0", "v1", "1.0.0.0", "v1.x.0", "v1.0.0-", "vv1.0.0", "v+1.0.0", "v-1.0.0"} {
		if _, err := CompareSemver(s, "v0.1.0"); err == nil {
			t.Errorf("CompareSemver(%q, v0.1.0): want error, got nil", s)
		}
		if _, err := CompareSemver("v0.1.0", s); err == nil {
			t.Errorf("CompareSemver(v0.1.0, %q): want error, got nil", s)
		}
	}
}

func TestFillFromVCSMirrorsDescribe(t *testing.T) {
	info := Info{}
	fillFromVCS(&info, []debug.BuildSetting{
		{Key: "vcs.revision", Value: "2f55e13abcdef0123456789abcdef0123456789a"},
		{Key: "vcs.modified", Value: "true"},
		{Key: "vcs.time", Value: "2026-09-02T10:00:00Z"},
	})
	if info.Version != "2f55e13-dirty" || info.Commit != "2f55e13" || info.Date != "2026-09-02T10:00:00Z" {
		t.Fatalf("fillFromVCS = %+v", info)
	}

	clean := Info{}
	fillFromVCS(&clean, []debug.BuildSetting{{Key: "vcs.revision", Value: "2f55e13abc"}})
	if clean.Version != "2f55e13" {
		t.Fatalf("clean tree version = %q, want bare short revision", clean.Version)
	}
}

func TestFillFromVCSNeverOverridesLdflags(t *testing.T) {
	info := Info{Version: "v0.1.0", Commit: "abc1234", Date: "2026-09-01T00:00:00Z"}
	fillFromVCS(&info, []debug.BuildSetting{
		{Key: "vcs.revision", Value: "ffffffffff"},
		{Key: "vcs.modified", Value: "true"},
		{Key: "vcs.time", Value: "1970-01-01T00:00:00Z"},
	})
	if info.Version != "v0.1.0" || info.Commit != "abc1234" || info.Date != "2026-09-01T00:00:00Z" {
		t.Fatalf("ldflags values were overridden: %+v", info)
	}
}

// Test binaries carry neither ldflags nor a VCS stamp, which is exactly the
// "source build with nothing set" case the fallback chain must survive.
func TestBuildInfoNeverEmpty(t *testing.T) {
	info := BuildInfo()
	if info.Version == "" || info.Commit == "" || info.Date == "" || info.GoVersion == "" || info.OS == "" || info.Arch == "" {
		t.Fatalf("BuildInfo has an empty field: %+v", info)
	}
	if BuildVersion() != info.Version {
		t.Fatalf("BuildVersion() = %q, BuildInfo().Version = %q", BuildVersion(), info.Version)
	}
	if !strings.HasPrefix(info.String(), "muxcode "+info.Version+" (") {
		t.Fatalf("String() = %q", info.String())
	}
}

func TestBuildVersionHonoursLdflags(t *testing.T) {
	old := Version
	Version = "v9.9.9"
	t.Cleanup(func() { Version = old })
	if got := BuildVersion(); got != "v9.9.9" {
		t.Fatalf("BuildVersion() = %q, want ldflags value", got)
	}
}

// Overflow must be an error, not a silently mis-ranked version (review
// must-fix 2026-09-02): Atoi's error was discarded, so a core component or
// describe distance past int range parsed as garbage.
func TestCompareSemverOverflowIsError(t *testing.T) {
	big := "99999999999999999999"
	for _, s := range []string{"v" + big + ".0.0", "v1." + big + ".0", "v1.0." + big, "v1.0.0-" + big + "-gabc1234"} {
		if _, err := CompareSemver(s, "v1.0.0"); err == nil {
			t.Errorf("CompareSemver(%q, v1.0.0): want out-of-range error, got nil", s)
		}
		if _, err := CompareSemver("v1.0.0", s); err == nil {
			t.Errorf("CompareSemver(v1.0.0, %q): want out-of-range error, got nil", s)
		}
	}
}

// SemVer §9: identifiers are non-empty [0-9A-Za-z-], numerics carry no
// leading zero; a numeric identifier past int range still orders by value.
func TestCompareSemverPrereleaseIdentifiers(t *testing.T) {
	for _, s := range []string{"v1.0.0-rc.01", "v1.0.0-rc..1", "v1.0.0-rc_1", "v1.0.0-rc.", "v1.0.0-r%c"} {
		if _, err := CompareSemver(s, "v1.0.0"); err == nil {
			t.Errorf("CompareSemver(%q, v1.0.0): want identifier error, got nil", s)
		}
	}
	for _, ok := range []string{"v1.0.0-rc-1", "v1.0.0-0", "v1.0.0-0.a-b.C1"} {
		if _, err := CompareSemver(ok, "v1.0.0"); err != nil {
			t.Errorf("CompareSemver(%q, v1.0.0): valid identifier rejected: %v", ok, err)
		}
	}
	a, b := "v1.0.0-1.99999999999999999999", "v1.0.0-1.100000000000000000000"
	if c, err := CompareSemver(a, b); err != nil || c != -1 {
		t.Errorf("CompareSemver(%q, %q) = %d, %v; want -1 (numeric by value, overflow-proof)", a, b, c, err)
	}
	if c, _ := CompareSemver("v1.0.0-99999999999999999999", "v1.0.0-alpha"); c != -1 {
		t.Errorf("a huge numeric identifier must still rank below an alphanumeric one, got %d", c)
	}
}

// The --json field names are parsed by scripts; pin them.
func TestInfoJSONFields(t *testing.T) {
	out, err := json.Marshal(Info{Version: "v0.1.0", Commit: "abc", Date: "d", GoVersion: "go1.22", OS: "darwin", Arch: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"version", "commit", "date", "go", "os", "arch"} {
		if m[k] == "" {
			t.Errorf("JSON field %q missing from %s", k, out)
		}
	}
}
