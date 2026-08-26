package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// The prompt-agent (MUX-109) runs headless: no tmux window, no harness
// TUI. That is the harness's default path — --tui is opt-in — so headless
// means simply launching `muxcode-llm-harness run prompt` with no flag.
// Because no pane exists, none of the pane-based supervision covers it;
// the daemon owns the lifecycle via checkPromptAgent, using the same PID
// marker the harness writes for itself at startup (loop.go), read through
// IsHarnessActive.
//
// The role's tool profile (bus/profile.go) is deliberately minimal, since
// the agent sits behind a free-text input — the least predictable input
// in the system. No include groups: "bus" would grant Bash(muxcode *),
// the entire CLI. No Write/Edit: IsToolAllowed gates write_file on the
// mere presence of a Write pattern (hasToolPattern does not path-match),
// so any grant would unlock writes everywhere. Graph definitions are
// instead written through the muxcode graph CLI, whose write path
// validates before writing (WriteGraphDefinition) — denying raw writes is
// what makes validate-before-write unbypassable by the model.
// TestPromptProfileDeniesRepoWriteAndGit pins this.

const promptAgentRole = "prompt"

// PromptAgentEnabled reports whether the daemon should supervise a
// headless prompt-agent for this session. Opt-out follows the watchdog
// convention: MUXCODE_PROMPT_AGENT_DISABLE=1.
func PromptAgentEnabled() bool {
	return os.Getenv("MUXCODE_PROMPT_AGENT_DISABLE") != "1"
}

// PromptAgentLogPath returns the headless agent's log file — its only
// output surface, since it has no pane.
func PromptAgentLogPath(session string) string {
	return filepath.Join(BusDir(session), "prompt-agent.log")
}

// PromptAgentAlive reports whether the prompt-agent process is running,
// via the harness PID marker (stale markers are cleaned by the check).
func PromptAgentAlive(session string) bool {
	return IsHarnessActive(session, promptAgentRole)
}

// StartPromptAgent launches the headless harness process for the prompt
// role. The child is detached (own process group, output to the log file)
// so it survives nothing it shouldn't and never wedges the daemon. The
// harness writes its own PID marker once its loop starts; callers gate
// re-starts on a cooldown rather than the marker to cover the write gap.
func StartPromptAgent(session string) (int, error) {
	bin, err := exec.LookPath("muxcode-llm-harness")
	if err != nil {
		return 0, fmt.Errorf("muxcode-llm-harness not in PATH: %w", err)
	}
	logf, err := os.OpenFile(PromptAgentLogPath(session), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return 0, err
	}
	defer logf.Close()

	cmd := exec.Command(bin, "run", promptAgentRole)
	// AGENT_ROLE pins the bus identity: outside a pane there is no tmux
	// window name to fall back on, and inheriting the daemon's TMUX_PANE
	// would resolve some other window's name instead.
	cmd.Env = append(os.Environ(),
		"MUXCODE_SESSION="+session,
		"AGENT_ROLE="+promptAgentRole,
	)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }() // reap; the marker liveness check owns restart
	return pid, nil
}

// StopPromptAgent terminates the prompt-agent process recorded in the
// harness marker and removes the marker. A missing or stale marker is not
// an error — the goal state is "not running".
func StopPromptAgent(session string) error {
	path := HarnessMarkerPath(session, promptAgentRole)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	return os.Remove(path)
}
