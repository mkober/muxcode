package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MemoryEntry represents a single parsed section from a memory file.
type MemoryEntry struct {
	Role      string // "shared", "build", "edit", etc.
	Section   string // from "## Title" line
	Timestamp string // from "_YYYY-MM-DD HH:MM_" line
	Content   string // body text after timestamp
}

// SearchResult pairs a memory entry with its relevance score.
type SearchResult struct {
	Entry MemoryEntry
	Score float64
}

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

// ParseMemoryEntries splits memory file content into individual entries.
// It relies on the rigid format produced by AppendMemory():
//
//	## Section Title
//	_2026-02-21 14:27_
//
//	body text
func ParseMemoryEntries(content, role string) []MemoryEntry {
	if strings.TrimSpace(content) == "" {
		return nil
	}

	var entries []MemoryEntry
	// Split on "## " headers — first element is text before the first header (usually empty)
	parts := strings.Split(content, "\n## ")

	for i, part := range parts {
		if i == 0 {
			// Handle case where content starts with "## " (leading newline stripped)
			if !strings.HasPrefix(content, "## ") {
				continue
			}
			// First part when content starts with "## " — no leading newline was consumed
		}

		lines := strings.SplitN(part, "\n", 2)
		if len(lines) == 0 {
			continue
		}

		section := strings.TrimSpace(lines[0])
		if section == "" {
			continue
		}

		var timestamp, body string
		if len(lines) > 1 {
			remaining := lines[1]
			remainLines := strings.Split(remaining, "\n")
			bodyStart := 0
			for j, line := range remainLines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "_") && strings.HasSuffix(trimmed, "_") && len(trimmed) > 2 {
					timestamp = trimmed[1 : len(trimmed)-1]
					bodyStart = j + 1
					break
				}
			}
			if bodyStart < len(remainLines) {
				body = strings.TrimSpace(strings.Join(remainLines[bodyStart:], "\n"))
			}
		}

		entries = append(entries, MemoryEntry{
			Role:      role,
			Section:   section,
			Timestamp: timestamp,
			Content:   body,
		})
	}

	return entries
}

// ListMemoryFiles scans the memory directory for .md files and returns role names.
// Returns an empty slice (not an error) if the directory does not exist.
func ListMemoryFiles() ([]string, error) {
	dir := MemoryDir()
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var roles []string
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if strings.HasSuffix(name, ".md") {
			roles = append(roles, strings.TrimSuffix(name, ".md"))
		}
	}
	return roles, nil
}

// AllMemoryEntries reads all memory files and returns their parsed entries.
func AllMemoryEntries() ([]MemoryEntry, error) {
	roles, err := ListMemoryFiles()
	if err != nil {
		return nil, err
	}

	var all []MemoryEntry
	for _, role := range roles {
		content, err := ReadMemory(role)
		if err != nil {
			return nil, err
		}
		entries := ParseMemoryEntries(content, role)
		all = append(all, entries...)
	}
	return all, nil
}

// SearchMemory searches all memory entries for the given query terms.
// If roleFilter is non-empty, only entries from that role are included.
// If limit > 0, results are truncated to that count.
func SearchMemory(query, roleFilter string, limit int) ([]SearchResult, error) {
	entries, err := AllMemoryEntries()
	if err != nil {
		return nil, err
	}

	queryTerms := strings.Fields(strings.ToLower(query))
	if len(queryTerms) == 0 {
		return nil, nil
	}

	var results []SearchResult
	for _, entry := range entries {
		if roleFilter != "" && entry.Role != roleFilter {
			continue
		}
		score := scoreEntry(entry, queryTerms)
		if score > 0 {
			results = append(results, SearchResult{Entry: entry, Score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// scoreEntry computes a relevance score for an entry against query terms.
// Header matches are weighted 2x body matches.
func scoreEntry(entry MemoryEntry, queryTerms []string) float64 {
	headerLower := strings.ToLower(entry.Section)
	contentLower := strings.ToLower(entry.Content)

	var score float64
	for _, term := range queryTerms {
		score += float64(strings.Count(headerLower, term)) * 2.0
		score += float64(strings.Count(contentLower, term))
	}
	return score
}

// FormatSearchResults formats search results in a block style matching FormatMessage().
func FormatSearchResults(results []SearchResult) string {
	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "--- [%s] %s (%s) score:%.1f ---\n",
			r.Entry.Role, r.Entry.Section, r.Entry.Timestamp, r.Score)
		b.WriteString(r.Entry.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// FormatMemoryList formats entries as a columnar inventory.
func FormatMemoryList(entries []MemoryEntry) string {
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%-10s %-36s %s\n", e.Role, e.Section, e.Timestamp)
	}
	return b.String()
}
