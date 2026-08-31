package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	root := SessionRepoDir(session)
	if root == "" {
		root = "."
	}
	return expandIntentKeyIn(root, input)
}

func expandIntentKeyIn(root, input string) (string, bool) {
	prefix, rawNum, ok := parseIntentKey(input)
	if !ok {
		return input, false
	}
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
			return describeSpecIntent(filepath.Join(full, e.Name()), km[1]), true
		}
	}
	return input, false
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
	switch {
	case title == "":
		return key
	case phase == "":
		return fmt.Sprintf("%s %s", key, title)
	}
	return fmt.Sprintf("%s %s — %s", key, title, phase)
}
