package bus

import (
	"cmp"
	"fmt"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
)

// Version, Commit and BuildDate are stamped by the Makefile through
// -ldflags "-X github.com/mkober/muxcode/tools/muxcode/bus.Version=...".
// A binary built any other way leaves them empty; BuildInfo then falls back
// to the VCS metadata Go embeds in the executable, and finally to "devel",
// so the version is never reported as an empty string.
var (
	Version   string
	Commit    string
	BuildDate string
)

// Info is the identity of the running binary. The JSON field names are the
// `muxcode version --json` output contract — scripts parse them, so a rename
// is a breaking change.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// String renders the one-line form printed by `muxcode version`:
// "muxcode v0.1.0 (abc1234, 2026-09-02T12:00:00Z, go1.22.5 darwin/arm64)".
func (i Info) String() string {
	return fmt.Sprintf("muxcode %s (%s, %s, %s %s/%s)", i.Version, i.Commit, i.Date, i.GoVersion, i.OS, i.Arch)
}

// BuildInfo resolves the binary identity: ldflags first, then the embedded
// VCS settings (vcs.revision, vcs.modified, vcs.time), then "devel"/"unknown".
func BuildInfo() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		Date:      BuildDate,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		fillFromVCS(&info, bi.Settings)
	}
	if info.Version == "" {
		info.Version = "devel"
	}
	if info.Commit == "" {
		info.Commit = "unknown"
	}
	if info.Date == "" {
		info.Date = "unknown"
	}
	return info
}

// BuildVersion returns the version string alone; see BuildInfo for the
// fallback chain.
func BuildVersion() string {
	return BuildInfo().Version
}

// fillFromVCS fills the fields ldflags left empty from Go's embedded VCS
// settings. An unstamped build mirrors what `git describe --always --dirty`
// produces with no tag in reach — the short revision plus a -dirty marker —
// so a stamped and an unstamped binary read the same way.
func fillFromVCS(info *Info, settings []debug.BuildSetting) {
	var revision, modified, when string
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value
		case "vcs.time":
			when = s.Value
		}
	}
	if revision == "" {
		return
	}
	short := revision
	if len(short) > 7 {
		short = short[:7]
	}
	if info.Commit == "" {
		info.Commit = short
	}
	if info.Version == "" {
		info.Version = short
		if modified == "true" {
			info.Version += "-dirty"
		}
	}
	if info.Date == "" {
		info.Date = when
	}
}

// semver is a parsed version: the numeric core, the optional pre-release
// identifiers, and the commit distance a `git describe` suffix encodes.
type semver struct {
	core       [3]int
	prerelease []string
	distance   int
}

// CompareSemver orders two version strings, returning -1, 0 or 1. Both forms
// this binary reports are accepted: a plain tag ("v0.1.0", "0.2.0-rc1") and
// the `git describe --tags --dirty` shape ("v0.1.0-3-gabc1234-dirty").
// Ordering follows SemVer 2.0 with one extension: a describe suffix sorts
// AFTER its base tag, since it names commits past that tag, while a
// pre-release still sorts BEFORE its release. A "-dirty" marker and "+build"
// metadata are ignored. Anything else — an untagged describe like "2f55e13",
// "devel", a two-part number — is an error rather than a guess.
func CompareSemver(a, b string) (int, error) {
	va, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	vb, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	return va.compare(vb), nil
}

func parseSemver(s string) (semver, error) {
	orig := s
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimSuffix(s, "-dirty")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}

	var v semver
	var err error
	if v.distance, s, err = splitDescribe(s); err != nil {
		return v, fmt.Errorf("malformed version %q: %v", orig, err)
	}

	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("malformed version %q: want MAJOR.MINOR.PATCH", orig)
	}
	for i, p := range parts {
		if !isDigits(p) {
			return v, fmt.Errorf("malformed version %q: %q is not a number", orig, p)
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, fmt.Errorf("malformed version %q: %q is out of range", orig, p)
		}
		v.core[i] = n
	}
	if hasPre {
		if pre == "" {
			return v, fmt.Errorf("malformed version %q: empty pre-release", orig)
		}
		v.prerelease = strings.Split(pre, ".")
		for _, id := range v.prerelease {
			if !validPrereleaseIdent(id) {
				return v, fmt.Errorf("malformed version %q: bad pre-release identifier %q", orig, id)
			}
		}
	}
	return v, nil
}

// splitDescribe strips a trailing "-<N>-g<sha>" describe suffix, returning
// the commit distance N and the remaining version. A distance past int
// range is an error rather than a silently wrong rank.
func splitDescribe(s string) (int, string, error) {
	g := strings.LastIndex(s, "-g")
	if g < 0 || !isHex(s[g+2:]) {
		return 0, s, nil
	}
	n := strings.LastIndexByte(s[:g], '-')
	if n < 0 || !isDigits(s[n+1:g]) {
		return 0, s, nil
	}
	dist, err := strconv.Atoi(s[n+1 : g])
	if err != nil {
		return 0, s, fmt.Errorf("describe distance %q is out of range", s[n+1:g])
	}
	return dist, s[:n], nil
}

// validPrereleaseIdent enforces SemVer §9: an identifier is non-empty ASCII
// alphanumerics and hyphens, and a numeric identifier has no leading zero.
func validPrereleaseIdent(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-') {
			return false
		}
	}
	return !(isDigits(id) && len(id) > 1 && id[0] == '0')
}

func (v semver) compare(w semver) int {
	for i := range v.core {
		if c := cmp.Compare(v.core[i], w.core[i]); c != 0 {
			return c
		}
	}
	if c := comparePrerelease(v.prerelease, w.prerelease); c != 0 {
		return c
	}
	return cmp.Compare(v.distance, w.distance)
}

// comparePrerelease implements SemVer §11.4: a release outranks any
// pre-release; identifiers compare numerically when both are numeric and
// lexically otherwise, numeric below alphanumeric, and a shorter list that
// matches as a prefix ranks lower.
func comparePrerelease(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := compareIdent(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

// compareIdent orders two validated identifiers: numeric ones by value —
// as digit strings, by length then lexically, so a number past int range
// still ranks correctly — numeric below alphanumeric, alphanumerics
// lexically.
func compareIdent(a, b string) int {
	aNum, bNum := isDigits(a), isDigits(b)
	switch {
	case aNum && bNum:
		if c := cmp.Compare(len(a), len(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return strings.Compare(a, b)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
