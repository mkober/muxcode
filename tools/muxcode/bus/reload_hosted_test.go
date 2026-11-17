package bus

import (
	"strings"
	"testing"
)

// stubTmuxCalls replaces every tmux runner with a recorder whose list-windows
// shows only edit, and returns how many tmux calls have been made.
func stubTmuxCalls(t *testing.T) func() int {
	t.Helper()
	origRun, origQuiet, origOutput := tmuxRunner, tmuxQuietRunner, tmuxOutputRunner
	n := 0
	tmuxRunner = func(args ...string) error { n++; return nil }
	tmuxQuietRunner = func(args ...string) error { n++; return nil }
	tmuxOutputRunner = func(args ...string) (string, error) {
		n++
		if len(args) > 0 && args[0] == "list-windows" {
			return "edit", nil
		}
		return "", nil
	}
	t.Cleanup(func() { tmuxRunner, tmuxQuietRunner, tmuxOutputRunner = origRun, origQuiet, origOutput })
	return func() int { return n }
}

// MUX-227: `muxcode reload docs` resolved docs to plan's window, stopped the
// planner and relaunched its pane as docs. A hosted role is now refused on
// both reload roads before anything is stopped — no tmux call is made — with
// the host's command named, as resume does; the reserved road releases the
// marker its caller took, and no marker is left on the role or its host.
func TestReloadAgent_RefusesHostedRole(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	calls := stubTmuxCalls(t)
	for _, tc := range []struct{ role, host string }{{"docs", "plan"}, {"pr-read", "commit"}} {
		t.Run(tc.role, func(t *testing.T) {
			before := calls()
			err := ReloadAgent(session, tc.role, "", "", false)
			want := tc.role + " runs inside " + tc.host + "'s pane — use `muxcode reload " + tc.host + "`"
			if err == nil || err.Error() != want {
				t.Errorf("ReloadAgent(%s) = %v, want %q", tc.role, err, want)
			}
			if got := calls() - before; got != 0 {
				t.Errorf("a refused reload made %d tmux call(s) — it must stop nothing", got)
			}

			release, err := ReserveReload(session, tc.role)
			if err != nil {
				t.Fatal(err)
			}
			if err := ReloadAgentReserved(session, tc.role, release); err == nil || err.Error() != want {
				t.Errorf("ReloadAgentReserved(%s) = %v, want %q", tc.role, err, want)
			}
			if got := calls() - before; got != 0 {
				t.Errorf("the reserved road made %d tmux call(s) on a refusal", got)
			}
			if IsReloading(session, tc.role) || IsReloading(session, tc.host) {
				t.Errorf("a reload marker was left on %s or %s", tc.role, tc.host)
			}
		})
	}
}

// Negative controls: a role that is not hosted, and the host itself, pass the
// hosted-role check — each stops only at the next guard, the missing window the
// stub reports, never at the hosted refusal. scripts/test-reload-hosted-role.sh
// drives a real reload of the host.
func TestReloadAgent_HostAndOrdinaryRolesPassTheHostedCheck(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	stubTmuxCalls(t)
	for _, role := range []string{"plan", "commit", "build"} {
		err := ReloadAgent(session, role, "", "", false)
		if err != nil && strings.Contains(err.Error(), "runs inside") {
			t.Errorf("ReloadAgent(%s) was refused as hosted: %v", role, err)
		}
		if err == nil {
			t.Errorf("ReloadAgent(%s) passed the missing-window guard on a stub with no %s window", role, role)
		}
		release, rerr := ReserveReload(session, role)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if err := ReloadAgentReserved(session, role, release); err != nil && strings.Contains(err.Error(), "runs inside") {
			t.Errorf("ReloadAgentReserved(%s) was refused as hosted: %v", role, err)
		}
	}
}
