package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// parseIntentKey splits a bare spec id typed as an intent ("115",
// "MUX-115", "mux115", "PBP1-456") into prefix and digits. Explicit
// parsing rather than a regex: a greedy [A-Z0-9]* prefix class would
// mis-split the hyphenless "MUX115". Anything with more words is a real
// intent, not an id.
func parseIntentKey(s string) (prefix, num string, ok bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return "", "", false
	}
	if i := strings.LastIndexByte(s, '-'); i >= 0 {
		prefix, num = s[:i], s[i+1:]
	} else if j := strings.IndexAny(s, "0123456789"); j > 0 {
		prefix, num = s[:j], s[j:]
	} else {
		prefix, num = "", s
	}
	if num == "" || strings.Trim(num, "0123456789") != "" {
		return "", "", false
	}
	for i, r := range prefix {
		alnum := (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !alnum || (i == 0 && r >= '0' && r <= '9') {
			return "", "", false
		}
	}
	return prefix, num, true
}

// specFileKeyRe extracts the tracking key from a spec filename
// (MUX-115-some-slug.md → MUX-115, 115).
var specFileKeyRe = regexp.MustCompile(`^(([A-Z][A-Z0-9]*)-([0-9]+))-.+\.md$`)

// intentSpecDirs is the resolution order: work in flight beats planned
// beats shipped.
var intentSpecDirs = []string{"drafts", "backlog", "completed"}

// ExpandIntentKeyFor resolves a bare spec id typed as an intent into a
// descriptive intent line built from the spec it names: key, H1 title,
// and the first phase heading that still has open checkboxes. A user
// launching work on a tracked spec should not have to retype what the
// spec already says (user request 2026-08-28). Specs resolve against the
// session's repo dir, falling back to the working directory — the TUI is
// not guaranteed to run from repo root. Returns the input unchanged with
// ok=false when it is not a bare id or no spec matches.
func ExpandIntentKeyFor(session, input string) (string, bool) {
	return expandIntentKeyIn(intentRoot(session), input)
}

func expandIntentKeyIn(root, input string) (string, bool) {
	prefix, rawNum, ok := parseIntentKey(input)
	if !ok {
		return input, false
	}
	m, ok := findSpecByKey(root, prefix, rawNum)
	if !ok {
		return input, false
	}
	return describeSpecIntent(filepath.Join(root, m.rel), m.key), true
}

// specMatch is one spec file located by its tracking key.
type specMatch struct {
	key string // as spelled by the filename, e.g. MUX-115
	dir string // drafts | backlog | completed
	rel string // repo-relative path
}

// findSpecByKey locates the spec file for a tracking key by number
// (leading zeros ignored) and optional prefix, in intentSpecDirs order.
func findSpecByKey(root, prefix, rawNum string) (specMatch, bool) {
	num := strings.TrimLeft(rawNum, "0")
	for _, dir := range intentSpecDirs {
		full := filepath.Join(root, "docs", "requirements", dir)
		entries, err := os.ReadDir(full)
		if err != nil {
			continue
		}
		for _, e := range entries {
			km := specFileKeyRe.FindStringSubmatch(e.Name())
			if km == nil || strings.TrimLeft(km[3], "0") != num {
				continue
			}
			if prefix != "" && km[2] != prefix {
				continue
			}
			return specMatch{key: km[1], dir: dir, rel: filepath.Join("docs", "requirements", dir, e.Name())}, true
		}
	}
	return specMatch{}, false
}

// branchKeyRe matches a tracking key leading a branch path segment:
// MUX-138-slug, MUX-138, or the segment after the slash in
// feature/MUX-138-slug. The key must lead its segment so a slug that
// merely mentions another spec ("fix-MUX-12-typo") never resolves to it.
var branchKeyRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*)-([0-9]+)(?:-|$)`)

// BranchSpecKey returns the tracking key a branch name carries
// (MUX-138-github-versioning → MUX-138), uppercased, or "" when no
// segment leads with one.
func BranchSpecKey(branch string) string {
	for _, seg := range strings.Split(branch, "/") {
		if m := branchKeyRe.FindStringSubmatch(seg); m != nil {
			return strings.ToUpper(m[1] + "-" + m[2])
		}
	}
	return ""
}

// BranchSpec is what a branch name resolves to: the tracking key it leads
// with, the spec file that key names, and the intent line the launcher
// would otherwise make the user type — the branch already says which spec
// the work is (user request 2026-09-02).
type BranchSpec struct {
	Branch string
	Key    string
	Dir    string // drafts | backlog | completed
	Path   string // repo-relative spec path
	Intent string
}

// ResolveBranchSpec resolves the session repo's current branch to its
// spec. The error names why not — no branch, no key, no spec file — so a
// caller can show the reason instead of an unexplained blank prompt.
func ResolveBranchSpec(session string) (BranchSpec, error) {
	root := intentRoot(session)
	return resolveBranchSpecIn(root, CurrentBranchIn(root))
}

func resolveBranchSpecIn(root, branch string) (BranchSpec, error) {
	if branch == "" {
		return BranchSpec{}, errors.New("no current branch (not a git checkout, or detached HEAD)")
	}
	key := BranchSpecKey(branch)
	if key == "" {
		return BranchSpec{}, fmt.Errorf("branch %s carries no spec key", branch)
	}
	prefix, num, _ := parseIntentKey(key)
	m, ok := findSpecByKey(root, prefix, num)
	if !ok {
		return BranchSpec{}, fmt.Errorf("branch %s names %s but no spec file matches it", branch, key)
	}
	return BranchSpec{
		Branch: branch,
		Key:    m.key,
		Dir:    m.dir,
		Path:   m.rel,
		Intent: describeSpecIntent(filepath.Join(root, m.rel), m.key),
	}, nil
}

// ActiveSpecRelation is how the session's active-spec pointer stands
// against a branch spec, so a launch can state what confirming does to it.
type ActiveSpecRelation struct {
	Current string // pointer as stored; "" when unset
	Matches bool   // Current names the same file as the branch spec
}

// ActiveSpecRelationFor reads the session's pointer and relates it to spec.
func ActiveSpecRelationFor(session string, spec BranchSpec) ActiveSpecRelation {
	return activeSpecRelationIn(intentRoot(session), ReadActiveSpec(session), spec.Path)
}

func activeSpecRelationIn(root, current, specPath string) ActiveSpecRelation {
	if current == "" {
		return ActiveSpecRelation{}
	}
	return ActiveSpecRelation{Current: current, Matches: absUnder(root, current) == absUnder(root, specPath)}
}

// ErrActiveSpecMismatch is returned by LaunchIntentFromBranch when the
// branch names a different spec than the active pointer — the one
// outcome a non-interactive launch must not resolve on its own.
var ErrActiveSpecMismatch = errors.New("active spec mismatch")

// LaunchIntentFromBranch is the non-interactive counterpart of the
// launcher's spec confirm for `graph run` with no intent: it resolves the
// branch spec and sets the active-spec pointer when unset. It refuses
// with ErrActiveSpecMismatch when confirming would SWITCH the pointer —
// the run's ${current_phase} resolves from the active spec, so an intent
// naming a different spec would drive the wrong work, and a switch needs
// the human a CLI run has not got. set reports whether the pointer was
// written.
func LaunchIntentFromBranch(session string) (spec BranchSpec, set bool, err error) {
	spec, err = ResolveBranchSpec(session)
	if err != nil {
		return BranchSpec{}, false, err
	}
	active := ActiveSpecRelationFor(session, spec)
	switch {
	case active.Matches:
		return spec, false, nil
	case active.Current != "":
		return BranchSpec{}, false, fmt.Errorf("%w: active spec is %s but branch %s names %s — run `muxcode spec set %s` first, or pass an intent",
			ErrActiveSpecMismatch, active.Current, spec.Branch, spec.Path, spec.Path)
	}
	if err := WriteActiveSpec(session, spec.Path); err != nil {
		return BranchSpec{}, false, fmt.Errorf("cannot set active spec: %w", err)
	}
	return spec, true, nil
}

// ErrNoActiveSpec reports that no active-spec pointer is set, so the
// caller must offer a choice rather than guess — the CLI prints its list,
// the TUI opens its picker.
var ErrNoActiveSpec = errors.New("no active spec set")

// ActiveSpecIntent derives a run intent from the session's active spec.
// The pointer names the spec; the phase is read from the file at launch,
// so a run picks up wherever the doc actually is.
//
// The branch is deliberately not consulted. Branch derivation
// (LaunchIntentFromBranch) refuses whenever branch and pointer disagree,
// which made a run unstartable on any branch not named for its spec —
// and one spec is worked from many branches. It also gave the run an
// intent from a different source than ${current_phase}, so the two named
// different phases and the commit guard scoped a ship to a phase the
// worker was never given (MUX-143).
func ActiveSpecIntent(session string) (BranchSpec, error) {
	root := intentRoot(session)
	rel := ReadActiveSpec(session)
	if rel == "" {
		return BranchSpec{}, ErrNoActiveSpec
	}
	full := ResolveSpecPath(root, rel)
	if full == "" {
		return BranchSpec{}, fmt.Errorf("active spec %s does not resolve to a file inside the repo", rel)
	}
	key := specKeyFromFile(full)
	return BranchSpec{
		Branch: CurrentBranchIn(root),
		Key:    key,
		Path:   rel,
		Intent: describeSpecIntent(full, key),
	}, nil
}

// specKeyFromFile reads the tracking key out of a spec's filename, empty
// when it carries none — a spec pointed at by hand need not be named for
// a key, and an empty key simply drops out of the intent line.
func specKeyFromFile(path string) string {
	if m := specFileKeyRe.FindStringSubmatch(filepath.Base(path)); m != nil {
		return m[1]
	}
	return ""
}

// SpecChoice is one selectable spec offered when no pointer is set.
type SpecChoice struct {
	Key    string
	Dir    string // drafts | backlog
	Path   string // repo-relative
	Intent string
}

// ListSpecChoices returns the specs a run can be pointed at, drafts
// before backlog. Completed specs are excluded: pointing a run at one
// would derive "no open phase" and drive an immediately vacuous run.
func ListSpecChoices(session string) ([]SpecChoice, error) {
	root := intentRoot(session)
	var out []SpecChoice
	for _, dir := range []string{"drafts", "backlog"} {
		full := filepath.Join(root, "docs", "requirements", dir)
		entries, err := os.ReadDir(full)
		if err != nil {
			continue
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			rel := filepath.Join("docs", "requirements", dir, name)
			key := specKeyFromFile(name)
			out = append(out, SpecChoice{
				Key: key, Dir: dir, Path: rel,
				Intent: describeSpecIntent(filepath.Join(root, rel), key),
			})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no specs found under %s", filepath.Join("docs", "requirements"))
	}
	return out, nil
}

// intentRoot is the repo dir spec lookups resolve against: the session's
// repo, falling back to the working directory — the TUI is not
// guaranteed to run from repo root.
func intentRoot(session string) string {
	if root := SessionRepoDir(session); root != "" {
		return root
	}
	return "."
}

// absUnder normalizes a spec pointer to an absolute cleaned path, taking
// a relative pointer against root the way ResolveSpecPath reads the
// active pointer.
func absUnder(root, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// describeSpecIntent builds the expanded intent: "<key> <H1 title> —
// <first phase heading with open items>". Fenced code blocks are skipped
// for the same reason as SpecOpenItems: quoted examples are not state.
func describeSpecIntent(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return key
	}
	title, phase, curPhase := "", "", ""
	inFence := false
	for _, ln := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if title == "" && strings.HasPrefix(ln, "# ") {
			title = strings.TrimSpace(strings.TrimPrefix(ln, "# "))
			continue
		}
		if strings.HasPrefix(trimmed, "### Phase") {
			curPhase = strings.TrimSpace(strings.TrimPrefix(trimmed, "###"))
			continue
		}
		if phase == "" && curPhase != "" && strings.HasPrefix(trimmed, "- [ ]") {
			phase = curPhase
		}
	}
	if strings.HasPrefix(title, key) {
		title = strings.TrimSpace(strings.TrimLeft(strings.TrimPrefix(title, key), ":-— "))
	}
	switch {
	case title == "":
		return key
	case phase == "":
		return fmt.Sprintf("%s %s", key, title)
	}
	return fmt.Sprintf("%s %s — %s", key, title, phase)
}
