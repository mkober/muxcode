package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ReadMemory reads the memory file for a role. Returns empty string if not found.
func ReadMemory(role string) (string, error) {
	data, err := os.ReadFile(MemoryPath(role))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// AppendMemory appends a formatted section to a role's memory file.
func AppendMemory(section, content, role string) error {
	memPath := MemoryPath(role)
	if err := os.MkdirAll(filepath.Dir(memPath), 0755); err != nil {
		return err
	}

	ts := time.Now().Format("2006-01-02 15:04")
	entry := fmt.Sprintf("\n## %s\n_%s_\n\n%s\n", section, ts, content)

	f, err := os.OpenFile(memPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write([]byte(entry))
	return err
}

// ReadContext reads shared memory and the role's own memory, concatenated.
func ReadContext(role string) (string, error) {
	shared, err := ReadMemory("shared")
	if err != nil {
		return "", err
	}

	own, err := ReadMemory(role)
	if err != nil {
		return "", err
	}

	result := ""
	if shared != "" {
		result += "# Shared Memory\n\n" + shared + "\n"
	}
	if own != "" {
		if result != "" {
			result += "\n"
		}
		result += fmt.Sprintf("# %s Memory\n\n", role) + own + "\n"
	}
	return result, nil
}
