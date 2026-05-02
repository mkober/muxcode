package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GetShellConfig loads the shell-sourceable config file and returns env vars.
// Resolution: MUXCODE_CONFIG env → .muxcode/config → ~/.config/muxcode/config
// Returns a map of KEY=VALUE pairs. Only includes vars not already set in environment.
// projectDir, if non-empty, is used to resolve the .muxcode/config path.
func GetShellConfig(projectDir string) map[string]string {
	result := make(map[string]string)

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
		return result
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return result
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
		// Only include if not already set (env takes precedence)
		if os.Getenv(key) == "" {
			result[key] = val
		}
	}

	return result
}

// LoadShellConfig loads the shell-sourceable config file and sets env vars.
// Deprecated: Use GetShellConfig and merge the result into command environment
// to avoid modifying the global process environment.
func LoadShellConfig(projectDir string) {
	// Reuse the new function but apply via os.Setenv for backward compatibility
	config := GetShellConfig(projectDir)
	for key, val := range config {
		os.Setenv(key, val)
	}
}

// ResolveConfigPath returns the path to the config file used for persistent
// settings. Resolution: MUXCODE_CONFIG env → .muxcode/config → ~/.config/muxcode/config.
// If no file exists, returns the user config path (~/.config/muxcode/config)
// as the default write target.
func ResolveConfigPath() string {
	if v := os.Getenv("MUXCODE_CONFIG"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}
	if _, err := os.Stat(".muxcode/config"); err == nil {
		return ".muxcode/config"
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		p := filepath.Join(home, ".config", "muxcode", "config")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		// Default write target even if it doesn't exist yet
		return p
	}
	return ".muxcode/config"
}

// SetShellConfigValue updates or appends a key=value pair in the config file.
// If the key already exists (with or without "export" prefix), it is updated
// in place. Otherwise the new entry is appended. Comments and blank lines are
// preserved.
func SetShellConfigValue(key, value string) error {
	configPath := ResolveConfigPath()

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Read existing content
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read config: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip comments and empty lines
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Check for KEY= with optional "export " prefix
		raw := strings.TrimPrefix(trimmed, "export ")
		parts := strings.SplitN(raw, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
			// Update in place, preserving export prefix
			if strings.HasPrefix(trimmed, "export ") {
				lines[i] = "export " + key + "=" + value
			} else {
				lines[i] = key + "=" + value
			}
			found = true
			break
		}
	}

	if !found {
		// Append to end — add blank line separator if file doesn't end with one
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, key+"="+value)
	}

	content := strings.Join(lines, "\n")
	// Ensure trailing newline
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	return os.WriteFile(configPath, []byte(content), 0644)
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
