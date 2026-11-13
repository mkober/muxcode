package bus

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// BacklogIndexPath is the backlog index, relative to the repo root.
const BacklogIndexPath = "docs/requirements/backlog/backlog.md"

// backlogDefectsHeading opens the prioritized Defects table in the index.
const backlogDefectsHeading = "### Defects"

// backlogDefectRow matches a ranked table row: its first cell is the rank.
var backlogDefectRow = regexp.MustCompile(`^\|\s*\d+\s*\|`)

// BacklogDefectCount returns the number of ranked rows in the backlog index's
// prioritized Defects table under repo — the section from the "### Defects"
// heading to the next heading. Header and separator rows carry no rank and
// are not counted. A missing index or section is an error, as is an empty
// table: a loop capped by the count would have nothing to bound.
func BacklogDefectCount(repo string) (int, error) {
	path := filepath.Join(repo, BacklogIndexPath)
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	in, found, count := false, false, 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			if in {
				break
			}
			in = strings.HasPrefix(line, backlogDefectsHeading)
			found = found || in
			continue
		}
		if in && backlogDefectRow.MatchString(line) {
			count++
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("%s has no %q section", BacklogIndexPath, backlogDefectsHeading)
	}
	if count == 0 {
		return 0, fmt.Errorf("%s lists no defects", BacklogIndexPath)
	}
	return count, nil
}
