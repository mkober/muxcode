package bus

import (
	"path/filepath"
	"strings"
	"testing"
)

// listenerCommand is the one command the inbox listener runs under. The
// launcher's shared prompt teaches it, `muxcode inbox --poll --loop` claims the
// polling marker while it runs, and the Stop hook re-issues it when the marker
// is gone — three places that must name the same command or a restarted agent
// starts a listener the hook cannot see, or is told to start one it was never
// taught. Either way receipts stop: the 2026-09-01 delivery-gap (MUX-136).
const listenerCommand = "muxcode inbox --poll --loop"

// TestRestartLaunchRestoresListenerProtocol pins the restart path's listener
// restoration end to end, not as a prompt string: the launch a restart
// produces (`muxcode agent launch <role>` → ResolveLaunchConfig → BuildExecArgs)
// carries the bound definition and instructs listenerCommand; the marker that
// command claims is what the Stop hook's liveness read sees, so a live
// listener is left alone; and once the marker is gone with work pending the
// hook blocks the stop with an instruction naming the same command.
func TestRestartLaunchRestoresListenerProtocol(t *testing.T) {
	session := "test-listener-restore"
	home, _ := launchSandbox(t, session)
	writeFile(t, filepath.Join(home, ".config", "muxcode", "agents", "planner.md"),
		"---\ndescription: Docs\n---\nMaintain docs.\n")

	cfg := ResolveLaunchConfig("plan")
	_, args := cfg.BuildExecArgs()
	if !ArgsCarryDefinition(args) {
		t.Fatalf("restart launch lacks the bound definition: %v", args)
	}
	prompt := appendedSystemPrompts(args)
	if !strings.Contains(prompt, "`"+listenerCommand+"`") {
		t.Fatalf("restart launch does not instruct the listener: %q", prompt)
	}
	if !strings.Contains(StopHookPollReason, "`"+listenerCommand+"`") {
		t.Fatalf("Stop hook re-issues a different command than the launch teaches: %q", StopHookPollReason)
	}

	if !SetPolling(session, "plan") {
		t.Fatal("the instructed listener could not claim its marker")
	}
	t.Cleanup(func() { ClearPolling(session, "plan") })
	if !stopHookSeesListener(session, "plan") {
		t.Fatal("a running listener is invisible to the Stop hook's liveness read")
	}
	if got := DecideStopHook(stopHookSeesListener(session, "plan"), false, false, true); got.Block {
		t.Fatalf("Stop hook blocks with a live listener: %+v", got)
	}

	ClearPolling(session, "plan")
	if stopHookSeesListener(session, "plan") {
		t.Fatal("a stopped listener still reads alive")
	}
	got := DecideStopHook(stopHookSeesListener(session, "plan"), false, false, true)
	if !got.Block || !strings.Contains(got.Reason, listenerCommand) {
		t.Fatalf("dead listener with pending work not re-launched by the same command: %+v", got)
	}
}

// stopHookSeesListener mirrors hookStop's liveness read (cmd/hook.go): a --poll
// or --wait marker owned by a live process.
func stopHookSeesListener(session, role string) bool {
	return IsPolling(session, role) || IsWaiting(session, role)
}

// appendedSystemPrompts joins every --append-system-prompt value in a launch.
func appendedSystemPrompts(args []string) string {
	var b strings.Builder
	for i, a := range args {
		if a == "--append-system-prompt" && i+1 < len(args) {
			b.WriteString(args[i+1])
			b.WriteString("\n")
		}
	}
	return b.String()
}
