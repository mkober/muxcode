package bus

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// jiraCreateFixture stands in for a Jira instance and counts what reached it,
// so a test can assert that nothing was written as well as what was.
type jiraCreateFixture struct {
	issueTypes []string
	boards     []jiraBoard
	sprints    []jiraSprint
	createCode int
	sprintCode int

	posts    int
	lastBody map[string]interface{}
}

func (f *jiraCreateFixture) serve(t *testing.T) *AtlassianConfig {
	t.Helper()
	if f.createCode == 0 {
		f.createCode = 201
	}
	if f.sprintCode == 0 {
		f.sprintCode = 204
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			f.posts++
		}
		switch {
		case strings.Contains(r.URL.Path, "/issuetypes"):
			var types []jiraIssueType
			for _, n := range f.issueTypes {
				types = append(types, jiraIssueType{ID: n, Name: n})
			}
			writeJiraJSON(w, 200, map[string]interface{}{"issueTypes": types})
		case strings.Contains(r.URL.Path, "/myself"):
			writeJiraJSON(w, 200, map[string]string{"accountId": "acc-1", "displayName": "Mark K"})
		case strings.Contains(r.URL.Path, "/sprint/") && r.Method == http.MethodPost:
			w.WriteHeader(f.sprintCode)
			if f.sprintCode >= 300 {
				fmt.Fprint(w, `{"errorMessages":["sprint is closed"]}`)
			}
		case strings.Contains(r.URL.Path, "/sprint"):
			writeJiraJSON(w, 200, map[string]interface{}{"values": f.sprints})
		case strings.Contains(r.URL.Path, "/board"):
			writeJiraJSON(w, 200, map[string]interface{}{"values": f.boards})
		case strings.HasSuffix(r.URL.Path, "/rest/api/3/issue"):
			_ = json.NewDecoder(r.Body).Decode(&f.lastBody)
			w.WriteHeader(f.createCode)
			fmt.Fprint(w, `{"key":"PROMGT-901"}`)
		default:
			w.WriteHeader(404)
		}
	})
	server := newPipeServer(handler)
	prev := atlassianHTTPClient
	atlassianHTTPClient = server.Client()
	t.Cleanup(func() {
		atlassianHTTPClient = prev
		server.Close()
	})
	return &AtlassianConfig{JiraBaseURL: server.URL, UserEmail: "u@example.com", APIToken: "t"}
}

func writeJiraJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func baseCreateOpts() JiraCreateOptions {
	return JiraCreateOptions{
		Project:   "PROMGT",
		IssueType: "Bug",
		Summary:   "a defect",
		Payload:   json.RawMessage(`{"fields":{"description":{"type":"doc"}}}`),
	}
}

func TestJiraCreateIssue_ValidIssueTypePasses(t *testing.T) {
	f := &jiraCreateFixture{issueTypes: []string{"Bug", "Story"}}
	cfg := f.serve(t)

	res, err := JiraCreateIssue(cfg, baseCreateOpts())
	if err != nil {
		t.Fatalf("a valid issue type must create, got %v", err)
	}
	if res.Key != "PROMGT-901" {
		t.Errorf("want the created key, got %q", res.Key)
	}
	if f.lastBody["fields"].(map[string]interface{})["description"] == nil {
		t.Error("the payload's fields must be merged into the created issue")
	}
}

// The negative control for the test above: an unmatched type must name the
// valid ones rather than guessing an id and filing the wrong type.
func TestJiraCreateIssue_UnknownIssueTypeListsValidNames(t *testing.T) {
	f := &jiraCreateFixture{issueTypes: []string{"Bug", "Story"}}
	cfg := f.serve(t)

	opts := baseCreateOpts()
	opts.IssueType = "Defect"
	_, err := JiraCreateIssue(cfg, opts)
	if err == nil {
		t.Fatal("an unknown issue type must fail")
	}
	if !strings.Contains(err.Error(), "Bug") || !strings.Contains(err.Error(), "Story") {
		t.Errorf("the error must list the valid types, got %v", err)
	}
	if f.posts != 0 {
		t.Errorf("resolution failed, so nothing may be written: got %d POSTs", f.posts)
	}
}

func TestJiraCreateIssue_BoardMatches(t *testing.T) {
	cases := []struct {
		name    string
		boards  []jiraBoard
		wantErr string
	}{
		{"none", nil, "no board named"},
		{"one", []jiraBoard{{ID: 7, Name: "PKH Build"}}, ""},
		{"two", []jiraBoard{{ID: 7, Name: "PKH Build"}, {ID: 9, Name: "PKH Build"}}, "2 boards match"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &jiraCreateFixture{
				issueTypes: []string{"Bug"},
				boards:     c.boards,
				sprints:    []jiraSprint{{ID: 42, Name: "Sprint 9", State: "active"}},
			}
			cfg := f.serve(t)

			opts := baseCreateOpts()
			opts.Sprint, opts.Board = "current", "PKH Build"
			res, err := JiraCreateIssue(cfg, opts)

			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("exactly one board must resolve, got %v", err)
				}
				if res.BoardID != 7 {
					t.Errorf("want board 7, got %d", res.BoardID)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("want an error containing %q, got %v", c.wantErr, err)
			}
			if f.posts != 0 {
				t.Errorf("an unresolved board must write nothing: got %d POSTs", f.posts)
			}
		})
	}
}

func TestJiraCreateIssue_ActiveSprintCounts(t *testing.T) {
	cases := []struct {
		name    string
		sprints []jiraSprint
		wantErr string
	}{
		{"none", nil, "no active sprint"},
		{"one", []jiraSprint{{ID: 42, Name: "Sprint 9", State: "active"}}, ""},
		{"two", []jiraSprint{{ID: 42, Name: "A", State: "active"}, {ID: 43, Name: "B", State: "active"}}, "2 active sprints"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &jiraCreateFixture{
				issueTypes: []string{"Bug"},
				boards:     []jiraBoard{{ID: 7, Name: "PKH Build"}},
				sprints:    c.sprints,
			}
			cfg := f.serve(t)

			opts := baseCreateOpts()
			opts.Sprint, opts.Board = "current", "PKH Build"
			res, err := JiraCreateIssue(cfg, opts)

			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("exactly one active sprint must resolve, got %v", err)
				}
				if res.SprintID != 42 || res.SprintName != "Sprint 9" {
					t.Errorf("want sprint 42/Sprint 9, got %d/%q", res.SprintID, res.SprintName)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("want an error containing %q, got %v", c.wantErr, err)
			}
			if f.posts != 0 {
				t.Errorf("an unresolved sprint must write nothing: got %d POSTs", f.posts)
			}
		})
	}
}

// The regression test named in the spec: create succeeds, the sprint call
// fails, and the issue EXISTS. Reporting this as a plain failure is what makes
// a caller retry and file a duplicate, so the key must survive the error.
func TestJiraCreateIssue_SprintFailureStillReportsKey(t *testing.T) {
	f := &jiraCreateFixture{
		issueTypes: []string{"Bug"},
		boards:     []jiraBoard{{ID: 7, Name: "PKH Build"}},
		sprints:    []jiraSprint{{ID: 42, Name: "Sprint 9", State: "active"}},
		sprintCode: 400,
	}
	cfg := f.serve(t)

	opts := baseCreateOpts()
	opts.Sprint, opts.Board = "current", "PKH Build"
	res, err := JiraCreateIssue(cfg, opts)

	if err == nil {
		t.Fatal("a failed sprint add must be an error")
	}
	if res.Key != "PROMGT-901" {
		t.Fatalf("the key must survive the error or the caller retries into a duplicate, got %q", res.Key)
	}
	if !strings.Contains(err.Error(), "sprint") {
		t.Errorf("the error must name the step that failed, got %v", err)
	}
	if !strings.Contains(FormatJiraCreate(res), "KEY=PROMGT-901") {
		t.Error("the formatted partial failure must still carry the machine-readable KEY line")
	}
}

// The counterpart to the sprint-failure case: a create that itself fails has
// written nothing, so there is no key to report.
func TestJiraCreateIssue_CreateFailureReportsNoKey(t *testing.T) {
	f := &jiraCreateFixture{issueTypes: []string{"Bug"}, createCode: 400}
	cfg := f.serve(t)

	res, err := JiraCreateIssue(cfg, baseCreateOpts())
	if err == nil {
		t.Fatal("a non-201 create must be an error")
	}
	if res.Key != "" {
		t.Errorf("nothing was created, so no key may be reported, got %q", res.Key)
	}
	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("the error must carry the verbatim status, got %v", err)
	}
}

func TestJiraCreateIssue_DryRunWritesNothing(t *testing.T) {
	f := &jiraCreateFixture{
		issueTypes: []string{"Bug"},
		boards:     []jiraBoard{{ID: 7, Name: "PKH Build"}},
		sprints:    []jiraSprint{{ID: 42, Name: "Sprint 9", State: "active"}},
	}
	cfg := f.serve(t)

	opts := baseCreateOpts()
	opts.Sprint, opts.Board, opts.Assignee = "current", "PKH Build", "me"
	opts.DryRun = true
	res, err := JiraCreateIssue(cfg, opts)
	if err != nil {
		t.Fatalf("dry run must succeed, got %v", err)
	}
	if f.posts != 0 {
		t.Errorf("a dry run must make zero POSTs, got %d", f.posts)
	}
	if res.Key != "" {
		t.Errorf("a dry run creates nothing, got key %q", res.Key)
	}
	// Resolution must be real, not a separate path: the dry run reports what a
	// live run would have used.
	if res.SprintID != 42 || res.BoardID != 7 || res.AssigneeName != "Mark K" {
		t.Errorf("dry run must exercise real resolution, got board=%d sprint=%d assignee=%q",
			res.BoardID, res.SprintID, res.AssigneeName)
	}
	if !strings.Contains(res.RequestBody, "PROMGT") {
		t.Error("dry run must print the body it would have sent")
	}
}

// A nonpositive sprint id parses as a number, so the create used to succeed
// and then skip placement on its own id > 0 guard — exiting 0 with
// "Sprint: none" after doing half the job the caller asked for.
func TestJiraCreateIssue_NonPositiveSprintRejectedBeforeWriting(t *testing.T) {
	for _, bad := range []string{"0", "-1"} {
		t.Run(bad, func(t *testing.T) {
			f := &jiraCreateFixture{issueTypes: []string{"Bug"}}
			cfg := f.serve(t)

			opts := baseCreateOpts()
			opts.Sprint = bad
			res, err := JiraCreateIssue(cfg, opts)
			if err == nil {
				t.Fatalf("--sprint %s must be rejected", bad)
			}
			if res.Key != "" {
				t.Errorf("rejection must precede the write, got key %q", res.Key)
			}
			if f.posts != 0 {
				t.Errorf("nothing may be created for an unusable sprint id, got %d POSTs", f.posts)
			}
		})
	}
}

// The negative control: a positive explicit id is still accepted and placed.
func TestJiraCreateIssue_PositiveSprintIDIsPlaced(t *testing.T) {
	f := &jiraCreateFixture{issueTypes: []string{"Bug"}}
	cfg := f.serve(t)

	opts := baseCreateOpts()
	opts.Sprint = "42"
	res, err := JiraCreateIssue(cfg, opts)
	if err != nil {
		t.Fatalf("an explicit positive sprint id must work, got %v", err)
	}
	if res.SprintID != 42 {
		t.Errorf("want sprint 42, got %d", res.SprintID)
	}
}

func TestJiraCreateIssue_SprintCurrentRequiresBoard(t *testing.T) {
	f := &jiraCreateFixture{issueTypes: []string{"Bug"}}
	cfg := f.serve(t)

	opts := baseCreateOpts()
	opts.Sprint = "current"
	if _, err := JiraCreateIssue(cfg, opts); err == nil {
		t.Fatal("--sprint current without --board must fail")
	}
	if f.posts != 0 {
		t.Errorf("a rejected argument set must write nothing, got %d POSTs", f.posts)
	}
}
