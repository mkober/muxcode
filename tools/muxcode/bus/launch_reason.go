package bus

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// LaunchReason names why an agent is being launched. The road that types the
// launch command knows which it is and says so with `--reason`; the agent,
// reading an identical startup message on every road, cannot (MUX-141).
//
// Only LaunchReasonUser counts as user-initiated. The empty reason and any
// unrecognized value read as a restart, so a call site that omits its reason
// gets an agent that waits, never one that acts.
type LaunchReason string

const (
	LaunchReasonUser      LaunchReason = "user"
	LaunchReasonRestart   LaunchReason = "restart"
	LaunchReasonReload    LaunchReason = "reload"
	LaunchReasonModeCycle LaunchReason = "mode-cycle"
	LaunchReasonResume    LaunchReason = "resume"
	LaunchReasonSpawn     LaunchReason = "spawn"
)

// UserInitiated reports whether the launch is a start the user asked for.
func (r LaunchReason) UserInitiated() bool {
	return r == LaunchReasonUser
}

// The two startup payloads PreLaunchSetup seeds. The autonomous-agent
// definitions tell them apart by their opening words, so a rewording here is
// a change to agents/autonomous-agent.md and agents/harness/autonomous-agent.md.
const (
	startupRestorePayload  = "Session started — review last saved context from memory to restore session state."
	autoStartupTaskPayload = "Agent started — search Jira for available stories and present them to the user for selection."
)

// autoStartupTaskEnabled reports whether a user-initiated launch of the auto
// agent seeds its task. MUXCODE_AUTO_STARTUP_TASK=0 turns the task off on
// every road, leaving auto available but idle — the quiet alternative to
// stopping the agent for a user driving commits by hand.
func autoStartupTaskEnabled() bool {
	return os.Getenv("MUXCODE_AUTO_STARTUP_TASK") != "0"
}

// logName is the reason as the launch lifecycle row records it.
func (r LaunchReason) logName() string {
	if r == "" {
		return "unset"
	}
	return string(r)
}

// AgentLaunchCommand returns the shell command that launches role's agent
// through bin (the muxcode binary) carrying reason. Every road that types a
// launch into a pane builds it here, so none can omit the reason.
func AgentLaunchCommand(bin, role string, reason LaunchReason) string {
	return fmt.Sprintf("%s agent launch %s --reason %s", bin, role, reason)
}

// ParseLaunchArgs parses the arguments of `muxcode agent launch <role>
// [--reason <reason>] [--resume [<session-id>]]`. An omitted --reason yields the
// empty reason, which PreLaunchSetup treats as a restart. A --resume with no id
// yields ResumeAuto: the launcher finds the id itself (FindResumeID).
func ParseLaunchArgs(args []string) (role, resumeID string, reason LaunchReason, err error) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--resume":
			resumeID = ResumeAuto
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				resumeID = args[i]
			}
		case args[i] == "--reason":
			if i+1 >= len(args) {
				return "", "", "", fmt.Errorf("%s requires a value", args[i])
			}
			i++
			reason = LaunchReason(args[i])
		case role == "" && len(args[i]) > 0 && args[i][0] != '-':
			role = args[i]
		default:
			return "", "", "", fmt.Errorf("unknown argument: %s", args[i])
		}
	}
	if role == "" {
		return "", "", "", errors.New("missing role")
	}
	return role, resumeID, reason, nil
}
