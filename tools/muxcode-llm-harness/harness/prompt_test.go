package harness

import (
	"strings"
	"testing"
)

func TestLocalLLMInstructions_ContainsInboxWarning(t *testing.T) {
	result := LocalLLMInstructions("commit")
	if !strings.Contains(result, "do NOT run") {
		t.Error("should contain inbox warning")
	}
	if !strings.Contains(result, "muxcode-agent-bus inbox") {
		t.Error("should mention inbox command")
	}
}

func TestLocalLLMInstructions_ContainsRole(t *testing.T) {
	result := LocalLLMInstructions("commit")
	if !strings.Contains(result, "commit") {
		t.Error("should contain role name")
	}
}

func TestRoleExamples_GitCommit(t *testing.T) {
	result := RoleExamples("commit")
	if result == "" {
		t.Fatal("commit role should have examples")
	}
	if !strings.Contains(result, "git status") {
		t.Error("should contain git status example")
	}
	if !strings.Contains(result, "git commit") {
		t.Error("should contain git commit example")
	}
	if !strings.Contains(result, "git push") {
		t.Error("should contain git push example")
	}
}

func TestRoleExamples_Git(t *testing.T) {
	result := RoleExamples("git")
	if result == "" {
		t.Fatal("git role should have examples")
	}
}

func TestRoleExamples_Build(t *testing.T) {
	result := RoleExamples("build")
	if result == "" {
		t.Fatal("build role should have examples")
	}
	if !strings.Contains(result, "./build.sh") {
		t.Error("should contain build command")
	}
}

func TestRoleExamples_Build_FullSequence(t *testing.T) {
	result := RoleExamples("build")
	if result == "" {
		t.Fatal("build role should have examples")
	}

	// Step 1: detect project type
	if !strings.Contains(result, "go.mod") {
		t.Error("should contain go.mod detection")
	}
	if !strings.Contains(result, "package.json") {
		t.Error("should contain package.json detection")
	}

	// Step 2: lint (multi-language)
	if !strings.Contains(result, "gofmt") {
		t.Error("should contain gofmt lint example")
	}
	if !strings.Contains(result, "go vet") {
		t.Error("should contain go vet lint example")
	}
	if !strings.Contains(result, "npm run lint") {
		t.Error("should contain npm lint example")
	}
	if !strings.Contains(result, "cargo clippy") {
		t.Error("should contain cargo clippy example")
	}

	// Step 3: build with fallbacks
	if !strings.Contains(result, "./build.sh") {
		t.Error("should contain build.sh command")
	}
	if !strings.Contains(result, "make build") {
		t.Error("should contain make build fallback")
	}

	// Step 4: auto-reply summary (not bus send)
	if !strings.Contains(result, "sent automatically") {
		t.Error("should explain auto-reply mechanism")
	}
	if strings.Contains(result, "--type response") {
		t.Error("should NOT contain --type response (harness handles reply)")
	}
	if strings.Contains(result, "--reply-to") {
		t.Error("should NOT contain --reply-to (harness handles reply)")
	}

	// Chaining warning
	if !strings.Contains(result, "Do NOT send to test") {
		t.Error("should warn not to send to test agent")
	}
}

func TestLocalLLMInstructions_BuildOverride(t *testing.T) {
	result := LocalLLMInstructions("build")

	// Should still have generic rules
	if !strings.Contains(result, "How You Work") {
		t.Error("should contain generic LLM instructions")
	}
	if !strings.Contains(result, "NEVER run") {
		t.Error("should contain inbox warning")
	}

	// Should have build-specific override
	if !strings.Contains(result, "Build Agent Override") {
		t.Error("should contain build agent override section")
	}
	if !strings.Contains(result, "skip that step entirely") {
		t.Error("should tell LLM to skip inbox step")
	}
	if !strings.Contains(result, "Start directly with the build sequence") {
		t.Error("should tell LLM to start with the build sequence")
	}
	if !strings.Contains(result, "do NOT call muxcode-agent-bus send to reply") {
		t.Error("should tell LLM not to send reply via bus")
	}
}

func TestLocalLLMInstructions_NonBuildNoOverride(t *testing.T) {
	for _, role := range []string{"commit", "test", "deploy", "review", "edit"} {
		result := LocalLLMInstructions(role)
		if strings.Contains(result, "Build Agent Override") {
			t.Errorf("role %q should NOT contain build override", role)
		}
	}
}

func TestRoleExamples_Test(t *testing.T) {
	result := RoleExamples("test")
	if result == "" {
		t.Fatal("test role should have examples")
	}
	if !strings.Contains(result, "./test.sh") {
		t.Error("should contain test command")
	}
}

func TestRoleExamples_UnknownRole(t *testing.T) {
	result := RoleExamples("unknown")
	if result != "" {
		t.Errorf("unknown role should return empty, got %q", result)
	}
}

func TestBuildSystemPrompt_Assembly(t *testing.T) {
	prompt := BuildSystemPrompt("commit", "Agent def here", "Skills here", "Context here")
	if !strings.Contains(prompt, "Agent def here") {
		t.Error("should contain agent definition")
	}
	if !strings.Contains(prompt, "How You Work") {
		t.Error("should contain LLM instructions")
	}
	if !strings.Contains(prompt, "Git Examples") {
		t.Error("should contain role examples")
	}
	if !strings.Contains(prompt, "Skills here") {
		t.Error("should contain skills")
	}
	if !strings.Contains(prompt, "Context here") {
		t.Error("should contain context")
	}
}

func TestBuildSystemPrompt_EmptyParts(t *testing.T) {
	prompt := BuildSystemPrompt("commit", "", "", "")
	if !strings.Contains(prompt, "How You Work") {
		t.Error("should always contain LLM instructions")
	}
	if strings.Contains(prompt, "Skills") {
		// Should not have explicit "Skills" header from empty input
	}
}

func TestStripFrontmatter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"no frontmatter",
			"Just content",
			"Just content",
		},
		{
			"with frontmatter",
			"---\ndescription: test\n---\nBody content",
			"Body content",
		},
		{
			"with frontmatter blank line",
			"---\ndescription: test\n---\n\nBody content",
			"\nBody content",
		},
		{
			"incomplete frontmatter",
			"---\nno closing delimiter",
			"---\nno closing delimiter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripFrontmatter(tt.input)
			if got != tt.want {
				t.Errorf("StripFrontmatter = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAgentFileName(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{"edit", "code-editor"},
		{"build", "code-builder"},
		{"test", "test-runner"},
		{"commit", "git-manager"},
		{"git", "git-manager"},
		{"review", "code-reviewer"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		got := AgentFileName(tt.role)
		if got != tt.want {
			t.Errorf("AgentFileName(%q) = %q, want %q", tt.role, got, tt.want)
		}
	}
}
