package bus

import (
	"strings"
	"testing"
)

// liveDetachedListenerCall is the Bash call the watch agent made on
// 2026-09-22 10:09:22 — the listener it started reparented to launchd within
// a second, writing every watch message to a log nobody read.
const liveDetachedListenerCall = `nohup muxcode inbox --poll --loop > /tmp/watch-listener.log 2>&1 & disown; sleep 1; ps aux | grep -i "muxcode inbox" | grep -v grep`

// guardRoles spans roles with delegation rules (edit, plan), with the
// evidence rule (build), and with neither (watch, serve, docs), so the test
// fails if the rule is scoped to any subset or if an earlier rule answers
// the call first with a different reason.
var guardRoles = []string{"edit", "plan", "build", "test", "watch", "serve", "run", "commit", "docs", "unknown"}

func bashEvent(cmd string) *ToolEvent {
	return &ToolEvent{ToolName: "Bash", ToolInput: ToolInput{Command: cmd}}
}

func TestListenerGuard_LiveDetachedCallDeniedForEveryRole(t *testing.T) {
	for _, role := range guardRoles {
		d := GuardDecisionFor(role, bashEvent(liveDetachedListenerCall))
		if d == nil || !d.Blocked {
			t.Errorf("GuardDecisionFor(%s, live call) = %+v, want blocked", role, d)
			continue
		}
		for _, want := range []string{"BLOCKED: inbox listener", "`nohup muxcode inbox --poll --loop", "run_in_background=true"} {
			if !strings.Contains(d.Reason, want) {
				t.Errorf("GuardDecisionFor(%s, live call) reason missing %q: %q", role, want, d.Reason)
			}
		}
	}
}

// Every detached shape is paired with its bare form as the positive control,
// so a guard that refuses every listener fails here as surely as one that
// refuses none. Both go through GuardDecisionFor, the rule set the hook runs.
// The first two rows are the PR review's bypasses of the whitespace-split
// parser: a paren glued to the flag, and a stdout redirect glued behind a
// stderr one.
func TestListenerGuard_DetachedDeniedBareAllowed(t *testing.T) {
	cases := []struct{ detached, bare string }{
		{"(muxcode inbox --poll) &", "muxcode inbox --poll"},
		{"muxcode inbox --poll --loop 2>/tmp/err>/tmp/log", "muxcode inbox --poll --loop 2>/tmp/err"},
		{"env muxcode inbox --poll --loop &", "muxcode inbox --poll --loop"},
		{"timeout 600 muxcode inbox --poll --loop", "muxcode inbox --poll --loop"},
		{"{ muxcode inbox --poll --loop; } &", "muxcode inbox --poll --loop"},
		{"sh -c 'muxcode inbox --poll --loop' &", "muxcode inbox --poll --loop"},
		{`bash -c "nohup muxcode inbox --poll --loop &"`, "muxcode inbox --poll --loop"},
		{"eval 'muxcode inbox --poll --loop &'", "muxcode inbox --poll --loop"},
		{"eval muxcode inbox --poll --loop", "muxcode inbox --poll --loop"},
		{"bash -lc 'muxcode inbox --poll --loop'", "muxcode inbox --poll --loop"},
		{"nice -n 10 muxcode inbox --poll --loop", "muxcode inbox --poll --loop"},
		{"muxcode inbox --poll --loop >&2", "muxcode inbox --poll --loop 2>&1"},
		{"muxcode inbox --poll --loop >| /tmp/l.log", "muxcode inbox --poll --loop"},
		{"muxcode inbox --poll --loop &", "muxcode inbox --poll --loop"},
		{"nohup muxcode inbox --poll --loop", "muxcode inbox --poll --loop"},
		{"setsid muxcode inbox --poll --loop", "muxcode inbox --poll --loop"},
		{"(muxcode inbox --poll --loop &)", "muxcode inbox --poll --loop"},
		{"( muxcode inbox --poll --loop & )", "muxcode inbox --poll --loop"},
		{"muxcode inbox --poll --loop > /tmp/l.log", "muxcode inbox --poll --loop"},
		{"muxcode inbox --poll --loop >> /tmp/l.log 2>&1", "muxcode inbox --poll --loop 2>&1"},
		{"muxcode inbox --poll --loop &>/tmp/l.log", "muxcode inbox --poll --loop"},
		{"muxcode inbox --poll --loop 1>/dev/null", "muxcode inbox --poll --loop 2>/dev/null"},
		{"muxcode inbox --loop>/tmp/l.log", "muxcode inbox --loop"},
		{"muxcode inbox --poll --loop | tee /tmp/l.log", "muxcode inbox --poll --loop"},
		{"muxcode inbox --poll --loop; echo started", "muxcode inbox --poll --loop"},
		{"echo start && muxcode inbox --poll --loop", "muxcode inbox --poll --loop"},
		{"muxcode inbox -poll 600 -loop &", "muxcode inbox -poll 600 -loop"},
		{"~/.local/bin/muxcode inbox --poll --loop &", "~/.local/bin/muxcode inbox --poll --loop"},
		{"cd /repo && nohup muxcode inbox --poll --loop &", "cd /repo && muxcode inbox --poll --loop"},
		{"MUXCODE_INBOX_POLL_TIMEOUT=600 muxcode inbox --poll --loop &", "MUXCODE_INBOX_POLL_TIMEOUT=600 muxcode inbox --poll --loop"},
		{"muxcode inbox --poll &", "muxcode inbox --poll"},
	}
	for _, c := range cases {
		if d := GuardDecisionFor("watch", bashEvent(c.detached)); d == nil || !d.Blocked {
			t.Errorf("GuardDecisionFor(watch, %q) = %+v, want blocked", c.detached, d)
		} else if !strings.Contains(d.Reason, "BLOCKED: inbox listener") {
			t.Errorf("GuardDecisionFor(watch, %q) blocked by another rule: %q", c.detached, d.Reason)
		}
		if d := GuardDecisionFor("watch", bashEvent(c.bare)); d != nil && d.Blocked {
			t.Errorf("GuardDecisionFor(watch, %q) = blocked, want allowed: %q", c.bare, d.Reason)
		}
	}
}

// The bare listener every Claude agent runs after each turn must pass the
// whole rule set for every role — a false refusal here silences the fleet.
func TestListenerGuard_BareListenerAllowedForEveryRole(t *testing.T) {
	for _, role := range guardRoles {
		if d := GuardDecisionFor(role, bashEvent("muxcode inbox --poll --loop")); d != nil && d.Blocked {
			t.Errorf("GuardDecisionFor(%s, bare listener) = blocked: %q", role, d.Reason)
		}
	}
}

// Negative control for the detector: a call that only mentions the listener,
// reads the inbox without polling, or detaches something else is not the
// guard's business — bus messages about this very defect quote the form, and
// diagnosing it greps and counts it. Only the executed word, `eval`'s string
// and a shell's `-c` string are commands; `rg -c`'s pattern is data.
func TestListenerGuard_NonListenerCallsAllowed(t *testing.T) {
	allowed := []string{
		`muxcode send edit notify "watch ran nohup muxcode inbox --poll --loop & disown" --type event`,
		`muxcode send edit notify eval "muxcode inbox --poll --loop &"`,
		`muxcode memory write "listener" "never run muxcode inbox --poll --loop > log &"`,
		"echo muxcode inbox --poll --loop &",
		`rg -c "muxcode inbox --poll" docs/`,
		`grep -c "muxcode inbox --poll" /tmp/watch-listener.log`,
		"muxcode inbox",
		"muxcode inbox --peek &",
		"muxcode inbox --role watch --peek > /tmp/peek.txt",
		"nohup pnpm dev > /tmp/muxcode-bus-x/serve-5173.log 2>&1 &",
		"echo 'muxcode inbox --poll --loop &'",
		"grep -n 'inbox --poll --loop' bus/hook.go",
		`ps aux | grep -i "muxcode inbox --poll" | grep -v grep`,
		"sh -c 'muxcode status' &",
		"muxcode status; muxcode tasks",
	}
	for _, cmd := range allowed {
		if d := CheckListenerGuard("watch", cmd); d != nil {
			t.Errorf("CheckListenerGuard(%q) = blocked, want allowed: %q", cmd, d.Reason)
		}
	}
}

func TestListenerInvocation(t *testing.T) {
	cases := []struct {
		stmt        string
		found, bare bool
	}{
		{"muxcode inbox --poll --loop", true, true},
		{"muxcode inbox --poll=600 --loop", true, true},
		{"./bin/muxcode inbox --loop 2>&1", true, true},
		{"nohup muxcode inbox --poll --loop", true, false},
		{"(muxcode inbox --poll --loop)", true, false},
		{"muxcode inbox --poll --loop > /tmp/x", true, false},
		{"(muxcode inbox --poll)", true, false},
		{`MUXCODE_X="a b" muxcode inbox --poll --loop`, true, true},
		{"sh -c 'muxcode inbox --loop'", true, false},
		{"muxcode inbox --peek", false, false},
		{"echo muxcode inbox --poll", false, false},
		{`rg -c "muxcode inbox --poll"`, false, false},
		{"muxcode send edit x \"inbox --poll --loop\"", false, false},
		{"muxcode send edit x \"muxcode inbox --poll --loop\"", false, false},
		{"muxcodex inbox --poll", false, false},
		{"muxcode", false, false},
	}
	for _, c := range cases {
		found, bare := listenerInvocation(c.stmt)
		if found != c.found || bare != c.bare {
			t.Errorf("listenerInvocation(%q) = (%v, %v), want (%v, %v)", c.stmt, found, bare, c.found, c.bare)
		}
	}
}

func TestCommandStart(t *testing.T) {
	cases := []struct {
		stmt    string
		k       int
		wrapped bool
	}{
		{"muxcode inbox", 0, false},
		{"A=1 B='x y' muxcode inbox", 2, false},
		{"nohup muxcode inbox", 1, true},
		{"timeout 600 muxcode inbox", 2, true},
		{"nice -n 10 muxcode inbox", 3, true},
		{"env -i A=1 muxcode inbox", 3, true},
		{"( muxcode inbox )", 1, true},
		{"echo muxcode inbox", 0, false},
		{"rg -c muxcode", 0, false},
	}
	for _, c := range cases {
		if k, wrapped := commandStart(shellWords(c.stmt)); k != c.k || wrapped != c.wrapped {
			t.Errorf("commandStart(%q) = (%d, %v), want (%d, %v)", c.stmt, k, wrapped, c.k, c.wrapped)
		}
	}
}

func TestShellWords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"(muxcode inbox --poll)", []string{"(", "muxcode", "inbox", "--poll", ")"}},
		{"x 2>/tmp/err>/tmp/log", []string{"x", "2>", "/tmp/err", ">", "/tmp/log"}},
		{"x --loop>/tmp/l", []string{"x", "--loop", ">", "/tmp/l"}},
		{"x 2>&1 &>/y >>z 1>w", []string{"x", "2>&", "1", "&>", "/y", ">>", "z", "1>", "w"}},
		{`echo "a > (b)" 'c d' e\ f`, []string{"echo", `"a > (b)"`, "'c d'", `e\ f`}},
		{`x "unterminated > y`, []string{"x", `"unterminated > y`}},
	}
	for _, c := range cases {
		if got := shellWords(c.in); strings.Join(got, "¦") != strings.Join(c.want, "¦") {
			t.Errorf("shellWords(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Negative control for the stdout detector: stderr-only redirections and a
// quoted `>` are not stdout redirects.
func TestRedirectsStdout(t *testing.T) {
	for in, want := range map[string]bool{
		"x > f":         true,
		"x >> f":        true,
		"x 1>f":         true,
		"x &>f":         true,
		"x >|f":         true,
		"x >&2":         true,
		"x 2>f":         false,
		"x 2>&1":        false,
		"x 2>>f":        false,
		`x "a > b"`:     false,
		"x <in 0<&0":    false,
		"x 2>/e>/tmp/o": true,
	} {
		if got := redirectsStdout(shellWords(in)); got != want {
			t.Errorf("redirectsStdout(%q) = %v, want %v", in, got, want)
		}
	}
}
