package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Log handles the "muxcode-agent-bus log" subcommand.
// Usage: muxcode-agent-bus log <role> "<summary>" [--exit-code N] [--command CMD] [--output TEXT] [--output-stdin] [--output-file PATH]
//
// Output sources (mutually exclusive):
//   --output TEXT        inline output string
//   --output-stdin       read output from stdin (for piping)
//   --output-file PATH   read output from a file (preferred for multi-line content)
//
// --output-file is the preferred method for multi-line output because it avoids
// piping through printf, which breaks allowedTools glob patterns when the LLM
// embeds literal newlines in the command string.
//
// Appends a timestamped JSON entry to <bus-dir>/<role>-history.jsonl.
// Rotates to keep the last 100 entries.
func Log(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus log <role> \"<summary>\" [--exit-code N] [--command CMD] [--output TEXT] [--output-stdin] [--output-file PATH]\n")
		os.Exit(1)
	}

	role := args[0]
	summary := args[1]
	remaining := args[2:]

	exitCode := "0"
	command := ""
	output := ""
	outputStdin := false
	outputFile := ""

	for i := 0; i < len(remaining); i++ {
		switch remaining[i] {
		case "--exit-code":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --exit-code requires a value\n")
				os.Exit(1)
			}
			i++
			exitCode = remaining[i]
		case "--command":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --command requires a value\n")
				os.Exit(1)
			}
			i++
			command = remaining[i]
		case "--output":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --output requires a value\n")
				os.Exit(1)
			}
			i++
			output = remaining[i]
		case "--output-stdin":
			outputStdin = true
		case "--output-file":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --output-file requires a path\n")
				os.Exit(1)
			}
			i++
			outputFile = remaining[i]
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", remaining[i])
			os.Exit(1)
		}
	}

	// Validate mutual exclusivity of output sources
	outputSources := 0
	if output != "" {
		outputSources++
	}
	if outputStdin {
		outputSources++
	}
	if outputFile != "" {
		outputSources++
	}
	if outputSources > 1 {
		fmt.Fprintf(os.Stderr, "Error: --output, --output-stdin, and --output-file are mutually exclusive\n")
		os.Exit(1)
	}

	// Read output from stdin if --output-stdin is set
	if outputStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}
		output = strings.TrimRight(string(data), "\n")
	}

	// Read output from file if --output-file is set
	if outputFile != "" {
		data, err := os.ReadFile(outputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading output file %s: %v\n", outputFile, err)
			os.Exit(1)
		}
		output = strings.TrimRight(string(data), "\n")
	}

	// Derive outcome from exit code
	outcome := "success"
	if exitCode != "0" {
		outcome = "failure"
	}

	session := bus.BusSession()
	historyPath := bus.HistoryPath(session, role)

	entry := map[string]interface{}{
		"ts":        time.Now().Unix(),
		"summary":   summary,
		"exit_code": exitCode,
		"command":   command,
		"output":    output,
		"outcome":   outcome,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}

	// Ensure bus directory exists
	busDir := bus.BusDir(session)
	if err := os.MkdirAll(busDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating bus directory: %v\n", err)
		os.Exit(1)
	}

	// Open file for append (create if needed), write entry
	f, err := os.OpenFile(historyPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening history file: %v\n", err)
		os.Exit(1)
	}

	// File-level locking for safety (non-blocking, best-effort)
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)

	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		fmt.Fprintf(os.Stderr, "Error writing history entry: %v\n", err)
		os.Exit(1)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()

	// Rotate: keep last 100 entries
	rotateHistory(historyPath, 100)

	fmt.Printf("Logged %s: %s (%s)\n", role, summary, outcome)
}

// rotateHistory truncates a JSONL file to keep only the last maxEntries lines.
func rotateHistory(path string, maxEntries int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := splitLines(data)
	if len(lines) <= maxEntries {
		return
	}

	// Keep only the last maxEntries lines
	keep := lines[len(lines)-maxEntries:]
	var out []byte
	for _, line := range keep {
		out = append(out, line...)
		out = append(out, '\n')
	}

	_ = os.WriteFile(path, out, 0644)
}

// splitLines splits data into non-empty lines.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			line := data[start:i]
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(data) {
		line := data[start:]
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}
