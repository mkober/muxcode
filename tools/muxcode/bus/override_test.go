package bus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeConfigDir(t *testing.T) {
	session := "test-session"
	dir := RuntimeConfigDir(session)
	expected := filepath.Join("/tmp", "muxcode-bus-"+session, "config")
	if dir != expected {
		t.Errorf("RuntimeConfigDir = %q, want %q", dir, expected)
	}
}

func TestRuntimeConfigDir_WithBusDirOverride(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	dir := RuntimeConfigDir("test-session")
	expected := filepath.Join(baseDir, "muxcode-bus-test-session", "config")
	if dir != expected {
		t.Errorf("RuntimeConfigDir with override = %q, want %q", dir, expected)
	}
}

func TestRuntimeOverridePath(t *testing.T) {
	session := "test-session"
	role := "build"
	path := RuntimeOverridePath(session, role)
	expected := RuntimeConfigDir(session) + "/" + role + ".env"
	if path != expected {
		t.Errorf("RuntimeOverridePath = %q, want %q", path, expected)
	}
}

func TestWriteRuntimeOverride_CreatesFile(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	role := "build"

	err := WriteRuntimeOverride(session, role, "MUXCODE_BUILD_CLI", "opencode")
	if err != nil {
		t.Fatalf("WriteRuntimeOverride: %v", err)
	}

	path := RuntimeOverridePath(session, role)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)
	if content != "MUXCODE_BUILD_CLI=opencode\n" {
		t.Errorf("file content = %q, want %q", content, "MUXCODE_BUILD_CLI=opencode\n")
	}
}

func TestWriteRuntimeOverride_MultipleKeys(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	role := "build"

	WriteRuntimeOverride(session, role, "MUXCODE_BUILD_CLI", "opencode")
	WriteRuntimeOverride(session, role, "MUXCODE_BUILD_MODEL", "opencode-go/deepseek-v4-pro")

	overrides, err := ReadRuntimeOverrides(session, role)
	if err != nil {
		t.Fatalf("ReadRuntimeOverrides: %v", err)
	}

	if overrides["MUXCODE_BUILD_CLI"] != "opencode" {
		t.Errorf("CLI = %q, want opencode", overrides["MUXCODE_BUILD_CLI"])
	}
	if overrides["MUXCODE_BUILD_MODEL"] != "opencode-go/deepseek-v4-pro" {
		t.Errorf("MODEL = %q, want opencode-go/deepseek-v4-pro", overrides["MUXCODE_BUILD_MODEL"])
	}
	if len(overrides) != 2 {
		t.Errorf("expected 2 overrides, got %d", len(overrides))
	}
}

func TestWriteRuntimeOverride_UpdatesExistingKey(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	role := "build"

	WriteRuntimeOverride(session, role, "MUXCODE_BUILD_CLI", "opencode")
	WriteRuntimeOverride(session, role, "MUXCODE_BUILD_CLI", "claude")

	overrides, err := ReadRuntimeOverrides(session, role)
	if err != nil {
		t.Fatalf("ReadRuntimeOverrides: %v", err)
	}

	if overrides["MUXCODE_BUILD_CLI"] != "claude" {
		t.Errorf("CLI = %q, want claude (should be updated)", overrides["MUXCODE_BUILD_CLI"])
	}
	if len(overrides) != 1 {
		t.Errorf("expected 1 override after update, got %d", len(overrides))
	}
}

func TestReadRuntimeOverrides_NoFile(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	overrides, err := ReadRuntimeOverrides("test-session", "build")
	if err != nil {
		t.Errorf("ReadRuntimeOverrides should not error on missing file: %v", err)
	}
	if overrides != nil {
		t.Errorf("expected nil map for missing file, got %v", overrides)
	}
}

func TestReadRuntimeOverrides_SkipsComments(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	role := "build"

	// Write an override file with comments
	err := WriteRuntimeOverride(session, role, "MUXCODE_BUILD_CLI", "opencode")
	if err != nil {
		t.Fatalf("WriteRuntimeOverride: %v", err)
	}

	// Prepend a comment to the file
	path := RuntimeOverridePath(session, role)
	data, _ := os.ReadFile(path)
	commentedContent := "# This is a comment\n" + string(data) + "# Another comment\n"
	os.WriteFile(path, []byte(commentedContent), 0644)

	overrides, err := ReadRuntimeOverrides(session, role)
	if err != nil {
		t.Fatalf("ReadRuntimeOverrides: %v", err)
	}

	if len(overrides) != 1 {
		t.Fatalf("expected 1 override, got %d: %v", len(overrides), overrides)
	}
	if overrides["MUXCODE_BUILD_CLI"] != "opencode" {
		t.Errorf("CLI = %q, want opencode", overrides["MUXCODE_BUILD_CLI"])
	}
}

func TestLoadRuntimeOverrides_SetsEnvVars(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	role := "build"

	WriteRuntimeOverride(session, role, "MUXCODE_BUILD_CLI", "opencode")
	WriteRuntimeOverride(session, role, "MUXCODE_BUILD_MODEL", "opencode-go/deepseek-v4-pro")

	// Clear existing env vars to test cleanly (t.Setenv restores after test)
	t.Setenv("MUXCODE_BUILD_CLI", "")
	t.Setenv("MUXCODE_BUILD_MODEL", "")

	err := LoadRuntimeOverrides(session, role)
	if err != nil {
		t.Fatalf("LoadRuntimeOverrides: %v", err)
	}

	if got := os.Getenv("MUXCODE_BUILD_CLI"); got != "opencode" {
		t.Errorf("MUXCODE_BUILD_CLI = %q after load, want opencode", got)
	}
	if got := os.Getenv("MUXCODE_BUILD_MODEL"); got != "opencode-go/deepseek-v4-pro" {
		t.Errorf("MUXCODE_BUILD_MODEL = %q after load, want opencode-go/deepseek-v4-pro", got)
	}
}

func TestLoadRuntimeOverrides_NoFile(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	// Should not error when no override file exists
	err := LoadRuntimeOverrides("test-session", "build")
	if err != nil {
		t.Errorf("LoadRuntimeOverrides should not error on missing file: %v", err)
	}
}

func TestClearRuntimeOverrides(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	role := "build"

	WriteRuntimeOverride(session, role, "MUXCODE_BUILD_CLI", "opencode")

	// Verify file exists
	path := RuntimeOverridePath(session, role)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("override file should exist: %v", err)
	}

	err := ClearRuntimeOverrides(session, role)
	if err != nil {
		t.Fatalf("ClearRuntimeOverrides: %v", err)
	}

	// Verify file is removed
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("override file should be removed, but stat reports: %v", err)
	}
}

func TestClearRuntimeOverrides_NoFile(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	// Should not error when no file exists
	err := ClearRuntimeOverrides("test-session", "build")
	if err != nil {
		t.Errorf("ClearRuntimeOverrides should not error on missing file: %v", err)
	}
}

// Test that ResolveProviderCLI picks up runtime overrides.
func TestResolveProviderCLI_WithRuntimeOverride(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	t.Setenv("BUS_SESSION", session)

	// Env var says claude, but runtime override says opencode
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "claude")

	WriteRuntimeOverride(session, "build", "MUXCODE_BUILD_CLI", "opencode")

	// ResolveProviderCLI reads override files inline (without os.Setenv),
	// so it will pick up the override value directly
	cli := ResolveProviderCLI("build")
	if cli != "opencode" {
		t.Errorf("ResolveProviderCLI = %q, want opencode (runtime override should beat env var)", cli)
	}
}

// Test that ResolveProviderCLI falls through to env var when no override exists.
func TestResolveProviderCLI_WithoutRuntimeOverride(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	t.Setenv("BUS_SESSION", "test-session")
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "claude")

	cli := ResolveProviderCLI("build")
	if cli != "claude" {
		t.Errorf("ResolveProviderCLI = %q, want claude (env var, no override)", cli)
	}
}

// Test that ResolveProviderCLI falls through to defaults when nothing is set.
func TestResolveProviderCLI_DefaultWithNoOverride(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	t.Setenv("BUS_SESSION", "test-session")
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "")

	// build defaults to opencode
	cli := ResolveProviderCLI("build")
	if cli != "opencode" {
		t.Errorf("ResolveProviderCLI = %q, want opencode (default)", cli)
	}

	// edit defaults to claude
	t.Setenv("MUXCODE_EDIT_CLI", "")
	cli = ResolveProviderCLI("edit")
	if cli != "claude" {
		t.Errorf("ResolveProviderCLI = %q, want claude (default)", cli)
	}
}

// Test that the override file path is under the overridden BusDir, not /tmp.
func TestRuntimeOverrides_BusDirOverride(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	role := "build"

	WriteRuntimeOverride(session, role, "KEY_ONE", "value1")

	// Verify file is in the overridden bus dir, not /tmp
	path := RuntimeOverridePath(session, role)
	expectedPrefix := baseDir
	if len(path) < len(expectedPrefix) || path[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("override path %q should be under %q", path, expectedPrefix)
	}

	// Verify it can be read back
	overrides, err := ReadRuntimeOverrides(session, role)
	if err != nil {
		t.Fatalf("ReadRuntimeOverrides: %v", err)
	}
	if overrides["KEY_ONE"] != "value1" {
		t.Errorf("KEY_ONE = %q, want value1", overrides["KEY_ONE"])
	}
}

// Test that runtime overrides take highest priority in the full resolution chain.
func TestRuntimeOverrides_TopPriority(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	t.Setenv("BUS_SESSION", session)

	// Set all lower-priority sources
	t.Setenv("MUXCODE_AGENT_CLI", "local") // session-wide
	t.Setenv("MUXCODE_BUILD_CLI", "codex") // per-role env

	// Runtime override beats both
	WriteRuntimeOverride(session, "build", "MUXCODE_BUILD_CLI", "opencode")

	cli := ResolveProviderCLI("build")
	if cli != "opencode" {
		t.Errorf("ResolveProviderCLI = %q, want opencode (runtime override, highest priority)", cli)
	}
}
