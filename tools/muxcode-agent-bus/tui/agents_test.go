package tui

import (
	"strings"
	"testing"
)

func TestDetectStatus_Empty(t *testing.T) {
	st, _ := DetectStatus("edit", "", "")
	if st.Status != "IDLE" {
		t.Errorf("Status = %q, want IDLE", st.Status)
	}
}

func TestDetectStatus_Whitespace(t *testing.T) {
	st, _ := DetectStatus("edit", "  \n  \n  ", "")
	if st.Status != "IDLE" {
		t.Errorf("Status = %q, want IDLE", st.Status)
	}
}

func TestDetectStatus_Error(t *testing.T) {
	tests := []string{
		"Error: something failed\nmore output",
		"FATAL crash\n",
		"command not found: foo\n",
		"exit code 1\n",
	}
	for _, output := range tests {
		st, _ := DetectStatus("build", output, "somehash")
		if st.Status != "ERROR" {
			t.Errorf("output %q → Status = %q, want ERROR", output, st.Status)
		}
	}
}

func TestDetectStatus_Active(t *testing.T) {
	output := "compiling...\nstep 2\n"
	st, _ := DetectStatus("build", output, "different-hash")
	if st.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE", st.Status)
	}
}

func TestDetectStatus_Ready(t *testing.T) {
	output := "done\n$ "
	// Use the same hash to avoid ACTIVE
	_, hash := DetectStatus("build", output, "")
	st, _ := DetectStatus("build", output, hash)
	if st.Status != "READY" {
		t.Errorf("Status = %q, want READY", st.Status)
	}
}

func TestDetectStatus_SnippetTruncation(t *testing.T) {
	long := strings.Repeat("x", 80)
	output := long + "\n"
	st, _ := DetectStatus("edit", output, "")
	if len([]rune(st.Snippet)) > 50 {
		t.Errorf("snippet length = %d, want <= 50", len([]rune(st.Snippet)))
	}
}

func TestExtractCost(t *testing.T) {
	got := ExtractCost("Total cost: $1.23 for this run")
	if got != "1.23" {
		t.Errorf("ExtractCost = %q, want %q", got, "1.23")
	}
}

func TestExtractCost_NoMatch(t *testing.T) {
	got := ExtractCost("no cost here")
	if got != "" {
		t.Errorf("ExtractCost = %q, want empty", got)
	}
}

func TestExtractCost_Multiple(t *testing.T) {
	got := ExtractCost("Cost: $0.50\nUpdated cost: $1.75\n")
	if got != "1.75" {
		t.Errorf("ExtractCost = %q, want %q", got, "1.75")
	}
}

func TestExtractTokens_CompactFormat(t *testing.T) {
	got := ExtractTokens("used 12.3k tokens")
	if got != "12.3k" {
		t.Errorf("ExtractTokens = %q, want %q", got, "12.3k")
	}
}

func TestExtractTokens_RawFormat(t *testing.T) {
	got := ExtractTokens("consumed 1,234 tokens")
	if got != "1.2k" {
		t.Errorf("ExtractTokens = %q, want %q", got, "1.2k")
	}
}

func TestExtractTokens_NoMatch(t *testing.T) {
	got := ExtractTokens("nothing here")
	if got != "" {
		t.Errorf("ExtractTokens = %q, want empty", got)
	}
}

func TestRawToCompact(t *testing.T) {
	tests := []struct {
		raw  int
		want string
	}{
		{12300, "12.3k"},
		{500, "500"},
		{1000, "1.0k"},
		{0, "0"},
	}
	for _, tt := range tests {
		got := RawToCompact(tt.raw)
		if got != tt.want {
			t.Errorf("RawToCompact(%d) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestTokensToRaw(t *testing.T) {
	tests := []struct {
		compact string
		want    int
	}{
		{"12.3k", 12300},
		{"500", 500},
		{"1k", 1000},
		{"bad", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := TokensToRaw(tt.compact)
		if got != tt.want {
			t.Errorf("TokensToRaw(%q) = %d, want %d", tt.compact, got, tt.want)
		}
	}
}

func TestPaneTarget(t *testing.T) {
	tests := []struct {
		window string
		want   string
	}{
		{"edit", "mysess:edit.1"},
		{"analyze", "mysess:analyze.1"},
		{"commit", "mysess:commit.1"},
		{"build", "mysess:build.0"},
		{"test", "mysess:test.0"},
		{"deploy", "mysess:deploy.0"},
	}
	for _, tt := range tests {
		got := PaneTarget("mysess", tt.window)
		if got != tt.want {
			t.Errorf("PaneTarget(%q, %q) = %q, want %q", "mysess", tt.window, got, tt.want)
		}
	}
}
