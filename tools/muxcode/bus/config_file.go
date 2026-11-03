package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GetShellConfig loads the shell-sourceable config and returns its KEY=VALUE
// pairs, leaving out keys already set in the environment. A MUXCODE_CONFIG that
// names an existing file is read alone. Otherwise ~/.config/muxcode/config is
// the base and the project's .muxcode/config overrides it key by key, so a
// project file changes only what it names: the user's role CLI and model
// choices and credentials still apply. Until 2026-11-02 the first file found
// was read alone, so a project config hid the user's entirely (MUX-207, user
// decision). projectDir, if non-empty, locates the .muxcode/config.
func GetShellConfig(projectDir string) map[string]string {
	result := make(map[string]string)
	for _, path := range shellConfigFiles(projectDir) {
		readShellConfigInto(path, result)
	}
	return result
}

// shellConfigFiles lists the files GetShellConfig reads, lowest precedence
// first. Absent files are listed too; reading one adds nothing.
func shellConfigFiles(projectDir string) []string {
	if v := os.Getenv("MUXCODE_CONFIG"); v != "" && fileExists(v) {
		return []string{v}
	}
	var files []string
	if home, _ := os.UserHomeDir(); home != "" {
		files = append(files, filepath.Join(home, ".config", "muxcode", "config"))
	}
	project := ".muxcode/config"
	if projectDir != "" {
		project = filepath.Join(projectDir, ".muxcode", "config")
	}
	return append(files, project)
}

// readShellConfigInto parses one config file into result, a later file's value
// replacing an earlier one's.
func readShellConfigInto(path string, result map[string]string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := os.ExpandEnv(StripQuotes(strings.TrimSpace(parts[1])))
		if os.Getenv(key) == "" {
			result[key] = val
		}
	}
}

// LoadShellConfig loads the shell-sourceable config file and sets env vars,
// returning the keys it set — never one the environment already held — for
// ReloadShellConfig.
// Deprecated: Use GetShellConfig and merge the result into command environment
// to avoid modifying the global process environment.
func LoadShellConfig(projectDir string) []string {
	config := GetShellConfig(projectDir)
	keys := make([]string, 0, len(config))
	for key, val := range config {
		os.Setenv(key, val)
		keys = append(keys, key)
	}
	return keys
}

// ReloadShellConfig unsets the keys an earlier LoadShellConfig set, then loads
// projectDir's config. Without the retraction a file value exported by the
// first load reads as an environment override on the second, so the launcher's
// pre-picker load let a home MUXCODE_BUILD_CLI beat the chosen project's
// (MUX-207 review). Keys the user exported themselves keep their precedence.
func ReloadShellConfig(projectDir string, prior []string) []string {
	for _, key := range prior {
		os.Unsetenv(key)
	}
	return LoadShellConfig(projectDir)
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
