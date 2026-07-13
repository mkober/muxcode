package bus

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfluenceAttachmentResponse(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantName string
		wantID   string
		wantErr  bool
	}{
		{
			name:     "create results array",
			body:     `{"results":[{"id":"att101","title":"diagram.svg","extensions":{"fileId":"media-abc"}}]}`,
			wantName: "diagram.svg",
			wantID:   "media-abc",
		},
		{
			name:     "update single object",
			body:     `{"id":"att101","title":"diagram.svg","extensions":{"fileId":"media-xyz"}}`,
			wantName: "diagram.svg",
			wantID:   "media-xyz",
		},
		{
			name:     "results takes precedence over empty single",
			body:     `{"results":[{"id":"a","title":"first.png","extensions":{"fileId":"f1"}}]}`,
			wantName: "first.png",
			wantID:   "f1",
		},
		{
			name:    "empty results",
			body:    `{"results":[]}`,
			wantErr: true,
		},
		{
			name:    "not json",
			body:    `<html>nope</html>`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, id, err := parseConfluenceAttachmentResponse([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got name=%q id=%q", name, id)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if name != tt.wantName || id != tt.wantID {
				t.Fatalf("got (%q, %q), want (%q, %q)", name, id, tt.wantName, tt.wantID)
			}
		})
	}
}

func TestParseJiraAttachmentResponse(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantName string
		wantID   string
		wantErr  bool
	}{
		{
			name:     "single attachment array",
			body:     `[{"id":"10010","filename":"diagram.svg"}]`,
			wantName: "diagram.svg",
			wantID:   "10010",
		},
		{
			name:     "first of multiple",
			body:     `[{"id":"1","filename":"a.png"},{"id":"2","filename":"b.png"}]`,
			wantName: "a.png",
			wantID:   "1",
		},
		{
			name:    "empty array",
			body:    `[]`,
			wantErr: true,
		},
		{
			name:    "error object not array",
			body:    `{"errorMessages":["nope"]}`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, id, err := parseJiraAttachmentResponse([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got name=%q id=%q", name, id)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if name != tt.wantName || id != tt.wantID {
				t.Fatalf("got (%q, %q), want (%q, %q)", name, id, tt.wantName, tt.wantID)
			}
		})
	}
}

// writeTempAttachment creates a small file to upload and returns its path.
func writeTempAttachment(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("<svg/>"), 0o644); err != nil {
		t.Fatalf("writing temp attachment: %v", err)
	}
	return p
}

func TestConfluenceUploadAttachment_Create(t *testing.T) {
	var postPath, contentType, token string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// no existing attachment with this filename
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		postPath = r.URL.Path
		contentType = r.Header.Get("Content-Type")
		token = r.Header.Get("X-Atlassian-Token")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"results":[{"id":"att1","title":"diagram.svg","extensions":{"fileId":"media-1"}}]}`))
	}))
	defer srv.Close()

	cfg := &AtlassianConfig{ConfluenceBaseURL: srv.URL, UserEmail: "u@e.com", APIToken: "tok"}
	name, id, err := ConfluenceUploadAttachment(cfg, "123", writeTempAttachment(t, "diagram.svg"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "diagram.svg" || id != "media-1" {
		t.Fatalf("got (%q, %q), want (diagram.svg, media-1)", name, id)
	}
	if !strings.HasSuffix(postPath, "/child/attachment") {
		t.Fatalf("expected create endpoint, POST hit %q", postPath)
	}
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		t.Fatalf("expected multipart content type, got %q", contentType)
	}
	if token != "no-check" {
		t.Fatalf("expected X-Atlassian-Token: no-check, got %q", token)
	}
}

func TestConfluenceUploadAttachment_UpdateExisting(t *testing.T) {
	var postPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"results":[{"id":"existing99"}]}`))
			return
		}
		postPath = r.URL.Path
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"existing99","title":"diagram.svg","extensions":{"fileId":"media-2"}}`))
	}))
	defer srv.Close()

	cfg := &AtlassianConfig{ConfluenceBaseURL: srv.URL, UserEmail: "u@e.com", APIToken: "tok"}
	name, id, err := ConfluenceUploadAttachment(cfg, "123", writeTempAttachment(t, "diagram.svg"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "diagram.svg" || id != "media-2" {
		t.Fatalf("got (%q, %q), want (diagram.svg, media-2)", name, id)
	}
	if !strings.HasSuffix(postPath, "/child/attachment/existing99/data") {
		t.Fatalf("expected update-data endpoint, POST hit %q", postPath)
	}
}

func TestConfluenceUploadAttachment_MissingConfig(t *testing.T) {
	cfg := &AtlassianConfig{} // no base URL / creds
	if _, _, err := ConfluenceUploadAttachment(cfg, "123", "x.svg"); err == nil {
		t.Fatal("expected missing-config error")
	}
}

func TestJiraUploadAttachment(t *testing.T) {
	var path, token string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		token = r.Header.Get("X-Atlassian-Token")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`[{"id":"10010","filename":"diagram.svg"}]`))
	}))
	defer srv.Close()

	cfg := &AtlassianConfig{JiraBaseURL: srv.URL, UserEmail: "u@e.com", APIToken: "tok"}
	name, id, err := JiraUploadAttachment(cfg, "PROJ-1", writeTempAttachment(t, "diagram.svg"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "diagram.svg" || id != "10010" {
		t.Fatalf("got (%q, %q), want (diagram.svg, 10010)", name, id)
	}
	if !strings.HasSuffix(path, "/rest/api/3/issue/PROJ-1/attachments") {
		t.Fatalf("unexpected Jira attachment path %q", path)
	}
	if token != "no-check" {
		t.Fatalf("expected X-Atlassian-Token: no-check, got %q", token)
	}
}

func TestJiraUploadAttachment_InvalidKey(t *testing.T) {
	cfg := &AtlassianConfig{JiraBaseURL: "https://x", UserEmail: "u@e.com", APIToken: "tok"}
	if _, _, err := JiraUploadAttachment(cfg, "not-a-key", "x.svg"); err == nil {
		t.Fatal("expected invalid-key error")
	}
}

func TestJiraUploadAttachment_MissingFile(t *testing.T) {
	cfg := &AtlassianConfig{JiraBaseURL: "https://x", UserEmail: "u@e.com", APIToken: "tok"}
	if _, _, err := JiraUploadAttachment(cfg, "PROJ-1", filepath.Join(t.TempDir(), "nope.svg")); err == nil {
		t.Fatal("expected missing-file error")
	}
}
