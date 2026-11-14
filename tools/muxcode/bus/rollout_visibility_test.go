package bus

import (
	"errors"
	"strings"
	"testing"
)

var (
	rolloutInstalled = Info{Version: "v0.3.0", Commit: "bbb2222", Date: "2026-11-14T10:00:00Z"}
	rolloutOlder     = Info{Version: "v0.2.0", Commit: "aaa1111", Date: "2026-11-01T10:00:00Z"}
)

// TestUnrolledReason is MUX-161 Phase 2: a rollout that leaves the caller's
// live session on an older build must say so — on 2026-09-09 a codex build's
// ps refusal skipped it with a stderr line while the build read green. The
// reason names the session, why discovery missed it and both builds — also
// when a plan found only the session's monitor. Negative controls: a plan
// that found the daemon, a daemon not running, one already on the installed
// build, and no session at all are no failure.
func TestUnrolledReason(t *testing.T) {
	psErr := errors.New("fork/exec /bin/ps: operation not permitted")
	reason := unrolledReason("muxcode", nil, psErr, true, rolloutOlder, true, rolloutInstalled)
	for _, want := range []string{"muxcode", "ps could not run", "operation not permitted", "daemon.owner", "v0.2.0", "v0.3.0", "upgrade-daemons --session muxcode"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q lacks %q", reason, want)
		}
	}
	if r := unrolledReason("muxcode", nil, nil, true, rolloutOlder, true, rolloutInstalled); !strings.Contains(r, "ps lists no daemon") {
		t.Errorf("with ps running the reason must say ps listed none: %q", r)
	}
	if r := unrolledReason("muxcode", nil, psErr, true, Info{}, false, rolloutInstalled); !strings.Contains(r, "unstamped") {
		t.Errorf("a daemon that recorded no build is stale, not current: %q", r)
	}

	monitorOnly := []UpgradePlan{{Session: "muxcode", MonitorPID: 4243}}
	if r := unrolledReason("muxcode", monitorOnly, psErr, true, rolloutOlder, true, rolloutInstalled); r == "" {
		t.Error("a plan that found only the monitor left the live daemon stale unreported")
	}

	for name, r := range map[string]string{
		"planned":     unrolledReason("muxcode", []UpgradePlan{{Session: "muxcode", DaemonPID: 4242}}, psErr, true, rolloutOlder, true, rolloutInstalled),
		"not running": unrolledReason("muxcode", nil, psErr, false, rolloutOlder, true, rolloutInstalled),
		"current":     unrolledReason("muxcode", nil, psErr, true, rolloutInstalled, true, rolloutInstalled),
		"no session":  unrolledReason("", nil, psErr, true, rolloutOlder, true, rolloutInstalled),
	} {
		if r != "" {
			t.Errorf("%s: want no failure, got %q", name, r)
		}
	}
}

// TestFailRollout_WritesTheRowAndErrs: a failed rollout is a warn-level
// daemon-upgrade-failed row carrying the reason, and an error on the result,
// which the CLI turns into exit 1 and build.sh into its warning line.
func TestFailRollout_WritesTheRowAndErrs(t *testing.T) {
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	res := failRollout(UpgradeResult{UpgradePlan: UpgradePlan{Session: "rollout-fail"}}, "relaunch daemon: exec: not found")
	if res.Err == nil || !strings.Contains(res.Err.Error(), "relaunch daemon") {
		t.Errorf("result error = %v", res.Err)
	}
	rows, _ := FilterLifecycleLog("rollout-fail", LifecycleFilterOpts{Event: "daemon-upgrade-failed"})
	if len(rows) != 1 || rows[0].Level != "warn" || !strings.Contains(rows[0].Detail, "relaunch daemon") {
		t.Errorf("want one warn daemon-upgrade-failed row naming the reason, got %+v", rows)
	}
}

// TestFormatDaemonBuildStatus: `session status` names a stale or unstamped
// daemon and the command that fixes it every time; a current daemon and a
// stopped one read as such, with no call to action.
func TestFormatDaemonBuildStatus(t *testing.T) {
	stale := formatDaemonBuildStatus("muxcode", true, rolloutOlder, true, rolloutInstalled)
	if !strings.Contains(stale, "STALE") || !strings.Contains(stale, "v0.2.0 → installed v0.3.0") || !strings.Contains(stale, "upgrade-daemons --session muxcode") {
		t.Errorf("stale line: %q", stale)
	}
	if line := formatDaemonBuildStatus("muxcode", true, Info{}, false, rolloutInstalled); !strings.Contains(line, "STALE") || !strings.Contains(line, "unstamped") {
		t.Errorf("unstamped line: %q", line)
	}
	if line := formatDaemonBuildStatus("muxcode", true, rolloutInstalled, true, rolloutInstalled); line != "Daemon: v0.3.0 (current)" {
		t.Errorf("current line: %q", line)
	}
	if line := formatDaemonBuildStatus("muxcode", false, rolloutOlder, true, rolloutInstalled); line != "Daemon: not running" {
		t.Errorf("stopped line: %q", line)
	}
}
