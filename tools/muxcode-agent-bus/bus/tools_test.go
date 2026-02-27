package bus

import (
	"testing"
)

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		text    string
		want    bool
	}{
		// Exact match
		{"hello", "hello", true},
		{"hello", "world", false},

		// Wildcard at end
		{"git *", "git status", true},
		{"git *", "git diff --cached", true},
		{"git *", "git", false},
		{"git*", "git", true},
		{"git*", "git status", true},

		// Wildcard in middle
		{"cd * && git *", "cd /tmp && git status", true},
		{"cd * && git *", "cd /home/user/project && git diff", true},
		{"cd * && git *", "git status", false},

		// Multiple wildcards
		{"*/*", "src/main.go", true},
		{"*.*", "file.txt", true},

		// Spaces in match
		{"Bash(git *)", "Bash(git status)", true},
		{"git *", "git commit -m 'message with spaces'", true},

		// Empty pattern
		{"*", "", true},
		{"*", "anything", true},
		{"", "", true},
		{"", "something", false},

		// Question mark
		{"file?.txt", "file1.txt", true},
		{"file?.txt", "fileA.txt", true},
		{"file?.txt", "file12.txt", false},

		// Real-world patterns
		{"muxcode-agent-bus *", "muxcode-agent-bus send build build \"Run tests\"", true},
		{"./build.sh*", "./build.sh", true},
		{"./build.sh*", "./build.sh --verbose", true},
		{"gh *", "gh pr create --title test", true},
	}

	for _, tt := range tests {
		got := globMatch(tt.pattern, tt.text)
		if got != tt.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pattern, tt.text, got, tt.want)
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
		{"make build", false},
	}

	for _, tt := range tests {
		got := IsToolAllowed("bash", tt.command, patterns)
		if got != tt.want {
			t.Errorf("IsToolAllowed(bash, %q) = %v, want %v", tt.command, got, tt.want)
		}
	}
}

func TestIsToolAllowed_NonBash(t *testing.T) {
	patterns := []string{
		"Bash(git *)",
		"Read",
		"Glob",
		"Grep",
	}

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

func TestIsToolAllowed_WriteEdit(t *testing.T) {
	patterns := []string{
		"Bash(git *)",
		"Read",
		"Write",
		"Edit",
	}

	if !IsToolAllowed("write_file", "", patterns) {
		t.Error("write_file should be allowed when Write in patterns")
	}
	if !IsToolAllowed("edit_file", "", patterns) {
		t.Error("edit_file should be allowed when Edit in patterns")
	}
}

func TestIsToolAllowed_UnknownTool(t *testing.T) {
	patterns := []string{"Read", "Bash(git *)"}
	if IsToolAllowed("unknown_tool", "", patterns) {
		t.Error("unknown tool should not be allowed")
	}
}

func TestHasToolPattern(t *testing.T) {
	patterns := []string{
		"Bash(git *)",
		"Bash(gh *)",
		"Read",
		"Glob",
	}

	if !hasToolPattern(patterns, "Bash") {
		t.Error("should find Bash in Bash(*) patterns")
	}
	if !hasToolPattern(patterns, "Read") {
		t.Error("should find exact Read match")
	}
	if !hasToolPattern(patterns, "Glob") {
		t.Error("should find exact Glob match")
	}
	if hasToolPattern(patterns, "Write") {
		t.Error("should not find Write")
	}
	if hasToolPattern(patterns, "Grep") {
		t.Error("should not find Grep")
	}
}

func TestBuildToolDefs_GitRole(t *testing.T) {
	// Save and restore config singleton
	oldCfg := configSingleton
	defer func() { configSingleton = oldCfg }()

	SetConfig(DefaultConfig())

	defs := BuildToolDefs("git")
	if len(defs) == 0 {
		t.Fatal("expected tool defs for git role")
	}

	// Git role should have bash, read, glob, grep, write, edit
	names := make(map[string]bool)
	for _, d := range defs {
		names[d.Function.Name] = true
	}

	for _, want := range []string{"bash", "read_file", "glob", "grep", "write_file", "edit_file"} {
		if !names[want] {
			t.Errorf("git role missing tool %q", want)
		}
	}
}

func TestBuildToolDefs_UnknownRole(t *testing.T) {
	oldCfg := configSingleton
	defer func() { configSingleton = oldCfg }()

	SetConfig(DefaultConfig())

	defs := BuildToolDefs("nonexistent-role")
	if len(defs) != 0 {
		t.Errorf("expected no tool defs for unknown role, got %d", len(defs))
	}
}

func TestIsBashAllowed_CdPrefix(t *testing.T) {
	patterns := []string{
		"Bash(git *)",
		"Bash(cd * && git *)",
	}

	if !isBashAllowed("git status", patterns) {
		t.Error("git status should be allowed")
	}
	if !isBashAllowed("cd /home/user && git status", patterns) {
		t.Error("cd + git should be allowed")
	}
	if isBashAllowed("cd /tmp && rm -rf /", patterns) {
		t.Error("cd + rm should not be allowed")
	}
}
