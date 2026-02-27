package harness

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

// Executor executes tool calls with allowedTools enforcement.
type Executor struct {
	Patterns []string // allowed tool patterns
	WorkDir  string   // working directory for commands
}

// NewExecutor creates a new executor with the given patterns.
func NewExecutor(patterns []string) *Executor {
	wd, _ := os.Getwd()
	return &Executor{
		Patterns: patterns,
		WorkDir:  wd,
	}
}

// Execute runs a tool call and returns the result text.
func (e *Executor) Execute(ctx context.Context, call ToolCall) string {
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
func (e *Executor) executeBash(ctx context.Context, argsJSON json.RawMessage) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		// Fallback: small LLMs sometimes send arguments as a plain string
		var cmdStr string
		if err2 := json.Unmarshal(argsJSON, &cmdStr); err2 == nil && cmdStr != "" {
			args.Command = unwrapCommand(cmdStr)
		} else {
			return fmt.Sprintf("Error: invalid arguments: %v", err)
		}
	}
	if args.Command == "" {
		return "Error: command is required"
	}

	if !IsToolAllowed("bash", args.Command, e.Patterns) {
		return fmt.Sprintf("Error: command not allowed by tool profile: %s", args.Command)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, BashTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", args.Command)
	cmd.Dir = e.WorkDir

	out, err := cmd.CombinedOutput()
	result := string(out)

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

// unwrapCommand handles double-encoded JSON from small LLMs.
// When a model sends arguments as a JSON string (e.g. "{\"command\":\"ls\"}")
// instead of a JSON object, the string fallback captures the whole JSON.
// This function detects that case and extracts the "command" field.
func unwrapCommand(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{") {
		return s
	}
	var inner struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(trimmed), &inner) == nil && inner.Command != "" {
		return inner.Command
	}
	return s
}

// unwrapPath handles double-encoded JSON for path-based tool arguments.
func unwrapPath(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{") {
		return s
	}
	var inner struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(trimmed), &inner) == nil && inner.Path != "" {
		return inner.Path
	}
	return s
}

// unwrapPattern handles double-encoded JSON for pattern-based tool arguments.
func unwrapPattern(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{") {
		return s
	}
	var inner struct {
		Pattern string `json:"pattern"`
	}
	if json.Unmarshal([]byte(trimmed), &inner) == nil && inner.Pattern != "" {
		return inner.Pattern
	}
	return s
}

// executeRead reads a file and returns its contents.
func (e *Executor) executeRead(argsJSON json.RawMessage) string {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		var pathStr string
		if err2 := json.Unmarshal(argsJSON, &pathStr); err2 == nil && pathStr != "" {
			args.Path = unwrapPath(pathStr)
		} else {
			return fmt.Sprintf("Error: invalid arguments: %v", err)
		}
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
func (e *Executor) executeGlob(argsJSON json.RawMessage) string {
	var args struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		var patStr string
		if err2 := json.Unmarshal(argsJSON, &patStr); err2 == nil && patStr != "" {
			args.Pattern = unwrapPattern(patStr)
		} else {
			return fmt.Sprintf("Error: invalid arguments: %v", err)
		}
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
func (e *Executor) executeGrep(ctx context.Context, argsJSON json.RawMessage) string {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		var patStr string
		if err2 := json.Unmarshal(argsJSON, &patStr); err2 == nil && patStr != "" {
			args.Pattern = unwrapPattern(patStr)
		} else {
			return fmt.Sprintf("Error: invalid arguments: %v", err)
		}
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

	if len(result) > MaxOutputLen {
		result = result[:MaxOutputLen] + "\n... [output truncated]"
	}

	if err != nil {
		if exitCodeStr(err) == "1" && result == "" {
			return "No matches found"
		}
		if cmdCtx.Err() == context.DeadlineExceeded {
			return result + "\nError: grep timed out"
		}
		if result != "" {
			return result
		}
		return fmt.Sprintf("Error: %v", err)
	}

	return result
}

// executeWrite writes content to a file.
func (e *Executor) executeWrite(argsJSON json.RawMessage) string {
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
func (e *Executor) executeEdit(argsJSON json.RawMessage) string {
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
