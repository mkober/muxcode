package harness

import "testing"

// TestRoleModel_SkipsOpenCodeCatalogPin mirrors the bus-side guard:
// MUXCODE_{ROLE}_MODEL doubles as OpenCode's catalog pin (provider/model
// form), so a role flipped to CLI=local must not hand Ollama an
// opencode-go/* name — catalog-form pins fall through to the default.
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
