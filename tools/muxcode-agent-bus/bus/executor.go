package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// BashTimeout is the max execution time for bash commands.
	BashTimeout = 60 * time.Second
	// MaxOutputLen is the maximum output length returned from tool execution.
	MaxOutputLen = 10000
)

// ToolExecutor executes tool calls with allowedTools enforcement.
type ToolExecutor struct {
	Patterns []string // resolved tool patterns for the role
	WorkDir  string   // working directory for commands
}

// NewToolExecutor creates a new executor with the resolved tool patterns for a role.
func NewToolExecutor(role string) *ToolExecutor {
	wd, _ := os.Getwd()
	return &ToolExecutor{
		Patterns: ResolveTools(role),
		WorkDir:  wd,
	}
}

// Execute runs a tool call and returns the result text.
// Returns an error description if the tool call is denied or fails.
func (e *ToolExecutor) Execute(ctx context.Context, call ToolCall) string {
	name := call.Function.Name
	args := call.Function.Arguments

	switch name {
	case "bash":
		return e.executeBash(ctx, args)
	case "read_file":
		return e.executeRead(args)
	case "glob":
		return e.executeGlob(args)
	case "grep":
		return e.executeGrep(ctx, args)
	case "write_file":
		return e.executeWrite(args)
	case "edit_file":
		return e.executeEdit(args)
	default:
		return fmt.Sprintf("Error: unknown tool %q", name)
	}
}

// executeBash runs a bash command with timeout and output truncation.
func (e *ToolExecutor) executeBash(ctx context.Context, argsJSON json.RawMessage) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}
	if args.Command == "" {
		return "Error: command is required"
	}

	// Check allowedTools
	if !IsToolAllowed("bash", args.Command, e.Patterns) {
		return fmt.Sprintf("Error: command not allowed by tool profile: %s", args.Command)
	}

	// Execute with timeout
	cmdCtx, cancel := context.WithTimeout(ctx, BashTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", args.Command)
	cmd.Dir = e.WorkDir

	out, err := cmd.CombinedOutput()
	result := string(out)

	// Truncate if too long
	if len(result) > MaxOutputLen {
		result = result[:MaxOutputLen] + "\n... [output truncated]"
	}

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return result + "\nError: command timed out after 60 seconds"
		}
		return result + "\nExit code: " + exitCodeStr(err)
	}

	return result
}

// executeRead reads a file and returns its contents.
func (e *ToolExecutor) executeRead(argsJSON json.RawMessage) string {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}
	if args.Path == "" {
		return "Error: path is required"
	}

	if !IsToolAllowed("read_file", "", e.Patterns) {
		return "Error: read_file not allowed by tool profile"
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	result := string(data)
	if len(result) > MaxOutputLen {
		result = result[:MaxOutputLen] + "\n... [output truncated]"
	}

	return result
}

// executeGlob finds files matching a glob pattern.
func (e *ToolExecutor) executeGlob(argsJSON json.RawMessage) string {
	var args struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}
	if args.Pattern == "" {
		return "Error: pattern is required"
	}

	if !IsToolAllowed("glob", "", e.Patterns) {
		return "Error: glob not allowed by tool profile"
	}

	matches, err := filepath.Glob(args.Pattern)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	if len(matches) == 0 {
		return "No matches found"
	}

	result := strings.Join(matches, "\n")
	if len(result) > MaxOutputLen {
		result = result[:MaxOutputLen] + "\n... [output truncated]"
	}

	return result
}

// executeGrep searches files using grep -rn.
func (e *ToolExecutor) executeGrep(ctx context.Context, argsJSON json.RawMessage) string {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}
	if args.Pattern == "" {
		return "Error: pattern is required"
	}

	if !IsToolAllowed("grep", "", e.Patterns) {
		return "Error: grep not allowed by tool profile"
	}

	path := args.Path
	if path == "" {
		path = "."
	}

	cmdCtx, cancel := context.WithTimeout(ctx, BashTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "grep", "-rn",
		"--exclude-dir=.git", "--exclude-dir=node_modules",
		"--exclude-dir=vendor", "--exclude-dir=__pycache__",
		args.Pattern, path)
	cmd.Dir = e.WorkDir

	out, err := cmd.CombinedOutput()
	result := string(out)

	// Truncate if too long
	if len(result) > MaxOutputLen {
		result = result[:MaxOutputLen] + "\n... [output truncated]"
	}

	if err != nil {
		// grep returns exit 1 for no matches — not a real error
		if exitCodeStr(err) == "1" && result == "" {
			return "No matches found"
		}
		if cmdCtx.Err() == context.DeadlineExceeded {
			return result + "\nError: grep timed out"
		}
		// Some grep errors, but we may still have partial output
		if result != "" {
			return result
		}
		return fmt.Sprintf("Error: %v", err)
	}

	return result
}

// executeWrite writes content to a file.
func (e *ToolExecutor) executeWrite(argsJSON json.RawMessage) string {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}
	if args.Path == "" {
		return "Error: path is required"
	}

	if !IsToolAllowed("write_file", "", e.Patterns) {
		return "Error: write_file not allowed by tool profile"
	}

	// Ensure parent directory exists
	dir := filepath.Dir(args.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Sprintf("Error creating directory: %v", err)
	}

	if err := os.WriteFile(args.Path, []byte(args.Content), 0644); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return fmt.Sprintf("Wrote %d bytes to %s", len(args.Content), args.Path)
}

// executeEdit performs a string replacement in a file.
func (e *ToolExecutor) executeEdit(argsJSON json.RawMessage) string {
	var args struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}
	if args.Path == "" {
		return "Error: path is required"
	}
	if args.OldString == "" {
		return "Error: old_string is required"
	}

	if !IsToolAllowed("edit_file", "", e.Patterns) {
		return "Error: edit_file not allowed by tool profile"
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return fmt.Sprintf("Error reading file: %v", err)
	}

	content := string(data)
	count := strings.Count(content, args.OldString)
	if count == 0 {
		return "Error: old_string not found in file"
	}
	if count > 1 {
		return fmt.Sprintf("Error: old_string found %d times — must be unique", count)
	}

	newContent := strings.Replace(content, args.OldString, args.NewString, 1)
	if err := os.WriteFile(args.Path, []byte(newContent), 0644); err != nil {
		return fmt.Sprintf("Error writing file: %v", err)
	}

	return fmt.Sprintf("Replaced 1 occurrence in %s", args.Path)
}

// exitCodeStr extracts the exit code from an exec error.
func exitCodeStr(err error) string {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return fmt.Sprintf("%d", exitErr.ExitCode())
	}
	return "unknown"
}
