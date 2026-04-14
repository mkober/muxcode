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
	Key     string `json:"key"`
	Summary string `json:"summary"`
	Status  string `json:"status"`
	Type    string `json:"type"`
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

	params := url.Values{
		"jql":        {jql},
		"fields":     {"summary,status,issuetype"},
		"maxResults": {fmt.Sprintf("%d", maxResults)},
	}
	apiURL := fmt.Sprintf("%s/rest/api/3/search?%s",
		strings.TrimRight(cfg.JiraBaseURL, "/"), params.Encode())

	resp, err := atlassianRequest("GET", apiURL, nil, cfg)
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
		Total  int `json:"total"`
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
			} `json:"fields"`
		} `json:"issues"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, 0, fmt.Errorf("parsing search results: %w", err)
	}

	var issues []JiraSearchResult
	for _, i := range result.Issues {
		issues = append(issues, JiraSearchResult{
			Key:     i.Key,
			Summary: i.Fields.Summary,
			Status:  i.Fields.Status.Name,
			Type:    i.Fields.IssueType.Name,
		})
	}

	return issues, result.Total, nil
}

// FormatJiraSearch returns a human-readable listing of search results.
func FormatJiraSearch(issues []JiraSearchResult, total int, jql string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "=== Jira Search Results (%d of %d) ===\n", len(issues), total)
	fmt.Fprintf(&sb, "JQL: %s\n\n", jql)
	for _, i := range issues {
		fmt.Fprintf(&sb, "%-12s  [%-12s]  %-10s  %s\n", i.Key, i.Status, i.Type, i.Summary)
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
