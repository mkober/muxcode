package bus

import (
	"strings"
	"testing"
)

// The guard exists because the nvim diff preview is a PreToolUse hook matched
// on Write|Edit|NotebookEdit: a bash write fires no such hook, so the edit
// reaches disk unreviewed. These tests pin both directions, because a guard
// that over-blocks is worse than none — it would break ordinary delegation and
// invite someone to disable the whole thing.

func TestCheckBashFileWriteGuard_BlocksRepoWrites(t *testing.T) {
	blocked := []struct {
		name string
		cmd  string
	}{
		{"heredoc into a source file", "cat > tools/muxcode/bus/x.go <<'EOF'\npackage bus\nEOF"},
		{"heredoc with spaces", "cat >  scripts/new.sh  <<EOF"},
		{"attached redirect", "echo package main >main.go"},
		{"append to a repo file", "echo more >> CLAUDE.md"},
		{"sed in place", "sed -i 's/a/b/' bus/hook.go"},
		{"sed in place with suffix", "sed -i.bak 's/a/b/' bus/hook.go"},
		{"sed long form", "sed --in-place 's/a/b/' bus/hook.go"},
		{"perl clustered flags", "perl -pi -e 's/a/b/' bus/hook.go"},
		{"python rewrite via redirect", "python3 -c 'print(1)' > gen.go"},
		{"tee into a repo file", "echo x | tee config/settings.json"},
		{"tee append into a repo file", "echo x | tee -a config/settings.json"},
		{"relative path with dir", "printf 'x' > ./scripts/thing.sh"},
		{"absolute non-scratch path", "echo x > /Users/me/.config/muxcode/config"},
	}

	for _, tc := range blocked {
		t.Run(tc.name, func(t *testing.T) {
			if d := CheckBashFileWriteGuard("edit", tc.cmd); d == nil || !d.Blocked {
				t.Errorf("CheckBashFileWriteGuard(edit, %q) = allowed, want blocked — "+
					"a bash write bypasses the diff preview entirely", tc.cmd)
			}
		})
	}
}

func TestCheckBashFileWriteGuard_AllowsNonFileWrites(t *testing.T) {
	allowed := []struct {
		name string
		cmd  string
	}{
		{"stderr to stdout", "muxcode inbox 2>&1 | head -20"},
		{"stdout to stderr", "echo problem >&2"},
		{"discard output", "muxcode init > /dev/null 2>&1"},
		{"attached discard", "muxcode init >/dev/null"},
		{"scratch file", "echo hi > /tmp/handoff.md"},
		{"scratch append", "echo hi >> /tmp/run.log"},
		{"private tmp", "echo hi > /private/tmp/x.out"},
		{"macos mktemp dir", "echo hi > /var/folders/ab/cd/T/x.out"},
		{"tee to scratch", "echo hi | tee /tmp/x.log"},
		{"plain read", "cat scripts/test-echo-as-result.sh"},
		{"grep", "grep -rn NewBusResponseEntry bus/"},
		{"bus delegation", `muxcode send run run "Run exactly this one command: bash scripts/x.sh" --track`},
		{"sed read-only range", "sed -n '351,366p' test/x.test.ts"},
		{"sed substitution to stdout", "muxcode console build --once | sed 's/x//g'"},
		{"perl without in-place", "perl -e 'print 1'"},
		{"chmod", "chmod +x scripts/test-echo-as-result.sh"},
	}

	for _, tc := range allowed {
		t.Run(tc.name, func(t *testing.T) {
			if d := CheckBashFileWriteGuard("edit", tc.cmd); d != nil && d.Blocked {
				t.Errorf("CheckBashFileWriteGuard(edit, %q) = blocked, want allowed — "+
					"over-blocking breaks ordinary work and invites disabling the guard", tc.cmd)
			}
		})
	}
}

// Talking about a bash write is not performing one. This fired on the guard's
// very first real use: a `muxcode memory write` whose payload documented the
// rule ("blocks sed -i, perl -pi") was rejected as if it were editing a file.
// Blocking prose would break bus messages, memory writes, and commit messages
// that describe the guard — including the message announcing it.
func TestCheckBashFileWriteGuard_AllowsProseMentioningBlockedForms(t *testing.T) {
	allowed := []struct {
		name string
		cmd  string
	}{
		{"memory write describing the guard",
			`muxcode memory write "editor" "the guard blocks sed -i and perl -pi in place edits"`},
		{"bus message quoting a redirect",
			`muxcode send run run "Run bash scripts/x.sh > /tmp/out.log and report" --track`},
		{"bus message quoting a heredoc",
			`muxcode send plan update-docs "explain why cat > file <<EOF is blocked"`},
		{"single-quoted prose",
			`muxcode memory write 'editor' 'never use tee config/settings.json'`},
		{"commit message mentioning the rule",
			`muxcode send commit commit "Block sed -i edits in the edit window" --force`},
	}

	for _, tc := range allowed {
		t.Run(tc.name, func(t *testing.T) {
			if d := CheckBashFileWriteGuard("edit", tc.cmd); d != nil && d.Blocked {
				t.Errorf("CheckBashFileWriteGuard(edit, %q) = blocked, want allowed — "+
					"describing a bash write is not performing one", tc.cmd)
			}
		})
	}
}

// Stripping quoted data must not disarm the real thing: the flags and redirects
// that matter sit outside quotes even when the command also carries a quoted
// script or filename.
func TestCheckBashFileWriteGuard_QuotedArgsDoNotHideRealWrites(t *testing.T) {
	blocked := []struct {
		name string
		cmd  string
	}{
		{"sed script is quoted, -i is not", `sed -i 's/a/b/' bus/hook.go`},
		{"perl script quoted", `perl -pi -e 's/x/y/' bus/hook.go`},
		{"quoted heredoc delimiter", `cat > bus/x.go <<'EOF'`},
		{"quoted target path", `echo pkg > "bus/generated.go"`},
	}

	for _, tc := range blocked {
		t.Run(tc.name, func(t *testing.T) {
			if d := CheckBashFileWriteGuard("edit", tc.cmd); d == nil || !d.Blocked {
				t.Errorf("CheckBashFileWriteGuard(edit, %q) = allowed, want blocked", tc.cmd)
			}
		})
	}
}

// Flag scanning must not bleed across a pipeline. `sed -n '1p' f | grep -i x`
// is a read: the -i belongs to grep, not sed. Blocking it would make ordinary
// inspection impossible in the edit window.
func TestCheckBashFileWriteGuard_NoPipelineFlagBleed(t *testing.T) {
	allowed := []string{
		`sed -n '1p' bus/hook.go | grep -i package`,
		`cat bus/hook.go | grep -i "func " | head -20`,
		`sed -n '351,366p' test/x.ts | grep -i describe`,
		`muxcode console build --once | grep -i pass`,
		`ls -la | grep -i test`,
	}
	for _, cmd := range allowed {
		t.Run(cmd, func(t *testing.T) {
			if d := CheckBashFileWriteGuard("edit", cmd); d != nil && d.Blocked {
				t.Errorf("CheckBashFileWriteGuard(edit, %q) = blocked, want allowed — "+
					"a later command's -i is not sed's", cmd)
			}
		})
	}
}

// A heredoc body is content being written, not shell syntax. Text that happens
// to contain "sed -i" or "> out" must not trip the guard when it is the payload
// of a legitimate scratch write.
func TestCheckBashFileWriteGuard_HeredocBodyIsNotScanned(t *testing.T) {
	cmd := "cat > /tmp/notes.md <<'EOF'\nnever use sed -i for edits\nredirect with > out.txt is blocked\nEOF"
	if d := CheckBashFileWriteGuard("edit", cmd); d != nil && d.Blocked {
		t.Errorf("heredoc body was scanned as shell syntax; want allowed for a /tmp target")
	}

	// The command line itself is still policed: the target is what matters.
	repo := "cat > bus/notes.go <<'EOF'\npackage bus\nEOF"
	if d := CheckBashFileWriteGuard("edit", repo); d == nil || !d.Blocked {
		t.Error("heredoc into a repo file must still be blocked")
	}
}

// tee writes every file argument it is given. Checking only the first let
// `tee /tmp/a.log config/settings.json` through: the scratch path was inspected
// and the repo file never was.
func TestCheckBashFileWriteGuard_TeeChecksEveryTarget(t *testing.T) {
	if d := CheckBashFileWriteGuard("edit", "echo x | tee /tmp/a.log config/settings.json"); d == nil || !d.Blocked {
		t.Error("tee with a scratch path first must still block the repo file that follows")
	}
	// A bare tee has no file argument at all; the pipe is a separator.
	if d := CheckBashFileWriteGuard("edit", "echo x | tee | wc -l"); d != nil && d.Blocked {
		t.Error("bare tee in a pipeline has no target and must not block")
	}
}

// A '>' inside a quoted pattern is data. Extracting it yielded a lone quote
// that trimmed to empty, and empty matched no scratch prefix, so an ordinary
// grep was blocked.
func TestCheckBashFileWriteGuard_QuotedAngleBracketIsNotARedirect(t *testing.T) {
	allowed := []string{
		`grep '->' bus/hook.go`,
		`grep -n "=>" src/app.ts`,
		`muxcode send test test "assert count > 0 in the report"`,
		`awk '$1 > 5 {print}' data.txt`,
	}
	for _, cmd := range allowed {
		t.Run(cmd, func(t *testing.T) {
			if d := CheckBashFileWriteGuard("edit", cmd); d != nil && d.Blocked {
				t.Errorf("CheckBashFileWriteGuard(edit, %q) = blocked, want allowed — "+
					"a quoted angle bracket is data, not a redirect", cmd)
			}
		})
	}
}

// Heredoc parsing has two failure modes, and one of them lets a real write
// through — the reason these are pinned separately from the basic heredoc case.
func TestCheckBashFileWriteGuard_HeredocDelimiterParsing(t *testing.T) {
	// FALSE-ALLOW: with `<<EOF > target` ordering, taking the whole line as the
	// delimiter meant nothing ever terminated the body, so every command after
	// the real EOF was swallowed — including a repo-file edit.
	trailing := "cat <<EOF > /tmp/plan.md\nsteps\nEOF\nsed -i 's/x/y/' bus/hook.go"
	if d := CheckBashFileWriteGuard("edit", trailing); d == nil || !d.Blocked {
		t.Error("a sed -i after a heredoc terminator must still block — it is not body")
	}

	// FALSE-BLOCK: locating the terminator by substring matched "EOF" inside an
	// ordinary body line, cutting mid-body and leaking the rest back as syntax.
	mentions := "cat > /tmp/notes.md <<EOF\nmentions EOF here\nsed -i stuff\nEOF"
	if d := CheckBashFileWriteGuard("edit", mentions); d != nil && d.Blocked {
		t.Error("a body line merely containing the delimiter must not terminate it")
	}
}

// tee followed by a redirect: the '>' token was collected as a filename.
func TestCheckBashFileWriteGuard_TeeStopsAtRedirect(t *testing.T) {
	allowed := []string{
		"muxcode console build --once | tee /tmp/c.log > /dev/null",
		"echo x | tee /tmp/a.log 2>&1",
	}
	for _, cmd := range allowed {
		t.Run(cmd, func(t *testing.T) {
			if d := CheckBashFileWriteGuard("edit", cmd); d != nil && d.Blocked {
				t.Errorf("CheckBashFileWriteGuard(edit, %q) = blocked, want allowed — "+
					"'>' is a redirect operator, not a tee filename", cmd)
			}
		})
	}
}

// Backgrounding is a command separator too; without it a later command's -i
// bleeds into an earlier sed, exactly as it did across pipelines.
func TestCheckBashFileWriteGuard_BackgroundSeparatorNoBleed(t *testing.T) {
	cmd := "sed 's/a/b/' /tmp/f.txt & grep -i x notes.txt"
	if d := CheckBashFileWriteGuard("edit", cmd); d != nil && d.Blocked {
		t.Error("grep's -i after '&' is not sed's in-place flag")
	}
}

// Only roles that own an editor pane are gated. The build and test agents are
// expected to run shell commands that write files; blocking them would be wrong.
func TestCheckBashFileWriteGuard_OnlyGuardedRoles(t *testing.T) {
	cmd := "cat > x.go <<EOF"

	for _, role := range []string{"edit", "plan"} {
		if d := CheckBashFileWriteGuard(role, cmd); d == nil || !d.Blocked {
			t.Errorf("role %q should be guarded", role)
		}
	}
	for _, role := range []string{"build", "test", "run", "commit"} {
		if d := CheckBashFileWriteGuard(role, cmd); d != nil && d.Blocked {
			t.Errorf("role %q must not be guarded — it legitimately writes files", role)
		}
	}
}

// A delegation-prohibited command must still name the agent that owns it, so
// the guard stays actionable rather than reporting a confusing file-write
// reason for `git commit`.
func TestCheckGuard_DelegationReasonWinsOverFileWrite(t *testing.T) {
	d := CheckGuard("edit", "git commit -m x > /tmp/out.log")
	if d == nil || !d.Blocked {
		t.Fatal("git commit should be blocked in the edit window")
	}
	if !strings.Contains(d.Reason, "commit agent") {
		t.Errorf("reason = %q, want the commit-agent delegation message", d.Reason)
	}
}

// CheckGuard must fire the file-write rule too, not just the prefix table —
// wiring it into CheckBashFileWriteGuard alone would leave the hook path unarmed.
func TestCheckGuard_WiresFileWriteRule(t *testing.T) {
	if d := CheckGuard("edit", "cat > bus/x.go <<EOF"); d == nil || !d.Blocked {
		t.Error("CheckGuard(edit, heredoc) = allowed, want blocked")
	}
}
