package harness

import "fmt"

// Version, Commit and BuildDate are stamped by the Makefile through
// -ldflags "-X muxcode-llm-harness/harness.Version=..."; an unstamped build
// reports "devel". The bus binary's bus/version.go is the full-featured
// counterpart — the harness only needs to name itself.
var (
	Version   string
	Commit    string
	BuildDate string
)

// BuildVersion returns the stamped version, or "devel" for an unstamped build.
func BuildVersion() string {
	if Version == "" {
		return "devel"
	}
	return Version
}

// VersionLine is the one-line identity printed by `muxcode-llm-harness --version`.
func VersionLine() string {
	commit, date := Commit, BuildDate
	if commit == "" {
		commit = "unknown"
	}
	if date == "" {
		date = "unknown"
	}
	return fmt.Sprintf("muxcode-llm-harness %s (%s, %s)", BuildVersion(), commit, date)
}
