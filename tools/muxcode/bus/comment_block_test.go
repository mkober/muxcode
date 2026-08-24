package bus

import (
	"strings"
	"testing"
)

// The shape these pin: prose wedged between statements hides the outline of the
// logic. The check exists because a skill rule alone did not survive twenty tool
// calls of context pressure — it has to fire without being remembered.

func TestScanCommentBlocks_FlagsIndentedRun(t *testing.T) {
	src := "func f() {\n" +
		"\t// one\n" +
		"\t// two\n" +
		"\t// three\n" +
		"\treturn nil\n}"

	got := ScanCommentBlocks("handler.go", src)
	if len(got) != 1 {
		t.Fatalf("ScanCommentBlocks() = %d findings, want 1", len(got))
	}
	if got[0].Count != 3 {
		t.Errorf("Count = %d, want 3", got[0].Count)
	}
	if got[0].Line != 2 {
		t.Errorf("Line = %d, want 2 (1-indexed within the fragment)", got[0].Line)
	}
}

func TestScanCommentBlocks_AllowsTwoLines(t *testing.T) {
	// Two lines is a wrapped sentence. Firing here would make the check noise.
	src := "func f() {\n\t// one\n\t// two\n\treturn nil\n}"
	if got := ScanCommentBlocks("handler.go", src); len(got) != 0 {
		t.Errorf("ScanCommentBlocks() = %d findings, want 0 — two lines must not trip it", len(got))
	}
}

func TestScanCommentBlocks_IgnoresColumnZero(t *testing.T) {
	// A run at column zero is a license header or package doc — the boundary
	// the skill tells authors to write at. Flagging it punishes correct work.
	src := "// Copyright\n// All rights reserved.\n// Line three.\n\npackage bus"
	if got := ScanCommentBlocks("handler.go", src); len(got) != 0 {
		t.Errorf("ScanCommentBlocks() = %d findings, want 0 — column-zero runs are boundary comments", len(got))
	}
}

// Regression: comparing raw against the fully-trimmed line reads a column-zero
// comment with a trailing space as indented, which flags file headers.
func TestScanCommentBlocks_TrailingSpaceIsNotIndentation(t *testing.T) {
	src := "// one \n// two \n// three \n\npackage bus"
	if got := ScanCommentBlocks("handler.go", src); len(got) != 0 {
		t.Errorf("ScanCommentBlocks() = %d findings, want 0 — trailing space is not indentation", len(got))
	}
}

func TestScanCommentBlocks_PythonDocstringNotScanned(t *testing.T) {
	// '#' lines inside a docstring are example code, not comments to hoist.
	src := "def f():\n" +
		"    \"\"\"Doc.\n" +
		"    # not a comment\n" +
		"    # still not\n" +
		"    # nor this\n" +
		"    \"\"\"\n" +
		"    return 1"
	if got := ScanCommentBlocks("handler.py", src); len(got) != 0 {
		t.Errorf("ScanCommentBlocks() = %d findings, want 0 — docstring contents are not comments", len(got))
	}
}

func TestScanCommentBlocks_PythonBodyRunFlagged(t *testing.T) {
	src := "def f():\n" +
		"    # one\n" +
		"    # two\n" +
		"    # three\n" +
		"    return 1"
	if got := ScanCommentBlocks("handler.py", src); len(got) != 1 {
		t.Fatalf("ScanCommentBlocks() = %d findings, want 1", len(got))
	}
}

// Regression: docstring tracking applied to every language let a lone `"""`
// inside a Go or TS string literal latch the scanner off, silently skipping the
// rest of the fragment — a check that quietly stops checking.
func TestScanCommentBlocks_TripleQuoteDoesNotDisableNonPython(t *testing.T) {
	src := "func f() {\n" +
		"\ts := `he said \"\"\" loudly`\n" +
		"\t// one\n" +
		"\t// two\n" +
		"\t// three\n" +
		"\treturn s\n}"

	if got := ScanCommentBlocks("handler.go", src); len(got) != 1 {
		t.Errorf("ScanCommentBlocks() = %d findings, want 1 — a triple quote in Go must not disable the scan", len(got))
	}
}

func TestScanCommentBlocks_SeparatedRunsDoNotMerge(t *testing.T) {
	// Two two-line groups split by a statement are two wrapped sentences, not
	// a four-line paragraph.
	src := "func f() {\n\t// a\n\t// b\n\tx := 1\n\t// c\n\t// d\n\treturn x\n}"
	if got := ScanCommentBlocks("handler.go", src); len(got) != 0 {
		t.Errorf("ScanCommentBlocks() = %d findings, want 0 — a statement breaks the run", len(got))
	}
}

func TestIsCommentBlockExempt(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"handler.go", false},
		{"handler.py", false},
		{"app/service.ts", false},
		{"daemon_test.go", true},
		{"test_handler.py", true},
		{"scripts/test-disk-pressure.sh", true},
		{"scripts/test-hot-reload.sh", true},
		{"src/__tests__/thing.ts", true},
		{"app/thing.spec.ts", true},
		{"README.md", true},
		{"config.json", true},
		{"", true},
	} {
		if got := IsCommentBlockExempt(tc.path); got != tc.want {
			t.Errorf("IsCommentBlockExempt(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// Regression: byte-slicing the label split multibyte runes, so a comment
// containing an em dash rendered U+FFFD in the message shown to the author.
func TestTruncate_CutsOnRuneBoundary(t *testing.T) {
	// 12 em dashes: 1 rune each, 3 bytes each. A byte cut lands mid-rune.
	got := truncate(strings.Repeat("—", 12), 6)
	if strings.ContainsRune(got, '�') {
		t.Errorf("truncate() = %q, contains U+FFFD — cut must land on a rune boundary", got)
	}
	if want := "—————…"; got != want {
		t.Errorf("truncate() = %q, want %q", got, want)
	}
}

// Pins the constant: lowering it to 2 would fire on every wrapped sentence and
// train people to ignore the check, which is worse than not having it.
func TestCommentBlockThreshold(t *testing.T) {
	if commentBlockThreshold != 3 {
		t.Errorf("commentBlockThreshold = %d, want 3", commentBlockThreshold)
	}
}
