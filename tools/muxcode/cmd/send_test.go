package cmd

import (
	"strings"
	"testing"
)

func TestRelaySuppressLimits_Default(t *testing.T) {
	t.Setenv("MUXCODE_RELAY_SUPPRESS_THRESHOLD", "")
	t.Setenv("MUXCODE_RELAY_SUPPRESS_WINDOW", "")
	threshold, window := relaySuppressLimits()
	if threshold != 4 {
		t.Errorf("default threshold = %d, want 4", threshold)
	}
	if window != 300 {
		t.Errorf("default window = %d, want 300", window)
	}
}

func TestRelaySuppressLimits_EnvOverride(t *testing.T) {
	t.Setenv("MUXCODE_RELAY_SUPPRESS_THRESHOLD", "7")
	t.Setenv("MUXCODE_RELAY_SUPPRESS_WINDOW", "120")
	threshold, window := relaySuppressLimits()
	if threshold != 7 {
		t.Errorf("threshold = %d, want 7", threshold)
	}
	if window != 120 {
		t.Errorf("window = %d, want 120", window)
	}
}

func TestRelaySuppressLimits_ZeroDisables(t *testing.T) {
	t.Setenv("MUXCODE_RELAY_SUPPRESS_THRESHOLD", "0")
	threshold, _ := relaySuppressLimits()
	if threshold != 0 {
		t.Errorf("threshold = %d, want 0 (disabled)", threshold)
	}
}

func TestValidatePayload_Clean(t *testing.T) {
	warnings := validatePayload("Build succeeded: all tests pass")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for clean payload, got %v", warnings)
	}
}

func TestValidatePayload_Newlines(t *testing.T) {
	warnings := validatePayload("line1\nline2")
	if len(warnings) == 0 {
		t.Fatal("expected warning for newlines")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "newlines") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected newline warning, got %v", warnings)
	}
}

func TestValidatePayload_TooLong(t *testing.T) {
	long := strings.Repeat("x", 501)
	warnings := validatePayload(long)
	if len(warnings) == 0 {
		t.Fatal("expected warning for long payload")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, ">500") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected length warning, got %v", warnings)
	}
}

func TestValidatePayload_BothIssues(t *testing.T) {
	long := strings.Repeat("x", 250) + "\n" + strings.Repeat("y", 251)
	warnings := validatePayload(long)
	if len(warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestValidatePayload_ExactlyAtLimit(t *testing.T) {
	exact := strings.Repeat("x", 500)
	warnings := validatePayload(exact)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for exactly 500 chars, got %v", warnings)
	}
}
