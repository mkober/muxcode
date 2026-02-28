package harness

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractToolCalls parses tool call objects from model text content when the
// model outputs raw JSON instead of using the structured tool_calls API field.
// Only tool calls whose name matches a known tool are returned. Returns an
// empty slice if no valid tool calls are found.
func ExtractToolCalls(text string, knownTools []string) []ToolCall {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	// Build lookup set for known tools
	known := make(map[string]bool, len(knownTools))
	for _, t := range knownTools {
		known[t] = true
	}

	// Strip markdown code fences to expose bare JSON
	stripped := stripCodeFences(text)

	// Extract all top-level JSON objects using brace-depth scanning
	candidates := extractJSONObjects(stripped)

	var calls []ToolCall
	for _, candidate := range candidates {
		tc, ok := parseToolCall(candidate, known)
		if !ok {
			continue
		}
		tc.ID = fmt.Sprintf("textcall_%d", len(calls))
		tc.Type = "function"
		calls = append(calls, tc)
	}

	return calls
}

// stripCodeFences removes markdown code fences (```json ... ``` or ``` ... ```)
// and returns the content between them concatenated with the rest of the text.
func stripCodeFences(text string) string {
	var b strings.Builder
	lines := strings.Split(text, "\n")
	inFence := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inFence && (trimmed == "```json" || trimmed == "```JSON" || trimmed == "```") {
			inFence = true
			continue
		}
		if inFence && trimmed == "```" {
			inFence = false
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}

	return b.String()
}

// extractJSONObjects finds all top-level JSON objects in text using brace-depth
// scanning. Handles nested braces correctly (e.g., arguments containing JSON).
func extractJSONObjects(text string) []string {
	var objects []string
	inString := false
	escaped := false
	depth := 0
	start := -1

	for i := 0; i < len(text); i++ {
		ch := text[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' && inString {
			escaped = true
			continue
		}

		if ch == '"' {
			inString = !inString
			continue
		}

		if inString {
			continue
		}

		if ch == '{' {
			if depth == 0 {
				start = i
			}
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 && start >= 0 {
				objects = append(objects, text[start:i+1])
				start = -1
			}
			if depth < 0 {
				depth = 0
			}
		}
	}

	return objects
}

// parseToolCall attempts to parse a JSON string as a tool call. Returns the
// ToolCall and true if the object has a valid "name" field matching a known
// tool and an "arguments" field.
func parseToolCall(jsonStr string, known map[string]bool) (ToolCall, bool) {
	// First unmarshal to check structure
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return ToolCall{}, false
	}

	// Must have "name" field
	nameRaw, hasName := raw["name"]
	if !hasName {
		return ToolCall{}, false
	}

	var name string
	if err := json.Unmarshal(nameRaw, &name); err != nil {
		return ToolCall{}, false
	}

	// Must be a known tool
	if !known[name] {
		return ToolCall{}, false
	}

	// Must have "arguments" field
	argsRaw, hasArgs := raw["arguments"]
	if !hasArgs {
		return ToolCall{}, false
	}

	// Validate that arguments is a JSON object
	var argsObj json.RawMessage
	if err := json.Unmarshal(argsRaw, &argsObj); err != nil {
		return ToolCall{}, false
	}

	return ToolCall{
		Function: FunctionCall{
			Name:      name,
			Arguments: argsObj,
		},
	}, true
}

// toolNames extracts tool names from a slice of ToolDef.
func toolNames(defs []ToolDef) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Function.Name
	}
	return names
}
