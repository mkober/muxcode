package bus

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// codexTrustPromptPaneV2 is Codex 0.162.1's trust prompt as captured from the
// dotfiles build pane on 2026-10-12, where the wake's Escape chose Quit.
const codexTrustPromptPaneV2 = `  >_ OpenAI Codex (v0.162.1)
     ~/Repos/mkober/dotfiles
› Ask Codex to do anything
  Folder access
  /Users/mkober/Repos/mkober/dotfiles
  Trust this folder? Codex can read, edit, and run files here, subject to your permission settings. Folder settings can run code automatically, even without
  a model request. Continue only if you trust these files. Your trust decision will be saved.
› 1. Trust and continue
  2. Quit
  enter continue · esc quit
`

func codexTrustEnv(t *testing.T) (home, repo string) {
	t.Helper()
	dir := t.TempDir()
	home = filepath.Join(dir, "codex-home")
	repo = filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	t.Setenv("MUXCODE_CODEX_AUTO_TRUST", "")
	return home, repo
}

func readCodexConfig(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	return string(b)
}

func TestEnsureCodexProjectTrust_CreatesConfig(t *testing.T) {
	home, repo := codexTrustEnv(t)

	added, err := EnsureCodexProjectTrust(repo)
	if err != nil || !added {
		t.Fatalf("EnsureCodexProjectTrust = %v, %v; want true, nil", added, err)
	}
	want := `[projects."` + repo + `"]` + "\ntrust_level = \"trusted\"\n"
	if got := readCodexConfig(t, home); got != want {
		t.Errorf("config.toml = %q, want %q", got, want)
	}
}

// A subdirectory launch trusts the repo root, the key Codex looks up.
func TestEnsureCodexProjectTrust_TrustsRepoRootFromSubdir(t *testing.T) {
	home, repo := codexTrustEnv(t)
	sub := filepath.Join(repo, "tools", "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureCodexProjectTrust(sub); err != nil {
		t.Fatal(err)
	}
	if got := readCodexConfig(t, home); !strings.Contains(got, `[projects."`+repo+`"]`) || strings.Contains(got, sub) {
		t.Errorf("want the repo root trusted, not the subdir; config.toml = %q", got)
	}
}

func TestEnsureCodexProjectTrust_AppendsKeepingExistingConfigAndMode(t *testing.T) {
	home, repo := codexTrustEnv(t)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := "model = \"gpt-6\"\n\n[projects.\"/other\"]\ntrust_level = \"trusted\""
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte(existing), 0o640); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureCodexProjectTrust(repo); err != nil {
		t.Fatal(err)
	}
	want := existing + "\n\n[projects.\"" + repo + "\"]\ntrust_level = \"trusted\"\n"
	if got := readCodexConfig(t, home); got != want {
		t.Errorf("config.toml = %q, want %q", got, want)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640 kept", info.Mode().Perm())
	}
}

// Every TOML spelling of an existing entry for the project — and an inline
// projects table, which no header can extend — must leave config.toml
// byte-for-byte unchanged: an appended table would define the project twice,
// invalid TOML that stops Codex starting, and would override "untrusted".
func TestEnsureCodexProjectTrust_LeavesExistingEntryAlone(t *testing.T) {
	for name, existing := range map[string]string{
		"plain":           "[projects.\"%s\"]\ntrust_level = \"untrusted\"\n",
		"comment":         "  [projects.\"%s\"]   # set by hand\ntrust_level = \"untrusted\"\n",
		"spaced header":   "[projects . \"%s\"]\ntrust_level = \"untrusted\"\n",
		"literal key":     "[projects.'%s']\ntrust_level = \"untrusted\"\n",
		"root dotted key": "projects.\"%s\".trust_level = \"untrusted\"\n",
		"inline in table": "[projects]\n\"%s\" = { trust_level = \"untrusted\" }\n",
		"inline projects": "projects = { \"/other\" = { trust_level = \"trusted\" } }\n",
		"quoted inline":   "\"projects\" = { \"/other\" = { trust_level = \"trusted\" } }\n",
		"unicode escape":  "[projects.\"/\\u0072epo\"]\ntrust_level = \"untrusted\"\n",
		"array of tables": "[[projects]]\npath = \"/other\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			home, repo := codexTrustEnv(t)
			if err := os.MkdirAll(home, 0o700); err != nil {
				t.Fatal(err)
			}
			existing := strings.Replace(existing, "%s", repo, 1)
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(existing), 0o600); err != nil {
				t.Fatal(err)
			}

			added, err := EnsureCodexProjectTrust(repo)
			if err != nil || added {
				t.Fatalf("EnsureCodexProjectTrust = %v, %v; want false, nil", added, err)
			}
			if got := readCodexConfig(t, home); got != existing {
				t.Errorf("config.toml changed: %q", got)
			}
		})
	}
}

// Negative control: a project whose path merely starts with the root's is a
// different project, and the root still gets its own table.
func TestEnsureCodexProjectTrust_PrefixProjectIsNotAMatch(t *testing.T) {
	home, repo := codexTrustEnv(t)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := "[projects.\"" + repo + "-two\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	added, err := EnsureCodexProjectTrust(repo)
	if err != nil || !added {
		t.Fatalf("EnsureCodexProjectTrust = %v, %v; want true, nil", added, err)
	}
	if got := readCodexConfig(t, home); !strings.Contains(got, `[projects."`+repo+`"]`) {
		t.Errorf("root table missing: %q", got)
	}
}

// Negative control for the inline/array guard: an ordinary [projects] table
// holding other projects can take a new sub-table, so trust is still added.
func TestEnsureCodexProjectTrust_AppendsAfterProjectsTable(t *testing.T) {
	home, repo := codexTrustEnv(t)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := "[projects]\n\"/other\" = { trust_level = \"trusted\" }\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	added, err := EnsureCodexProjectTrust(repo)
	if err != nil || !added {
		t.Fatalf("EnsureCodexProjectTrust = %v, %v; want true, nil", added, err)
	}
}

// A dotfile-managed config.toml symlink must survive: the write lands in its
// target, not over the link.
func TestEnsureCodexProjectTrust_KeepsConfigSymlink(t *testing.T) {
	home, repo := codexTrustEnv(t)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "dotfiles-config.toml")
	if err := os.WriteFile(target, []byte("model = \"gpt-6\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureCodexProjectTrust(repo); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config.toml is no longer a symlink (err %v)", err)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `[projects."`+repo+`"]`) {
		t.Errorf("symlink target not updated: %q", b)
	}
}

// Every Codex agent in a session launches at once; exactly one table may land.
func TestEnsureCodexProjectTrust_ConcurrentLaunchesAddOneTable(t *testing.T) {
	home, repo := codexTrustEnv(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := EnsureCodexProjectTrust(repo); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if n := strings.Count(readCodexConfig(t, home), `[projects."`+repo+`"]`); n != 1 {
		t.Errorf("project tables = %d, want 1", n)
	}
}

func TestEnsureCodexProjectTrust_OptOut(t *testing.T) {
	home, repo := codexTrustEnv(t)
	t.Setenv("MUXCODE_CODEX_AUTO_TRUST", "0")

	added, err := EnsureCodexProjectTrust(repo)
	if err != nil || added {
		t.Fatalf("EnsureCodexProjectTrust = %v, %v; want false, nil", added, err)
	}
	if _, err := os.Stat(filepath.Join(home, "config.toml")); !os.IsNotExist(err) {
		t.Errorf("opt-out must write nothing, stat err = %v", err)
	}
}

// The 0.162 prompt must be answered with Enter alone: the injection's Escape
// preamble is that prompt's Quit.
func TestCodexSendWakeUp_AnswersV2TrustPromptWithoutEscape(t *testing.T) {
	session := "inject-codex-trust-v2"
	injectionTestSession(t, session)
	calls := stubInjectionPane(t, codexTrustPromptPaneV2, nil)

	err := (&CodexProvider{hooks: true}).SendWakeUp(session, "build", true)
	if !errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("trust prompt must defer with ErrInjectionSkipped, got %v", err)
	}
	keys := sentKeys(*calls)
	if len(keys) != 1 || !strings.HasSuffix(keys[0], " Enter") {
		t.Fatalf("trust prompt must receive exactly one Enter, sent %v", keys)
	}
}

// Negative control: the answered prompt stays in scrollback above a live
// composer, and must not be answered again.
func TestCodexTrustPromptLive_AnsweredV2PromptIsNotLive(t *testing.T) {
	answered := codexTrustPromptPaneV2 + "\n› Ask Codex to do anything\n\n  gpt-5.6-luna · ~/Repos/mkober/dotfiles\n"
	if codexTrustPromptLive(answered) {
		t.Error("an answered trust prompt in scrollback must not read as live")
	}
	if !codexTrustPromptLive(codexTrustPromptPaneV2) {
		t.Error("the live 0.162 trust prompt must be recognised")
	}
}
