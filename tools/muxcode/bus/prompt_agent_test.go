package bus

import (
	"strings"
	"testing"
)

// TestPromptRoleKnown pins the role registration: membership in KnownRoles
// is what gives prompt its inbox at Init and its transcript truncation on
// session re-init (purgeStaleFiles iterates KnownRoles over HistoryPath).
func TestPromptRoleKnown(t *testing.T) {
	if !IsKnownRole("prompt") {
		t.Error("prompt must be a known role")
	}
}

func TestPromptHistoryPath(t *testing.T) {
	p := PromptHistoryPath("mysess")
	if !strings.HasSuffix(p, "prompt-history.jsonl") {
		t.Errorf("unexpected transcript path %q", p)
	}
	if p != HistoryPath("mysess", "prompt") {
		t.Error("transcript path must match the generic role history path the harness writes to")
	}
}

func TestPromptAgentEnabled(t *testing.T) {
	t.Setenv("MUXCODE_PROMPT_AGENT_DISABLE", "")
	if !PromptAgentEnabled() {
		t.Error("prompt-agent supervision should default on")
	}
	t.Setenv("MUXCODE_PROMPT_AGENT_DISABLE", "1")
	if PromptAgentEnabled() {
		t.Error("MUXCODE_PROMPT_AGENT_DISABLE=1 must disable supervision")
	}
}

// TestRoleModel_PromptDefaultArm confirms the spec expectation that the
// prompt role needs no code change for model resolution: roleModelEnvVar's
// default arm yields MUXCODE_PROMPT_MODEL, falling back to the global
// default when unset (the one-model-resident decision keeps it unset).
func TestRoleModel_PromptDefaultArm(t *testing.T) {
	t.Setenv("MUXCODE_OLLAMA_MODEL", "")
	t.Setenv("MUXCODE_PROMPT_MODEL", "custom:1b")
	if model := RoleModel("prompt"); model != "custom:1b" {
		t.Errorf("RoleModel(prompt) = %q, want custom:1b", model)
	}
	t.Setenv("MUXCODE_PROMPT_MODEL", "")
	if model := RoleModel("prompt"); model != "qwen3:4b" {
		t.Errorf("RoleModel(prompt) = %q, want global default qwen3:4b", model)
	}
}

// TestRoleModel_SkipsOpenCodeCatalogPin pins the overload guard:
// MUXCODE_{ROLE}_MODEL doubles as OpenCode's catalog pin (provider/model
// form), and this project's own config pins roles that way — flipping one
// to CLI=local must fall back to the Ollama default, never hand Ollama an
// opencode-go/* name it cannot pull.
func TestRoleModel_SkipsOpenCodeCatalogPin(t *testing.T) {
	t.Setenv("MUXCODE_OLLAMA_MODEL", "")
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
