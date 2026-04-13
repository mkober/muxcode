package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Launch handles the "muxcode agent launch <role>" subcommand.
// It replaces muxcode-agent.sh — resolves agent config, loads venv, and execs
// the agent CLI (claude or muxcode-llm-harness).
func Launch(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode agent launch <role>\n")
		os.Exit(1)
	}

	role := args[0]

	// Load shell-sourceable config (same resolution as muxcode.sh load_config)
	bus.LoadShellConfig("")

	// Resolve all launch configuration
	cfg := bus.ResolveLaunchConfig(role)

	// Pre-launch: generate agent config for non-Claude providers
	if cfg.Provider != nil {
		if err := cfg.Provider.WriteAgentConfig(role); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: WriteAgentConfig(%s): %v\n", role, err)
		}
	}

	// Pre-launch: startup inbox message + lifecycle log
	session := bus.BusSession()
	binary, launchArgs := cfg.BuildExecArgs()
	bus.PreLaunchSetup(role, session, binary)

	// Activate Python venv if found
	if cfg.VenvDir != "" {
		activateVenv(cfg.VenvDir)
	}

	// Export AGENT_ROLE so child processes (e.g. `muxcode send`) can identify
	// the sender. Without this, BusRole() falls back to tmux window name or
	// "unknown" — the latter happens when Codex/OpenCode TUI processes call
	// muxcode send and tmux context variables aren't inherited.
	// Normalize to canonical bus role — the launcher's RoleMap may pass aliases
	// like legacy aliases that aren't valid bus targets.
	os.Setenv("AGENT_ROLE", bus.NormalizeBusRole(role))

	// Clear terminal for clean agent startup
	fmt.Print("\033[2J\033[H")

	// Resolve binary to absolute path for exec
	binPath, err := resolveExecPath(binary)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot find %s: %v\n", binary, err)
		os.Exit(1)
	}

	// Build argv for exec (argv[0] must be the binary name)
	argv := append([]string{binary}, launchArgs...)

	// Replace this process with the agent CLI
	if err := syscall.Exec(binPath, argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: exec %s: %v\n", binary, err)
		os.Exit(1)
	}
}

// activateVenv sets PATH and VIRTUAL_ENV environment variables to activate
// a Python venv. This is equivalent to `source <venv>/bin/activate`.
func activateVenv(venvDir string) {
	absVenv, err := filepath.Abs(venvDir)
	if err != nil {
		return
	}
	binDir := filepath.Join(absVenv, "bin")
	os.Setenv("VIRTUAL_ENV", absVenv)
	// Prepend venv bin to PATH
	os.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Remove PYTHONHOME if set (matches activate script behavior)
	os.Unsetenv("PYTHONHOME")
}

// resolveExecPath finds the absolute path for a binary name.
func resolveExecPath(binary string) (string, error) {
	if filepath.IsAbs(binary) {
		return binary, nil
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", err
	}
	return filepath.Abs(path)
}
