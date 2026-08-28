package bus

import (
	"os"
	"regexp"
	"strings"
)

// openItemRe matches an unchecked markdown checkbox line (`- [ ] text`),
// including nested/indented items. The repo's docs convention requires
// checkboxes for every actionable item, so this count is the mechanical
// definition of "work still open" a spec close-out must respect (MUX-114).
var openItemRe = regexp.MustCompile(`^\s*- \[ \]\s*(.*)`)

// SpecOpenItems reads a requirements spec and returns how many checkbox
// items remain unchecked, along with their texts in file order. Lines
// inside fenced code blocks are skipped — specs quote checkbox examples in
// fences, and counting those would block a legitimate close-out.
func SpecOpenItems(path string) (int, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, err
	}
	var names []string
	inFence := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if m := openItemRe.FindStringSubmatch(line); m != nil {
			name := strings.TrimSpace(m[1])
			if name == "" {
				name = "(unnamed item)"
			}
			names = append(names, name)
		}
	}
	return len(names), names, nil
}
