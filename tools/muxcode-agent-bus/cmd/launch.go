package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Launch handles the "muxcode-agent-bus agent launch <role>" subcommand.
// It replaces muxcode-agent.sh — resolves agent config, loads venv, and execs
// the agent CLI (claude or muxcode-llm-harness).
func Launch(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus agent launch <role>\n")
		os.Exit(1)
	}

	role := args[0]

	// Load shell-sourceable config (same resolution as muxcode-agent.sh load_config)
	loadShellConfig()

	// Resolve all launch configuration
	cfg := bus.ResolveLaunchConfig(role)

	// Pre-launch: startup inbox message + lifecycle log
	session := bus.BusSession()
	binary, launchArgs := cfg.BuildExecArgs()
	bus.PreLaunchSetup(role, session, binary)

	// Activate Python venv if found
	if cfg.VenvDir != "" {
		activateVenv(cfg.VenvDir)
	}

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

// loadShellConfig loads the shell-sourceable config file.
// Resolution: MUXCODE_CONFIG env → .muxcode/config → ~/.config/muxcode/config
// Parses KEY=VALUE lines and sets them as env vars (matching bash `set -a; source`).
func loadShellConfig() {
	var configFile string

	if v := os.Getenv("MUXCODE_CONFIG"); v != "" {
		if _, err := os.Stat(v); err == nil {
			configFile = v
		}
	}
	if configFile == "" {
		if _, err := os.Stat(".muxcode/config"); err == nil {
			configFile = ".muxcode/config"
		}
	}
	if configFile == "" {
		home, _ := os.UserHomeDir()
		if home != "" {
			p := filepath.Join(home, ".config", "muxcode", "config")
			if _, err := os.Stat(p); err == nil {
				configFile = p
			}
		}
	}

	if configFile == "" {
		return
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Handle export prefix
		line = strings.TrimPrefix(line, "export ")
		// Parse KEY=VALUE
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Strip surrounding quotes
		val = stripQuotes(val)
		// Only set if not already set (env takes precedence)
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

// stripQuotes removes surrounding single or double quotes from a string.
func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
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
