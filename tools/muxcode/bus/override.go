package bus

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RuntimeOverridePath returns the path to the runtime override file for a role.
// Override files are shell-sourceable key=value pairs stored at:
//
//	/tmp/muxcode-bus-{session}/config/{role}.env
func RuntimeOverridePath(session, role string) string {
	return filepath.Join(RuntimeConfigDir(session), role+".env")
}

// WriteRuntimeOverride writes a key=value pair to the runtime override file
// for a role. Creates the config directory if it doesn't exist. If the file
// already exists, the key is updated in place; other keys are preserved.
//
// Example:
//
//	err := WriteRuntimeOverride("main", "build", "MUXCODE_BUILD_CLI", "opencode")
func WriteRuntimeOverride(session, role, key, value string) error {
	// Read existing overrides
	existing, err := ReadRuntimeOverrides(session, role)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read existing overrides: %w", err)
	}
	if existing == nil {
		existing = make(map[string]string)
	}

	// Set/update the key
	existing[key] = value

	// Ensure config directory exists
	if err := os.MkdirAll(RuntimeConfigDir(session), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Write back with sorted keys for deterministic output
	var keys []string
	for k := range existing {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		lines = append(lines, k+"="+existing[k])
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(RuntimeOverridePath(session, role), []byte(content), 0644); err != nil {
		return fmt.Errorf("write override file: %w", err)
	}

	return nil
}

// ReadRuntimeOverrides reads all key=value pairs from the runtime override file.
// Returns nil map and nil error if the file doesn't exist.
// Lines starting with # are treated as comments and skipped.
func ReadRuntimeOverrides(session, role string) (map[string]string, error) {
	data, err := os.ReadFile(RuntimeOverridePath(session, role))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read override file: %w", err)
	}

	result := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Parse key=value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan override file: %w", err)
	}

	return result, nil
}

// GetRuntimeOverrides reads the runtime override file for a role and returns
// the key=value pairs as a map. Returns nil if the override file doesn't exist.
//
// Callers can apply these overrides to a command's environment by merging them
// into the cmd.Env slice, or apply them process-wide via applyEnvOverrides.
func GetRuntimeOverrides(session, role string) (map[string]string, error) {
	overrides, err := ReadRuntimeOverrides(session, role)
	if err != nil {
		return nil, err
	}
	return overrides, nil
}

// applyEnvOverrides applies the given overrides to the current process environment.
// This modifies the global process environment and should be used sparingly.
func applyEnvOverrides(overrides map[string]string) {
	for key, value := range overrides {
		os.Setenv(key, value)
	}
}

// LoadRuntimeOverrides reads the runtime override file for a role and sets
// each key=value pair as an environment variable via os.Setenv. This is the
// primary entry point used by ResolveProviderCLI, ResolveLaunchConfig, and
// ReloadAgent to inject runtime overrides before provider/model resolution.
func LoadRuntimeOverrides(session, role string) error {
	overrides, err := GetRuntimeOverrides(session, role)
	if err != nil {
		return err
	}
	applyEnvOverrides(overrides)
	return nil
}

// ClearRuntimeOverrides removes the runtime override file for a role.
// Returns nil if the file doesn't exist.
func ClearRuntimeOverrides(session, role string) error {
	if err := os.Remove(RuntimeOverridePath(session, role)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove override file: %w", err)
	}
	return nil
}
