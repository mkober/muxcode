package bus

import (
	"fmt"
	"path/filepath"
	"strings"
)

// commentBlockThreshold is the run length that trips the check.
//
// The skill's rule is stricter than this (at most one short line inside a body).
// The hook deliberately fires later so it flags only the unambiguous case: two
// lines can be a wrapped sentence, three is a paragraph, and a paragraph in a
// function body is the shape that hides the code. A hook that fired at the
// skill's true limit would be right more often but ignored, which is worse than
// one that fires rarely and is always worth acting on.
const commentBlockThreshold = 3

// CommentBlockFinding is one run of consecutive comment lines found inside a
// function body.
type CommentBlockFinding struct {
	// Line is 1-indexed within the scanned fragment, not the file: a
	// PostToolUse hook sees the replacement text, which has no file offset.
	Line  int
	Count int
	Text  string
}

// commentPrefixes maps a file extension to its line-comment marker. An
// extension absent from this map is not scanned at all — silence on an unknown
// language beats guessing a marker and flagging strings as prose.
var commentPrefixes = map[string]string{
	".go": "//", ".ts": "//", ".tsx": "//", ".js": "//", ".jsx": "//",
	".java": "//", ".rs": "//", ".c": "//", ".cc": "//", ".cpp": "//", ".h": "//",
	".py": "#", ".sh": "#", ".bash": "#", ".rb": "#",
}

// testPathMarkers identify files where a long explanatory block is legitimate:
// a test's comment often IS the specification of the behaviour under test, and
// flagging it trains people to dismiss the check.
var testPathMarkers = []string{
	"_test.go", "_test.py", "_test.rb", ".test.ts", ".test.js",
	".spec.ts", ".spec.js", "/tests/", "/test/", "/__tests__/", "/spec/",
}

// IsCommentBlockExempt reports whether a path is outside the check's scope —
// an unsupported language or a test file.
func IsCommentBlockExempt(path string) bool {
	if path == "" {
		return true
	}
	if _, ok := commentPrefixes[strings.ToLower(filepath.Ext(path))]; !ok {
		return true
	}
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)
	// Both separators: Python uses test_foo.py, while this repo's own
	// integration tests are scripts/test-foo.sh. Matching only the underscore
	// left every test-*.sh unexempt.
	if strings.HasPrefix(base, "test_") || strings.HasPrefix(base, "test-") {
		return true
	}
	for _, m := range testPathMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// ScanCommentBlocks finds runs of >= commentBlockThreshold consecutive comment
// lines that sit at function-body indentation in the given text.
//
// Only indented runs count. A run at column zero is a file header, license
// block, or package/module doc — all of which are the boundary the skill tells
// authors to write at, so flagging them would punish the correct behaviour.
//
// text is expected to be the fragment an edit introduced (Edit's new_string or
// Write's content) rather than the whole file, so the check reports only what
// the author just wrote and never pre-existing blocks in a file they touched.
func ScanCommentBlocks(path, text string) []CommentBlockFinding {
	if IsCommentBlockExempt(path) || text == "" {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	prefix := commentPrefixes[ext]

	// Docstring tracking is Python-only. A lone `"""` is a docstring delimiter
	// there, but in Go or TypeScript it is just characters inside a string
	// literal — treating it as a delimiter would latch inDocstring on and
	// silently skip every remaining line, disabling the check exactly where a
	// long comment block is most likely to follow.
	tracksDocstrings := ext == ".py"

	var findings []CommentBlockFinding
	lines := strings.Split(text, "\n")

	runStart, runLen := 0, 0
	inDocstring := false
	var docQuote string

	flush := func() {
		if runLen >= commentBlockThreshold {
			findings = append(findings, CommentBlockFinding{
				Line:  runStart,
				Count: runLen,
				Text:  strings.TrimSpace(lines[runStart-1]),
			})
		}
		runLen = 0
	}

	for i, raw := range lines {
		trimmed := strings.TrimSpace(raw)

		// A '#' inside a Python docstring is example code or prose, not a
		// comment the author is being asked to hoist.
		if tracksDocstrings {
			if q := docstringToggle(trimmed); q != "" {
				if !inDocstring {
					inDocstring, docQuote = true, q
				} else if q == docQuote {
					inDocstring = false
				}
				flush()
				continue
			}
			if inDocstring {
				flush()
				continue
			}
		}

		// Leading whitespace specifically — comparing against the fully
		// trimmed line would read a column-zero comment with a trailing space
		// as indented, flagging the file headers this check must never touch.
		indented := trimmed != "" && (raw[0] == ' ' || raw[0] == '\t')
		if indented && strings.HasPrefix(trimmed, prefix) {
			if runLen == 0 {
				runStart = i + 1
			}
			runLen++
			continue
		}
		flush()
	}
	flush()

	return findings
}

// docstringToggle returns the triple-quote marker if the line opens or closes a
// Python docstring, or "" otherwise. A line carrying two markers (a one-line
// docstring) opens and closes, so it toggles nothing.
func docstringToggle(trimmed string) string {
	for _, q := range []string{`"""`, `'''`} {
		if n := strings.Count(trimmed, q); n == 1 {
			return q
		}
	}
	return ""
}

// FormatCommentBlockReason renders findings as the reason text fed back to the
// agent.
func FormatCommentBlockReason(path string, findings []CommentBlockFinding) string {
	var b strings.Builder
	plural := "block"
	if len(findings) > 1 {
		plural = "blocks"
	}
	fmt.Fprintf(&b, "code-comments: %d multi-line comment %s inside a function body in %s.\n\n",
		len(findings), plural, filepath.Base(path))
	for _, f := range findings {
		fmt.Fprintf(&b, "  line %d of the edited text: %d consecutive comment lines — %q\n", f.Line, f.Count, truncate(f.Text, 60))
	}
	b.WriteString("\nProse between statements hides the shape of the code. Move the rationale to " +
		"the function or type doc comment, or extract a named function and let the name carry it. " +
		"Inside a body, keep it to one short line.")
	return b.String()
}

// truncate cuts on rune boundaries, not bytes: comment prose is full of em
// dashes and quotes, and slicing one in half emits U+FFFD into the very message
// meant to show the author their own line.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
