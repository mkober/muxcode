package harness

import (
	"testing"
)

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		text    string
		want    bool
	}{
		{"hello", "hello", true},
		{"hello", "world", false},
		{"git *", "git status", true},
		{"git *", "git diff --cached", true},
		{"git *", "git", false},
		{"git*", "git", true},
		{"git*", "git status", true},
		{"cd * && git *", "cd /tmp && git status", true},
		{"cd * && git *", "git status", false},
		{"*", "", true},
		{"*", "anything", true},
		{"", "", true},
		{"", "something", false},
		{"file?.txt", "file1.txt", true},
		{"file?.txt", "file12.txt", false},
		{"muxcode-agent-bus *", "muxcode-agent-bus send build build \"test\"", true},
		{"./build.sh*", "./build.sh", true},
		{"./build.sh*", "./build.sh --verbose", true},
	}

	for _, tt := range tests {
		got := GlobMatch(tt.pattern, tt.text)
		if got != tt.want {
			t.Errorf("GlobMatch(%q, %q) = %v, want %v", tt.pattern, tt.text, got, tt.want)
		}
	}
}

func TestIsToolAllowed_Bash(t *testing.T) {
	patterns := []string{
		"Bash(git *)",
		"Bash(gh *)",
		"Bash(muxcode-agent-bus *)",
		"Read",
		"Glob",
	}

	tests := []struct {
		command string
		want    bool
	}{
		{"git status", true},
		{"git commit -m 'test'", true},
		{"gh pr create", true},
		{"muxcode-agent-bus send build build \"test\"", true},
		{"rm -rf /", false},
		{"curl http://example.com", false},
	}

	for _, tt := range tests {
		got := IsToolAllowed("bash", tt.command, patterns)
		if got != tt.want {
			t.Errorf("IsToolAllowed(bash, %q) = %v, want %v", tt.command, got, tt.want)
		}
	}
}

func TestIsToolAllowed_NonBash(t *testing.T) {
	patterns := []string{"Bash(git *)", "Read", "Glob", "Grep"}

	if !IsToolAllowed("read_file", "", patterns) {
		t.Error("read_file should be allowed")
	}
	if !IsToolAllowed("glob", "", patterns) {
		t.Error("glob should be allowed")
	}
	if !IsToolAllowed("grep", "", patterns) {
		t.Error("grep should be allowed")
	}
	if IsToolAllowed("write_file", "", patterns) {
		t.Error("write_file should not be allowed")
	}
	if IsToolAllowed("edit_file", "", patterns) {
		t.Error("edit_file should not be allowed")
	}
}

func TestIsToolAllowed_Unknown(t *testing.T) {
	patterns := []string{"Read", "Bash(git *)"}
	if IsToolAllowed("unknown_tool", "", patterns) {
		t.Error("unknown tool should not be allowed")
	}
}

func TestBuildToolDefs_WithBashAndRead(t *testing.T) {
	patterns := []string{"Bash(git *)", "Read"}
	defs := BuildToolDefs(patterns)

	names := make(map[string]bool)
	for _, d := range defs {
		names[d.Function.Name] = true
	}

	if !names["bash"] {
		t.Error("should include bash tool")
	}
	if !names["read_file"] {
		t.Error("should include read_file tool")
	}
	if names["glob"] {
		t.Error("should not include glob")
	}
}

func TestBuildToolDefs_Empty(t *testing.T) {
	defs := BuildToolDefs(nil)
	if defs != nil {
		t.Errorf("expected nil for empty patterns, got %d defs", len(defs))
	}
}

func TestBuildToolDefs_AllTools(t *testing.T) {
	patterns := []string{"Bash(git *)", "Read", "Glob", "Grep", "Write", "Edit"}
	defs := BuildToolDefs(patterns)
	if len(defs) != 6 {
		t.Errorf("expected 6 tool defs, got %d", len(defs))
	}
}

func TestHasToolPattern(t *testing.T) {
	patterns := []string{"Bash(git *)", "Read", "Glob"}

	if !hasToolPattern(patterns, "Bash") {
		t.Error("should find Bash in Bash(*) patterns")
	}
	if !hasToolPattern(patterns, "Read") {
		t.Error("should find Read")
	}
	if hasToolPattern(patterns, "Write") {
		t.Error("should not find Write")
	}
}
