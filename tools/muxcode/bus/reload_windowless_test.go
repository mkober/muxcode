package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubConfigFile points config writes at a scratch file.
//
// The file must be CREATED, not merely named: ResolveConfigPath only honours
// MUXCODE_CONFIG when the path already exists, and otherwise falls through to
// ~/.config/muxcode/config — so a test that skipped this would silently rewrite
// the developer's own config.
func stubConfigFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("# test\n"), 0644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	t.Setenv("MUXCODE_CONFIG", path)
	return path
}

// stubTmuxWindowList makes TmuxOutput answer list-windows with the given names
// and fail every other query, so window-presence logic can be pinned without a
// tmux server. Failing the other queries is deliberate: it reproduces the
// capture failure that makes IsAgentAlive fail-safe to "alive", which is the
// condition the windowless marking has to survive.
func stubTmuxWindowList(t *testing.T, names ...string) {
	t.Helper()
	orig := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "list-windows" {
			return strings.Join(names, "\n"), nil
		}
		return "", fmt.Errorf("stub: no output for %v", args)
	}
	t.Cleanup(func() { tmuxOutputRunner = orig })
}

// stubTmuxUnavailable makes every tmux query fail, standing in for "no server
// running".
func stubTmuxUnavailable(t *testing.T) {
	t.Helper()
	orig := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		return "", fmt.Errorf("no server running")
	}
	t.Cleanup(func() { tmuxOutputRunner = orig })
}

func TestRoleWindowMissing(t *testing.T) {
	t.Run("windowless role is refused", func(t *testing.T) {
		stubTmuxWindowList(t, "plan", "edit", "build", "test")
		err := RoleWindowMissing("sess", "analyze")
		if err == nil {
			t.Fatal("a role with no window must be refused — reloading it can only fail")
		}
		if !strings.Contains(err.Error(), "no window") {
			t.Errorf("error must name the cause, got: %v", err)
		}
	})

	// Negative control: without this the guard could refuse everything and the
	// case above would still pass.
	t.Run("windowed role passes", func(t *testing.T) {
		stubTmuxWindowList(t, "plan", "edit", "build", "test")
		if err := RoleWindowMissing("sess", "build"); err != nil {
			t.Fatalf("a windowed role must not be refused: %v", err)
		}
	})

	t.Run("unreadable window list is indeterminate", func(t *testing.T) {
		stubTmuxUnavailable(t)
		if err := RoleWindowMissing("sess", "analyze"); err != nil {
			t.Fatalf("an unreadable window list must not refuse the reload: %v", err)
		}
	})

	// A mode-cycled role is judged on its HOLD window, not the window it shares.
	// Testing the host window instead made research read as present while the
	// reload addressed a `research` window that had never been created.
	t.Run("mode role without its hold window is refused", func(t *testing.T) {
		stubTmuxWindowList(t, "plan", "edit")
		if err := RoleWindowMissing("sess", "research"); err == nil {
			t.Fatal("research has no hold window yet — reloading it can only time out")
		}
	})

	// Negative control: once the hold window exists the role is reloadable again,
	// so the rule above cannot harden into "mode roles are never reloadable".
	t.Run("mode role with its hold window passes", func(t *testing.T) {
		stubTmuxWindowList(t, "plan", "edit", "research")
		if err := RoleWindowMissing("sess", "research"); err != nil {
			t.Fatalf("research has a hold window and must be reloadable: %v", err)
		}
	})
}

func TestActiveAgentStatusesMarksWindowlessRoles(t *testing.T) {
	_, cleanup := setupTestBusDir(t)
	defer cleanup()
	stubTmuxWindowList(t, "plan", "edit", "build", "test", "serve",
		"review", "deploy", "run", "watch", "commit")

	byRole := map[string]AgentReloadStatus{}
	for _, s := range ActiveAgentStatuses("sess") {
		byRole[s.Role] = s
	}

	analyze, ok := byRole["analyze"]
	if !ok {
		t.Fatal("analyze should still be listed — it is a known role")
	}
	if !analyze.Windowless {
		t.Error("analyze has no window in the list and must be marked Windowless")
	}
	// The discriminating assertion: IsAgentAlive fail-safes to true when it
	// cannot capture a pane, so this is what made a phantom role selectable.
	if analyze.Alive {
		t.Error("a windowless role must never report Alive")
	}

	// Negative control: a real window must not be swept up by the same rule.
	build, ok := byRole["build"]
	if !ok {
		t.Fatal("build should be listed")
	}
	if build.Windowless {
		t.Error("build has a window and must not be marked Windowless")
	}

	// Mode roles whose hold window has never been created have no pane and no
	// process, so they are windowless in the sense that matters: configurable,
	// not reloadable. The window list above carries no research/auto window.
	for _, role := range []string{"research", "auto"} {
		s, ok := byRole[role]
		if !ok {
			t.Fatalf("%s should be listed", role)
		}
		if !s.Windowless {
			t.Errorf("%s has no hold window and must be marked Windowless", role)
		}
	}
}

// Negative control for the rule above: a mode role IS reloadable once its hold
// window exists, so "mode role" must not become a synonym for "windowless".
func TestActiveAgentStatusesModeRoleWithHoldWindow(t *testing.T) {
	_, cleanup := setupTestBusDir(t)
	defer cleanup()
	stubTmuxWindowList(t, "plan", "edit", "build", "research")

	for _, s := range ActiveAgentStatuses("sess") {
		if s.Role == "research" && s.Windowless {
			t.Error("research has a hold window and must not be marked Windowless")
		}
	}
}

// A windowless role is configured, not reloaded: the setting is persisted to the
// shell config so it survives to a future launch, and nothing is stopped.
func TestReloadBatchConfiguresWindowlessRole(t *testing.T) {
	_, cleanup := setupTestBusDir(t)
	defer cleanup()
	cfg := stubConfigFile(t)
	stubTmuxWindowList(t, "plan", "edit", "build")

	// Mirrors the live report: analyze is switched to codex while the session's
	// exported env still says opencode.
	t.Setenv("BUS_SESSION", "sess")
	t.Setenv(RoleCLIEnvVar("analyze"), "opencode")
	t.Setenv(RoleModelEnvVar("analyze"), "stale/env-model")

	results := ReloadBatch("sess", []string{"analyze"}, "codex", "some/model-x", false, nil)
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	r := results[0]
	if !r.ConfigOnly {
		t.Error("a windowless role must be applied as config-only, never reloaded")
	}
	if !r.Success {
		t.Fatalf("config-only apply failed: %v", r.Error)
	}

	if got := ResolveProviderCLI("analyze"); got != "codex" {
		t.Errorf("CLI must resolve to the new value despite the stale env var, got %q", got)
	}
	if got := EffectiveConfig("analyze").Model; got != "some/model-x" {
		t.Errorf("model must resolve to the new value despite the stale env var, got %q", got)
	}
	if r.NewCLI != "codex" {
		t.Errorf("result row must show the new CLI, got %q", r.NewCLI)
	}

	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	// And it must also outlive the session, so it has to reach disk.
	for _, want := range []string{"MUXCODE_ANALYZE_MODEL", "some/model-x"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("config missing %q:\n%s", want, data)
		}
	}
}

// Routing is asserted on the predicate rather than through ReloadBatch: a
// windowed role there would run the real 12s stop sequence against tmux.
func TestConfigOnlyRole(t *testing.T) {
	windows := []string{"plan", "edit", "build"}
	cases := []struct {
		name       string
		role       string
		known      bool
		wantConfig bool
	}{
		{"windowless role is config-only", "analyze", true, true},
		{"windowed role still reloads", "build", true, false},
		// research holds on a `research` window that is not created until that
		// mode is first cycled to, and the list above has none.
		{"mode role without its hold window is config-only", "research", true, true},
		{"headless prompt role is not config-only", promptAgentRole, true, false},
		{"unreadable window list falls back to reload", "analyze", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConfigOnlyRole(windows, tc.known, tc.role); got != tc.wantConfig {
				t.Errorf("ConfigOnlyRole(%q, known=%v) = %v, want %v",
					tc.role, tc.known, got, tc.wantConfig)
			}
		})
	}
}

// Without a readable window list nothing may be marked windowless, or a
// transient tmux failure would blank out every reload target at once.
func TestActiveAgentStatusesIndeterminateWhenTmuxUnavailable(t *testing.T) {
	_, cleanup := setupTestBusDir(t)
	defer cleanup()
	stubTmuxUnavailable(t)

	for _, s := range ActiveAgentStatuses("sess") {
		if s.Windowless {
			t.Errorf("role %s marked Windowless from an unreadable window list", s.Role)
		}
	}
}
