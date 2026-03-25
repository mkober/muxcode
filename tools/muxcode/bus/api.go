package bus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Environment represents an API testing environment with base URL, headers, and variables.
type Environment struct {
	Name      string            `json:"name"`
	BaseURL   string            `json:"base_url"`
	Headers   map[string]string `json:"headers"`
	Variables map[string]string `json:"variables"`
	CreatedAt int64             `json:"created_at"`
	UpdatedAt int64             `json:"updated_at"`
}

// Collection represents a named group of API requests.
type Collection struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	BaseURL     string    `json:"base_url,omitempty"`
	Requests    []Request `json:"requests"`
	CreatedAt   int64     `json:"created_at"`
	UpdatedAt   int64     `json:"updated_at"`
}

// Request represents a single API request within a collection.
type Request struct {
	Name    string            `json:"name"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	Query   map[string]string `json:"query,omitempty"`
}

// ApiHistoryEntry records a single executed API request.
type ApiHistoryEntry struct {
	TS         int64  `json:"ts"`
	Collection string `json:"collection,omitempty"`
	Request    string `json:"request,omitempty"`
	Method     string `json:"method"`
	URL        string `json:"url"`
	Status     int    `json:"status"`
	Duration   int64  `json:"duration_ms"`
}

// --- Path helpers ---

// ApiDir returns the project-local API directory path.
// Uses BUS_API_DIR env if set, otherwise defaults to ".muxcode/api".
func ApiDir() string {
	if v := os.Getenv("BUS_API_DIR"); v != "" {
		return v
	}
	return filepath.Join(".muxcode", "api")
}

// ApiEnvDir returns the API environments directory path.
func ApiEnvDir() string {
	return filepath.Join(ApiDir(), "environments")
}

// ApiCollectionDir returns the API collections directory path.
func ApiCollectionDir() string {
	return filepath.Join(ApiDir(), "collections")
}

// ApiHistoryPath returns the API history JSONL file path.
func ApiHistoryPath() string {
	return filepath.Join(ApiDir(), "history.jsonl")
}

// sanitizeFilename converts a name to a safe filename: lowercase, hyphens for spaces.
func sanitizeFilename(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

// envFilePath returns the file path for an environment by name.
func envFilePath(name string) string {
	return filepath.Join(ApiEnvDir(), sanitizeFilename(name)+".json")
}

// collectionFilePath returns the file path for a collection by name.
func collectionFilePath(name string) string {
	return filepath.Join(ApiCollectionDir(), sanitizeFilename(name)+".json")
}

// --- Environment CRUD ---

// ListEnvironments returns all environments from the environments directory.
func ListEnvironments() ([]Environment, error) {
	dir := ApiEnvDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var envs []Environment
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		env, err := readEnvFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip malformed files
		}
		envs = append(envs, env)
	}
	return envs, nil
}

// ReadEnvironment reads a single environment by name.
func ReadEnvironment(name string) (Environment, error) {
	return readEnvFile(envFilePath(name))
}

// CreateEnvironment creates a new environment. Returns error if it already exists.
func CreateEnvironment(env Environment) error {
	path := envFilePath(env.Name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("environment %q already exists", env.Name)
	}

	now := time.Now().Unix()
	env.CreatedAt = now
	env.UpdatedAt = now
	if env.Headers == nil {
		env.Headers = make(map[string]string)
	}
	if env.Variables == nil {
		env.Variables = make(map[string]string)
	}

	return writeEnvFile(env)
}

// WriteEnvironment overwrites an environment file.
func WriteEnvironment(env Environment) error {
	env.UpdatedAt = time.Now().Unix()
	return writeEnvFile(env)
}

// SetEnvironmentVar sets a variable on an environment.
func SetEnvironmentVar(name, key, value string) error {
	env, err := ReadEnvironment(name)
	if err != nil {
		return fmt.Errorf("reading environment %q: %v", name, err)
	}
	if env.Variables == nil {
		env.Variables = make(map[string]string)
	}
	env.Variables[key] = value
	return WriteEnvironment(env)
}

// DeleteEnvironment removes an environment file.
func DeleteEnvironment(name string) error {
	path := envFilePath(name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("environment %q not found", name)
	}
	return os.Remove(path)
}

func readEnvFile(path string) (Environment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Environment{}, err
	}
	var env Environment
	if err := json.Unmarshal(data, &env); err != nil {
		return Environment{}, err
	}
	return env, nil
}

func writeEnvFile(env Environment) error {
	if err := os.MkdirAll(ApiEnvDir(), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(envFilePath(env.Name), append(data, '\n'), 0644)
}

// --- Collection CRUD ---

// ListCollections returns all collections from the collections directory.
func ListCollections() ([]Collection, error) {
	dir := ApiCollectionDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cols []Collection
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		col, err := readCollectionFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip malformed files
		}
		cols = append(cols, col)
	}
	return cols, nil
}

// ReadCollection reads a single collection by name.
func ReadCollection(name string) (Collection, error) {
	return readCollectionFile(collectionFilePath(name))
}

// CreateCollection creates a new collection. Returns error if it already exists.
func CreateCollection(col Collection) error {
	path := collectionFilePath(col.Name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("collection %q already exists", col.Name)
	}

	now := time.Now().Unix()
	col.CreatedAt = now
	col.UpdatedAt = now
	if col.Requests == nil {
		col.Requests = []Request{}
	}

	return writeCollectionFile(col)
}

// WriteCollection overwrites a collection file.
func WriteCollection(col Collection) error {
	col.UpdatedAt = time.Now().Unix()
	return writeCollectionFile(col)
}

// AddRequest adds a request to a collection. Returns error if name already exists.
func AddRequest(collectionName string, req Request) error {
	col, err := ReadCollection(collectionName)
	if err != nil {
		return fmt.Errorf("reading collection %q: %v", collectionName, err)
	}

	for _, r := range col.Requests {
		if r.Name == req.Name {
			return fmt.Errorf("request %q already exists in collection %q", req.Name, collectionName)
		}
	}

	col.Requests = append(col.Requests, req)
	return WriteCollection(col)
}

// RemoveRequest removes a request from a collection by name.
func RemoveRequest(collectionName, requestName string) error {
	col, err := ReadCollection(collectionName)
	if err != nil {
		return fmt.Errorf("reading collection %q: %v", collectionName, err)
	}

	found := false
	var kept []Request
	for _, r := range col.Requests {
		if r.Name == requestName {
			found = true
			continue
		}
		kept = append(kept, r)
	}

	if !found {
		return fmt.Errorf("request %q not found in collection %q", requestName, collectionName)
	}

	col.Requests = kept
	return WriteCollection(col)
}

// DeleteCollection removes a collection file.
func DeleteCollection(name string) error {
	path := collectionFilePath(name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("collection %q not found", name)
	}
	return os.Remove(path)
}

func readCollectionFile(path string) (Collection, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Collection{}, err
	}
	var col Collection
	if err := json.Unmarshal(data, &col); err != nil {
		return Collection{}, err
	}
	return col, nil
}

func writeCollectionFile(col Collection) error {
	if err := os.MkdirAll(ApiCollectionDir(), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(col, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(collectionFilePath(col.Name), append(data, '\n'), 0644)
}

// --- History ---

// AppendApiHistory appends a history entry to the API history JSONL file.
func AppendApiHistory(entry ApiHistoryEntry) error {
	if err := os.MkdirAll(ApiDir(), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return appendToFile(ApiHistoryPath(), append(data, '\n'))
}

// ReadApiHistory reads API history entries, optionally filtered by collection.
// Pass empty collection to read all entries. Limit 0 means no limit.
func ReadApiHistory(collection string, limit int) ([]ApiHistoryEntry, error) {
	data, err := os.ReadFile(ApiHistoryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []ApiHistoryEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e ApiHistoryEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if collection != "" && e.Collection != collection {
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Apply limit (take last N entries)
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}

	return entries, nil
}

// --- Import ---

// ImportApiDir copies environments and collections from a source directory
// into the project's .muxcode/api/ directory. Existing files are not overwritten.
func ImportApiDir(srcDir string) (envCount, colCount int, err error) {
	// Import environments
	envSrc := filepath.Join(srcDir, "environments")
	if entries, readErr := os.ReadDir(envSrc); readErr == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			src := filepath.Join(envSrc, e.Name())
			dst := filepath.Join(ApiEnvDir(), e.Name())
			if _, statErr := os.Stat(dst); statErr == nil {
				continue // don't overwrite
			}
			if copyErr := copyFile(src, dst); copyErr == nil {
				envCount++
			}
		}
	}

	// Import collections
	colSrc := filepath.Join(srcDir, "collections")
	if entries, readErr := os.ReadDir(colSrc); readErr == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			src := filepath.Join(colSrc, e.Name())
			dst := filepath.Join(ApiCollectionDir(), e.Name())
			if _, statErr := os.Stat(dst); statErr == nil {
				continue // don't overwrite
			}
			if copyErr := copyFile(src, dst); copyErr == nil {
				colCount++
			}
		}
	}

	if envCount == 0 && colCount == 0 {
		return 0, 0, fmt.Errorf("no environments or collections found in %s", srcDir)
	}
	return envCount, colCount, nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// --- Formatters ---

// FormatEnvList formats environments as a human-readable table.
func FormatEnvList(envs []Environment) string {
	var b strings.Builder
	if len(envs) == 0 {
		b.WriteString("No environments.\n")
		return b.String()
	}

	b.WriteString(fmt.Sprintf("%-20s %-40s %s\n", "Name", "Base URL", "Variables"))
	b.WriteString(strings.Repeat("-", 80) + "\n")

	for _, e := range envs {
		vars := fmt.Sprintf("%d", len(e.Variables))
		b.WriteString(fmt.Sprintf("%-20s %-40s %s\n", e.Name, e.BaseURL, vars))
	}
	return b.String()
}

// FormatEnvDetail formats a single environment with full details.
func FormatEnvDetail(env Environment) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Name:     %s\n", env.Name))
	b.WriteString(fmt.Sprintf("Base URL: %s\n", env.BaseURL))

	if len(env.Headers) > 0 {
		b.WriteString("Headers:\n")
		for k, v := range env.Headers {
			b.WriteString(fmt.Sprintf("  %s: %s\n", k, v))
		}
	}

	if len(env.Variables) > 0 {
		b.WriteString("Variables:\n")
		for k, v := range env.Variables {
			b.WriteString(fmt.Sprintf("  %s = %s\n", k, v))
		}
	}

	if env.CreatedAt > 0 {
		b.WriteString(fmt.Sprintf("Created:  %s\n", time.Unix(env.CreatedAt, 0).Format("2006-01-02 15:04:05")))
	}
	if env.UpdatedAt > 0 {
		b.WriteString(fmt.Sprintf("Updated:  %s\n", time.Unix(env.UpdatedAt, 0).Format("2006-01-02 15:04:05")))
	}

	return b.String()
}

// FormatCollectionList formats collections as a human-readable table.
func FormatCollectionList(cols []Collection) string {
	var b strings.Builder
	if len(cols) == 0 {
		b.WriteString("No collections.\n")
		return b.String()
	}

	b.WriteString(fmt.Sprintf("%-20s %-40s %s\n", "Name", "Description", "Requests"))
	b.WriteString(strings.Repeat("-", 80) + "\n")

	for _, c := range cols {
		desc := c.Description
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}
		b.WriteString(fmt.Sprintf("%-20s %-40s %d\n", c.Name, desc, len(c.Requests)))
	}
	return b.String()
}

// FormatCollectionDetail formats a single collection with full details.
func FormatCollectionDetail(col Collection) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Name:        %s\n", col.Name))
	if col.Description != "" {
		b.WriteString(fmt.Sprintf("Description: %s\n", col.Description))
	}
	if col.BaseURL != "" {
		b.WriteString(fmt.Sprintf("Base URL:    %s\n", col.BaseURL))
	}

	if len(col.Requests) > 0 {
		b.WriteString(fmt.Sprintf("\nRequests (%d):\n", len(col.Requests)))
		b.WriteString(fmt.Sprintf("  %-20s %-8s %s\n", "Name", "Method", "Path"))
		b.WriteString("  " + strings.Repeat("-", 60) + "\n")
		for _, r := range col.Requests {
			b.WriteString(fmt.Sprintf("  %-20s %-8s %s\n", r.Name, r.Method, r.Path))
		}
	} else {
		b.WriteString("\nNo requests.\n")
	}

	return b.String()
}

// FormatApiHistory formats API history entries as a human-readable table.
func FormatApiHistory(entries []ApiHistoryEntry) string {
	var b strings.Builder
	if len(entries) == 0 {
		b.WriteString("No API history.\n")
		return b.String()
	}

	b.WriteString(fmt.Sprintf("%-20s %-8s %-6s %-8s %s\n", "Time", "Method", "Status", "Duration", "URL"))
	b.WriteString(strings.Repeat("-", 100) + "\n")

	for _, e := range entries {
		t := time.Unix(e.TS, 0).Format("2006-01-02 15:04:05")
		dur := fmt.Sprintf("%dms", e.Duration)
		b.WriteString(fmt.Sprintf("%-20s %-8s %-6d %-8s %s\n", t, e.Method, e.Status, dur, e.URL))
	}
	return b.String()
}
