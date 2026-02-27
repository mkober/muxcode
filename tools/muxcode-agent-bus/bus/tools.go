package bus

import (
	"strings"
)

// BuildToolDefs returns tool definitions for the Ollama API based on a role's
// allowed tools. Only tools that the role is permitted to use are included.
func BuildToolDefs(role string) []ToolDef {
	patterns := ResolveTools(role)
	if len(patterns) == 0 {
		return nil
	}

	var defs []ToolDef

	// Check which tool categories are allowed
	hasBash := hasToolPattern(patterns, "Bash")
	hasRead := hasToolPattern(patterns, "Read")
	hasGlob := hasToolPattern(patterns, "Glob")
	hasGrep := hasToolPattern(patterns, "Grep")
	hasWrite := hasToolPattern(patterns, "Write")
	hasEdit := hasToolPattern(patterns, "Edit")

	if hasBash {
		defs = append(defs, ToolDef{
			Type: "function",
			Function: ToolDefFunction{
				Name:        "bash",
				Description: "Execute a bash command and return its output. Use for git operations, file manipulation, and running tools.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{
							"type":        "string",
							"description": "The bash command to execute",
						},
					},
					"required": []string{"command"},
				},
			},
		})
	}

	if hasRead {
		defs = append(defs, ToolDef{
			Type: "function",
			Function: ToolDefFunction{
				Name:        "read_file",
				Description: "Read the contents of a file. Returns the file content as text.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Absolute path to the file to read",
						},
					},
					"required": []string{"path"},
				},
			},
		})
	}

	if hasGlob {
		defs = append(defs, ToolDef{
			Type: "function",
			Function: ToolDefFunction{
				Name:        "glob",
				Description: "Find files matching a glob pattern. Returns matching file paths. Uses single * per directory level (** is not supported).",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"pattern": map[string]interface{}{
							"type":        "string",
							"description": "Glob pattern to match files (e.g. 'src/*.ts', '*.go'). Use single * per directory level.",
						},
					},
					"required": []string{"pattern"},
				},
			},
		})
	}

	if hasGrep {
		defs = append(defs, ToolDef{
			Type: "function",
			Function: ToolDefFunction{
				Name:        "grep",
				Description: "Search file contents using grep. Returns matching lines with file paths and line numbers.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"pattern": map[string]interface{}{
							"type":        "string",
							"description": "Regular expression pattern to search for",
						},
						"path": map[string]interface{}{
							"type":        "string",
							"description": "File or directory to search in (default: current directory)",
						},
					},
					"required": []string{"pattern"},
				},
			},
		})
	}

	if hasWrite {
		defs = append(defs, ToolDef{
			Type: "function",
			Function: ToolDefFunction{
				Name:        "write_file",
				Description: "Write content to a file, creating it if needed or overwriting if it exists.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Absolute path to the file to write",
						},
						"content": map[string]interface{}{
							"type":        "string",
							"description": "Content to write to the file",
						},
					},
					"required": []string{"path", "content"},
				},
			},
		})
	}

	if hasEdit {
		defs = append(defs, ToolDef{
			Type: "function",
			Function: ToolDefFunction{
				Name:        "edit_file",
				Description: "Replace a specific string in a file. The old_string must be unique in the file.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Absolute path to the file to edit",
						},
						"old_string": map[string]interface{}{
							"type":        "string",
							"description": "The exact string to find and replace",
						},
						"new_string": map[string]interface{}{
							"type":        "string",
							"description": "The replacement string",
						},
					},
					"required": []string{"path", "old_string", "new_string"},
				},
			},
		})
	}

	return defs
}

// hasToolPattern checks if any pattern in the list grants access to a tool type.
func hasToolPattern(patterns []string, toolType string) bool {
	for _, p := range patterns {
		if p == toolType {
			return true
		}
		if toolType == "Bash" && strings.HasPrefix(p, "Bash(") {
			return true
		}
	}
	return false
}

// IsToolAllowed checks if a specific tool invocation is permitted by the
// resolved tool patterns for a role. For Bash tools, it matches the command
// against Bash(...) glob patterns. For other tools (Read, Glob, Grep, Write,
// Edit), it checks if the tool type appears in the patterns.
func IsToolAllowed(toolName string, command string, patterns []string) bool {
	switch toolName {
	case "bash":
		return isBashAllowed(command, patterns)
	case "read_file":
		return hasToolPattern(patterns, "Read")
	case "glob":
		return hasToolPattern(patterns, "Glob")
	case "grep":
		return hasToolPattern(patterns, "Grep")
	case "write_file":
		return hasToolPattern(patterns, "Write")
	case "edit_file":
		return hasToolPattern(patterns, "Edit")
	default:
		return false
	}
}

// isBashAllowed checks if a bash command is permitted by the tool patterns.
// Matches against Bash(pattern) entries using glob-style matching where
// * matches any characters including spaces.
func isBashAllowed(command string, patterns []string) bool {
	for _, p := range patterns {
		if !strings.HasPrefix(p, "Bash(") || !strings.HasSuffix(p, ")") {
			continue
		}
		// Extract the inner pattern: "Bash(git *)" -> "git *"
		inner := p[5 : len(p)-1]
		if globMatch(inner, command) {
			return true
		}
	}
	return false
}

// globMatch performs glob-style pattern matching where * matches any sequence
// of characters (including spaces). This differs from filepath.Match which
// treats * as not matching path separators.
func globMatch(pattern, text string) bool {
	// Fast path: exact match
	if pattern == text {
		return true
	}

	// dp[i][j] = true if pattern[:i] matches text[:j]
	// Use simplified two-row DP for memory efficiency
	pLen := len(pattern)
	tLen := len(text)

	// prev = dp[i-1], curr = dp[i]
	prev := make([]bool, tLen+1)
	curr := make([]bool, tLen+1)

	prev[0] = true // empty pattern matches empty text

	for i := 1; i <= pLen; i++ {
		curr[0] = prev[0] && pattern[i-1] == '*'

		for j := 1; j <= tLen; j++ {
			if pattern[i-1] == '*' {
				// * matches zero chars (curr[j-1]) or one more char (prev[j])
				curr[j] = curr[j-1] || prev[j]
			} else {
				// Exact character match
				curr[j] = prev[j-1] && (pattern[i-1] == text[j-1] || pattern[i-1] == '?')
			}
		}

		prev, curr = curr, prev
		for k := range curr {
			curr[k] = false
		}
	}

	return prev[tLen]
}
