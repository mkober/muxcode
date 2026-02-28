package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupApiTestDir creates a temp directory and sets BUS_API_DIR to point there.
// Returns a cleanup function.
func setupApiTestDir(t *testing.T) func() {
	t.Helper()
	dir := t.TempDir()
	os.Setenv("BUS_API_DIR", dir)
	return func() {
		os.Unsetenv("BUS_API_DIR")
	}
}

// --- Environment tests ---

func TestEnvironmentCRUD(t *testing.T) {
	cleanup := setupApiTestDir(t)
	defer cleanup()

	// List empty
	envs, err := ListEnvironments()
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	if len(envs) != 0 {
		t.Fatalf("expected 0 envs, got %d", len(envs))
	}

	// Create
	err = CreateEnvironment(Environment{
		Name:    "dev",
		BaseURL: "http://localhost:8080",
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	// Read back
	env, err := ReadEnvironment("dev")
	if err != nil {
		t.Fatalf("ReadEnvironment: %v", err)
	}
	if env.Name != "dev" {
		t.Errorf("Name = %q, want %q", env.Name, "dev")
	}
	if env.BaseURL != "http://localhost:8080" {
		t.Errorf("BaseURL = %q, want %q", env.BaseURL, "http://localhost:8080")
	}
	if env.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}

	// List with one entry
	envs, err = ListEnvironments()
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("expected 1 env, got %d", len(envs))
	}

	// Delete
	err = DeleteEnvironment("dev")
	if err != nil {
		t.Fatalf("DeleteEnvironment: %v", err)
	}

	// Verify deleted
	_, err = ReadEnvironment("dev")
	if err == nil {
		t.Error("expected error reading deleted environment")
	}
}

func TestEnvironmentDuplicate(t *testing.T) {
	cleanup := setupApiTestDir(t)
	defer cleanup()

	err := CreateEnvironment(Environment{Name: "staging", BaseURL: "http://staging"})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	err = CreateEnvironment(Environment{Name: "staging", BaseURL: "http://staging2"})
	if err == nil {
		t.Error("expected error creating duplicate environment")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want 'already exists'", err.Error())
	}
}

func TestSetEnvironmentVar(t *testing.T) {
	cleanup := setupApiTestDir(t)
	defer cleanup()

	err := CreateEnvironment(Environment{Name: "test-env", BaseURL: "http://test"})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	err = SetEnvironmentVar("test-env", "api_key", "secret123")
	if err != nil {
		t.Fatalf("SetEnvironmentVar: %v", err)
	}

	env, err := ReadEnvironment("test-env")
	if err != nil {
		t.Fatalf("ReadEnvironment: %v", err)
	}
	if env.Variables["api_key"] != "secret123" {
		t.Errorf("Variables[api_key] = %q, want %q", env.Variables["api_key"], "secret123")
	}
}

func TestDeleteEnvironmentNotFound(t *testing.T) {
	cleanup := setupApiTestDir(t)
	defer cleanup()

	err := DeleteEnvironment("nonexistent")
	if err == nil {
		t.Error("expected error deleting nonexistent environment")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want 'not found'", err.Error())
	}
}

// --- Collection tests ---

func TestCollectionCRUD(t *testing.T) {
	cleanup := setupApiTestDir(t)
	defer cleanup()

	// List empty
	cols, err := ListCollections()
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if len(cols) != 0 {
		t.Fatalf("expected 0 collections, got %d", len(cols))
	}

	// Create
	err = CreateCollection(Collection{
		Name:        "auth",
		Description: "Auth API",
	})
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	// Read back
	col, err := ReadCollection("auth")
	if err != nil {
		t.Fatalf("ReadCollection: %v", err)
	}
	if col.Name != "auth" {
		t.Errorf("Name = %q, want %q", col.Name, "auth")
	}
	if col.Description != "Auth API" {
		t.Errorf("Description = %q, want %q", col.Description, "Auth API")
	}
	if col.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}
	if col.Requests == nil {
		t.Error("Requests should be initialized, not nil")
	}

	// List with one entry
	cols, err = ListCollections()
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if len(cols) != 1 {
		t.Fatalf("expected 1 collection, got %d", len(cols))
	}

	// Delete
	err = DeleteCollection("auth")
	if err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}

	_, err = ReadCollection("auth")
	if err == nil {
		t.Error("expected error reading deleted collection")
	}
}

func TestCollectionDuplicate(t *testing.T) {
	cleanup := setupApiTestDir(t)
	defer cleanup()

	err := CreateCollection(Collection{Name: "users", Description: "Users API"})
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	err = CreateCollection(Collection{Name: "users", Description: "Duplicate"})
	if err == nil {
		t.Error("expected error creating duplicate collection")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want 'already exists'", err.Error())
	}
}

func TestAddRemoveRequest(t *testing.T) {
	cleanup := setupApiTestDir(t)
	defer cleanup()

	err := CreateCollection(Collection{Name: "api", Description: "Test API"})
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	// Add request
	err = AddRequest("api", Request{
		Name:   "get-users",
		Method: "GET",
		Path:   "/users",
	})
	if err != nil {
		t.Fatalf("AddRequest: %v", err)
	}

	// Add another
	err = AddRequest("api", Request{
		Name:   "create-user",
		Method: "POST",
		Path:   "/users",
		Body:   `{"name":"test"}`,
	})
	if err != nil {
		t.Fatalf("AddRequest: %v", err)
	}

	// Verify
	col, err := ReadCollection("api")
	if err != nil {
		t.Fatalf("ReadCollection: %v", err)
	}
	if len(col.Requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(col.Requests))
	}

	// Duplicate request name
	err = AddRequest("api", Request{Name: "get-users", Method: "GET", Path: "/users"})
	if err == nil {
		t.Error("expected error adding duplicate request name")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want 'already exists'", err.Error())
	}

	// Remove request
	err = RemoveRequest("api", "get-users")
	if err != nil {
		t.Fatalf("RemoveRequest: %v", err)
	}

	col, err = ReadCollection("api")
	if err != nil {
		t.Fatalf("ReadCollection: %v", err)
	}
	if len(col.Requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(col.Requests))
	}
	if col.Requests[0].Name != "create-user" {
		t.Errorf("remaining request = %q, want %q", col.Requests[0].Name, "create-user")
	}

	// Remove nonexistent
	err = RemoveRequest("api", "nonexistent")
	if err == nil {
		t.Error("expected error removing nonexistent request")
	}
}

func TestDeleteCollectionNotFound(t *testing.T) {
	cleanup := setupApiTestDir(t)
	defer cleanup()

	err := DeleteCollection("nonexistent")
	if err == nil {
		t.Error("expected error deleting nonexistent collection")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want 'not found'", err.Error())
	}
}

// --- History tests ---

func TestApiHistory(t *testing.T) {
	cleanup := setupApiTestDir(t)
	defer cleanup()

	// Empty history
	entries, err := ReadApiHistory("", 0)
	if err != nil {
		t.Fatalf("ReadApiHistory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}

	// Append entries
	for i := 0; i < 5; i++ {
		col := "api-a"
		if i%2 == 0 {
			col = "api-b"
		}
		err := AppendApiHistory(ApiHistoryEntry{
			TS:         int64(1700000000 + i),
			Collection: col,
			Method:     "GET",
			URL:        "http://test/endpoint",
			Status:     200,
			Duration:   int64(100 + i*10),
		})
		if err != nil {
			t.Fatalf("AppendApiHistory[%d]: %v", i, err)
		}
	}

	// Read all
	entries, err = ReadApiHistory("", 0)
	if err != nil {
		t.Fatalf("ReadApiHistory: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	// Filter by collection
	entries, err = ReadApiHistory("api-a", 0)
	if err != nil {
		t.Fatalf("ReadApiHistory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 api-a entries, got %d", len(entries))
	}

	entries, err = ReadApiHistory("api-b", 0)
	if err != nil {
		t.Fatalf("ReadApiHistory: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 api-b entries, got %d", len(entries))
	}

	// Limit
	entries, err = ReadApiHistory("", 2)
	if err != nil {
		t.Fatalf("ReadApiHistory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with limit, got %d", len(entries))
	}
	// Should be the last 2
	if entries[0].TS != 1700000003 {
		t.Errorf("entries[0].TS = %d, want %d", entries[0].TS, 1700000003)
	}
}

// --- Import tests ---

func TestImportApiDir(t *testing.T) {
	cleanup := setupApiTestDir(t)
	defer cleanup()

	// Create a source directory with example files
	srcDir := t.TempDir()
	envDir := filepath.Join(srcDir, "environments")
	colDir := filepath.Join(srcDir, "collections")
	os.MkdirAll(envDir, 0755)
	os.MkdirAll(colDir, 0755)

	os.WriteFile(filepath.Join(envDir, "test.json"), []byte(`{"name":"test","base_url":"http://test"}`), 0644)
	os.WriteFile(filepath.Join(colDir, "test-api.json"), []byte(`{"name":"test-api","description":"Test","requests":[]}`), 0644)

	envCount, colCount, err := ImportApiDir(srcDir)
	if err != nil {
		t.Fatalf("ImportApiDir: %v", err)
	}
	if envCount != 1 {
		t.Errorf("envCount = %d, want 1", envCount)
	}
	if colCount != 1 {
		t.Errorf("colCount = %d, want 1", colCount)
	}

	// Verify files exist
	envs, _ := ListEnvironments()
	if len(envs) != 1 {
		t.Errorf("expected 1 env after import, got %d", len(envs))
	}
	cols, _ := ListCollections()
	if len(cols) != 1 {
		t.Errorf("expected 1 collection after import, got %d", len(cols))
	}

	// Import again — should not overwrite (counts stay 0)
	envCount2, colCount2, err := ImportApiDir(srcDir)
	if err == nil {
		// No new files imported, so it should return error
		if envCount2 != 0 || colCount2 != 0 {
			t.Errorf("second import: envCount=%d colCount=%d, want 0,0", envCount2, colCount2)
		}
	}
}

func TestImportEmptyDir(t *testing.T) {
	cleanup := setupApiTestDir(t)
	defer cleanup()

	srcDir := t.TempDir()
	_, _, err := ImportApiDir(srcDir)
	if err == nil {
		t.Error("expected error importing empty directory")
	}
}

// --- Sanitize filename ---

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"dev", "dev"},
		{"My API", "my-api"},
		{"  Production  ", "production"},
		{"Test Environment", "test-environment"},
	}

	for _, tt := range tests {
		got := sanitizeFilename(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// --- Formatter tests ---

func TestFormatEnvList_Empty(t *testing.T) {
	out := FormatEnvList(nil)
	if !strings.Contains(out, "No environments") {
		t.Errorf("expected 'No environments', got %q", out)
	}
}

func TestFormatCollectionList_Empty(t *testing.T) {
	out := FormatCollectionList(nil)
	if !strings.Contains(out, "No collections") {
		t.Errorf("expected 'No collections', got %q", out)
	}
}

func TestFormatApiHistory_Empty(t *testing.T) {
	out := FormatApiHistory(nil)
	if !strings.Contains(out, "No API history") {
		t.Errorf("expected 'No API history', got %q", out)
	}
}

func TestFormatEnvDetail(t *testing.T) {
	env := Environment{
		Name:      "dev",
		BaseURL:   "http://localhost",
		Variables: map[string]string{"key": "val"},
	}
	out := FormatEnvDetail(env)
	if !strings.Contains(out, "dev") {
		t.Errorf("expected 'dev' in output")
	}
	if !strings.Contains(out, "key = val") {
		t.Errorf("expected variable in output")
	}
}

func TestFormatCollectionDetail(t *testing.T) {
	col := Collection{
		Name:        "auth",
		Description: "Auth API",
		Requests: []Request{
			{Name: "login", Method: "POST", Path: "/auth/login"},
		},
	}
	out := FormatCollectionDetail(col)
	if !strings.Contains(out, "auth") {
		t.Errorf("expected 'auth' in output")
	}
	if !strings.Contains(out, "login") {
		t.Errorf("expected 'login' in output")
	}
}

// --- Auto-create directories ---

func TestAutoCreateDirectories(t *testing.T) {
	cleanup := setupApiTestDir(t)
	defer cleanup()

	// Directories should not exist yet
	apiDir := ApiDir()
	if _, err := os.Stat(filepath.Join(apiDir, "environments")); !os.IsNotExist(err) {
		t.Fatal("environments dir should not exist yet")
	}

	// Creating an environment should auto-create the directory
	err := CreateEnvironment(Environment{Name: "auto-test", BaseURL: "http://auto"})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	if _, err := os.Stat(filepath.Join(apiDir, "environments")); os.IsNotExist(err) {
		t.Error("environments dir should have been auto-created")
	}
}
