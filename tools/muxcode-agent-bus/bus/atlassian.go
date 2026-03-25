package bus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	// jiraKeyPattern validates Jira issue keys (e.g. PROJ-123, PBP1-4365).
	jiraKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)
	// pageIDPattern validates Confluence page IDs (numeric only).
	pageIDPattern = regexp.MustCompile(`^[0-9]+$`)
	// maxResponseSize limits API response body reads (10 MB).
	maxResponseSize int64 = 10 * 1024 * 1024
)

// AtlassianConfig holds Jira/Confluence connection settings.
type AtlassianConfig struct {
	JiraBaseURL       string // JIRA_BASE_URL
	ConfluenceBaseURL string // CONFLUENCE_BASE_URL (falls back to JIRA_BASE_URL)
	UserEmail         string // JIRA_USER_EMAIL
	APIToken          string // JIRA_API_TOKEN
}

// LoadAtlassianConfig reads credentials from .muxcode/config and ~/.config/muxcode/config.
// Config files are shell-sourceable KEY=VALUE files (no export prefix).
func LoadAtlassianConfig() (*AtlassianConfig, error) {
	env := make(map[string]string)

	// Load in order: user global first, then project-local (overrides)
	for _, path := range []string{
		filepath.Join(os.Getenv("HOME"), ".config", "muxcode", "config"),
		".muxcode/config",
	} {
		loadShellConfig(path, env)
	}

	// Also check env vars (highest priority)
	for _, key := range []string{"JIRA_BASE_URL", "CONFLUENCE_BASE_URL", "JIRA_USER_EMAIL", "JIRA_API_TOKEN"} {
		if v := os.Getenv(key); v != "" {
			env[key] = v
		}
	}

	cfg := &AtlassianConfig{
		JiraBaseURL:       env["JIRA_BASE_URL"],
		ConfluenceBaseURL: env["CONFLUENCE_BASE_URL"],
		UserEmail:         env["JIRA_USER_EMAIL"],
		APIToken:          env["JIRA_API_TOKEN"],
	}

	// Confluence falls back to Jira base URL
	if cfg.ConfluenceBaseURL == "" {
		cfg.ConfluenceBaseURL = cfg.JiraBaseURL
	}

	return cfg, nil
}

// loadShellConfig reads a KEY=VALUE config file into the provided map.
// Supports quoted values and skips comments/blanks.
func loadShellConfig(path string, env map[string]string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip optional "export " prefix
		line = strings.TrimPrefix(line, "export ")
		idx := strings.IndexByte(line, '=')
		if idx < 1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Strip surrounding quotes
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		env[key] = val
	}
}

// --- HTTP helpers ---

var atlassianHTTPClient = &http.Client{Timeout: 30 * time.Second}

func atlassianRequest(method, url string, body io.Reader, cfg *AtlassianConfig) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(cfg.UserEmail, cfg.APIToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return atlassianHTTPClient.Do(req)
}

func readResponseBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
}

func validateJiraKey(key string) error {
	if !jiraKeyPattern.MatchString(key) {
		return fmt.Errorf("invalid Jira issue key: %q (expected format: PROJ-123)", key)
	}
	return nil
}

func validatePageID(id string) error {
	if !pageIDPattern.MatchString(id) {
		return fmt.Errorf("invalid Confluence page ID: %q (expected numeric)", id)
	}
	return nil
}

// --- Jira API ---

// JiraRead fetches a Jira issue and returns formatted output.
func JiraRead(cfg *AtlassianConfig, issueKey string) (string, error) {
	if cfg.JiraBaseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return "", fmt.Errorf("missing Jira config (JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if err := validateJiraKey(issueKey); err != nil {
		return "", err
	}

	apiURL := fmt.Sprintf("%s/rest/api/3/issue/%s?fields=description,summary,status,assignee,issuetype,priority",
		strings.TrimRight(cfg.JiraBaseURL, "/"), issueKey)

	resp, err := atlassianRequest("GET", apiURL, nil, cfg)
	if err != nil {
		return "", fmt.Errorf("Jira API request failed: %w", err)
	}

	body, err := readResponseBody(resp)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Jira API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var issue struct {
		Fields struct {
			Summary string `json:"summary"`
			Status  struct {
				Name string `json:"name"`
			} `json:"status"`
			Assignee *struct {
				DisplayName string `json:"displayName"`
			} `json:"assignee"`
			IssueType struct {
				Name string `json:"name"`
			} `json:"issuetype"`
			Priority struct {
				Name string `json:"name"`
			} `json:"priority"`
			Description json.RawMessage `json:"description"`
		} `json:"fields"`
	}

	if err := json.Unmarshal(body, &issue); err != nil {
		return "", fmt.Errorf("parsing Jira response: %w", err)
	}

	assignee := "Unassigned"
	if issue.Fields.Assignee != nil && issue.Fields.Assignee.DisplayName != "" {
		assignee = issue.Fields.Assignee.DisplayName
	}

	summary := issue.Fields.Summary
	if summary == "" {
		summary = "No summary"
	}
	status := issue.Fields.Status.Name
	if status == "" {
		status = "Unknown"
	}
	issueType := issue.Fields.IssueType.Name
	if issueType == "" {
		issueType = "Unknown"
	}
	priority := issue.Fields.Priority.Name
	if priority == "" {
		priority = "Unknown"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "=== %s ===\n", issueKey)
	fmt.Fprintf(&sb, "Summary: %s\n", summary)
	fmt.Fprintf(&sb, "Type: %s | Priority: %s\n", issueType, priority)
	fmt.Fprintf(&sb, "Status: %s | Assignee: %s\n", status, assignee)
	sb.WriteString("\n--- Description ---\n")
	sb.WriteString(flattenADF(issue.Fields.Description))

	return sb.String(), nil
}

// JiraUpdate updates a Jira issue using a JSON payload file.
func JiraUpdate(cfg *AtlassianConfig, issueKey, payloadFile string) (string, error) {
	if cfg.JiraBaseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return "", fmt.Errorf("missing Jira config (JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if err := validateJiraKey(issueKey); err != nil {
		return "", err
	}

	payload, err := os.ReadFile(payloadFile)
	if err != nil {
		return "", fmt.Errorf("reading payload file: %w", err)
	}

	apiURL := fmt.Sprintf("%s/rest/api/3/issue/%s",
		strings.TrimRight(cfg.JiraBaseURL, "/"), issueKey)

	resp, err := atlassianRequest("PUT", apiURL, strings.NewReader(string(payload)), cfg)
	if err != nil {
		return "", fmt.Errorf("Jira API request failed: %w", err)
	}

	body, err := readResponseBody(resp)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 204 {
		return "", fmt.Errorf("Jira API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	return fmt.Sprintf("Updated description for %s", issueKey), nil
}

// JiraComment posts a comment on a Jira issue using a JSON payload file.
func JiraComment(cfg *AtlassianConfig, issueKey, payloadFile string) (string, error) {
	if cfg.JiraBaseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return "", fmt.Errorf("missing Jira config (JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if err := validateJiraKey(issueKey); err != nil {
		return "", err
	}

	payload, err := os.ReadFile(payloadFile)
	if err != nil {
		return "", fmt.Errorf("reading payload file: %w", err)
	}

	apiURL := fmt.Sprintf("%s/rest/api/3/issue/%s/comment",
		strings.TrimRight(cfg.JiraBaseURL, "/"), issueKey)

	resp, err := atlassianRequest("POST", apiURL, strings.NewReader(string(payload)), cfg)
	if err != nil {
		return "", fmt.Errorf("Jira API request failed: %w", err)
	}

	body, err := readResponseBody(resp)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 201 {
		return "", fmt.Errorf("Jira API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	return fmt.Sprintf("Posted comment to %s", issueKey), nil
}

// --- Confluence API ---

// ConfluenceRead fetches a Confluence page and returns formatted output.
func ConfluenceRead(cfg *AtlassianConfig, pageID string) (string, error) {
	baseURL := cfg.ConfluenceBaseURL
	if baseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return "", fmt.Errorf("missing Confluence config (CONFLUENCE_BASE_URL or JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if err := validatePageID(pageID); err != nil {
		return "", err
	}

	apiURL := fmt.Sprintf("%s/wiki/rest/api/content/%s?expand=body.atlas_doc_format,version,space,ancestors",
		strings.TrimRight(baseURL, "/"), pageID)

	resp, err := atlassianRequest("GET", apiURL, nil, cfg)
	if err != nil {
		return "", fmt.Errorf("Confluence API request failed: %w", err)
	}

	body, err := readResponseBody(resp)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Confluence API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var page struct {
		Title string `json:"title"`
		Space struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"space"`
		Version struct {
			Number int `json:"number"`
			By     struct {
				DisplayName string `json:"displayName"`
			} `json:"by"`
			When string `json:"when"`
		} `json:"version"`
		Body struct {
			AtlasDocFormat struct {
				Value string `json:"value"`
			} `json:"atlas_doc_format"`
		} `json:"body"`
	}

	if err := json.Unmarshal(body, &page); err != nil {
		return "", fmt.Errorf("parsing Confluence response: %w", err)
	}

	title := page.Title
	if title == "" {
		title = "Untitled"
	}
	spaceKey := page.Space.Key
	if spaceKey == "" {
		spaceKey = "Unknown"
	}
	spaceName := page.Space.Name
	if spaceName == "" {
		spaceName = "Unknown"
	}
	versionBy := page.Version.By.DisplayName
	if versionBy == "" {
		versionBy = "Unknown"
	}
	versionWhen := page.Version.When
	if versionWhen == "" {
		versionWhen = "Unknown"
	}

	pageURL := fmt.Sprintf("%s/wiki/spaces/%s/pages/%s",
		strings.TrimRight(baseURL, "/"), spaceKey, pageID)

	var sb strings.Builder
	fmt.Fprintf(&sb, "=== Confluence Page %s ===\n", pageID)
	fmt.Fprintf(&sb, "Title: %s\n", title)
	fmt.Fprintf(&sb, "Space: %s [%s]\n", spaceName, spaceKey)
	fmt.Fprintf(&sb, "Version: %d by %s (%s)\n", page.Version.Number, versionBy, versionWhen)
	fmt.Fprintf(&sb, "URL: %s\n", pageURL)
	sb.WriteString("\n--- Content ---\n")

	// Parse ADF content to extract text
	adfContent := page.Body.AtlasDocFormat.Value
	if adfContent != "" {
		var adfDoc json.RawMessage
		if err := json.Unmarshal([]byte(adfContent), &adfDoc); err == nil {
			sb.WriteString(flattenADF(adfDoc))
		} else {
			sb.WriteString("(unable to parse ADF content)")
		}
	}

	sb.WriteString("\n\n--- Raw ADF ---\n")
	sb.WriteString(adfContent)

	return sb.String(), nil
}

// ConfluenceUpdate updates a Confluence page using a JSON payload file.
func ConfluenceUpdate(cfg *AtlassianConfig, pageID, payloadFile string) (string, error) {
	baseURL := cfg.ConfluenceBaseURL
	if baseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return "", fmt.Errorf("missing Confluence config (CONFLUENCE_BASE_URL or JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if err := validatePageID(pageID); err != nil {
		return "", err
	}

	payload, err := os.ReadFile(payloadFile)
	if err != nil {
		return "", fmt.Errorf("reading payload file: %w", err)
	}

	apiURL := fmt.Sprintf("%s/wiki/rest/api/content/%s",
		strings.TrimRight(baseURL, "/"), pageID)

	resp, err := atlassianRequest("PUT", apiURL, strings.NewReader(string(payload)), cfg)
	if err != nil {
		return "", fmt.Errorf("Confluence API request failed: %w", err)
	}

	body, err := readResponseBody(resp)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Confluence API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	return fmt.Sprintf("Updated Confluence page %s", pageID), nil
}

// ConfluenceSearch searches Confluence using CQL.
// spaceKey is used only for display in the results header — filtering is done by the CQL query.
func ConfluenceSearch(cfg *AtlassianConfig, spaceKey, cql string) (string, error) {
	baseURL := cfg.ConfluenceBaseURL
	if baseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return "", fmt.Errorf("missing Confluence config (CONFLUENCE_BASE_URL or JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}

	params := url.Values{
		"cql":    {cql},
		"expand": {"version"},
		"limit":  {"25"},
	}
	apiURL := fmt.Sprintf("%s/wiki/rest/api/content/search?%s",
		strings.TrimRight(baseURL, "/"), params.Encode())

	resp, err := atlassianRequest("GET", apiURL, nil, cfg)
	if err != nil {
		return "", fmt.Errorf("Confluence API request failed: %w", err)
	}

	body, err := readResponseBody(resp)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Confluence API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Results []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Version struct {
				Number int `json:"number"`
			} `json:"version"`
		} `json:"results"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing search results: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "=== Search Results (%s) ===\n", spaceKey)
	for _, r := range result.Results {
		fmt.Fprintf(&sb, "%s\t%s\tv%d\n", r.ID, r.Title, r.Version.Number)
	}

	return sb.String(), nil
}

// --- ADF text extraction ---

// flattenADF recursively extracts text from an ADF JSON document.
func flattenADF(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var node struct {
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		return ""
	}

	var texts []string
	if node.Text != "" {
		texts = append(texts, node.Text)
	}

	// Recurse into content array
	if len(node.Content) > 0 {
		var children []json.RawMessage
		if err := json.Unmarshal(node.Content, &children); err == nil {
			for _, child := range children {
				if t := flattenADF(child); t != "" {
					texts = append(texts, t)
				}
			}
		}
	}

	return strings.Join(texts, " ")
}
