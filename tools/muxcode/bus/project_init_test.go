package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// isolateProjectInit gives a test no user config and the built-in patterns.
func isolateProjectInit(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MUXCODE_CONFIG", "")
	for _, k := range initPatternKeys {
		t.Setenv(k.env, "")
	}
}

// swiftRepo lays out a project shaped like tibo: a SwiftPM package and an
// XcodeGen spec below the root, nothing at it.
func swiftRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "packages", "Kit", "Package.swift"), "// swift-tools-version:5.9\n")
	writeFile(t, filepath.Join(dir, "apps", "ios", "project.yml"), "name: App\ntargets:\n  App: {}\n")
	return dir
}

func TestInitProjectFiles_CreatesMissingFromDetection(t *testing.T) {
	isolateProjectInit(t)
	dir := swiftRepo(t)

	res := InitProjectFiles(dir, DetectProject(dir))
	want := []string{initConfigPath, initSettingsPath, initClaudeMDPath}
	if !reflect.DeepEqual(res.Created, want) || len(res.Skipped) != 0 {
		t.Fatalf("result = %+v, want Created %v and nothing skipped", res, want)
	}

	cfg := GetShellConfig(dir)
	for _, k := range initPatternKeys {
		if got, want := cfg[k.env], strings.Join(k.defaults, "|"); got != want {
			t.Errorf("config %s round-trips as %q, want %q", k.env, got, want)
		}
	}
	if !strings.Contains(cfg["MUXCODE_TEST_PATTERNS"], "swift test") {
		t.Errorf("test patterns lack swift test: %q", cfg["MUXCODE_TEST_PATTERNS"])
	}

	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	data, err := os.ReadFile(filepath.Join(dir, initSettingsPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("settings.json does not parse: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(settings.Permissions.Allow, toolchainPermissions["swift"]) {
		t.Errorf("allow = %v, want the swift toolchain queries %v", settings.Permissions.Allow, toolchainPermissions["swift"])
	}

	md, err := os.ReadFile(filepath.Join(dir, initClaudeMDPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"# " + filepath.Base(dir) + "\n", "swift build", "swift test", "xcodegen generate", "packages/Kit"} {
		if !strings.Contains(string(md), s) {
			t.Errorf("CLAUDE.md missing %q:\n%s", s, md)
		}
	}
}

// TestInitProjectFiles_ConfigCarriesNoRepositoryText: a directory name is
// repository data, and one holding a newline once wrote an active assignment
// out of the config's "# Detected:" comment (review, 2026-11-02). The fixture
// check proves the payload reached detection, so the test can still fail.
func TestInitProjectFiles_ConfigCarriesNoRepositoryText(t *testing.T) {
	isolateProjectInit(t)
	t.Setenv("MUXCODE_BUILD_CLI", "")
	dir := t.TempDir()
	evil := "x\nMUXCODE_BUILD_CLI=codex\necho pwned\n"
	writeFile(t, filepath.Join(dir, "packages", evil, "Package.swift"), "// swift-tools-version:5.9\n")

	types := DetectProject(dir)
	if len(types) != 1 || !slices.ContainsFunc(types[0].Indicators, func(s string) bool { return strings.Contains(s, "MUXCODE_BUILD_CLI") }) {
		t.Fatalf("fixture: detection did not record the newline path: %+v", types)
	}
	InitProjectFiles(dir, types)

	if v, ok := GetShellConfig(dir)["MUXCODE_BUILD_CLI"]; ok {
		t.Errorf("config sets MUXCODE_BUILD_CLI=%q from a directory name", v)
	}
	data, err := os.ReadFile(filepath.Join(dir, initConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		key, _, _ := strings.Cut(line, "=")
		if !strings.HasPrefix(key, "MUXCODE_") || !strings.HasSuffix(key, "_PATTERNS") {
			t.Errorf("config has a non-pattern active line: %q", line)
		}
	}
}

// TestToolchainPermissions_PassEditGuard: only edit consults these, and a
// permission edit's guard refuses would be dead — or, were the guard to
// regress, an open door to a build edit must delegate.
func TestToolchainPermissions_PassEditGuard(t *testing.T) {
	for typ, perms := range toolchainPermissions {
		for _, p := range perms {
			if !strings.HasPrefix(p, "Bash(") || !strings.HasSuffix(p, ")") {
				t.Errorf("%s: %q is not a Bash permission", typ, p)
				continue
			}
			cmd := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(p, "Bash("), ")"), "*"))
			if d := CheckEditGuard(cmd); d != nil {
				t.Errorf("%s: %q is refused by edit's guard: %s", typ, cmd, d.Reason)
			}
		}
	}
	for _, blocked := range []string{"swift build", "swift test", "xcodebuild -list"} {
		if CheckEditGuard(blocked) == nil {
			t.Errorf("guard control: %q should be refused, or this test cannot catch a blocked permission", blocked)
		}
	}
}

// TestInitProjectFiles_LeavesExistingByteIdentical is the negative control: a
// configured project — every file present, one as a dangling symlink, and the
// stub's alternate location in use — gets nothing written.
func TestInitProjectFiles_LeavesExistingByteIdentical(t *testing.T) {
	isolateProjectInit(t)
	cases := map[string]func(dir string){
		"all three present": func(dir string) {
			writeFile(t, filepath.Join(dir, initConfigPath), "MUXCODE_TEST_PATTERNS='mine'\n")
			writeFile(t, filepath.Join(dir, initSettingsPath), "{\"permissions\":{}}\n")
			writeFile(t, filepath.Join(dir, initClaudeMDPath), "# mine\n")
		},
		"symlinks and .claude/CLAUDE.md": func(dir string) {
			for _, rel := range []string{initConfigPath, initSettingsPath} {
				os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755)
				if err := os.Symlink(filepath.Join(dir, "missing-target"), filepath.Join(dir, rel)); err != nil {
					t.Fatal(err)
				}
			}
			writeFile(t, filepath.Join(dir, ".claude", "CLAUDE.md"), "# mine\n")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			dir := swiftRepo(t)
			setup(dir)
			before := snapshotTree(t, dir)

			res := InitProjectFiles(dir, DetectProject(dir))

			if len(res.Created) != 0 || len(res.Skipped) != 0 {
				t.Errorf("result = %+v, want nothing created or skipped", res)
			}
			if after := snapshotTree(t, dir); !reflect.DeepEqual(before, after) {
				t.Errorf("tree changed:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

// snapshotTree maps every path under dir to its bytes, or a symlink's target.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			out[rel] = "-> " + target
		case info.IsDir():
			out[rel] = "dir"
		default:
			b, _ := os.ReadFile(p)
			out[rel] = string(b)
		}
		return nil
	})
	return out
}

func TestInitProjectFiles_PartialFillsOnlyTheGap(t *testing.T) {
	isolateProjectInit(t)
	dir := swiftRepo(t)
	writeFile(t, filepath.Join(dir, initClaudeMDPath), "# mine\n")

	res := InitProjectFiles(dir, DetectProject(dir))

	if want := []string{initConfigPath, initSettingsPath}; !reflect.DeepEqual(res.Created, want) {
		t.Errorf("Created = %v, want %v", res.Created, want)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, initClaudeMDPath)); string(b) != "# mine\n" {
		t.Errorf("existing CLAUDE.md rewritten: %q", b)
	}
}

// TestInitProjectFiles_LayersOverHomeConfig: a home config no longer withholds
// the project's. The project file is written, the home file's role CLI choice
// still applies through it — before layering it would have relaunched tibo's
// Codex agents on the default provider — and the project's patterns win.
func TestInitProjectFiles_LayersOverHomeConfig(t *testing.T) {
	isolateProjectInit(t)
	t.Setenv("MUXCODE_BUILD_CLI", "")
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".config", "muxcode", "config"),
		"MUXCODE_BUILD_CLI=codex\nMUXCODE_TEST_PATTERNS='home-only'\n")
	dir := swiftRepo(t)

	res := InitProjectFiles(dir, DetectProject(dir))

	if want := []string{initConfigPath, initSettingsPath, initClaudeMDPath}; !reflect.DeepEqual(res.Created, want) || len(res.Skipped) != 0 {
		t.Fatalf("result = %+v, want Created %v and nothing skipped", res, want)
	}
	cfg := GetShellConfig(dir)
	if got := cfg["MUXCODE_BUILD_CLI"]; got != "codex" {
		t.Errorf("home MUXCODE_BUILD_CLI = %q through the project config, want codex", got)
	}
	if got := cfg["MUXCODE_TEST_PATTERNS"]; !strings.Contains(got, "swift test") {
		t.Errorf("MUXCODE_TEST_PATTERNS = %q, want the project's patterns over the home value", got)
	}
}

// TestInitProjectFiles_WithholdsConfigUnderMuxcodeConfig: a MUXCODE_CONFIG file
// is read alone, so a project config would be ignored and is not written.
func TestInitProjectFiles_WithholdsConfigUnderMuxcodeConfig(t *testing.T) {
	isolateProjectInit(t)
	p := filepath.Join(t.TempDir(), "config")
	writeFile(t, p, "MUXCODE_BUILD_CLI=codex\n")
	t.Setenv("MUXCODE_CONFIG", p)
	dir := swiftRepo(t)

	res := InitProjectFiles(dir, DetectProject(dir))

	if pathPresent(filepath.Join(dir, initConfigPath)) {
		t.Error(".muxcode/config created although MUXCODE_CONFIG means it is ignored")
	}
	if len(res.Skipped) != 1 || !strings.HasPrefix(res.Skipped[0], initConfigPath+": ") || !strings.Contains(res.Skipped[0], "MUXCODE_CONFIG names") {
		t.Errorf("Skipped = %v, want one config entry naming MUXCODE_CONFIG", res.Skipped)
	}
	if want := []string{initSettingsPath, initClaudeMDPath}; !reflect.DeepEqual(res.Created, want) {
		t.Errorf("Created = %v, want %v", res.Created, want)
	}
}

func TestInitProjectFiles_NothingDetectedWritesNothing(t *testing.T) {
	isolateProjectInit(t)
	dir := t.TempDir()

	res := InitProjectFiles(dir, DetectProject(dir))

	if len(res.Created) != 0 || len(res.Skipped) != 0 {
		t.Errorf("result = %+v, want empty", res)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("empty project gained %d entries", len(entries))
	}
}

func TestAnnounceProjectInit(t *testing.T) {
	t.Run("created files notify edit", func(t *testing.T) {
		session := testSession(t)
		announceProjectInit(session, ProjectInitResult{
			Created: []string{initSettingsPath, initClaudeMDPath},
			Skipped: []string{initConfigPath + ": MUXCODE_CONFIG names /x, which muxcode reads instead"},
		})

		msgs, _ := Peek(session, "edit")
		if len(msgs) != 1 || msgs[0].Action != "project-init" || msgs[0].Type != "request" {
			t.Fatalf("edit inbox = %+v, want one project-init request", msgs)
		}
		for _, s := range []string{initSettingsPath, initClaudeMDPath, "untracked", "never commit", "MUXCODE_CONFIG names"} {
			if !strings.Contains(msgs[0].Payload, s) {
				t.Errorf("notice missing %q: %s", s, msgs[0].Payload)
			}
		}
		events := lifecycleEvents(t, session)
		for _, e := range []string{"project-init", "project-init-skipped"} {
			if !slices.Contains(events, e) {
				t.Errorf("lifecycle events %v lack %s", events, e)
			}
		}
	})

	t.Run("skip only is logged, not sent", func(t *testing.T) {
		session := testSession(t)
		announceProjectInit(session, ProjectInitResult{Skipped: []string{initConfigPath + ": x"}})

		if msgs, _ := Peek(session, "edit"); len(msgs) != 0 {
			t.Errorf("edit told about a launch that created nothing: %+v", msgs)
		}
		events := lifecycleEvents(t, session)
		if !slices.Contains(events, "project-init-skipped") || slices.Contains(events, "project-init") {
			t.Errorf("lifecycle events = %v, want project-init-skipped alone", events)
		}
	})
}

func lifecycleEvents(t *testing.T, session string) []string {
	t.Helper()
	entries, err := ReadLifecycleLog(session)
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, e := range entries {
		events = append(events, e.Event)
	}
	return events
}
