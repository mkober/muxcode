package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MUX-157: build, test and review validate and never author. These began as
// Phase 1 characterizations asserting the writes passed; Phase 2's guard
// flipped them. Each refusal is paired with the negative controls the spec
// names — edit, plan's docs/, watch and analyze, and the paths the Phase 1
// inventory found a validation role legitimately writing.

var validationRoleProbe = []string{"build", "test", "review"}

// inCheckout runs the test from the root of a scratch git checkout — the
// guard resolves paths against the working directory, as the hook does in
// an agent's pane.
func inCheckout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	return dir
}

func fileEvent(tool, path string) *ToolEvent {
	if tool == "NotebookEdit" {
		return &ToolEvent{ToolName: tool, ToolInput: ToolInput{NotebookPath: path}}
	}
	return &ToolEvent{ToolName: tool, ToolInput: ToolInput{FilePath: path}}
}

func bashToolEvent(cmd string) *ToolEvent {
	return &ToolEvent{ToolName: "Bash", ToolInput: ToolInput{Command: cmd}}
}

func TestSourceWriteGuardRefusesValidationRoleFileWrites(t *testing.T) {
	root := inCheckout(t)
	for _, role := range validationRoleProbe {
		for _, ev := range []*ToolEvent{
			fileEvent("Write", "tools/muxcode/bus/x.go"),
			fileEvent("Edit", filepath.Join(root, "tools/muxcode/bus/x.go")),
			fileEvent("NotebookEdit", "notebooks/x.ipynb"),
			fileEvent("Write", "$source.go"),   // a file tool's path is literal: no variable,
			fileEvent("Write", "~/source.go"),  // no tilde expansion,
			fileEvent("Write", `"/tmp/x.txt"`), // no quote removal
		} {
			d := GuardDecisionFor(role, ev)
			if d == nil || !d.Blocked {
				t.Errorf("%s %s to source must be refused", role, ev.ToolName)
				continue
			}
			if !strings.Contains(d.Reason, "the "+role+" agent validates and never authors") ||
				!strings.Contains(d.Reason, "muxcode send edit") || d.Path == "" {
				t.Errorf("%s %s: the refusal must name the role, the edit delegation and the path: %+v", role, ev.ToolName, d)
			}
		}
	}
}

func TestSourceWriteGuardRefusesValidationRoleShellWrites(t *testing.T) {
	inCheckout(t)
	for _, cmd := range []string{
		"sed -i 's/a/b/' bus/x.go",
		"perl -pi -e 's/a/b/' bus/x.go",
		"gofmt -w bus/x.go",
		"goimports -w .",
		"npx eslint --fix src/",
		"npx prettier --write src/app.ts",
		"echo x > bus/x.go",
		"cat > bus/x.go <<'EOF'\npackage bus\nEOF",
		"go test ./... | tee results.txt",
		"gofmt -w /tmp/a.go bus/x.go", // mixed targets: the in-tree one decides
		"npx eslint --fix",            // no operand rewrites the working directory
		"sed -i '' 's/a/b/' bus/x.go",
	} {
		for _, role := range validationRoleProbe {
			if d := GuardDecisionFor(role, bashToolEvent(cmd)); d == nil || !d.Blocked || d.Path == "" {
				t.Errorf("%s: %q must be refused as a source write, got %+v", role, cmd, d)
			}
		}
	}
	if d := GuardDecisionFor("test", bashToolEvent("sed -i 's/a/b/' bus/x.go")); d == nil || d.Path != "bus/x.go" {
		t.Errorf("an in-place refusal names the file it rewrites, not the command: %+v", d)
	}
}

// Negative controls: the guard binds the three validation roles' writes into
// the checkout and nothing else.
func TestSourceWriteGuardLeavesOtherWritesAlone(t *testing.T) {
	inCheckout(t)
	outside := filepath.Join(t.TempDir(), "notes.txt")
	allowed := []struct {
		role string
		ev   *ToolEvent
	}{
		{"edit", fileEvent("Write", "tools/muxcode/bus/x.go")},
		{"edit", bashToolEvent("gofmt -w bus/x.go")}, // edit's bash rules are unchanged
		{"plan", fileEvent("Write", "docs/requirements/backlog/x.md")},
		{"watch", fileEvent("Write", "tools/muxcode/bus/x.go")},
		{"analyze", fileEvent("Write", "tools/muxcode/bus/x.go")},
		{"build", fileEvent("Write", "bin/muxcode")},
		{"build", bashToolEvent("./build.sh 2>&1")},
		{"build", bashToolEvent("go -C tools/muxcode vet ./... >/dev/null 2>&1")},
		{"test", bashToolEvent("./test.sh > /tmp/muxcode-test.out 2>&1")},
		{"test", bashToolEvent("gofmt -l .")},
		{"review", fileEvent("Write", "/tmp/muxcode-review-1.txt")},
		{"review", bashToolEvent("tmpfile=$(mktemp /tmp/muxcode-review-XXXXXX.txt); cat > \"$tmpfile\" <<'EOF'\nmust-fix: x\nEOF")},
		{"review", bashToolEvent(`muxcode send edit review "found a sed -i and a gofmt -w --fix in x.go" --type response --reply-to 1-edit-a`)},
		{"review", fileEvent("Write", outside)},
		{"test", fileEvent("Write", "/tmp/muxcode-x.txt")},
		{"test", bashToolEvent("echo x > ~/.cache/muxcode/x")},     // the shell expands ~
		{"review", bashToolEvent(`echo x > "/tmp/muxcode x.txt"`)}, // the shell removes quotes
		{"test", bashToolEvent(`echo x > "$tmpfile"`)},             // shell variable: unresolvable, passes (MUX-197)
		{"review", bashToolEvent("sed -i 's/a/b/' /tmp/muxcode-review.txt")},
		{"review", bashToolEvent("sed -i '' 's/a/b/' /tmp/muxcode-review.txt")},
		{"build", bashToolEvent("gofmt -w bin/generated.go")},
		{"test", bashToolEvent("npx prettier --write /tmp/x.ts")},
	}
	for _, c := range allowed {
		if d := GuardDecisionFor(c.role, c.ev); d != nil && d.Blocked {
			t.Errorf("%s %s %s must pass: %s", c.role, c.ev.ToolName, c.ev.ToolInput.FilePath+c.ev.ToolInput.Command, d.Reason)
		}
	}
}

// OpenCode runs no PreToolUse guard: the validation roles carry the rule as
// path-aware edit rules — the checkout denied, outside it and the role's own
// output re-allowed after, since the last match wins — and their
// shell-writer deny patterns. Edit is the control on both: its edit tool
// stays allowed, and the probe sees its delegation denies.
func TestValidationRolesCarryOpenCodeWriteDeny(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	for _, role := range validationRoleProbe {
		perm := translateToolProfile(role)
		deny, outside := strings.Index(perm, "\"*\": deny"), strings.Index(perm, "\"/*\": allow")
		if !strings.Contains(perm, "  edit:\n") || deny < 0 || outside < deny || strings.Contains(perm, "edit: allow") || strings.Contains(perm, "edit: deny") {
			t.Errorf("%s must deny the checkout and then re-allow outside it:\n%s", role, perm)
		}
		if bin := strings.Index(perm, "\"bin/*\": allow"); (role == "build") != (bin > deny) {
			t.Errorf("%s: bin/ re-allowed = %v, want %v (build's own output only):\n%s", role, bin > deny, role == "build", perm)
		}
		for _, p := range sourceWriteDenyTools {
			if !strings.Contains(perm, fmt.Sprintf("%q: deny", p)) {
				t.Errorf("%s lacks the deny rule %q", role, p)
			}
		}
	}
	edit := translateToolProfile("edit")
	if !strings.Contains(edit, "edit: allow") || strings.Contains(edit, "edit: deny") || !strings.Contains(edit, ": deny") {
		t.Errorf("control: edit keeps its edit tool and its delegation denies:\n%s", edit)
	}
	if strings.Contains(translateToolProfile("watch"), "edit: deny") {
		t.Error("control: watch is not a validation role (Decision 1)")
	}
}
