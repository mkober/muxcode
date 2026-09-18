package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePayload(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(path, []byte(`{"fields":{"description":{"type":"doc"}}}`), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

func TestParseJiraCreateArgs_PositionalsAndFlags(t *testing.T) {
	payload := writePayload(t)

	opts, err := parseJiraCreateArgs([]string{
		"PROMGT", "Bug", "a defect", payload,
		"--assignee", "me", "--priority", "High",
		"--sprint", "current", "--board", "PKH Build",
		"--label", "one", "--label", "two",
	})
	if err != nil {
		t.Fatalf("valid args must parse, got %v", err)
	}
	if opts.Project != "PROMGT" || opts.IssueType != "Bug" || opts.Summary != "a defect" {
		t.Errorf("positionals mis-parsed: %+v", opts)
	}
	if opts.Assignee != "me" || opts.Priority != "High" || opts.Sprint != "current" || opts.Board != "PKH Build" {
		t.Errorf("flags mis-parsed: %+v", opts)
	}
	if len(opts.Labels) != 2 {
		t.Errorf("--label must accumulate, got %v", opts.Labels)
	}
	if opts.DryRun {
		t.Error("--dry-run was not passed, so it must be false")
	}
	if !strings.Contains(string(opts.Payload), "description") {
		t.Error("the payload file must be read")
	}
}

// The negative control for the flag above: a summary that looks like a flag
// value must not be swallowed into the positionals.
func TestParseJiraCreateArgs_DryRunIsAFlagNotAPositional(t *testing.T) {
	payload := writePayload(t)

	opts, err := parseJiraCreateArgs([]string{"PROMGT", "Bug", "a defect", payload, "--dry-run"})
	if err != nil {
		t.Fatalf("--dry-run must parse, got %v", err)
	}
	if !opts.DryRun {
		t.Error("--dry-run must set DryRun")
	}
	if opts.Summary != "a defect" {
		t.Errorf("--dry-run must not shift the positionals, got summary %q", opts.Summary)
	}
}

func TestParseJiraCreateArgs_MissingPositionalsFail(t *testing.T) {
	if _, err := parseJiraCreateArgs([]string{"PROMGT", "Bug", "a defect"}); err == nil {
		t.Fatal("three positionals must fail: the payload file is required")
	}
}

func TestParseJiraCreateArgs_FlagWithoutValueFails(t *testing.T) {
	payload := writePayload(t)

	if _, err := parseJiraCreateArgs([]string{"PROMGT", "Bug", "s", payload, "--assignee"}); err == nil {
		t.Fatal("a trailing flag with no value must fail rather than silently drop")
	}
}

// `--board --dry-run` must not read the dry run as the board's value: that
// combination silently creates a real issue for someone who asked for a
// rehearsal, and --board is ignored without --sprint so nothing else complains.
func TestParseJiraCreateArgs_FlagDoesNotSwallowNextOption(t *testing.T) {
	payload := writePayload(t)

	_, err := parseJiraCreateArgs([]string{"PROMGT", "Bug", "s", payload, "--board", "--dry-run"})
	if err == nil {
		t.Fatal("an option token is a missing value, not a value")
	}
	if !strings.Contains(err.Error(), "--board") {
		t.Errorf("the error must name the flag missing its value, got %v", err)
	}
}

func TestParseJiraCreateArgs_UnknownFlagFails(t *testing.T) {
	payload := writePayload(t)

	if _, err := parseJiraCreateArgs([]string{"PROMGT", "Bug", "s", payload, "--assinee", "me"}); err == nil {
		t.Fatal("a misspelled flag must fail, not be treated as a positional")
	}
}

func TestParseJiraCreateArgs_MissingPayloadFileFails(t *testing.T) {
	if _, err := parseJiraCreateArgs([]string{"PROMGT", "Bug", "s", "/nonexistent/payload.json"}); err == nil {
		t.Fatal("an unreadable payload must fail before any API call")
	}
}
