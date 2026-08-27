package harness

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNoThink pins the MUX-109 latency lever end to end at the unit
// level: env opt-in reaches the config, the request marshals an explicit
// think:false (never omitted, never true), and the system prompt carries
// qwen3's /no_think soft switch — with the off state as negative control.
func TestNoThink(t *testing.T) {
	t.Setenv("MUXCODE_HARNESS_NOTHINK", "1")
	if !DefaultConfig().NoThink {
		t.Error("MUXCODE_HARNESS_NOTHINK=1 must enable NoThink")
	}
	t.Setenv("MUXCODE_HARNESS_NOTHINK", "")
	if DefaultConfig().NoThink {
		t.Error("NoThink must default off")
	}

	f := false
	data, err := json.Marshal(ChatRequest{Model: "m", Think: &f})
	if err != nil || !strings.Contains(string(data), `"think":false`) {
		t.Errorf("think:false must marshal explicitly, got %s (%v)", data, err)
	}
	data, _ = json.Marshal(ChatRequest{Model: "m"})
	if strings.Contains(string(data), "think") {
		t.Errorf("unset Think must be omitted, got %s", data)
	}

	if got := applyNoThink("PROMPT", true); !strings.HasSuffix(got, "/no_think") {
		t.Errorf("no-think prompt must end with the soft switch, got %q", got)
	}
	if got := applyNoThink("PROMPT", false); got != "PROMPT" {
		t.Errorf("thinking-on prompt must be untouched, got %q", got)
	}
}

// TestRoleModel_SkipsOpenCodeCatalogPin mirrors the bus-side guard:
// MUXCODE_{ROLE}_MODEL doubles as OpenCode's catalog pin (provider/model
// form), so a role flipped to CLI=local must not hand Ollama an
// opencode-go/* name — catalog-form pins fall through to the default.
func TestRoleModel_SkipsOpenCodeCatalogPin(t *testing.T) {
	t.Setenv("MUXCODE_OLLAMA_MODEL", "")
	t.Setenv("MUXCODE_OLLAMA_URL", "") // guard applies on the local default endpoint
	t.Setenv("MUXCODE_BUILD_MODEL", "opencode-go/minimax-m2.5")
	if model := RoleModel("build"); model != "qwen3:4b" {
		t.Errorf("RoleModel(build) = %q, want qwen3:4b — catalog-form pins belong to OpenCode", model)
	}
	// Negative control: a plain Ollama pin still applies.
	t.Setenv("MUXCODE_BUILD_MODEL", "qwen3:8b")
	if model := RoleModel("build"); model != "qwen3:8b" {
		t.Errorf("RoleModel(build) = %q, want qwen3:8b — plain pins must still win", model)
	}
}
