package bus

import (
	"os"
	"path/filepath"
	"strings"
)

// LoadShellConfig loads the shell-sourceable config file and sets env vars.
// Resolution: MUXCODE_CONFIG env → .muxcode/config → ~/.config/muxcode/config
// Parses KEY=VALUE lines and sets them as env vars (matching bash `set -a; source`).
// Only sets vars that are not already set (env takes precedence).
// projectDir, if non-empty, is used to resolve the .muxcode/config path.
func LoadShellConfig(projectDir string) {
	var configFile string

	if v := os.Getenv("MUXCODE_CONFIG"); v != "" {
		if _, err := os.Stat(v); err == nil {
			configFile = v
		}
	}
	if configFile == "" {
		p := ".muxcode/config"
		if projectDir != "" {
			p = filepath.Join(projectDir, ".muxcode", "config")
		}
		if _, err := os.Stat(p); err == nil {
			configFile = p
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
		// Strip surrounding quotes and expand env vars (e.g. $HOME)
		val = os.ExpandEnv(StripQuotes(val))
		// Only set if not already set (env takes precedence)
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

// StripQuotes removes surrounding single or double quotes from a string.
func StripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
