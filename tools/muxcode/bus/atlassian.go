package bus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
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
	// jiraKeyFindPattern extracts a Jira key embedded in a larger string such
	// as a branch name (e.g. "PBP1-456-add-validation" → "PBP1-456").
	jiraKeyFindPattern = regexp.MustCompile(`[A-Z][A-Z0-9]*-[0-9]+`)
	// pageIDPattern validates Confluence page IDs (numeric only).
	pageIDPattern = regexp.MustCompile(`^[0-9]+$`)
	// maxResponseSize limits API response body reads (10 MB).
	maxResponseSize int64 = 10 * 1024 * 1024
)

// JiraKeyFromString extracts the first Jira issue key from an arbitrary string
// (typically a branch name), or "" if none is present.
func JiraKeyFromString(s string) string {
	return jiraKeyFindPattern.FindString(s)
}

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

	apiURL := fmt.Sprintf("%s/rest/api/3/issue/%s?fields=description,summary,status,assignee,issuetype,priority,issuelinks,parent,subtasks",
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
			IssueLinks  []struct {
				Type struct {
					Name    string `json:"name"`
					Inward  string `json:"inward"`
					Outward string `json:"outward"`
				} `json:"type"`
				InwardIssue *struct {
					Key    string `json:"key"`
					Fields struct {
						Summary string `json:"summary"`
						Status  struct {
							Name string `json:"name"`
						} `json:"status"`
					} `json:"fields"`
				} `json:"inwardIssue"`
				OutwardIssue *struct {
					Key    string `json:"key"`
					Fields struct {
						Summary string `json:"summary"`
						Status  struct {
							Name string `json:"name"`
						} `json:"status"`
					} `json:"fields"`
				} `json:"outwardIssue"`
			} `json:"issuelinks"`
			Parent *struct {
				Key    string `json:"key"`
				Fields struct {
					Summary string `json:"summary"`
				} `json:"fields"`
			} `json:"parent"`
			Subtasks []struct {
				Key    string `json:"key"`
				Fields struct {
					Summary string `json:"summary"`
					Status  struct {
						Name string `json:"name"`
					} `json:"status"`
				} `json:"fields"`
			} `json:"subtasks"`
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

	if issue.Fields.Parent != nil {
		fmt.Fprintf(&sb, "Parent: %s — %s\n", issue.Fields.Parent.Key, issue.Fields.Parent.Fields.Summary)
	}

	if len(issue.Fields.IssueLinks) > 0 {
		sb.WriteString("\n--- Links ---\n")
		for _, link := range issue.Fields.IssueLinks {
			if link.OutwardIssue != nil {
				// OutwardIssue present → current issue is the inward side
				// Show inward label (e.g. "is blocked by") toward the outward issue
				fmt.Fprintf(&sb, "  %s %s [%s] — %s\n",
					link.Type.Inward, link.OutwardIssue.Key,
					link.OutwardIssue.Fields.Status.Name, link.OutwardIssue.Fields.Summary)
			}
			if link.InwardIssue != nil {
				// InwardIssue present → current issue is the outward side
				// Show outward label (e.g. "blocks") toward the inward issue
				fmt.Fprintf(&sb, "  %s %s [%s] — %s\n",
					link.Type.Outward, link.InwardIssue.Key,
					link.InwardIssue.Fields.Status.Name, link.InwardIssue.Fields.Summary)
			}
		}
	}

	if len(issue.Fields.Subtasks) > 0 {
		sb.WriteString("\n--- Subtasks ---\n")
		for _, st := range issue.Fields.Subtasks {
			fmt.Fprintf(&sb, "  %s [%s] — %s\n", st.Key, st.Fields.Status.Name, st.Fields.Summary)
		}
	}

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

// JiraAddWorklog posts a worklog entry recording timeSpentSeconds against the
// issue. An optional comment is attached as an ADF paragraph. Jira requires a
// minimum of 60 seconds; callers should round up sub-minute durations.
// POST /rest/api/3/issue/{key}/worklog — expects HTTP 201 on success.
func JiraAddWorklog(cfg *AtlassianConfig, issueKey string, timeSpentSeconds int64, comment string) (string, error) {
	if cfg.JiraBaseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return "", fmt.Errorf("missing Jira config (JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if err := validateJiraKey(issueKey); err != nil {
		return "", err
	}
	if timeSpentSeconds <= 0 {
		return "", fmt.Errorf("timeSpentSeconds must be positive, got %d", timeSpentSeconds)
	}

	payload := map[string]any{"timeSpentSeconds": timeSpentSeconds}
	if strings.TrimSpace(comment) != "" {
		payload["comment"] = map[string]any{
			"type":    "doc",
			"version": 1,
			"content": []any{
				map[string]any{
					"type":    "paragraph",
					"content": []any{map[string]any{"type": "text", "text": comment}},
				},
			},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshaling worklog payload: %w", err)
	}

	apiURL := fmt.Sprintf("%s/rest/api/3/issue/%s/worklog",
		strings.TrimRight(cfg.JiraBaseURL, "/"), issueKey)

	resp, err := atlassianRequest("POST", apiURL, bytes.NewReader(body), cfg)
	if err != nil {
		return "", fmt.Errorf("Jira API request failed: %w", err)
	}

	respBody, err := readResponseBody(resp)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 201 {
		return "", fmt.Errorf("Jira API returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return fmt.Sprintf("Logged %ds of work to %s", timeSpentSeconds, issueKey), nil
}

// --- Jira Issue Links ---

// JiraLinkType describes an available link type on the Jira instance.
type JiraLinkType struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Inward  string `json:"inward"`
	Outward string `json:"outward"`
}

// JiraListLinkTypes fetches all available issue link types from the Jira instance.
func JiraListLinkTypes(cfg *AtlassianConfig) ([]JiraLinkType, error) {
	if cfg.JiraBaseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return nil, fmt.Errorf("missing Jira config (JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}

	apiURL := fmt.Sprintf("%s/rest/api/3/issueLinkType",
		strings.TrimRight(cfg.JiraBaseURL, "/"))

	resp, err := atlassianRequest("GET", apiURL, nil, cfg)
	if err != nil {
		return nil, fmt.Errorf("Jira API request failed: %w", err)
	}

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Jira API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		IssueLinkTypes []JiraLinkType `json:"issueLinkTypes"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing link types: %w", err)
	}

	return result.IssueLinkTypes, nil
}

// FormatLinkTypes returns a human-readable listing of available link types.
func FormatLinkTypes(types []JiraLinkType) string {
	var sb strings.Builder
	sb.WriteString("=== Available Issue Link Types ===\n")
	for _, lt := range types {
		fmt.Fprintf(&sb, "%-20s  outward: %-25s  inward: %s\n", lt.Name, lt.Outward, lt.Inward)
	}
	return sb.String()
}

// JiraLinkIssues creates a link between two Jira issues.
// linkTypeName is the link type name (e.g. "Blocks", "Dependency").
// inwardKey is the inward issue (e.g. "is blocked by" side).
// outwardKey is the outward issue (e.g. "blocks" side).
//
// Note: the CLI swaps args so users write "link Blocks A B" (A blocks B).
// Internally, inwardKey=B (is blocked by), outwardKey=A (blocks).
//
// Example: JiraLinkIssues(cfg, "Blocks", "PROJ-2", "PROJ-1")
// means "PROJ-1 blocks PROJ-2" / "PROJ-2 is blocked by PROJ-1".
func JiraLinkIssues(cfg *AtlassianConfig, linkTypeName, inwardKey, outwardKey string) (string, error) {
	if cfg.JiraBaseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return "", fmt.Errorf("missing Jira config (JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if err := validateJiraKey(inwardKey); err != nil {
		return "", fmt.Errorf("inward issue: %w", err)
	}
	if err := validateJiraKey(outwardKey); err != nil {
		return "", fmt.Errorf("outward issue: %w", err)
	}
	if linkTypeName == "" {
		return "", fmt.Errorf("link type name is required")
	}

	payload := map[string]interface{}{
		"type": map[string]string{
			"name": linkTypeName,
		},
		"inwardIssue": map[string]string{
			"key": inwardKey,
		},
		"outwardIssue": map[string]string{
			"key": outwardKey,
		},
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshalling payload: %w", err)
	}

	apiURL := fmt.Sprintf("%s/rest/api/3/issueLink",
		strings.TrimRight(cfg.JiraBaseURL, "/"))

	resp, err := atlassianRequest("POST", apiURL, strings.NewReader(string(payloadJSON)), cfg)
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

	return fmt.Sprintf("Linked %s -[%s]-> %s", outwardKey, linkTypeName, inwardKey), nil
}

// --- Jira Transitions ---

// JiraTransition describes an available workflow transition for an issue.
type JiraTransition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   struct {
		Name string `json:"name"`
	} `json:"to"`
}

// JiraListTransitions fetches available transitions for a specific issue.
func JiraListTransitions(cfg *AtlassianConfig, issueKey string) ([]JiraTransition, error) {
	if cfg.JiraBaseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return nil, fmt.Errorf("missing Jira config (JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if err := validateJiraKey(issueKey); err != nil {
		return nil, err
	}

	apiURL := fmt.Sprintf("%s/rest/api/3/issue/%s/transitions",
		strings.TrimRight(cfg.JiraBaseURL, "/"), issueKey)

	resp, err := atlassianRequest("GET", apiURL, nil, cfg)
	if err != nil {
		return nil, fmt.Errorf("Jira API request failed: %w", err)
	}

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Jira API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Transitions []JiraTransition `json:"transitions"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing transitions: %w", err)
	}

	return result.Transitions, nil
}

// FormatTransitions returns a human-readable listing of available transitions.
func FormatTransitions(issueKey string, transitions []JiraTransition) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "=== Available Transitions for %s ===\n", issueKey)
	for _, t := range transitions {
		fmt.Fprintf(&sb, "  ID: %-6s  %-25s  -> %s\n", t.ID, t.Name, t.To.Name)
	}
	return sb.String()
}

// JiraTransitionIssue moves an issue to a new status via a transition.
// transitionID is the numeric transition ID (from JiraListTransitions).
func JiraTransitionIssue(cfg *AtlassianConfig, issueKey, transitionID string) (string, error) {
	if cfg.JiraBaseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return "", fmt.Errorf("missing Jira config (JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if err := validateJiraKey(issueKey); err != nil {
		return "", err
	}
	if transitionID == "" {
		return "", fmt.Errorf("transition ID is required")
	}

	payload := map[string]interface{}{
		"transition": map[string]string{
			"id": transitionID,
		},
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshalling payload: %w", err)
	}

	apiURL := fmt.Sprintf("%s/rest/api/3/issue/%s/transitions",
		strings.TrimRight(cfg.JiraBaseURL, "/"), issueKey)

	resp, err := atlassianRequest("POST", apiURL, strings.NewReader(string(payloadJSON)), cfg)
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

	return fmt.Sprintf("Transitioned %s via transition %s", issueKey, transitionID), nil
}

// --- Jira Search (JQL) ---

// JiraSearchResult holds a single issue from a JQL search.
type JiraSearchResult struct {
	Key      string `json:"key"`
	Summary  string `json:"summary"`
	Status   string `json:"status"`
	Type     string `json:"type"`
	Priority string `json:"priority"`
}

// JiraSearch executes a JQL query and returns matching issues.
func JiraSearch(cfg *AtlassianConfig, jql string, maxResults int) ([]JiraSearchResult, int, error) {
	if cfg.JiraBaseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return nil, 0, fmt.Errorf("missing Jira config (JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if jql == "" {
		return nil, 0, fmt.Errorf("JQL query is required")
	}
	if maxResults <= 0 {
		maxResults = 50
	}

	reqBody := map[string]interface{}{
		"jql":        jql,
		"fields":     []string{"summary", "status", "issuetype", "priority"},
		"maxResults": maxResults,
	}
	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("encoding search request: %w", err)
	}
	apiURL := fmt.Sprintf("%s/rest/api/3/search/jql",
		strings.TrimRight(cfg.JiraBaseURL, "/"))

	resp, err := atlassianRequest("POST", apiURL, bytes.NewReader(reqJSON), cfg)
	if err != nil {
		return nil, 0, fmt.Errorf("Jira API request failed: %w", err)
	}

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, 0, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, 0, fmt.Errorf("Jira API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		IsLast bool `json:"isLast"`
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
				Status  struct {
					Name string `json:"name"`
				} `json:"status"`
				IssueType struct {
					Name string `json:"name"`
				} `json:"issuetype"`
				Priority struct {
					Name string `json:"name"`
				} `json:"priority"`
			} `json:"fields"`
		} `json:"issues"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, 0, fmt.Errorf("parsing search results: %w", err)
	}

	var issues []JiraSearchResult
	for _, i := range result.Issues {
		issues = append(issues, JiraSearchResult{
			Key:      i.Key,
			Summary:  i.Fields.Summary,
			Status:   i.Fields.Status.Name,
			Type:     i.Fields.IssueType.Name,
			Priority: i.Fields.Priority.Name,
		})
	}

	// New /search/jql API uses cursor pagination; total count not available.
	return issues, len(issues), nil
}

// FormatJiraSearch returns a human-readable listing of search results.
func FormatJiraSearch(issues []JiraSearchResult, total int, jql string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "=== Jira Search Results (%d) ===\n", len(issues))
	fmt.Fprintf(&sb, "JQL: %s\n\n", jql)
	for _, i := range issues {
		priority := i.Priority
		if priority == "" {
			priority = "-"
		}
		fmt.Fprintf(&sb, "%-12s  [%-12s]  %-10s  %-8s  %s\n", i.Key, i.Status, i.Type, priority, i.Summary)
	}
	return sb.String()
}

// --- Jira Comments (Read) ---

// JiraCommentEntry holds a single comment from an issue.
type JiraCommentEntry struct {
	ID      string `json:"id"`
	Author  string `json:"author"`
	Created string `json:"created"`
	Body    string `json:"body"`
}

// JiraReadComments fetches comments for a Jira issue.
func JiraReadComments(cfg *AtlassianConfig, issueKey string) ([]JiraCommentEntry, error) {
	if cfg.JiraBaseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return nil, fmt.Errorf("missing Jira config (JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if err := validateJiraKey(issueKey); err != nil {
		return nil, err
	}

	apiURL := fmt.Sprintf("%s/rest/api/3/issue/%s/comment?orderBy=-created&maxResults=50",
		strings.TrimRight(cfg.JiraBaseURL, "/"), issueKey)

	resp, err := atlassianRequest("GET", apiURL, nil, cfg)
	if err != nil {
		return nil, fmt.Errorf("Jira API request failed: %w", err)
	}

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Jira API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Comments []struct {
			ID     string `json:"id"`
			Author struct {
				DisplayName string `json:"displayName"`
			} `json:"author"`
			Created string          `json:"created"`
			Body    json.RawMessage `json:"body"`
		} `json:"comments"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing comments: %w", err)
	}

	var comments []JiraCommentEntry
	for _, c := range result.Comments {
		author := c.Author.DisplayName
		if author == "" {
			author = "Unknown"
		}
		comments = append(comments, JiraCommentEntry{
			ID:      c.ID,
			Author:  author,
			Created: c.Created,
			Body:    flattenADF(c.Body),
		})
	}

	return comments, nil
}

// FormatJiraComments returns a human-readable listing of comments.
func FormatJiraComments(issueKey string, comments []JiraCommentEntry) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "=== Comments on %s (%d) ===\n\n", issueKey, len(comments))
	for _, c := range comments {
		fmt.Fprintf(&sb, "--- %s at %s ---\n", c.Author, c.Created)
		sb.WriteString(c.Body)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// --- Jira Create Subtask ---

// JiraCreateSubtask creates a subtask under a parent issue.
// summary is the subtask summary/title. description is optional ADF JSON (pass nil to skip).
func JiraCreateSubtask(cfg *AtlassianConfig, parentKey, projectKey, summary string, description json.RawMessage) (string, error) {
	if cfg.JiraBaseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return "", fmt.Errorf("missing Jira config (JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if err := validateJiraKey(parentKey); err != nil {
		return "", fmt.Errorf("parent issue: %w", err)
	}
	if summary == "" {
		return "", fmt.Errorf("subtask summary is required")
	}

	// If no project key provided, derive from parent key (e.g. "PROJ-123" -> "PROJ")
	if projectKey == "" {
		idx := strings.LastIndex(parentKey, "-")
		if idx > 0 {
			projectKey = parentKey[:idx]
		}
	}

	fields := map[string]interface{}{
		"project": map[string]string{
			"key": projectKey,
		},
		"parent": map[string]string{
			"key": parentKey,
		},
		"summary": summary,
		"issuetype": map[string]string{
			"name": "Sub-task",
		},
	}

	if len(description) > 0 && string(description) != "null" {
		fields["description"] = json.RawMessage(description)
	}

	payload := map[string]interface{}{
		"fields": fields,
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshalling payload: %w", err)
	}

	apiURL := fmt.Sprintf("%s/rest/api/3/issue",
		strings.TrimRight(cfg.JiraBaseURL, "/"))

	resp, err := atlassianRequest("POST", apiURL, strings.NewReader(string(payloadJSON)), cfg)
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

	var created struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	return fmt.Sprintf("Created subtask %s under %s: %s", created.Key, parentKey, summary), nil
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

// --- Attachment upload (Confluence + Jira) ---

// atlassianMultipartUpload sends a multipart/form-data POST with a single
// "file" field to a Confluence/Jira attachment endpoint. It uses basic auth and
// the X-Atlassian-Token: no-check header both APIs require to accept uploads.
// The file is buffered in memory (diagram images are small).
func atlassianMultipartUpload(apiURL, filePath string, cfg *AtlassianConfig) (*http.Response, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("opening attachment file: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("building multipart body: %w", err)
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, fmt.Errorf("reading attachment file: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("finalizing multipart body: %w", err)
	}

	req, err := http.NewRequest("POST", apiURL, &buf)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(cfg.UserEmail, cfg.APIToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Atlassian-Token", "no-check")
	req.Header.Set("Accept", "application/json")
	return atlassianHTTPClient.Do(req)
}

// atlassianUploadFile stats filePath, POSTs it as a multipart attachment to
// apiURL, verifies the response status, and extracts the filename + id via
// parse. label prefixes error messages ("Confluence" / "Jira"). Shared by the
// Confluence and Jira attachment upload functions.
func atlassianUploadFile(apiURL, filePath, label string, cfg *AtlassianConfig, parse func([]byte) (string, string, error)) (string, string, error) {
	if _, err := os.Stat(filePath); err != nil {
		return "", "", fmt.Errorf("attachment file: %w", err)
	}
	resp, err := atlassianMultipartUpload(apiURL, filePath, cfg)
	if err != nil {
		return "", "", fmt.Errorf("%s attachment upload failed: %w", label, err)
	}
	body, err := readResponseBody(resp)
	if err != nil {
		return "", "", fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return "", "", fmt.Errorf("%s attachment API returned HTTP %d: %s", label, resp.StatusCode, string(body))
	}
	return parse(body)
}

// confluenceFindAttachmentID returns the id of an existing attachment on the
// page with the given filename, or "" if none exists. Errors other than a clean
// "not found" are treated as absent so the caller can fall through to create.
func confluenceFindAttachmentID(cfg *AtlassianConfig, base, pageID, filename string) (string, error) {
	apiURL := fmt.Sprintf("%s/wiki/rest/api/content/%s/child/attachment?filename=%s",
		base, pageID, url.QueryEscape(filename))
	resp, err := atlassianRequest("GET", apiURL, nil, cfg)
	if err != nil {
		return "", fmt.Errorf("checking existing attachment: %w", err)
	}
	body, err := readResponseBody(resp)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", nil // treat as "not found"; the upload path will surface real errors
	}
	var wrapped struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return "", nil
	}
	if len(wrapped.Results) > 0 {
		return wrapped.Results[0].ID, nil
	}
	return "", nil
}

// parseConfluenceAttachmentResponse extracts the attachment filename and fileId
// (media id, used for ADF media embeds) from a Confluence attachment API
// response. Handles both the create shape ({"results":[{...}]}) and the update
// shape (a single attachment object).
func parseConfluenceAttachmentResponse(body []byte) (filename, fileID string, err error) {
	type attachment struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Extensions struct {
			FileID string `json:"fileId"`
		} `json:"extensions"`
	}
	var wrapped struct {
		Results []attachment `json:"results"`
	}
	if e := json.Unmarshal(body, &wrapped); e == nil && len(wrapped.Results) > 0 {
		a := wrapped.Results[0]
		return a.Title, a.Extensions.FileID, nil
	}
	var single attachment
	if e := json.Unmarshal(body, &single); e == nil && (single.Title != "" || single.ID != "") {
		return single.Title, single.Extensions.FileID, nil
	}
	return "", "", fmt.Errorf("could not parse attachment from Confluence response: %s", string(body))
}

// ConfluenceUploadAttachment uploads filePath as an attachment on the given
// page and returns the stored filename and fileId (media id for ADF embeds).
// If an attachment with the same filename already exists it is updated in place
// (Confluence rejects a create with a duplicate filename).
func ConfluenceUploadAttachment(cfg *AtlassianConfig, pageID, filePath string) (filename, fileID string, err error) {
	baseURL := cfg.ConfluenceBaseURL
	if baseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return "", "", fmt.Errorf("missing Confluence config (CONFLUENCE_BASE_URL or JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if err := validatePageID(pageID); err != nil {
		return "", "", err
	}
	base := strings.TrimRight(baseURL, "/")

	existingID, err := confluenceFindAttachmentID(cfg, base, pageID, filepath.Base(filePath))
	if err != nil {
		return "", "", err
	}

	var apiURL string
	if existingID != "" {
		apiURL = fmt.Sprintf("%s/wiki/rest/api/content/%s/child/attachment/%s/data", base, pageID, existingID)
	} else {
		apiURL = fmt.Sprintf("%s/wiki/rest/api/content/%s/child/attachment", base, pageID)
	}

	return atlassianUploadFile(apiURL, filePath, "Confluence", cfg, parseConfluenceAttachmentResponse)
}

// parseJiraAttachmentResponse extracts the filename and attachment id from a
// Jira attachment API response (a JSON array of attachment objects).
func parseJiraAttachmentResponse(body []byte) (filename, attachmentID string, err error) {
	var arr []struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
	}
	if e := json.Unmarshal(body, &arr); e == nil && len(arr) > 0 {
		return arr[0].Filename, arr[0].ID, nil
	}
	return "", "", fmt.Errorf("could not parse attachment from Jira response: %s", string(body))
}

// JiraUploadAttachment uploads filePath as an attachment on the given issue and
// returns the stored filename and attachment id. Jira does not dedupe by
// filename — re-uploading the same name creates a new attachment.
func JiraUploadAttachment(cfg *AtlassianConfig, issueKey, filePath string) (filename, attachmentID string, err error) {
	if cfg.JiraBaseURL == "" || cfg.UserEmail == "" || cfg.APIToken == "" {
		return "", "", fmt.Errorf("missing Jira config (JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)")
	}
	if err := validateJiraKey(issueKey); err != nil {
		return "", "", err
	}
	apiURL := fmt.Sprintf("%s/rest/api/3/issue/%s/attachments",
		strings.TrimRight(cfg.JiraBaseURL, "/"), issueKey)

	return atlassianUploadFile(apiURL, filePath, "Jira", cfg, parseJiraAttachmentResponse)
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
