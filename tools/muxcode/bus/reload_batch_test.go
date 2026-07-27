package bus

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAbbreviateModel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"claude-sonnet-5", "sonnet-5"},
		{"claude-opus-5", "opus-5"},
		{"claude-haiku-4-5", "haiku-4-5"},
		{"opencode-go/minimax-m2.5", "minimax-m2.5"},
		{"opencode-go/deepseek-v4-pro", "deepseek-v4-pro"},
		{"gpt-5.5", "gpt-5.5"},
		{"gpt-5.4-mini", "gpt-5.4-mini"},
		{"", ""},
		{"custom-model", "custom-model"},
		{"org/sub/deep-model", "deep-model"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := AbbreviateModel(tt.input)
			if got != tt.want {
				t.Errorf("AbbreviateModel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReloadableRoles(t *testing.T) {
	roles := ReloadableRoles()

	// Should not contain hosted roles
	for _, r := range roles {
		if r == "docs" || r == "pr-read" {
			t.Errorf("ReloadableRoles() contains hosted role %q", r)
		}
		if r == "webhook" || r == "api" {
			t.Errorf("ReloadableRoles() contains non-agent role %q", r)
		}
	}

	// Should contain key agent roles
	has := make(map[string]bool)
	for _, r := range roles {
		has[r] = true
	}
	for _, want := range []string{"plan", "edit", "build", "test", "review", "deploy", "run", "commit", "auto", "research", "serve"} {
		if !has[want] {
			t.Errorf("ReloadableRoles() missing expected role %q", want)
		}
	}
}

func TestFormatReloadResults(t *testing.T) {
	results := []ReloadResult{
		{Role: "build", Success: true, OldCLI: "claude", NewCLI: "opencode", NewModel: "opencode-go/minimax-m2.5", Duration: 3 * time.Second},
		{Role: "test", Success: true, OldCLI: "claude", NewCLI: "opencode", NewModel: "opencode-go/minimax-m2.5", Duration: 4 * time.Second},
		{Role: "review", Success: false, Error: fmt.Errorf("agent review did not exit"), OldCLI: "claude"},
	}
	out := FormatReloadResults(results)
	if !strings.Contains(out, "✓ build") {
		t.Errorf("expected success line for build, got:\n%s", out)
	}
	if !strings.Contains(out, "✗ review") {
		t.Errorf("expected failure line for review, got:\n%s", out)
	}
	if !strings.Contains(out, "2/3") {
		t.Errorf("expected 2/3 summary, got:\n%s", out)
	}
}
