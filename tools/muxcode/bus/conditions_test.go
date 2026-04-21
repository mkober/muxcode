package bus

import (
	"os"
	"testing"
)

func TestFileGlobMatch_Simple(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// Basic filepath.Match patterns
		{"*.ts", "foo.ts", true},
		{"*.ts", "foo.go", false},
		{"lib/*.ts", "lib/foo.ts", true},
		{"lib/*.ts", "lib/sub/foo.ts", false}, // * does NOT cross directories

		// ** patterns
		{"lib/**/*.ts", "lib/foo.ts", true},
		{"lib/**/*.ts", "lib/sub/foo.ts", true},
		{"lib/**/*.ts", "lib/a/b/c/foo.ts", true},
		{"lib/**/*.ts", "src/foo.ts", false},
		{"**/*.ts", "foo.ts", true},
		{"**/*.ts", "lib/foo.ts", true},
		{"**/*.ts", "a/b/c.ts", true},
		{"**/*.ts", "foo.go", false},

		// ** at end
		{"lib/**", "lib/foo.ts", true},
		{"lib/**", "lib/sub/foo.ts", true},

		// Exact match
		{"cdk.json", "cdk.json", true},
		{"cdk.json", "other.json", false},
	}

	for _, tt := range tests {
		got := fileGlobMatch(tt.pattern, tt.path)
		if got != tt.want {
			t.Errorf("fileGlobMatch(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestEvaluateConditions_Empty(t *testing.T) {
	passed, results := EvaluateConditions(nil, &ChainContext{})
	if !passed {
		t.Error("nil conditions should pass")
	}
	if results != nil {
		t.Error("nil conditions should return nil results")
	}

	passed, results = EvaluateConditions(map[string]any{}, &ChainContext{})
	if !passed {
		t.Error("empty conditions should pass")
	}
	if results != nil {
		t.Error("empty conditions should return nil results")
	}
}

func TestEvaluateConditions_NilContext(t *testing.T) {
	conditions := map[string]any{"files_match": "*.ts"}
	passed, _ := EvaluateConditions(conditions, nil)
	if !passed {
		t.Error("nil context should skip condition evaluation and pass")
	}
}

func TestEvaluateConditions_FilesMatch(t *testing.T) {
	ctx := &ChainContext{
		ChangedFiles: []string{"lib/constructs/foo.ts", "lib/stacks/bar.ts"},
	}

	// Should pass — matching files exist
	passed, results := EvaluateConditions(map[string]any{
		"files_match": "lib/**/*.ts",
	}, ctx)
	if !passed {
		t.Error("files_match should pass when matching files exist")
	}
	if len(results) != 1 || !results[0].Passed {
		t.Errorf("expected 1 passing result, got %v", results)
	}

	// Should fail — no matching files
	passed, results = EvaluateConditions(map[string]any{
		"files_match": "src/**/*.py",
	}, ctx)
	if passed {
		t.Error("files_match should fail when no matching files")
	}
	if len(results) != 1 || results[0].Passed {
		t.Errorf("expected 1 failing result, got %v", results)
	}
}

func TestEvaluateConditions_FilesNotMatch(t *testing.T) {
	ctx := &ChainContext{
		ChangedFiles: []string{"lib/constructs/foo.ts"},
	}

	// Should pass — no Python files
	passed, _ := EvaluateConditions(map[string]any{
		"files_not_match": "**/*.py",
	}, ctx)
	if !passed {
		t.Error("files_not_match should pass when no files match")
	}

	// Should fail — TS files exist
	passed, _ = EvaluateConditions(map[string]any{
		"files_not_match": "lib/**/*.ts",
	}, ctx)
	if passed {
		t.Error("files_not_match should fail when matching files exist")
	}
}

func TestEvaluateConditions_BranchMatch(t *testing.T) {
	ctx := &ChainContext{Branch: "feat/add-login"}

	passed, _ := EvaluateConditions(map[string]any{
		"branch_match": "^feat/",
	}, ctx)
	if !passed {
		t.Error("branch_match should pass for matching branch")
	}

	passed, _ = EvaluateConditions(map[string]any{
		"branch_match": "^main$",
	}, ctx)
	if passed {
		t.Error("branch_match should fail for non-matching branch")
	}
}

func TestEvaluateConditions_BranchNotMatch(t *testing.T) {
	ctx := &ChainContext{Branch: "feat/add-login"}

	passed, _ := EvaluateConditions(map[string]any{
		"branch_not_match": "^main$",
	}, ctx)
	if !passed {
		t.Error("branch_not_match should pass when branch doesn't match")
	}

	passed, _ = EvaluateConditions(map[string]any{
		"branch_not_match": "^feat/",
	}, ctx)
	if passed {
		t.Error("branch_not_match should fail when branch matches")
	}
}

func TestEvaluateConditions_EnvSet(t *testing.T) {
	os.Setenv("TEST_COND_VAR", "hello")
	defer os.Unsetenv("TEST_COND_VAR")

	passed, _ := EvaluateConditions(map[string]any{
		"env_set": "TEST_COND_VAR",
	}, &ChainContext{})
	if !passed {
		t.Error("env_set should pass when var is set")
	}

	passed, _ = EvaluateConditions(map[string]any{
		"env_set": "TEST_COND_NONEXISTENT_VAR",
	}, &ChainContext{})
	if passed {
		t.Error("env_set should fail when var is not set")
	}
}

func TestEvaluateConditions_EnvEquals(t *testing.T) {
	os.Setenv("TEST_COND_ENV", "production")
	defer os.Unsetenv("TEST_COND_ENV")

	passed, _ := EvaluateConditions(map[string]any{
		"env_equals": map[string]any{"name": "TEST_COND_ENV", "value": "production"},
	}, &ChainContext{})
	if !passed {
		t.Error("env_equals should pass when var matches")
	}

	passed, _ = EvaluateConditions(map[string]any{
		"env_equals": map[string]any{"name": "TEST_COND_ENV", "value": "staging"},
	}, &ChainContext{})
	if passed {
		t.Error("env_equals should fail when var doesn't match")
	}
}

func TestEvaluateConditions_OutputContains(t *testing.T) {
	ctx := &ChainContext{Output: "PASS: 42 tests passed\nFAIL: 0 tests failed"}

	passed, _ := EvaluateConditions(map[string]any{
		"output_contains": "42 tests passed",
	}, ctx)
	if !passed {
		t.Error("output_contains should pass when substring found")
	}

	passed, _ = EvaluateConditions(map[string]any{
		"output_contains": "error: compilation failed",
	}, ctx)
	if passed {
		t.Error("output_contains should fail when substring not found")
	}
}

func TestEvaluateConditions_ExitCode(t *testing.T) {
	ctx := &ChainContext{ExitCode: 2}

	passed, _ := EvaluateConditions(map[string]any{
		"exit_code": float64(2), // JSON numbers are float64
	}, ctx)
	if !passed {
		t.Error("exit_code should pass when code matches")
	}

	passed, _ = EvaluateConditions(map[string]any{
		"exit_code": float64(0),
	}, ctx)
	if passed {
		t.Error("exit_code should fail when code doesn't match")
	}
}

func TestEvaluateConditions_MultipleAND(t *testing.T) {
	ctx := &ChainContext{
		ChangedFiles: []string{"lib/constructs/foo.ts"},
		Branch:       "main",
	}

	// Both conditions pass
	passed, results := EvaluateConditions(map[string]any{
		"files_match":  "lib/**/*.ts",
		"branch_match": "^main$",
	}, ctx)
	if !passed {
		t.Error("AND conditions should pass when all match")
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// One fails
	passed, _ = EvaluateConditions(map[string]any{
		"files_match":  "lib/**/*.ts",
		"branch_match": "^feat/",
	}, ctx)
	if passed {
		t.Error("AND conditions should fail when any doesn't match")
	}
}

func TestEvaluateConditions_UnknownType(t *testing.T) {
	ctx := &ChainContext{}
	passed, results := EvaluateConditions(map[string]any{
		"unknown_cond": "value",
	}, ctx)
	if passed {
		t.Error("unknown condition type should fail")
	}
	if len(results) != 1 || results[0].Passed {
		t.Errorf("expected 1 failing result for unknown type, got %v", results)
	}
}

func TestValidateConditions(t *testing.T) {
	// Known types — no warnings
	warnings := ValidateConditions(map[string]any{
		"files_match":  "*.ts",
		"branch_match": "^main$",
	})
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for known types, got %v", warnings)
	}

	// Unknown types — should warn
	warnings = ValidateConditions(map[string]any{
		"files_match":  "*.ts",
		"custom_check": "value",
	})
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning for unknown type, got %v", warnings)
	}
}

func TestValidateConfig_Conditions(t *testing.T) {
	cfg := &MuxcodeConfig{
		EventChains: map[string]EventChain{
			"build": {
				OnSuccess: ChainActions{{
					SendTo:     "test",
					Action:     "test",
					Type:       "request",
					Conditions: map[string]any{"files_match": "*.ts", "bad_cond": "x"},
				}},
			},
		},
	}
	warnings := ValidateConfig(cfg)
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestResolveChain_WithConditions(t *testing.T) {
	cfg := &MuxcodeConfig{
		EventChains: map[string]EventChain{
			"build": {
				OnSuccess: ChainActions{{
					SendTo: "deploy",
					Action: "deploy",
					Type:   "request",
					Conditions: map[string]any{
						"files_match":  "lib/**/*.ts",
						"branch_match": "^main$",
					},
				}},
			},
		},
	}
	SetConfig(cfg)
	defer SetConfig(nil)

	// Conditions met
	ctx := &ChainContext{
		ChangedFiles: []string{"lib/stacks/app.ts"},
		Branch:       "main",
	}
	action := ResolveChain("build", "success", ctx)
	if action == nil {
		t.Fatal("expected action when conditions met")
	}
	if action.SendTo != "deploy" {
		t.Errorf("send_to = %q, want deploy", action.SendTo)
	}

	// Conditions not met — wrong branch
	ctx2 := &ChainContext{
		ChangedFiles: []string{"lib/stacks/app.ts"},
		Branch:       "feat/new-feature",
	}
	action = ResolveChain("build", "success", ctx2)
	if action != nil {
		t.Error("expected nil when conditions not met")
	}

	// Nil context — conditions skipped (backward compatible)
	action = ResolveChain("build", "success", nil)
	if action == nil {
		t.Fatal("expected action when context is nil (backward compat)")
	}
}

func TestResolveChain_ActionArrayFirstMatchWins(t *testing.T) {
	cfg := &MuxcodeConfig{
		EventChains: map[string]EventChain{
			"build": {
				OnSuccess: ChainActions{
					{
						SendTo: "deploy",
						Action: "deploy",
						Type:   "request",
						Conditions: map[string]any{
							"files_match":  "lib/**/*.ts",
							"branch_match": "^main$",
						},
					},
					{
						SendTo: "test",
						Action: "test",
						Type:   "request",
					},
				},
			},
		},
	}
	SetConfig(cfg)
	defer SetConfig(nil)

	// First action matches — infra files on main branch
	ctx := &ChainContext{
		ChangedFiles: []string{"lib/stacks/app.ts"},
		Branch:       "main",
	}
	action := ResolveChain("build", "success", ctx)
	if action == nil {
		t.Fatal("expected action")
	}
	if action.SendTo != "deploy" {
		t.Errorf("send_to = %q, want deploy (first match)", action.SendTo)
	}

	// First action fails (wrong branch) — falls through to unconditional second
	ctx2 := &ChainContext{
		ChangedFiles: []string{"lib/stacks/app.ts"},
		Branch:       "feat/new-feature",
	}
	action = ResolveChain("build", "success", ctx2)
	if action == nil {
		t.Fatal("expected fallback action")
	}
	if action.SendTo != "test" {
		t.Errorf("send_to = %q, want test (fallback)", action.SendTo)
	}

	// First action fails (no matching files) — falls through to unconditional second
	ctx3 := &ChainContext{
		ChangedFiles: []string{"README.md"},
		Branch:       "main",
	}
	action = ResolveChain("build", "success", ctx3)
	if action == nil {
		t.Fatal("expected fallback action")
	}
	if action.SendTo != "test" {
		t.Errorf("send_to = %q, want test (fallback)", action.SendTo)
	}

	// Nil context — conditions skipped, first action returned
	action = ResolveChain("build", "success", nil)
	if action == nil {
		t.Fatal("expected first action with nil context")
	}
	if action.SendTo != "deploy" {
		t.Errorf("send_to = %q, want deploy (nil ctx returns first)", action.SendTo)
	}
}

func TestResolveChain_AllConditionsFail(t *testing.T) {
	cfg := &MuxcodeConfig{
		EventChains: map[string]EventChain{
			"build": {
				OnSuccess: ChainActions{
					{
						SendTo:     "deploy",
						Action:     "deploy",
						Type:       "request",
						Conditions: map[string]any{"branch_match": "^main$"},
					},
					{
						SendTo:     "test",
						Action:     "test",
						Type:       "request",
						Conditions: map[string]any{"branch_match": "^release/"},
					},
				},
			},
		},
	}
	SetConfig(cfg)
	defer SetConfig(nil)

	// Neither condition matches — no fallback
	ctx := &ChainContext{Branch: "feat/new-feature"}
	action := ResolveChain("build", "success", ctx)
	if action != nil {
		t.Errorf("expected nil when all conditions fail, got %+v", action)
	}
}

func TestResolveChainVerbose_ActionArray(t *testing.T) {
	cfg := &MuxcodeConfig{
		EventChains: map[string]EventChain{
			"build": {
				OnSuccess: ChainActions{
					{
						SendTo:     "deploy",
						Action:     "deploy",
						Type:       "request",
						Conditions: map[string]any{"branch_match": "^main$"},
					},
					{
						SendTo: "test",
						Action: "test",
						Type:   "request",
					},
				},
			},
		},
	}
	SetConfig(cfg)
	defer SetConfig(nil)

	// First action fails, falls through to unconditional — results should contain first action's conditions
	ctx := &ChainContext{Branch: "feat/x"}
	action, results := ResolveChainVerbose("build", "success", ctx)
	if action == nil {
		t.Fatal("expected fallback action")
	}
	if action.SendTo != "test" {
		t.Errorf("send_to = %q, want test", action.SendTo)
	}
	if len(results) == 0 {
		t.Error("expected condition results from first action evaluation")
	}
	// The first action's branch_match should have failed
	found := false
	for _, r := range results {
		if r.Type == "branch_match" && !r.Passed {
			found = true
		}
	}
	if !found {
		t.Error("expected failed branch_match result in verbose output")
	}
}

func TestFormatConditionResults(t *testing.T) {
	results := []ConditionResult{
		{Type: "files_match", Pattern: "lib/**/*.ts", Passed: true, Detail: "matched: lib/foo.ts"},
		{Type: "branch_match", Pattern: "^main$", Passed: false, Detail: `branch "feat/x" does not match`},
	}
	output := FormatConditionResults(results)
	if output == "" {
		t.Error("expected non-empty output")
	}
	if !contains(output, "[PASS]") || !contains(output, "[FAIL]") {
		t.Errorf("expected PASS and FAIL labels in output: %s", output)
	}
}

func TestFormatConditionResults_Empty(t *testing.T) {
	output := FormatConditionResults(nil)
	if !contains(output, "no conditions") {
		t.Errorf("expected 'no conditions' for nil input, got: %s", output)
	}
}

func TestExpandMessageWithContext(t *testing.T) {
	ctx := &ChainContext{
		Branch:       "main",
		ChangedFiles: []string{"lib/foo.ts", "lib/bar.ts"},
	}
	msg := ExpandMessageWithContext("Build on ${branch} changed ${changed_files}", "0", "make", ctx)
	if !contains(msg, "main") {
		t.Errorf("expected branch in message: %s", msg)
	}
	if !contains(msg, "lib/foo.ts") {
		t.Errorf("expected changed files in message: %s", msg)
	}
}

func TestExpandMessageWithContext_NilContext(t *testing.T) {
	msg := ExpandMessageWithContext("exit ${exit_code}: ${command}", "1", "make build", nil)
	if msg != "exit 1: make build" {
		t.Errorf("unexpected message: %s", msg)
	}
}

func TestFormatChangedFiles_Truncation(t *testing.T) {
	files := make([]string, 15)
	for i := range files {
		files[i] = "file" + string(rune('a'+i)) + ".ts"
	}
	result := formatChangedFiles(files)
	if !contains(result, "5 more") {
		t.Errorf("expected truncation indicator, got: %s", result)
	}
}

func TestBuildChainContextFromFlags(t *testing.T) {
	ctx := BuildChainContextFromFlags("a.ts,b.go", "feat/x", "test output", "42")
	if len(ctx.ChangedFiles) != 2 {
		t.Errorf("expected 2 files, got %d", len(ctx.ChangedFiles))
	}
	if ctx.Branch != "feat/x" {
		t.Errorf("branch = %q, want feat/x", ctx.Branch)
	}
	if ctx.Output != "test output" {
		t.Errorf("output = %q, want 'test output'", ctx.Output)
	}
	if ctx.ExitCode != 42 {
		t.Errorf("exit_code = %d, want 42", ctx.ExitCode)
	}
}

// contains is a test helper for substring matching.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
