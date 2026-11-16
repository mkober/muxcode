package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// reloadExited carries the code configureIfWindowless passed to reloadExit.
type reloadExited int

// configureWindowless runs configureIfWindowless for role in session "sess"
// against a window list (listErr for an unreadable one), returning whether it
// handled the request and the exit code it asked for, 0 when none.
func configureWindowless(t *testing.T, windows []string, listErr error, role, cli, model string) (handled bool, code int) {
	t.Helper()
	savedList, savedExit := listWindows, reloadExit
	listWindows = func(string) ([]string, error) { return windows, listErr }
	reloadExit = func(c int) { panic(reloadExited(c)) }
	defer func() {
		listWindows, reloadExit = savedList, savedExit
		if p := recover(); p != nil {
			c, ok := p.(reloadExited)
			if !ok {
				panic(p)
			}
			handled, code = true, int(c)
		}
	}()
	return configureIfWindowless("sess", role, cli, model), 0
}

// windowlessReloadEnv gives the test its own bus and a seeded config file, and
// leaves a stale per-role env value behind, as the live report had.
func windowlessReloadEnv(t *testing.T) string {
	t.Helper()
	bus.SetBusDirBase(t.TempDir())
	t.Cleanup(bus.ResetBusDirBase)
	t.Setenv("BUS_SESSION", "sess")
	cfg := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(cfg, []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUXCODE_CONFIG", cfg)
	t.Setenv(bus.RoleCLIEnvVar("analyze"), "opencode")
	t.Setenv(bus.RoleModelEnvVar("analyze"), "stale/env-model")
	return cfg
}

// TestConfigureIfWindowless is MUX-145 Phase 5's CLI road, kept in step with
// the modal's TestReloadBatchConfiguresWindowlessRole: `muxcode reload analyze
// --cli codex --model …` on a session with no analyze window applies the
// config — resolving over the stale env value and persisted to the config
// file — and writes no reload marker. With neither flag it exits 1 having
// written nothing. Controls: a role with a window, and an unreadable window
// list, fall through to the ordinary reload.
func TestConfigureIfWindowless(t *testing.T) {
	t.Run("windowless role is configured, not reloaded", func(t *testing.T) {
		cfg := windowlessReloadEnv(t)
		handled, code := configureWindowless(t, []string{"plan", "edit", "build"}, nil, "analyze", "codex", "some/model-x")
		if !handled || code != 0 {
			t.Fatalf("handled=%v code=%d, want a config-only apply", handled, code)
		}
		if got := bus.ResolveProviderCLI("analyze"); got != "codex" {
			t.Errorf("CLI resolves to %q, want codex over the stale env value", got)
		}
		if got := bus.EffectiveConfig("analyze").Model; got != "some/model-x" {
			t.Errorf("model resolves to %q, want some/model-x", got)
		}
		if data, _ := os.ReadFile(cfg); !strings.Contains(string(data), "some/model-x") {
			t.Errorf("the model must reach the config file to outlive the session:\n%s", data)
		}
		if bus.IsReloading("sess", "analyze") {
			t.Error("a config-only apply wrote a reload marker")
		}
	})

	t.Run("no --cli or --model exits 1 and writes nothing", func(t *testing.T) {
		cfg := windowlessReloadEnv(t)
		handled, code := configureWindowless(t, []string{"plan", "edit"}, nil, "analyze", "", "")
		if !handled || code != 1 {
			t.Fatalf("handled=%v code=%d, want exit 1", handled, code)
		}
		if data, _ := os.ReadFile(cfg); string(data) != "# test\n" {
			t.Errorf("config changed with nothing to apply:\n%s", data)
		}
	})

	for _, c := range []struct {
		name    string
		windows []string
		listErr error
	}{
		{name: "a role with a window falls through", windows: []string{"plan", "edit", "analyze"}},
		{name: "an unreadable window list falls through", listErr: errors.New("no server")},
	} {
		t.Run(c.name, func(t *testing.T) {
			windowlessReloadEnv(t)
			if handled, code := configureWindowless(t, c.windows, c.listErr, "analyze", "codex", "some/model-x"); handled || code != 0 {
				t.Errorf("handled=%v code=%d, want the ordinary reload road", handled, code)
			}
			if got := bus.ResolveProviderCLI("analyze"); got != "opencode" {
				t.Errorf("nothing may be configured on the reload road here, CLI now %q", got)
			}
		})
	}
}
