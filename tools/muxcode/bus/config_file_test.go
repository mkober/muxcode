package bus

import (
	"os"
	"path/filepath"
	"testing"
)

// The home config is the base and the project's overrides it key by key; until
// 2026-11-02 the project file was read alone and hid every home setting
// (MUX-207). Keys set in the environment stay out of the result.
func TestGetShellConfig_LayersProjectOverHome(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MUXCODE_CONFIG", "")
	for _, k := range []string{"LAYER_HOME_ONLY", "LAYER_BOTH", "LAYER_PROJECT_ONLY", "LAYER_FROM_ENV"} {
		t.Setenv(k, "")
	}
	t.Setenv("LAYER_FROM_ENV", "env")
	writeFile(t, filepath.Join(home, ".config", "muxcode", "config"),
		"LAYER_HOME_ONLY=home\nLAYER_BOTH=home\nLAYER_FROM_ENV=home\n")
	writeFile(t, filepath.Join(project, ".muxcode", "config"),
		"LAYER_BOTH=project\nexport LAYER_PROJECT_ONLY='project'\n")

	cfg := GetShellConfig(project)

	want := map[string]string{"LAYER_HOME_ONLY": "home", "LAYER_BOTH": "project", "LAYER_PROJECT_ONLY": "project"}
	for k, v := range want {
		if cfg[k] != v {
			t.Errorf("%s = %q, want %q", k, cfg[k], v)
		}
	}
	if v, ok := cfg["LAYER_FROM_ENV"]; ok {
		t.Errorf("LAYER_FROM_ENV = %q from a file, want it left to the environment", v)
	}
}

// The launcher loads config before a project is picked, from whatever directory
// it was started in, and again once the project is known. The second load must
// replace the first's file values — the review's repro is a home
// MUXCODE_BUILD_CLI=claude surviving a project's codex — while a value the user
// exported keeps precedence and a key only the start directory's config set is
// dropped. The old double LoadShellConfig is run as the control showing the bug.
func TestReloadShellConfig_ProjectReplacesPreliminaryLoad(t *testing.T) {
	home, start, project := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MUXCODE_CONFIG", "")
	for _, k := range []string{"MUXCODE_BUILD_CLI", "RELOAD_START_ONLY", "RELOAD_HOME_ONLY", "RELOAD_FROM_ENV"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("RELOAD_FROM_ENV", "env")
	writeFile(t, filepath.Join(home, ".config", "muxcode", "config"),
		"MUXCODE_BUILD_CLI=claude\nRELOAD_HOME_ONLY=home\nRELOAD_FROM_ENV=home\n")
	writeFile(t, filepath.Join(start, ".muxcode", "config"), "RELOAD_START_ONLY=start\n")
	writeFile(t, filepath.Join(project, ".muxcode", "config"),
		"MUXCODE_BUILD_CLI=codex\nRELOAD_FROM_ENV=project\n")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	if err := os.Chdir(start); err != nil {
		t.Fatal(err)
	}

	ReloadShellConfig(project, LoadShellConfig(""))

	want := map[string]string{"MUXCODE_BUILD_CLI": "codex", "RELOAD_HOME_ONLY": "home", "RELOAD_FROM_ENV": "env", "RELOAD_START_ONLY": ""}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q after the launcher's two loads, want %q", k, got, v)
		}
	}

	os.Unsetenv("MUXCODE_BUILD_CLI")
	LoadShellConfig("")
	LoadShellConfig(project)
	if got := os.Getenv("MUXCODE_BUILD_CLI"); got != "claude" {
		t.Errorf("control: double LoadShellConfig gave MUXCODE_BUILD_CLI = %q, want the bug's claude", got)
	}
}

// Control: a MUXCODE_CONFIG file is still read alone — the hermetic road tests
// and users rely on — so neither the home nor the project file leaks in.
func TestGetShellConfig_MuxcodeConfigReadAlone(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LAYER_HOME_ONLY", "")
	t.Setenv("LAYER_PROJECT_ONLY", "")
	t.Setenv("LAYER_EXPLICIT", "")
	writeFile(t, filepath.Join(home, ".config", "muxcode", "config"), "LAYER_HOME_ONLY=home\n")
	writeFile(t, filepath.Join(project, ".muxcode", "config"), "LAYER_PROJECT_ONLY=project\n")
	explicit := filepath.Join(t.TempDir(), "config")
	writeFile(t, explicit, "LAYER_EXPLICIT=explicit\n")
	t.Setenv("MUXCODE_CONFIG", explicit)

	cfg := GetShellConfig(project)

	if len(cfg) != 1 || cfg["LAYER_EXPLICIT"] != "explicit" {
		t.Errorf("config = %v, want only LAYER_EXPLICIT from MUXCODE_CONFIG", cfg)
	}
}
