package bus

import (
	"os"
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

// TestPromptBackend pins backend selection and the per-backend child
// environment (MUX-109: the user switched the prompt-agent to OpenCode's
// Zen gateway while keeping the muxcode-native harness surface).
func TestPromptBackend(t *testing.T) {
	t.Setenv("MUXCODE_CONFIG", "/dev/null") // hermetic: ignore the user's real config
	t.Setenv("HOME", t.TempDir())           // and the home-config fallback
	t.Setenv("MUXCODE_PROMPT_BACKEND", "")
	if PromptBackend("") != "opencode" {
		t.Error("backend must default to opencode (user-flipped 2026-08-27)")
	}
	t.Setenv("MUXCODE_PROMPT_BACKEND", "ollama")
	if PromptBackend("") != "ollama" {
		t.Error("MUXCODE_PROMPT_BACKEND=ollama must select local Ollama")
	}
	t.Setenv("MUXCODE_PROMPT_BACKEND", "bogus")
	if PromptBackend("") != "opencode" {
		t.Error("an unknown backend must degrade to the default")
	}
}

// TestPromptBackendInfo pins the display helper to the launch-time
// resolution: what the footer names is what StartPromptAgent runs.
func TestPromptBackendInfo(t *testing.T) {
	t.Setenv("MUXCODE_CONFIG", "/dev/null") // hermetic: ignore the user's real config
	t.Setenv("HOME", t.TempDir())           // and the home-config fallback
	t.Setenv("MUXCODE_PROMPT_BACKEND", "")
	t.Setenv("MUXCODE_PROMPT_MODEL", "")

	if b, m := PromptBackendInfo(""); b != "opencode" || m != promptBackendDefaultModel {
		t.Errorf("default = %s/%s, want opencode/%s", b, m, promptBackendDefaultModel)
	}

	t.Setenv("MUXCODE_PROMPT_MODEL", "deepseek-v4-flash-free")
	if _, m := PromptBackendInfo(""); m != "deepseek-v4-flash-free" {
		t.Errorf("MUXCODE_PROMPT_MODEL override ignored, got %s", m)
	}

	t.Setenv("MUXCODE_PROMPT_BACKEND", "ollama")
	t.Setenv("MUXCODE_PROMPT_MODEL", "qwen3:8b")
	if b, m := PromptBackendInfo(""); b != "ollama" || m != "qwen3:8b" {
		t.Errorf("ollama = %s/%s, want ollama/qwen3:8b (RoleModel resolution)", b, m)
	}
}

// TestPromptSettingRuntimeOverride pins the selector's write channel:
// a session runtime override outranks env, and PromptBackendInfo sees
// the switched backend/model (what the daemon respawn will launch).
func TestPromptSettingRuntimeOverride(t *testing.T) {
	t.Setenv("MUXCODE_CONFIG", "/dev/null")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MUXCODE_PROMPT_BACKEND", "opencode") // the override must beat this
	t.Setenv("MUXCODE_PROMPT_MODEL", "")

	session := "prompt-override-test"
	t.Cleanup(func() { _ = os.RemoveAll(BusDir(session)) })
	if err := WriteRuntimeOverride(session, "prompt", "MUXCODE_PROMPT_BACKEND", "ollama"); err != nil {
		t.Fatal(err)
	}
	if err := WriteRuntimeOverride(session, "prompt", "MUXCODE_PROMPT_MODEL", "qwen3:8b"); err != nil {
		t.Fatal(err)
	}

	if b, m := PromptBackendInfo(session); b != "ollama" || m != "qwen3:8b" {
		t.Errorf("override = %s/%s, want ollama/qwen3:8b — the selector's switch must win over env", b, m)
	}
	if b, _ := PromptBackendInfo("no-such-session"); b != "opencode" {
		t.Error("a session without overrides must fall back to env (negative control)")
	}

	// An empty model override CLEARS the pin — a model-less reload must
	// resolve the backend default, not a prior reload's model.
	if err := WriteRuntimeOverride(session, "prompt", "MUXCODE_PROMPT_MODEL", ""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUXCODE_PROMPT_BACKEND", "")
	if err := WriteRuntimeOverride(session, "prompt", "MUXCODE_PROMPT_BACKEND", "opencode"); err != nil {
		t.Fatal(err)
	}
	if _, m := PromptBackendInfo(session); m != promptBackendDefaultModel {
		t.Errorf("cleared model override must fall back to the backend default, got %s", m)
	}
}

// TestPromptReloadTarget pins the selector→backend mapping: catalog ids
// are stripped to the Zen gateway's bare namespace, local maps to
// ollama, and providers with no harness endpoint are refused.
func TestPromptReloadTarget(t *testing.T) {
	if b, m, err := promptReloadTarget("opencode", "opencode-go/deepseek-v4-flash"); err != nil || b != "opencode" || m != "deepseek-v4-flash" {
		t.Errorf("catalog id must strip to the bare Zen form, got %s/%s err=%v", b, m, err)
	}
	if b, m, err := promptReloadTarget("local", "qwen3:8b"); err != nil || b != "ollama" || m != "qwen3:8b" {
		t.Errorf("local must map to ollama verbatim, got %s/%s err=%v", b, m, err)
	}
	if _, _, err := promptReloadTarget("claude", "claude-sonnet-5"); err == nil {
		t.Error("claude has no headless harness endpoint — must refuse")
	}
}

func envHas(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestPromptAgentEnv(t *testing.T) {
	t.Setenv("MUXCODE_CONFIG", "/dev/null") // hermetic: ignore the user's real config
	t.Setenv("HOME", t.TempDir())           // and the home-config fallback
	// Ollama backend (now the explicit opt-in): thinking suppressed, no
	// gateway vars.
	t.Setenv("MUXCODE_PROMPT_BACKEND", "ollama")
	t.Setenv("MUXCODE_PROMPT_MODEL", "")
	env := promptAgentEnv("s")
	if !envHas(env, "MUXCODE_HARNESS_NOTHINK=1") {
		t.Error("ollama backend must suppress thinking")
	}
	for _, e := range env {
		if strings.HasPrefix(e, "MUXCODE_HARNESS_API_KEY=") {
			t.Error("ollama backend must not carry a gateway key")
		}
	}

	// Opencode backend: gateway URL + default model + bearer key, and NO
	// think suppression (/no_think is a qwen-family switch).
	t.Setenv("MUXCODE_PROMPT_BACKEND", "opencode")
	t.Setenv("MUXCODE_OPENCODE_API_KEY", "sk-test-123")
	env = promptAgentEnv("s")
	if !envHas(env, "MUXCODE_OLLAMA_URL="+opencodeGatewayURL) {
		t.Error("opencode backend must point the harness at the Zen gateway")
	}
	if !envHas(env, "MUXCODE_PROMPT_MODEL="+promptBackendDefaultModel) {
		t.Error("opencode backend must default the model to DeepSeek V4 Flash")
	}
	if !envHas(env, "MUXCODE_HARNESS_API_KEY=sk-test-123") {
		t.Error("opencode backend must hand the harness the bearer key")
	}
	if envHas(env, "MUXCODE_HARNESS_NOTHINK=1") {
		t.Error("opencode backend must not plant the qwen no-think switch")
	}

	// An explicit model pin wins over the backend default.
	t.Setenv("MUXCODE_PROMPT_MODEL", "opencode-go/other-model")
	env = promptAgentEnv("s")
	if envHas(env, "MUXCODE_PROMPT_MODEL="+promptBackendDefaultModel) {
		t.Error("an explicit MUXCODE_PROMPT_MODEL must not be overridden")
	}
}

// The catalog-pin guard stands down for remote gateways: provider/model
// IS the correct name there (and stays up for local Ollama).
func TestRoleModel_CatalogPinAllowedOnRemoteGateway(t *testing.T) {
	t.Setenv("MUXCODE_OLLAMA_MODEL", "")
	t.Setenv("MUXCODE_PROMPT_MODEL", "opencode-go/deepseek-v4-flash")

	t.Setenv("MUXCODE_OLLAMA_URL", "")
	if model := RoleModel("prompt"); model != "qwen3:4b" {
		t.Errorf("local endpoint must still skip catalog pins, got %q", model)
	}
	t.Setenv("MUXCODE_OLLAMA_URL", "https://opencode.ai/zen/v1")
	if model := RoleModel("prompt"); model != "opencode-go/deepseek-v4-flash" {
		t.Errorf("remote gateway must accept the catalog pin, got %q", model)
	}
	// localhost with a custom port is still local.
	t.Setenv("MUXCODE_OLLAMA_URL", "http://localhost:11435")
	if model := RoleModel("prompt"); model != "qwen3:4b" {
		t.Errorf("localhost endpoints must keep the guard up, got %q", model)
	}
}

// TestRoleModel_SkipsOpenCodeCatalogPin pins the overload guard:
// MUXCODE_{ROLE}_MODEL doubles as OpenCode's catalog pin (provider/model
// form), and this project's own config pins roles that way — flipping one
// to CLI=local must fall back to the Ollama default, never hand Ollama an
// opencode-go/* name it cannot pull.
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
