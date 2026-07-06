package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCommitMsgHook_NoGitDir(t *testing.T) {
	dir := t.TempDir()
	// No .git directory — should be a no-op.
	if err := InstallCommitMsgHook(dir); err != nil {
		t.Fatalf("expected nil error for non-git dir, got: %v", err)
	}
	hookPath := filepath.Join(dir, ".git", "hooks", "commit-msg")
	if _, err := os.Stat(hookPath); err == nil {
		t.Fatal("hook should not exist in non-git directory")
	}
}

func TestInstallCommitMsgHook_CreatesNewHook(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0755)

	if err := InstallCommitMsgHook(dir); err != nil {
		t.Fatalf("InstallCommitMsgHook: %v", err)
	}

	hookPath := filepath.Join(dir, ".git", "hooks", "commit-msg")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}

	content := string(data)
	if !strings.HasPrefix(content, "#!/bin/sh") {
		t.Error("hook should start with shebang")
	}
	if !strings.Contains(content, commitMsgHookMarker) {
		t.Error("hook should contain marker")
	}
	if !strings.Contains(content, "Co-authored-by") {
		t.Error("hook should reference Co-authored-by pattern")
	}

	// Verify executable.
	fi, _ := os.Stat(hookPath)
	if fi.Mode().Perm()&0111 == 0 {
		t.Error("hook should be executable")
	}
}

func TestInstallCommitMsgHook_Idempotent(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0755)

	// Install twice.
	InstallCommitMsgHook(dir)
	InstallCommitMsgHook(dir)

	hookPath := filepath.Join(dir, ".git", "hooks", "commit-msg")
	data, _ := os.ReadFile(hookPath)
	content := string(data)

	// Marker should appear exactly once.
	count := strings.Count(content, commitMsgHookMarker)
	if count != 1 {
		t.Errorf("expected marker once, found %d times", count)
	}
}

func TestInstallCommitMsgHook_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, ".git", "hooks")
	os.MkdirAll(hooksDir, 0755)

	// Write an existing hook (e.g. Jira prefix enforcement).
	existing := "#!/bin/sh\n# jira-prefix-check\ncheck-jira-prefix \"$1\"\n"
	os.WriteFile(filepath.Join(hooksDir, "commit-msg"), []byte(existing), 0755)

	if err := InstallCommitMsgHook(dir); err != nil {
		t.Fatalf("InstallCommitMsgHook: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(hooksDir, "commit-msg"))
	content := string(data)

	// Original content preserved.
	if !strings.Contains(content, "jira-prefix-check") {
		t.Error("existing hook content should be preserved")
	}
	// Our marker appended.
	if !strings.Contains(content, commitMsgHookMarker) {
		t.Error("marker should be appended")
	}
	// Our stripping logic appended.
	if !strings.Contains(content, "Co-authored-by") {
		t.Error("stripping logic should be appended")
	}
	// Shebang should appear only once (from the original).
	if strings.Count(content, "#!/bin/sh") != 1 {
		t.Error("shebang should appear only once")
	}
}

func TestInstallCommitMsgHook_AppendsIdempotent(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, ".git", "hooks")
	os.MkdirAll(hooksDir, 0755)

	existing := "#!/bin/sh\nsome-hook \"$1\"\n"
	os.WriteFile(filepath.Join(hooksDir, "commit-msg"), []byte(existing), 0755)

	// Append twice.
	InstallCommitMsgHook(dir)
	InstallCommitMsgHook(dir)

	data, _ := os.ReadFile(filepath.Join(hooksDir, "commit-msg"))
	content := string(data)

	count := strings.Count(content, commitMsgHookMarker)
	if count != 1 {
		t.Errorf("expected marker once after double-append, found %d times", count)
	}
}

func TestInstallCommitMsgHook_CreatesHooksDir(t *testing.T) {
	dir := t.TempDir()
	// Create .git but NOT .git/hooks — the function should create it.
	os.MkdirAll(filepath.Join(dir, ".git"), 0755)

	if err := InstallCommitMsgHook(dir); err != nil {
		t.Fatalf("InstallCommitMsgHook: %v", err)
	}

	hookPath := filepath.Join(dir, ".git", "hooks", "commit-msg")
	if _, err := os.Stat(hookPath); err != nil {
		t.Fatalf("hook should exist: %v", err)
	}
}

func TestInstallPrepareCommitMsgHook_NoGitDir(t *testing.T) {
	dir := t.TempDir()
	if err := InstallPrepareCommitMsgHook(dir); err != nil {
		t.Fatalf("expected nil error for non-git dir, got: %v", err)
	}
	hookPath := filepath.Join(dir, ".git", "hooks", "prepare-commit-msg")
	if _, err := os.Stat(hookPath); err == nil {
		t.Fatal("hook should not exist in non-git directory")
	}
}

func TestInstallPrepareCommitMsgHook_CreatesNewHook(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0755)

	if err := InstallPrepareCommitMsgHook(dir); err != nil {
		t.Fatalf("InstallPrepareCommitMsgHook: %v", err)
	}

	hookPath := filepath.Join(dir, ".git", "hooks", "prepare-commit-msg")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, prepareCommitMsgHookMarker) {
		t.Error("hook missing marker")
	}
	if !strings.Contains(content, "branch-time --trailer") {
		t.Error("hook should invoke branch-time --trailer")
	}
	if !strings.HasPrefix(content, "#!/bin/sh") {
		t.Error("hook should have a shebang")
	}
	info, _ := os.Stat(hookPath)
	if info.Mode()&0100 == 0 {
		t.Error("hook should be executable")
	}
}

func TestInstallPrepareCommitMsgHook_Idempotent(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0755)

	InstallPrepareCommitMsgHook(dir)
	InstallPrepareCommitMsgHook(dir)

	hookPath := filepath.Join(dir, ".git", "hooks", "prepare-commit-msg")
	data, _ := os.ReadFile(hookPath)
	if count := strings.Count(string(data), prepareCommitMsgHookMarker); count != 1 {
		t.Errorf("expected marker once, found %d times", count)
	}
}

func TestInstallPrepareCommitMsgHook_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, ".git", "hooks")
	os.MkdirAll(hooksDir, 0755)

	existing := "#!/bin/sh\n# other-hook\nsome-check \"$1\"\n"
	os.WriteFile(filepath.Join(hooksDir, "prepare-commit-msg"), []byte(existing), 0755)

	if err := InstallPrepareCommitMsgHook(dir); err != nil {
		t.Fatalf("InstallPrepareCommitMsgHook: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(hooksDir, "prepare-commit-msg"))
	content := string(data)
	if !strings.Contains(content, "some-check") {
		t.Error("existing hook logic should be preserved")
	}
	if !strings.Contains(content, prepareCommitMsgHookMarker) {
		t.Error("our marker should be appended")
	}
	if count := strings.Count(content, "#!/bin/sh"); count != 1 {
		t.Errorf("shebang should appear once, found %d", count)
	}
}

func TestCommitMsgHookFull_StripsAttribution(t *testing.T) {
	// Verify the sed pattern in the hook matches various Co-authored-by forms.
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "standard",
			input: "Add feature\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>\n",
			want:  "Add feature\n\n",
		},
		{
			name:  "lowercase",
			input: "Fix bug\n\nco-authored-by: Bot <bot@example.com>\n",
			want:  "Fix bug\n\n",
		},
		{
			name:  "mixed case",
			input: "Update docs\n\nCo-authored-by: Claude Sonnet 4.5 <noreply@anthropic.com>\n",
			want:  "Update docs\n\n",
		},
		{
			name:  "no attribution",
			input: "Clean commit\n\nJust a normal message.\n",
			want:  "Clean commit\n\nJust a normal message.\n",
		},
		{
			name:  "indented",
			input: "Feature\n\n   Co-Authored-By: AI <ai@example.com>\n",
			want:  "Feature\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write a temp commit message file.
			tmpDir := t.TempDir()
			msgFile := filepath.Join(tmpDir, "COMMIT_MSG")
			os.WriteFile(msgFile, []byte(tt.input), 0644)

			// Run the sed command from our hook against the temp file.
			script := `sed '/^[[:space:]]*[Cc]o-[Aa]uthored-[Bb]y:/d' "` + msgFile + `" > "` + msgFile + `.tmp" && mv "` + msgFile + `.tmp" "` + msgFile + `"`
			cmd := exec.Command("sh", "-c", script)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("sed command failed: %v (output: %s)", err, string(out))
			}

			got, _ := os.ReadFile(msgFile)
			if string(got) != tt.want {
				t.Errorf("got %q, want %q", string(got), tt.want)
			}
		})
	}
}
