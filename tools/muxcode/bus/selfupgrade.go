package bus

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// UpgradeVerdict is how the installed build stands against the latest
// release. The values are part of the `muxcode upgrade --check --json`
// output contract.
type UpgradeVerdict string

const (
	UpgradeCurrent UpgradeVerdict = "current"
	UpgradeAhead   UpgradeVerdict = "ahead"
	UpgradeNewer   UpgradeVerdict = "newer"
	UpgradeUnknown UpgradeVerdict = "unknown"
)

// upgradeBuildTools are what the Build step runs; Check probes them so a
// machine without a toolchain fails before anything is downloaded.
var upgradeBuildTools = []string{"go", "make", "tar"}

// UpgradeCheck is the result of the self-upgrade Check step (MUX-202). The
// JSON names are the `muxcode upgrade --check --json` output contract.
type UpgradeCheck struct {
	Installed    Info           `json:"installed"`
	Latest       Release        `json:"latest"`
	Verdict      UpgradeVerdict `json:"verdict"`
	Reason       string         `json:"reason,omitempty"`
	MissingTools []string       `json:"missing_tools,omitempty"`
}

// CheckUpgrade runs the Check step: the installed BuildInfo, the latest
// release from rc, the verdict and the build-tool probe. Only a failed
// release lookup is an error, returned with Installed still filled. An
// unknown verdict and missing tools are reported on the result for the
// caller to decide whether they block — a forced upgrade proceeds past the
// first and never past the second.
func CheckUpgrade(ctx context.Context, rc ReleaseClient) (UpgradeCheck, error) {
	check := UpgradeCheck{Installed: BuildInfo()}
	latest, err := rc.LatestRelease(ctx)
	if err != nil {
		return check, err
	}
	check.Latest = latest
	check.Verdict, check.Reason = ClassifyUpgrade(check.Installed.Version, latest.Tag)
	check.MissingTools = missingTools(upgradeBuildTools)
	return check, nil
}

// ClassifyUpgrade compares an installed version with the latest release tag
// (MUX-202 Decision 2): newer only when the tag is strictly past the
// installed version. A `git describe` build past the tag reads ahead, so a
// hot fix never downgrades a tree that is ahead of the release. A version
// with no semver rank — "devel", a bare commit — is unknown, with the
// comparison error as the reason.
func ClassifyUpgrade(installed, latest string) (UpgradeVerdict, string) {
	c, err := CompareSemver(latest, installed)
	switch {
	case err != nil:
		return UpgradeUnknown, err.Error()
	case c > 0:
		return UpgradeNewer, ""
	case c < 0:
		return UpgradeAhead, ""
	}
	return UpgradeCurrent, ""
}

// Summary is the verdict line the CLI prints and the modal shows.
func (c UpgradeCheck) Summary() string {
	have, latest := c.Installed.Version, c.Latest.Tag
	switch c.Verdict {
	case UpgradeNewer:
		return fmt.Sprintf("installed %s → latest %s available", have, latest)
	case UpgradeAhead:
		return fmt.Sprintf("installed %s is ahead of the latest release %s", have, latest)
	case UpgradeCurrent:
		return fmt.Sprintf("installed %s is current (latest %s)", have, latest)
	}
	return fmt.Sprintf("installed %s cannot be compared with the latest release %s: %s", have, latest, c.Reason)
}

// ToolsErr names the build tools Check found missing, or is nil.
func (c UpgradeCheck) ToolsErr() error {
	if len(c.MissingTools) == 0 {
		return nil
	}
	return fmt.Errorf("missing build tools: %s — install them to upgrade", strings.Join(c.MissingTools, ", "))
}

func missingTools(names []string) []string {
	var missing []string
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	return missing
}
