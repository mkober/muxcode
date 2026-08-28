package bus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSpecOpenItems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.md")
	content := "# T\n\n" +
		"- [x] done item\n" +
		"- [ ] open one\n" +
		"  - [ ] nested open\n" +
		"- [X] loudly done\n" +
		"prose mentioning - [ ] mid-line is not a checkbox\n" +
		"```markdown\n" +
		"- [ ] fenced example must not count\n" +
		"```\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	count, names, err := SpecOpenItems(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (checked and mid-line rows must not count)", count)
	}
	if len(names) != 2 || names[0] != "open one" || names[1] != "nested open" {
		t.Errorf("names = %v, want [open one, nested open]", names)
	}
}

func TestSpecOpenItemsFullyChecked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(path, []byte("# T\n- [x] a\n- [x] b\n"), 0644); err != nil {
		t.Fatal(err)
	}
	count, names, err := SpecOpenItems(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || len(names) != 0 {
		t.Errorf("fully-checked spec: count=%d names=%v, want 0/empty", count, names)
	}
}

func TestSpecOpenItemsBareCheckbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(path, []byte("- [ ]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	count, names, err := SpecOpenItems(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || names[0] != "(unnamed item)" {
		t.Errorf("bare checkbox: count=%d names=%v, want 1/[(unnamed item)]", count, names)
	}
}

func TestSpecOpenItemsMissingFile(t *testing.T) {
	if _, _, err := SpecOpenItems(filepath.Join(t.TempDir(), "absent.md")); err == nil {
		t.Error("missing spec file must return an error, not a zero count")
	}
}
