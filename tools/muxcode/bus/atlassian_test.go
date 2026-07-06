package bus

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadShellConfig(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")

	content := `# Comment line
JIRA_BASE_URL=https://test.atlassian.net
JIRA_USER_EMAIL="user@example.com"
JIRA_API_TOKEN='secret-token-123'
export CONFLUENCE_BASE_URL=https://confluence.example.com

EMPTY_VAL=
`
	os.WriteFile(cfgFile, []byte(content), 0644)

	env := make(map[string]string)
	loadShellConfig(cfgFile, env)

	tests := []struct {
		key, want string
	}{
		{"JIRA_BASE_URL", "https://test.atlassian.net"},
		{"JIRA_USER_EMAIL", "user@example.com"},
		{"JIRA_API_TOKEN", "secret-token-123"},
		{"CONFLUENCE_BASE_URL", "https://confluence.example.com"},
		{"EMPTY_VAL", ""},
	}

	for _, tt := range tests {
		if got := env[tt.key]; got != tt.want {
			t.Errorf("key %s: got %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestLoadShellConfig_MissingFile(t *testing.T) {
	env := make(map[string]string)
	loadShellConfig("/nonexistent/config", env)
	if len(env) != 0 {
		t.Errorf("expected empty env for missing file, got %d entries", len(env))
	}
}

func TestLoadAtlassianConfig_EnvOverride(t *testing.T) {
	t.Setenv("JIRA_BASE_URL", "https://env.atlassian.net")
	t.Setenv("JIRA_USER_EMAIL", "env@test.com")
	t.Setenv("JIRA_API_TOKEN", "env-token")

	cfg, err := LoadAtlassianConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.JiraBaseURL != "https://env.atlassian.net" {
		t.Errorf("JiraBaseURL: got %q, want env value", cfg.JiraBaseURL)
	}
	if cfg.UserEmail != "env@test.com" {
		t.Errorf("UserEmail: got %q, want env value", cfg.UserEmail)
	}
	// Confluence should fall back to Jira
	if cfg.ConfluenceBaseURL != "https://env.atlassian.net" {
		t.Errorf("ConfluenceBaseURL: got %q, want Jira fallback", cfg.ConfluenceBaseURL)
	}
}

func TestLoadAtlassianConfig_ConfluenceFallback(t *testing.T) {
	t.Setenv("JIRA_BASE_URL", "https://jira.example.com")
	t.Setenv("JIRA_USER_EMAIL", "u@e.com")
	t.Setenv("JIRA_API_TOKEN", "t")
	t.Setenv("CONFLUENCE_BASE_URL", "")

	cfg, _ := LoadAtlassianConfig()
	if cfg.ConfluenceBaseURL != "https://jira.example.com" {
		t.Errorf("ConfluenceBaseURL should fall back to JiraBaseURL, got %q", cfg.ConfluenceBaseURL)
	}
}

func TestFlattenADF(t *testing.T) {
	tests := []struct {
		name string
		adf  string
		want string
	}{
		{
			name: "simple paragraph",
			adf:  `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Hello world"}]}]}`,
			want: "Hello world",
		},
		{
			name: "nested content",
			adf:  `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"First"},{"type":"text","text":"Second"}]}]}`,
			want: "First Second",
		},
		{
			name: "null input",
			adf:  `null`,
			want: "",
		},
		{
			name: "empty object",
			adf:  `{}`,
			want: "",
		},
		{
			name: "bullet list",
			adf: `{"type":"doc","content":[{"type":"bulletList","content":[
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Item 1"}]}]},
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Item 2"}]}]}
			]}]}`,
			want: "Item 1 Item 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flattenADF(json.RawMessage(tt.adf))
			if got != tt.want {
				t.Errorf("flattenADF() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Jira API tests with httptest ---

func newTestConfig(serverURL string) *AtlassianConfig {
	return &AtlassianConfig{
		JiraBaseURL:       serverURL,
		ConfluenceBaseURL: serverURL,
		UserEmail:         "test@example.com",
		APIToken:          "test-token",
	}
}

func TestJiraRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/TEST-123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// Check basic auth
		user, pass, ok := r.BasicAuth()
		if !ok || user != "test@example.com" || pass != "test-token" {
			t.Errorf("bad auth: %s:%s (ok=%v)", user, pass, ok)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"fields": {
				"summary": "Test issue",
				"status": {"name": "In Progress"},
				"assignee": {"displayName": "Jane Doe"},
				"issuetype": {"name": "Story"},
				"priority": {"name": "High"},
				"description": {"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Fix the bug"}]}]}
			}
		}`)
	}))
	defer srv.Close()

	cfg := newTestConfig(srv.URL)
	result, err := JiraRead(cfg, "TEST-123")
	if err != nil {
		t.Fatalf("JiraRead failed: %v", err)
	}

	checks := []string{
		"=== TEST-123 ===",
		"Summary: Test issue",
		"Type: Story | Priority: High",
		"Status: In Progress | Assignee: Jane Doe",
		"Fix the bug",
	}
	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("result missing %q:\n%s", check, result)
		}
	}
}

func TestJiraRead_NullAssignee(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"fields":{"summary":"No owner","status":{"name":"Open"},"assignee":null,"issuetype":{"name":"Bug"},"priority":{"name":"Medium"},"description":null}}`)
	}))
	defer srv.Close()

	result, err := JiraRead(newTestConfig(srv.URL), "BUG-1")
	if err != nil {
		t.Fatalf("JiraRead failed: %v", err)
	}
	if !strings.Contains(result, "Assignee: Unassigned") {
		t.Errorf("expected Unassigned, got:\n%s", result)
	}
}

func TestJiraRead_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, `{"errorMessages":["Issue does not exist"]}`)
	}))
	defer srv.Close()

	_, err := JiraRead(newTestConfig(srv.URL), "NOPE-999")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("error should mention HTTP 404: %v", err)
	}
}

func TestJiraRead_MissingConfig(t *testing.T) {
	cfg := &AtlassianConfig{}
	_, err := JiraRead(cfg, "TEST-1")
	if err == nil || !strings.Contains(err.Error(), "missing Jira config") {
		t.Errorf("expected missing config error, got: %v", err)
	}
}

func TestValidateJiraKey(t *testing.T) {
	valid := []string{"TEST-1", "PROJ-123", "PBP1-4365", "A-1"}
	for _, key := range valid {
		if err := validateJiraKey(key); err != nil {
			t.Errorf("expected valid key %q, got error: %v", key, err)
		}
	}

	invalid := []string{"", "test-1", "123", "../etc/passwd", "TEST", "TEST-", "-123", "test-123/../../foo"}
	for _, key := range invalid {
		if err := validateJiraKey(key); err == nil {
			t.Errorf("expected error for invalid key %q", key)
		}
	}
}

func TestValidatePageID(t *testing.T) {
	valid := []string{"12345", "1", "9999999"}
	for _, id := range valid {
		if err := validatePageID(id); err != nil {
			t.Errorf("expected valid page ID %q, got error: %v", id, err)
		}
	}

	invalid := []string{"", "abc", "12.34", "../etc", "123/../../foo"}
	for _, id := range invalid {
		if err := validatePageID(id); err == nil {
			t.Errorf("expected error for invalid page ID %q", id)
		}
	}
}

func TestJiraRead_InvalidKey(t *testing.T) {
	cfg := newTestConfig("https://example.com")
	_, err := JiraRead(cfg, "../etc/passwd")
	if err == nil || !strings.Contains(err.Error(), "invalid Jira issue key") {
		t.Errorf("expected validation error, got: %v", err)
	}
}

func TestConfluenceRead_InvalidPageID(t *testing.T) {
	cfg := newTestConfig("https://example.com")
	_, err := ConfluenceRead(cfg, "abc/../etc")
	if err == nil || !strings.Contains(err.Error(), "invalid Confluence page ID") {
		t.Errorf("expected validation error, got: %v", err)
	}
}

func TestJiraUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()

	tmpFile := filepath.Join(t.TempDir(), "payload.json")
	os.WriteFile(tmpFile, []byte(`{"fields":{"description":{"type":"doc"}}}`), 0644)

	result, err := JiraUpdate(newTestConfig(srv.URL), "TEST-1", tmpFile)
	if err != nil {
		t.Fatalf("JiraUpdate failed: %v", err)
	}
	if result != "Updated description for TEST-1" {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestJiraComment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/comment") {
			t.Errorf("expected /comment path, got %s", r.URL.Path)
		}
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"12345"}`)
	}))
	defer srv.Close()

	tmpFile := filepath.Join(t.TempDir(), "comment.json")
	os.WriteFile(tmpFile, []byte(`{"body":{"type":"doc"}}`), 0644)

	result, err := JiraComment(newTestConfig(srv.URL), "TEST-1", tmpFile)
	if err != nil {
		t.Fatalf("JiraComment failed: %v", err)
	}
	if result != "Posted comment to TEST-1" {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestJiraComment_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"errorMessages":["Bad request"]}`)
	}))
	defer srv.Close()

	tmpFile := filepath.Join(t.TempDir(), "comment.json")
	os.WriteFile(tmpFile, []byte(`{}`), 0644)

	_, err := JiraComment(newTestConfig(srv.URL), "TEST-1", tmpFile)
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("expected HTTP 400 error, got: %v", err)
	}
}

// --- Confluence API tests ---

func TestConfluenceRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/wiki/rest/api/content/12345") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{
			"title": "Test Page",
			"space": {"key": "DEV", "name": "Development"},
			"version": {"number": 3, "by": {"displayName": "John"}, "when": "2026-03-25T10:00:00Z"},
			"body": {"atlas_doc_format": {"value": "{\"type\":\"doc\",\"content\":[{\"type\":\"paragraph\",\"content\":[{\"type\":\"text\",\"text\":\"Page content\"}]}]}"}}
		}`)
	}))
	defer srv.Close()

	result, err := ConfluenceRead(newTestConfig(srv.URL), "12345")
	if err != nil {
		t.Fatalf("ConfluenceRead failed: %v", err)
	}

	checks := []string{
		"=== Confluence Page 12345 ===",
		"Title: Test Page",
		"Space: Development [DEV]",
		"Version: 3 by John",
		"Page content",
		"--- Raw ADF ---",
	}
	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("result missing %q:\n%s", check, result)
		}
	}
}

func TestConfluenceUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		w.WriteHeader(200)
		fmt.Fprint(w, `{"id":"12345","title":"Updated"}`)
	}))
	defer srv.Close()

	tmpFile := filepath.Join(t.TempDir(), "payload.json")
	os.WriteFile(tmpFile, []byte(`{"version":{"number":4},"title":"Updated","type":"page"}`), 0644)

	result, err := ConfluenceUpdate(newTestConfig(srv.URL), "12345", tmpFile)
	if err != nil {
		t.Fatalf("ConfluenceUpdate failed: %v", err)
	}
	if result != "Updated Confluence page 12345" {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestConfluenceSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/wiki/rest/api/content/search") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		cql := r.URL.Query().Get("cql")
		if cql == "" {
			t.Error("expected cql parameter")
		}
		fmt.Fprint(w, `{"results":[
			{"id":"111","title":"Page One","version":{"number":2}},
			{"id":"222","title":"Page Two","version":{"number":5}}
		]}`)
	}))
	defer srv.Close()

	result, err := ConfluenceSearch(newTestConfig(srv.URL), "DEV", `space=DEV AND title="Test"`)
	if err != nil {
		t.Fatalf("ConfluenceSearch failed: %v", err)
	}

	if !strings.Contains(result, "=== Search Results (DEV) ===") {
		t.Errorf("missing header:\n%s", result)
	}
	if !strings.Contains(result, "111\tPage One\tv2") {
		t.Errorf("missing first result:\n%s", result)
	}
	if !strings.Contains(result, "222\tPage Two\tv5") {
		t.Errorf("missing second result:\n%s", result)
	}
}

func TestConfluenceRead_MissingConfig(t *testing.T) {
	cfg := &AtlassianConfig{}
	_, err := ConfluenceRead(cfg, "12345")
	if err == nil || !strings.Contains(err.Error(), "missing Confluence config") {
		t.Errorf("expected missing config error, got: %v", err)
	}
}

func TestConfluenceSearch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"message":"Forbidden"}`)
	}))
	defer srv.Close()

	_, err := ConfluenceSearch(newTestConfig(srv.URL), "DEV", "space=DEV")
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("expected HTTP 403 error, got: %v", err)
	}
}

func TestJiraUpdate_MissingFile(t *testing.T) {
	cfg := newTestConfig("https://example.com")
	_, err := JiraUpdate(cfg, "TEST-1", "/nonexistent/file.json")
	if err == nil || !strings.Contains(err.Error(), "reading payload file") {
		t.Errorf("expected file error, got: %v", err)
	}
}

func TestJiraKeyFromString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"PBP1-456-add-validation", "PBP1-456"},
		{"feature/PROJ-123-do-thing", "PROJ-123"},
		{"PROJ-7", "PROJ-7"},
		{"A1B2-99", "A1B2-99"},
		{"no-key-here", ""},
		{"lowercase-12", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := JiraKeyFromString(c.in); got != c.want {
			t.Errorf("JiraKeyFromString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
