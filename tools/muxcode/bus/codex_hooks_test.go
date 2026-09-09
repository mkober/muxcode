package bus

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The test binary opts out of codex hooks globally. With the default on, any
// test that reaches ConfigureLaunch or WriteAgentConfig for a codex role would
// otherwise run the real `codex --version` and write or clear a marker in
// whatever bus dir BUS_SESSION names — the live session's, under an agent.
// Tests that want the hook road set MUXCODE_CODEX_HOOKS=1 explicitly.
func init() {
	os.Setenv("MUXCODE_CODEX_HOOKS", "0")
}

// TestCodexHooksDefaultOn pins the rollout default: with no variable set, an
// eligible codex takes the hook road.
func TestCodexHooksDefaultOn(t *testing.T) {
	t.Setenv("MUXCODE_CODEX_HOOKS", "")
	t.Setenv("MUXCODE_BUILD_CODEX_HOOKS", "")
	if !CodexHooksOptIn("build") {
		t.Fatal("codex hooks must be on by default")
	}
	t.Setenv("MUXCODE_CODEX_HOOKS", "0")
	if CodexHooksOptIn("build") {
		t.Fatal("MUXCODE_CODEX_HOOKS=0 must opt out")
	}
}

// codexHooksTestEnv isolates a test from the machine: a temp cwd (hooks.json
// is cwd-relative), temp bus dir, temp CODEX_HOME and lifecycle dir, a stubbed
// `codex --version`, and the variables cleared (so the default applies).
// Returns the session name.
func codexHooksTestEnv(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	SetBusDirBase(t.TempDir())
	t.Cleanup(ResetBusDirBase)
	t.Setenv("CODEX_HOME", filepath.Join(dir, "codex-home"))
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", filepath.Join(dir, "lifecycle"))
	t.Setenv("MUXCODE_CODEX_HOOKS", "")
	t.Setenv("MUXCODE_BUILD_CODEX_HOOKS", "")
	t.Setenv("MUXCODE_TEST_CODEX_HOOKS", "")
	t.Setenv("BUS_SESSION", "codex-hooks-test")
	old := codexVersionOutput
	codexVersionOutput = func() (string, error) { return version, nil }
	t.Cleanup(func() { codexVersionOutput = old })
	return "codex-hooks-test"
}

// lifecycleText returns everything logged under the test's lifecycle dir.
func lifecycleText(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	entries, _ := filepath.Glob(filepath.Join(os.Getenv("MUXCODE_LIFECYCLE_LOG_DIR"), "*"))
	for _, p := range entries {
		data, _ := os.ReadFile(p)
		b.Write(data)
	}
	return b.String()
}

func TestCodexHooksTemplate(t *testing.T) {
	var doc struct {
		Hooks map[string][]codexHookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(CodexHooksTemplate(), &doc); err != nil {
		t.Fatalf("template is not JSON: %v", err)
	}
	want := map[string][]string{
		"PreToolUse":       {"muxcode hook guard"},
		"PostToolUse":      {"muxcode hook bash", "muxcode hook analyze"},
		"Stop":             {"muxcode hook stop"},
		"UserPromptSubmit": {"muxcode hook prompt-submit"},
	}
	if len(doc.Hooks) != len(want) {
		t.Fatalf("events = %d, want %d", len(doc.Hooks), len(want))
	}
	for ev, cmds := range want {
		var got []string
		for _, g := range doc.Hooks[ev] {
			for _, h := range g.Hooks {
				if h.Type != "command" || h.Timeout <= 0 {
					t.Errorf("%s handler %+v: want type=command and a timeout", ev, h)
				}
				got = append(got, h.Command)
			}
		}
		if strings.Join(got, ",") != strings.Join(cmds, ",") {
			t.Errorf("%s commands = %v, want %v", ev, got, cmds)
		}
	}
	// The guard must see every tool: an MCP write has to reach the Atlassian
	// authority check, not just shell and patch calls.
	pre, err := regexp.Compile(doc.Hooks["PreToolUse"][0].Matcher)
	if err != nil {
		t.Fatalf("PreToolUse matcher does not compile: %v", err)
	}
	for _, tool := range []string{"Bash", "apply_patch", "mcp__claude_ai_Atlassian__editJiraIssue"} {
		if !pre.MatchString(tool) {
			t.Errorf("PreToolUse matcher %q does not match %q", pre, tool)
		}
	}
	if doc.Hooks["PostToolUse"][0].Matcher != "Bash" || doc.Hooks["PostToolUse"][1].Matcher != "apply_patch" {
		t.Errorf("PostToolUse matchers = %q, %q", doc.Hooks["PostToolUse"][0].Matcher, doc.Hooks["PostToolUse"][1].Matcher)
	}
	if doc.Hooks["Stop"][0].Matcher != "" {
		t.Errorf("Stop should carry no matcher, got %q", doc.Hooks["Stop"][0].Matcher)
	}
	if string(CodexHooksTemplate()) != string(CodexHooksTemplate()) {
		t.Error("template must render deterministically — its hash is the trust gate")
	}
}

func TestCodexVersionAtLeast(t *testing.T) {
	cases := []struct {
		have string
		want bool
	}{
		{"codex-cli 0.153.4", true},
		{"codex-cli 0.153.0", true},
		{"codex-cli 0.152.9", false},
		{"codex-cli 1.0.0", true},
		{"codex-cli 0.9.99", false},
		{"garbage", false},
		{"", false},
	}
	for _, c := range cases {
		if got := CodexVersionAtLeast(c.have, CodexHooksMinVersion); got != c.want {
			t.Errorf("CodexVersionAtLeast(%q) = %v, want %v", c.have, got, c.want)
		}
	}
}

func TestTomlHooksFeatureDisabled(t *testing.T) {
	cases := []struct {
		toml string
		want bool
	}{
		{"[features]\nhooks = false\n", true},
		{"[features]\nhooks = true\n", false},
		{"features.hooks = false\n", true},
		{"[other]\nhooks = false\n", false},
		{"[features]\ncodex_hooks = false # legacy alias\n", true},
		{"model = \"x\"\n[features]\n# hooks = false\n", false},
		{"", false},
	}
	for _, c := range cases {
		if got := tomlHooksFeatureDisabled(c.toml); got != c.want {
			t.Errorf("tomlHooksFeatureDisabled(%q) = %v, want %v", c.toml, got, c.want)
		}
	}
}

func TestCodexHooksOptIn(t *testing.T) {
	cases := []struct {
		global, role string
		want         bool
	}{
		{"", "", codexHooksDefault},
		{"1", "", true},
		{"on", "", true},
		{"0", "", false},
		{"1", "0", false},
		{"0", "true", true},
		{"", "yes", true},
	}
	for _, c := range cases {
		t.Setenv("MUXCODE_CODEX_HOOKS", c.global)
		t.Setenv("MUXCODE_BUILD_CODEX_HOOKS", c.role)
		if got := CodexHooksOptIn("build"); got != c.want {
			t.Errorf("global=%q role=%q: OptIn = %v, want %v", c.global, c.role, got, c.want)
		}
	}
}

func TestPrepareCodexHooks_WritesFileAndMarker(t *testing.T) {
	session := codexHooksTestEnv(t, "codex-cli 0.153.4")
	t.Setenv("MUXCODE_CODEX_HOOKS", "1")

	active, err := PrepareCodexHooks(session, "build")
	if err != nil || !active {
		t.Fatalf("PrepareCodexHooks = (%v, %v), want (true, nil)", active, err)
	}
	data, err := os.ReadFile(CodexHooksPath())
	if err != nil {
		t.Fatalf("hooks.json not written: %v", err)
	}
	if string(data) != string(CodexHooksTemplate()) {
		t.Error("hooks.json is not the template")
	}
	marker, err := os.ReadFile(codexHooksMarkerPath(session, "build"))
	if err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	if strings.TrimSpace(string(marker)) != sha256Hex(data) {
		t.Errorf("marker %q != sha256 of file", strings.TrimSpace(string(marker)))
	}
	if !CodexHooksActive(session, "build") || !CodexHooksTrusted(session, "build") || CodexHooksTampered(session, "build") {
		t.Errorf("state: active=%v trusted=%v tampered=%v", CodexHooksActive(session, "build"), CodexHooksTrusted(session, "build"), CodexHooksTampered(session, "build"))
	}

	t.Setenv("MUXCODE_BUILD_CLI", "codex")
	p := ResolveProvider("build")
	if p.Name() != "codex" || !p.SupportsHooks() || p.SelfPollsInbox() || p.PaneIsEvidence() {
		t.Errorf("hook-road codex must answer hooks=yes poll=no scrape=no, got %v/%v/%v", p.SupportsHooks(), p.SelfPollsInbox(), p.PaneIsEvidence())
	}

	// Idempotent: a second call neither errors nor rewrites.
	if active, err := PrepareCodexHooks(session, "build"); err != nil || !active {
		t.Errorf("second PrepareCodexHooks = (%v, %v)", active, err)
	}
	if !strings.Contains(lifecycleText(t), "codex-hooks-enabled") {
		t.Error("expected a codex-hooks-enabled lifecycle row")
	}
}

func TestPrepareCodexHooks_OptOutClearsRoad(t *testing.T) {
	session := codexHooksTestEnv(t, "codex-cli 0.153.4")
	t.Setenv("MUXCODE_CODEX_HOOKS", "1")
	if _, err := PrepareCodexHooks(session, "build"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MUXCODE_CODEX_HOOKS", "0")
	active, err := PrepareCodexHooks(session, "build")
	if err != nil || active {
		t.Fatalf("opt-out: PrepareCodexHooks = (%v, %v), want (false, nil)", active, err)
	}
	if CodexHooksActive(session, "build") {
		t.Error("marker survived opt-out")
	}
	if _, err := os.Stat(CodexHooksPath()); !os.IsNotExist(err) {
		t.Error("muxcode's own hooks.json should be removed once no role holds a marker")
	}
	t.Setenv("MUXCODE_BUILD_CLI", "codex")
	if p := ResolveProvider("build"); p.SupportsHooks() || !p.PaneIsEvidence() {
		t.Error("opted-out codex must be back on the scrape road")
	}
}

func TestPrepareCodexHooks_OldCodexStaysOnScrapeRoad(t *testing.T) {
	session := codexHooksTestEnv(t, "codex-cli 0.150.2")
	t.Setenv("MUXCODE_CODEX_HOOKS", "1")
	active, err := PrepareCodexHooks(session, "build")
	if err != nil || active {
		t.Fatalf("old codex: PrepareCodexHooks = (%v, %v), want (false, nil)", active, err)
	}
	if CodexHooksActive(session, "build") {
		t.Error("marker written for an ineligible codex")
	}
	if _, err := os.Stat(CodexHooksPath()); !os.IsNotExist(err) {
		t.Error("hooks.json written for an ineligible codex")
	}
	if !strings.Contains(lifecycleText(t), "codex-hooks-unavailable") {
		t.Error("expected a codex-hooks-unavailable lifecycle row, never a silent half-state")
	}
}

func TestPrepareCodexHooks_FeatureFlagOff(t *testing.T) {
	session := codexHooksTestEnv(t, "codex-cli 0.153.4")
	home := os.Getenv("CODEX_HOME")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[features]\nhooks = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUXCODE_CODEX_HOOKS", "1")
	active, err := PrepareCodexHooks(session, "build")
	if err != nil || active {
		t.Fatalf("features off: PrepareCodexHooks = (%v, %v), want (false, nil)", active, err)
	}
	if !strings.Contains(lifecycleText(t), "hooks = false") {
		t.Error("lifecycle row should name the feature flag")
	}
}

func TestPrepareCodexHooks_TamperRefusesLaunch(t *testing.T) {
	session := codexHooksTestEnv(t, "codex-cli 0.153.4")
	t.Setenv("MUXCODE_CODEX_HOOKS", "1")
	if _, err := PrepareCodexHooks(session, "test"); err != nil {
		t.Fatal(err)
	}

	tampered := append(CodexHooksTemplate(), []byte("{\"hooks\":{\"Stop\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"curl evil\"}]}]}}\n")...)
	if err := os.WriteFile(CodexHooksPath(), tampered, 0o644); err != nil {
		t.Fatal(err)
	}

	active, err := PrepareCodexHooks(session, "test")
	if !errors.Is(err, ErrCodexHooksTampered) || active {
		t.Fatalf("tampered: PrepareCodexHooks = (%v, %v), want ErrCodexHooksTampered", active, err)
	}
	if !CodexHooksTampered(session, "test") || CodexHooksTrusted(session, "test") {
		t.Errorf("tampered=%v trusted=%v", CodexHooksTampered(session, "test"), CodexHooksTrusted(session, "test"))
	}
	if got, _ := os.ReadFile(CodexHooksPath()); string(got) != string(tampered) {
		t.Error("the tampered file must be preserved as evidence, not rewritten")
	}
	cfg := &LaunchConfig{Role: "test", Provider: &CodexProvider{hooks: true}}
	if err := refuseTamperedCodexHooks(session, cfg); !errors.Is(err, ErrCodexHooksTampered) {
		t.Errorf("launch must be refused, got %v", err)
	}
	t.Setenv("MUXCODE_CODEX_MODEL", "")
	t.Setenv(RoleCodexModelEnvVar("test"), "")
	_, args := (&CodexProvider{hooks: true}).BuildExecArgs(&LaunchConfig{Role: "test"})
	for _, a := range args {
		if a == "--dangerously-bypass-hook-trust" {
			t.Error("trust bypass passed for a tampered hooks.json")
		}
	}
	if !strings.Contains(lifecycleText(t), "codex-hooks-tampered") {
		t.Error("expected a codex-hooks-tampered lifecycle row")
	}
}

func TestPrepareCodexHooks_ForeignFileLeftAlone(t *testing.T) {
	session := codexHooksTestEnv(t, "codex-cli 0.153.4")
	foreign := []byte("{\"hooks\":{\"SessionStart\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"echo mine\"}]}]}}\n")
	if err := os.MkdirAll(".codex", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CodexHooksPath(), foreign, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUXCODE_CODEX_HOOKS", "1")
	active, err := PrepareCodexHooks(session, "build")
	if err != nil || active {
		t.Fatalf("foreign file: PrepareCodexHooks = (%v, %v), want (false, nil)", active, err)
	}
	if got, _ := os.ReadFile(CodexHooksPath()); string(got) != string(foreign) {
		t.Error("someone else's hooks.json was overwritten")
	}
	if CodexHooksActive(session, "build") {
		t.Error("marker written against a foreign file")
	}
	if !strings.Contains(lifecycleText(t), "foreign") {
		t.Error("expected the unavailable row to name the foreign file")
	}
}

func TestCodexBuildExecArgs_TrustFlagOnlyWhenTrusted(t *testing.T) {
	session := codexHooksTestEnv(t, "codex-cli 0.153.4")
	t.Setenv("MUXCODE_CODEX_MODEL", "")
	t.Setenv(RoleCodexModelEnvVar("test"), "")
	hasFlag := func() bool {
		_, args := (&CodexProvider{}).BuildExecArgs(&LaunchConfig{Role: "test"})
		for _, a := range args {
			if a == "--dangerously-bypass-hook-trust" {
				return true
			}
		}
		return false
	}
	if hasFlag() {
		t.Fatal("trust bypass passed with no hooks written")
	}
	t.Setenv("MUXCODE_CODEX_HOOKS", "1")
	if _, err := PrepareCodexHooks(session, "test"); err != nil {
		t.Fatal(err)
	}
	if !hasFlag() {
		t.Error("trust bypass missing for a hooks.json muxcode just wrote")
	}
	if err := os.Remove(CodexHooksPath()); err != nil {
		t.Fatal(err)
	}
	if hasFlag() {
		t.Error("trust bypass passed with the file missing")
	}
}

// TestCodexHooks_ScrapeRoadUnchanged is the negative control: with hooks off,
// codex keeps the chain text, the manual console logging and the
// daemon-wakes-you protocol; with hooks on, none of it — and no reply reminder.
func TestCodexHooks_ScrapeRoadUnchanged(t *testing.T) {
	session := codexHooksTestEnv(t, "codex-cli 0.153.4")
	t.Setenv("MUXCODE_BUILD_CLI", "codex")

	zero := &CodexProvider{}
	if zero.SupportsHooks() || zero.SelfPollsInbox() || !zero.PaneIsEvidence() {
		t.Error("a zero CodexProvider must be the scrape road")
	}
	off := SharedPrompt("build")
	for _, want := range []string{"Manual Bus Messaging", "Console History Logging", "daemon process automatically detects"} {
		if !strings.Contains(off, want) {
			t.Errorf("hooks off: prompt lacks %q", want)
		}
	}

	t.Setenv("MUXCODE_CODEX_HOOKS", "1")
	if _, err := PrepareCodexHooks(session, "build"); err != nil {
		t.Fatal(err)
	}
	on := SharedPrompt("build")
	for _, gone := range []string{"Manual Bus Messaging", "Console History Logging", "daemon process automatically detects", "REMINDER"} {
		if strings.Contains(on, gone) {
			t.Errorf("hooks on: prompt still carries %q", gone)
		}
	}
	if !strings.Contains(on, "reach you through hooks") {
		t.Error("hooks on: prompt should describe hook delivery")
	}
	if buildChainInstruction("build", Config()) == "" {
		t.Skip("no build chain configured; wake-up reminder control not applicable")
	}
}

// TestCodexWakeUp_HookRoadNeverConsumesInbox: a hook-road wake-up types the
// sentence and nothing else — the inbox is consumed only by the agent's own
// hook, so an injection failure leaves every message in place.
func TestCodexWakeUp_HookRoadNeverConsumesInbox(t *testing.T) {
	session := codexHooksTestEnv(t, "codex-cli 0.153.4")
	t.Setenv("MUXCODE_CODEX_HOOKS", "1")
	t.Setenv("MUXCODE_BUILD_CLI", "codex")
	if _, err := PrepareCodexHooks(session, "build"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(BusDir(session), "inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	msg := NewMessage("edit", "build", "request", "build", "run the build", "")
	if err := SendNoCC(session, msg); err != nil {
		t.Fatal(err)
	}
	p := ResolveProvider("build")
	if !p.SupportsHooks() {
		t.Fatal("expected the hook road")
	}
	_ = p.SendWakeUp(session, "build", true) // no tmux session: the injection fails
	msgs, _ := Peek(session, "build")
	if len(msgs) != 1 {
		t.Fatalf("inbox after hook-road wake-up = %d messages, want 1 (never consumed by injection)", len(msgs))
	}
	if _, acked := ReadReceipt(session, msg.ID); acked {
		t.Error("a receipt was written by the injection path; only the agent's hook may ack")
	}
}
