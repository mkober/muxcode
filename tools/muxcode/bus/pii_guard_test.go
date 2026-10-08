package bus

import (
	"strings"
	"testing"
)

// The commands known to leak (MUX-203 acceptance criterion 3), in the
// spellings agents use, are refused bare on a PII-sensitive role, and the
// reason names the piped form so the agent can comply in one step.
func TestCheckPIIPipeGuard_DeniesBareLeakyCommands(t *testing.T) {
	cases := []string{
		"env",
		"env | grep AWS",
		"env > /tmp/env.txt",
		"( env )",
		"FOO=1 env",
		"sudo env",
		"env -u PATH",
		"env FOO=1 printenv",
		"printenv",
		"printenv AWS_SECRET_ACCESS_KEY",
		"ps eww -p 123",
		"ps e",
		"ps auxeww",
		"ps -E -p 1",
		"cat ~/.config/muxcode/config",
		"cat .muxcode/config",
		`cat "$MUXCODE_CONFIG"`,
		"cd /tmp && env",
		"env | muxcode pii-scrub; printenv",
		"printenv AWS_SECRET_ACCESS_KEY | muxcode pii-scrub",
		"env | cut -d= -f2 | muxcode pii-scrub",
		"env | grep AWS | muxcode pii-scrub",
		"env | muxcode pii-scrub --role build",
		"env | muxcode pii-scrub --role",
	}
	for _, cmd := range cases {
		d := CheckPIIPipeGuard("run", cmd)
		if d == nil || !d.Blocked {
			t.Errorf("CheckPIIPipeGuard(run, %q) = %+v, want blocked", cmd, d)
			continue
		}
		if !strings.Contains(d.Reason, "| muxcode pii-scrub") {
			t.Errorf("CheckPIIPipeGuard(run, %q) reason does not name the piped form: %q", cmd, d.Reason)
		}
	}
	if d := CheckPIIPipeGuard("run", "env > /tmp/env.txt"); d == nil || !strings.Contains(d.Reason, "`env | muxcode pii-scrub`") {
		t.Errorf("redirected env: suggestion must drop the redirect, or the pipe carries nothing: %+v", d)
	}
}

// The remedy a denial names is what the agent runs next, so it must pass the
// guard and print labelled lines: `printenv NAME | muxcode pii-scrub` would
// hand the scrubber a bare value it cannot recognise.
func TestCheckPIIPipeGuard_RemedyPassesGuard(t *testing.T) {
	cases := []string{
		"env", "FOO=1 env", "env -u PATH", "( env )", "printenv",
		"printenv AWS_SECRET_ACCESS_KEY HOME", "ps eww -p 123", `cat "$MUXCODE_CONFIG"`,
	}
	for _, cmd := range cases {
		remedy := piiRemedy(cmd)
		if d := CheckPIIPipeGuard("run", cmd); d == nil || !strings.Contains(d.Reason, "`"+remedy+"`") {
			t.Errorf("%q: reason does not name the remedy %q: %+v", cmd, remedy, d)
		}
		if d := CheckPIIPipeGuard("run", remedy); d != nil {
			t.Errorf("%q: remedy %q is itself denied: %q", cmd, remedy, d.Reason)
		}
	}
	if got, want := piiRemedy("printenv AWS_SECRET_ACCESS_KEY"), "printenv | muxcode pii-scrub | grep -E '^(AWS_SECRET_ACCESS_KEY)='"; got != want {
		t.Errorf("printenv NAME remedy = %q, want the labelled dump narrowed after the scrub %q", got, want)
	}
}

// The piped form is the compliant road the denial points to; refusing it
// would leave a sensitive role no way to read its own environment.
func TestCheckPIIPipeGuard_AllowsScrubbedPipe(t *testing.T) {
	cases := []string{
		"env | muxcode pii-scrub",
		"printenv 2>&1 | muxcode pii-scrub",
		"printenv | muxcode pii-scrub | grep -E '^(AWS_REGION)='",
		"ps eww -p 123 | muxcode pii-scrub | tr ' ' '\\n' | grep -E '^(AGENT_ROLE|BUS_SESSION)='",
		"cat ~/.config/muxcode/config | ./bin/muxcode pii-scrub",
		"env | muxcode pii-scrub >/tmp/env.txt",
	}
	for _, cmd := range cases {
		if d := CheckPIIPipeGuard("watch", cmd); d != nil {
			t.Errorf("CheckPIIPipeGuard(watch, %q) = %+v, want allowed", cmd, d)
		}
	}
}

// Negative control: ordinary work on a sensitive role — process listings that
// print no environment, env running a command, reads of other files, the
// words appearing as data — passes untouched.
func TestCheckPIIPipeGuard_OrdinaryCommandsAllowed(t *testing.T) {
	cases := []string{
		"ps -ef",
		"ps aux",
		"ps -o user,pid -p 1",
		"ps -U edgar",
		"env FOO=1 ./scripts/run.sh",
		"command -v env",
		"cat README.md",
		"cat config/settings.json",
		"echo env",
		"grep printenv agents/runner.md",
		"bash scripts/test-pii-scrub-roles.sh",
	}
	for _, cmd := range cases {
		if d := CheckPIIPipeGuard("api", cmd); d != nil {
			t.Errorf("CheckPIIPipeGuard(api, %q) = %+v, want allowed", cmd, d)
		}
	}
}

// Negative control: the rule is scoped to the PII-sensitive roles, so the
// same bare commands on any other role are not this guard's to refuse.
func TestCheckPIIPipeGuard_NonSensitiveRoleUntouched(t *testing.T) {
	for _, role := range []string{"edit", "plan", "build", "commit"} {
		for _, cmd := range []string{"env", "printenv", "ps eww -p 1", "cat ~/.config/muxcode/config"} {
			if d := CheckPIIPipeGuard(role, cmd); d != nil {
				t.Errorf("CheckPIIPipeGuard(%s, %q) = %+v, want nil", role, cmd, d)
			}
		}
	}
}

// A spawn worker's bus role is spawn-<id>; one spawned as run must be held to
// run's rule, and one spawned as edit must not.
func TestCheckPIIPipeGuard_SpawnWorkerUsesBaseRole(t *testing.T) {
	session := testSession(t)
	t.Setenv("BUS_SESSION", session)
	entries := []SpawnEntry{
		{ID: "1-spawn-a1b2c3d4", Role: "run", SpawnRole: "spawn-a1b2c3d4", Window: "spawn-a1b2c3d4", Status: "running"},
		{ID: "2-spawn-e5f6a7b8", Role: "edit", SpawnRole: "spawn-e5f6a7b8", Window: "spawn-e5f6a7b8", Status: "running"},
	}
	if err := WriteSpawnEntries(session, entries); err != nil {
		t.Fatalf("WriteSpawnEntries: %v", err)
	}
	if d := CheckPIIPipeGuard("spawn-a1b2c3d4", "env"); d == nil || !d.Blocked {
		t.Errorf("run spawn: bare env = %+v, want blocked", d)
	} else if !strings.Contains(d.Reason, "run is a PII-sensitive role") {
		t.Errorf("run spawn: reason should name the base role: %q", d.Reason)
	}
	if d := CheckPIIPipeGuard("spawn-e5f6a7b8", "env"); d != nil {
		t.Errorf("edit spawn: bare env = %+v, want nil", d)
	}
}

// The hook road reaches the rule only through GuardDecisionFor.
func TestGuardDecisionFor_PIIPipeGuardWired(t *testing.T) {
	if d := GuardDecisionFor("watch", bashEvent("printenv")); d == nil || !d.Blocked {
		t.Errorf("GuardDecisionFor(watch, printenv) = %+v, want blocked", d)
	}
	if d := GuardDecisionFor("build", bashEvent("printenv")); d != nil {
		t.Errorf("GuardDecisionFor(build, printenv) = %+v, want nil", d)
	}
}
