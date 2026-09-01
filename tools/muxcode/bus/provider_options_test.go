package bus

import (
	"strings"
	"testing"
)

func TestAvailableProviders_Count(t *testing.T) {
	providers := AvailableProviders()
	if len(providers) != 4 {
		t.Errorf("AvailableProviders() returned %d providers, want 4", len(providers))
	}
}

func TestAvailableProviders_CLIs(t *testing.T) {
	providers := AvailableProviders()
	expectedCLIs := []string{"claude", "opencode", "codex", "local"}
	for i, expected := range expectedCLIs {
		if i >= len(providers) {
			t.Fatalf("missing provider at index %d", i)
		}
		if providers[i].CLI != expected {
			t.Errorf("providers[%d].CLI = %q, want %q", i, providers[i].CLI, expected)
		}
	}
}

func TestAvailableProviders_Names(t *testing.T) {
	providers := AvailableProviders()
	expectedNames := []string{"Claude Code", "OpenCode", "Codex", "Local (Ollama)"}
	for i, expected := range expectedNames {
		if i >= len(providers) {
			t.Fatalf("missing provider at index %d", i)
		}
		if providers[i].Name != expected {
			t.Errorf("providers[%d].Name = %q, want %q", i, providers[i].Name, expected)
		}
	}
}

func TestAvailableProviders_Defaults(t *testing.T) {
	providers := AvailableProviders()
	tests := []struct {
		cli     string
		wantDef string
	}{
		{"claude", "claude-sonnet-5"},
		{"opencode", "opencode-go/minimax-m3"},
		{"codex", "gpt-5.5"},
	}
	for _, tt := range tests {
		p := ProviderByCLI(providers, tt.cli)
		if p == nil {
			t.Errorf("ProviderByCLI(%q) returned nil", tt.cli)
			continue
		}
		if p.Default != tt.wantDef {
			t.Errorf("ProviderByCLI(%q).Default = %q, want %q", tt.cli, p.Default, tt.wantDef)
		}
	}
}

// Asserted as properties rather than exact counts: AvailableProviders reads the
// user's models.conf, so a count pinned here fails on any machine whose model
// list has been edited — including every time the shipped list changes.
func TestAvailableProviders_Models(t *testing.T) {
	providers := AvailableProviders()
	for _, cli := range []string{"claude", "opencode"} {
		p := ProviderByCLI(providers, cli)
		if p == nil {
			t.Fatalf("%s provider not found", cli)
		}
		if len(p.Models) == 0 {
			t.Errorf("%s offers no models", cli)
		}
		if p.Default == "" {
			t.Errorf("%s has no default model", cli)
		}
	}

	// OpenCode ids are meaningless to the CLI without their provider prefix.
	opencode := ProviderByCLI(providers, "opencode")
	for _, m := range opencode.Models {
		if !strings.Contains(m, "/") {
			t.Errorf("opencode model %q is missing its provider prefix", m)
		}
	}
}

func TestProviderByIndex(t *testing.T) {
	providers := AvailableProviders()

	// Valid index
	p := ProviderByIndex(providers, 0)
	if p == nil {
		t.Fatal("ProviderByIndex(0) returned nil")
	}
	if p.CLI != "claude" {
		t.Errorf("ProviderByIndex(0).CLI = %q, want %q", p.CLI, "claude")
	}

	// Out of range
	if ProviderByIndex(providers, -1) != nil {
		t.Error("ProviderByIndex(-1) should return nil")
	}
	if ProviderByIndex(providers, 100) != nil {
		t.Error("ProviderByIndex(100) should return nil")
	}
}

func TestProviderByCLI(t *testing.T) {
	providers := AvailableProviders()

	// Found
	p := ProviderByCLI(providers, "opencode")
	if p == nil {
		t.Fatal("ProviderByCLI(opencode) returned nil")
	}
	if p.Name != "OpenCode" {
		t.Errorf("ProviderByCLI(opencode).Name = %q, want %q", p.Name, "OpenCode")
	}

	// Not found
	if ProviderByCLI(providers, "nonexistent") != nil {
		t.Error("ProviderByCLI(nonexistent) should return nil")
	}
}

func TestIsProviderInstalled_Claude(t *testing.T) {
	// claude should be installed in the dev environment
	installed := isProviderInstalled("claude")
	// We can't assert the value since it depends on the environment,
	// but we verify it doesn't panic
	_ = installed
}

func TestIsProviderInstalled_Local(t *testing.T) {
	// "local" maps to "ollama" binary
	installed := isProviderInstalled("local")
	_ = installed
}

// stubWindowList answers list-windows with the given index:name census.
func stubWindowList(t *testing.T, census string) {
	t.Helper()
	orig := tmuxOutputRunner
	t.Cleanup(func() { tmuxOutputRunner = orig })
	tmuxOutputRunner = func(args ...string) (string, error) {
		return census, nil
	}
}

// WindowFKey must derive labels from what the bindings select —
// window_index for F1–F10, spawn slot order for F11/F12 — never list
// position: with the research hold window at index 0, position-derived
// labels were all one too high and advertised commit as F11 before that
// key existed. Spawn slots follow ascending index (non-contiguous here,
// so a hardcoded 11/12 mapping is caught), and a third spawn has no key.
func TestWindowFKey_ByIndexNotPosition(t *testing.T) {
	stubWindowList(t, "0:research\n1:plan\n2:edit\n3:build\n5:my:notes\n10:commit\n14:spawn-bbb22222\n11:spawn-aaa11111\n17:spawn-ccc33333")

	tests := []struct {
		window, want string
	}{
		{"research", ""},          // index 0 — no binding
		{"plan", "F1"},            // was F2 under position derivation
		{"edit", "F2"},            // was F3
		{"my:notes", "F5"},        // colon in the name survives the census parse
		{"commit", "F10"},         // was mislabeled F11
		{"spawn-aaa11111", "F11"}, // first spawn slot (lowest index)
		{"spawn-bbb22222", "F12"}, // second slot despite later listing
		{"spawn-ccc33333", ""},    // third spawn — no binding
		{"nonexistent-win", ""},   // documented not-found value
	}
	for _, tt := range tests {
		if got := WindowFKey("s", tt.window); got != tt.want {
			t.Errorf("WindowFKey(%q) = %q, want %q", tt.window, got, tt.want)
		}
	}
}

// Observed 2026-09-01 (MUX-134): the spawn at index 12 is selected by F11, not the raw-index F12.
func TestWindowFKey_RawIndexDivergence(t *testing.T) {
	stubWindowList(t, "0:auto\n1:plan\n2:edit\n11:research\n12:spawn-aaa11111")

	got := WindowFKey("s", "spawn-aaa11111")
	if got == "F12" {
		t.Error("spawn at index 12 labelled F12 — the raw-index F#I divergence is back; F12's binding selects nothing here")
	}
	if got != "F11" {
		t.Errorf("spawn at index 12 = %q, want F11 (sole spawn, first slot)", got)
	}
	if got := WindowFKey("s", "research"); got != "" {
		t.Errorf("non-spawn at index 11 = %q, want empty — no binding selects it, a key label would lie", got)
	}
}

// RefreshWindowFKeyLabels must reconcile @muxcode_fkey to what the
// bindings select, touching only drifted windows: a stale F11 on a
// non-spawn window at index 11 is CLEARED (the lying-label case, user
// report 2026-09-01), a spawn past index 10 gains its slot label, and
// windows already correct get no set-option at all — the sweep runs
// every poll, so a stable layout must cost one read and zero writes.
func TestRefreshWindowFKeyLabels_DiffsOnly(t *testing.T) {
	// Census fields are index:label:name — name LAST because it may
	// contain colons (my:notes pins that a colon name parses intact).
	stubWindowList(t, "0::auto\n1:F1:plan\n2::edit\n11:F11:research\n12::spawn-aaa11111\n13:F13:my:notes")
	var calls [][]string
	origRun := tmuxRunner
	t.Cleanup(func() { tmuxRunner = origRun })
	tmuxRunner = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}

	n, err := RefreshWindowFKeyLabels("s")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if n != 4 {
		t.Errorf("changed = %d, want 4 (edit gains F2, research clears F11, spawn gains F11, colon-named window clears F13); calls: %v", n, calls)
	}
	set := map[string]string{}
	for _, c := range calls {
		if len(c) >= 6 && c[0] == "set-option" {
			set[c[3]] = c[5]
		}
	}
	want := map[string]string{"s:2": "F2", "s:11": "", "s:12": "F11", "s:13": ""}
	for target, label := range want {
		got, ok := set[target]
		if !ok || got != label {
			t.Errorf("set-option for %s = %q (present=%v), want %q", target, got, ok, label)
		}
	}
	if _, touched := set["s:1"]; touched {
		t.Error("plan already correct at F1 but was rewritten — the sweep must be diff-only")
	}
	if _, touched := set["s:0"]; touched {
		t.Error("auto has no binding and no stale label but was written")
	}
}

// A cleanup that shifts a surviving spawn's slot (second→first) must
// rewrite its stale F12 to F11 — a slot CHANGE, not just a lost key (MUX-134 Phase 3).
func TestRefreshWindowFKeyLabels_SlotShiftAfterCleanup(t *testing.T) {
	stubWindowList(t, "1:F1:plan\n12:F12:spawn-bbb22222")
	var calls [][]string
	origRun := tmuxRunner
	t.Cleanup(func() { tmuxRunner = origRun })
	tmuxRunner = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}

	n, err := RefreshWindowFKeyLabels("s")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if n != 1 {
		t.Errorf("changed = %d, want 1 (surviving spawn shifts F12→F11); calls: %v", n, calls)
	}
	set := map[string]string{}
	for _, c := range calls {
		if len(c) >= 6 && c[0] == "set-option" {
			set[c[3]] = c[5]
		}
	}
	if got, ok := set["s:12"]; !ok || got != "F11" {
		t.Errorf("set-option for s:12 = %q (present=%v), want F11", got, ok)
	}
	if _, touched := set["s:1"]; touched {
		t.Error("plan already correct at F1 but was rewritten — sweep must stay diff-only")
	}
}

// Negative control: with no index-0 window the mapping is identity, so
// a fix that merely subtracted one from position would be caught here.
func TestWindowFKey_NoHoldWindow(t *testing.T) {
	stubWindowList(t, "1:plan\n2:edit\n3:build")

	if got := WindowFKey("s", "plan"); got != "F1" {
		t.Errorf("plan = %q, want F1", got)
	}
	if got := WindowFKey("s", "build"); got != "F3" {
		t.Errorf("build = %q, want F3", got)
	}
}
