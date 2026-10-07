package bus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func releaseHandler(tag string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"tag_name":%q,"published_at":"2026-10-06T12:00:00Z","tarball_url":"https://api.github.com/repos/mkober/muxcode/tarball/%s"}`, tag, tag)
	})
}

func pipeReleaseClient(s *pipeServer) ReleaseClient {
	return ReleaseClient{APIURL: s.URL + "/repos/mkober/muxcode/releases/latest", HTTP: s.Client()}
}

func setInstalledVersion(t *testing.T, v string) {
	t.Helper()
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

// The ahead rows are the negative control for Decision 2: a dev build past
// the tag must never read newer, or the hot fix downgrades the developer's
// own install.
func TestCheckUpgradeVerdicts(t *testing.T) {
	s := newPipeServer(releaseHandler("v0.1.20"))
	cases := []struct {
		installed string
		verdict   UpgradeVerdict
		summary   string
	}{
		{"v0.1.19", UpgradeNewer, "installed v0.1.19 → latest v0.1.20 available"},
		{"v0.1.19-4-gabc1234", UpgradeNewer, "installed v0.1.19-4-gabc1234 → latest v0.1.20 available"},
		{"v0.1.20", UpgradeCurrent, "installed v0.1.20 is current (latest v0.1.20)"},
		{"v0.1.20-9-g7d339be", UpgradeAhead, "installed v0.1.20-9-g7d339be is ahead of the latest release v0.1.20"},
		{"v0.1.20-9-g7d339be-dirty", UpgradeAhead, "installed v0.1.20-9-g7d339be-dirty is ahead of the latest release v0.1.20"},
		{"v0.2.0", UpgradeAhead, "installed v0.2.0 is ahead of the latest release v0.1.20"},
		{"devel", UpgradeUnknown, "installed devel cannot be compared with the latest release v0.1.20: malformed version"},
	}
	for _, c := range cases {
		t.Run(c.installed, func(t *testing.T) {
			setInstalledVersion(t, c.installed)
			got, err := CheckUpgrade(context.Background(), pipeReleaseClient(s))
			if err != nil {
				t.Fatalf("CheckUpgrade: %v", err)
			}
			if got.Verdict != c.verdict {
				t.Errorf("verdict = %q, want %q", got.Verdict, c.verdict)
			}
			if got.Installed.Version != c.installed || got.Latest.Tag != "v0.1.20" {
				t.Errorf("installed %q latest %q, want %q and v0.1.20", got.Installed.Version, got.Latest.Tag, c.installed)
			}
			if !strings.HasPrefix(got.Summary(), c.summary) {
				t.Errorf("Summary() = %q, want prefix %q", got.Summary(), c.summary)
			}
		})
	}
}

// A failed lookup still names the installed build, which the CLI's JSON
// error shape reports.
func TestCheckUpgradeLookupFailureKeepsInstalled(t *testing.T) {
	setInstalledVersion(t, "v0.1.19")
	s := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	got, err := CheckUpgrade(context.Background(), pipeReleaseClient(s))
	if err == nil {
		t.Fatal("CheckUpgrade succeeded against a 404")
	}
	if got.Installed.Version != "v0.1.19" || got.Verdict != "" {
		t.Errorf("got installed %q verdict %q, want v0.1.19 and no verdict", got.Installed.Version, got.Verdict)
	}
}

func TestLatestReleaseStatusErrors(t *testing.T) {
	rateLimited := `{"message":"API rate limit exceeded for 203.0.113.7. (But here's the good news: Authenticated requests get a higher rate limit.)","documentation_url":"https://docs.github.com/rest/overview/resources-in-the-rest-api#rate-limiting"}`
	cases := []struct {
		name    string
		status  int
		body    string
		apiURL  string
		token   string
		want    []string
		wantNot []string
	}{
		{"404", http.StatusNotFound, `{"message":"Not Found"}`, "", "", []string{"HTTP 404", "Not Found"}, []string{"GITHUB_TOKEN"}},
		{"403 unauthenticated", http.StatusForbidden, rateLimited, DefaultUpgradeAPIURL, "", []string{"HTTP 403", "API rate limit exceeded", "set GITHUB_TOKEN or GH_TOKEN"}, nil},
		{"403 with token", http.StatusForbidden, rateLimited, DefaultUpgradeAPIURL, "tok", []string{"HTTP 403", "API rate limit exceeded"}, []string{"set GITHUB_TOKEN"}},
		{"500 oversized body", http.StatusInternalServerError, strings.Repeat("<p>error</p>\n", 500), "", "", []string{"HTTP 500", "<p>error</p> <p>error</p>", "…"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			}))
			rc := pipeReleaseClient(s)
			if c.apiURL != "" {
				rc.APIURL = c.apiURL
			}
			rc.Token = c.token
			_, err := rc.LatestRelease(context.Background())
			if err == nil {
				t.Fatalf("LatestRelease succeeded on HTTP %d", c.status)
			}
			msg := err.Error()
			for _, w := range c.want {
				if !strings.Contains(msg, w) {
					t.Errorf("error %q missing %q", msg, w)
				}
			}
			for _, w := range c.wantNot {
				if strings.Contains(msg, w) {
					t.Errorf("error %q must not contain %q", msg, w)
				}
			}
			if len(msg) > 400 {
				t.Errorf("error is %d bytes; the body must be an excerpt", len(msg))
			}
		})
	}
}

func TestLatestReleaseTimeout(t *testing.T) {
	s := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	rc := ReleaseClient{APIURL: s.URL, HTTP: s.clientWithTimeout(50 * time.Millisecond)}
	_, err := rc.LatestRelease(context.Background())
	var te interface{ Timeout() bool }
	if err == nil || !errors.As(err, &te) || !te.Timeout() {
		t.Fatalf("LatestRelease against a hung API = %v, want a timeout error", err)
	}
}

// The token is sent to GitHub's API and nowhere else: an override URL is
// any host the environment names, and plaintext http would expose it.
func TestLatestReleaseTokenOnlyToGitHub(t *testing.T) {
	var gotAuth string
	s := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"tag_name":"v0.1.20"}`)
	}))
	cases := []struct{ url, want string }{
		{DefaultUpgradeAPIURL, "Bearer tok"},
		{"https://mirror.example/repos/mkober/muxcode/releases/latest", ""},
		{"http://api.github.com/repos/mkober/muxcode/releases/latest", ""},
	}
	for _, c := range cases {
		gotAuth = "unset"
		rc := ReleaseClient{APIURL: c.url, Token: "tok", HTTP: s.Client()}
		if _, err := rc.LatestRelease(context.Background()); err != nil {
			t.Fatalf("%s: %v", c.url, err)
		}
		if gotAuth != c.want {
			t.Errorf("%s: Authorization = %q, want %q", c.url, gotAuth, c.want)
		}
	}
}

// The tag names the Phase 2 cache directory, so anything but a plain
// release tag fails Check before it reaches the filesystem.
func TestLatestReleaseRejectsTagThatIsNotARelease(t *testing.T) {
	for _, tag := range []string{"", "latest", "1.0.0", "v1.0", "v1.0.0+../../evil", "v1.0.0/../x"} {
		s := newPipeServer(releaseHandler(tag))
		_, err := pipeReleaseClient(s).LatestRelease(context.Background())
		if err == nil || !strings.Contains(err.Error(), "is not a vMAJOR.MINOR.PATCH release") {
			t.Errorf("tag %q: err = %v, want a not-a-release refusal", tag, err)
		}
	}
}

func TestLatestReleaseTarballURL(t *testing.T) {
	s := newPipeServer(releaseHandler("v0.1.20"))
	rel, err := pipeReleaseClient(s).LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	want := Release{
		Tag:         "v0.1.20",
		TarballURL:  "https://github.com/mkober/muxcode/archive/refs/tags/v0.1.20.tar.gz",
		PublishedAt: "2026-10-06T12:00:00Z",
	}
	if rel != want {
		t.Errorf("release = %+v, want %+v", rel, want)
	}

	rc := pipeReleaseClient(s)
	rc.TarballURL = "file:///scratch/muxcode-src.tar.gz"
	rel, err = rc.LatestRelease(context.Background())
	if err != nil || rel.TarballURL != rc.TarballURL {
		t.Errorf("override: tarball %q err %v, want %q", rel.TarballURL, err, rc.TarballURL)
	}
}

// The production client serves file:// so the hermetic integration script
// can stand in a local releases/latest file, and a missing one is a 404.
func TestLatestReleaseReadsFileURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest.json")
	if err := os.WriteFile(path, []byte(`{"tag_name":"v0.2.0","published_at":"2026-10-06T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err := ReleaseClient{APIURL: "file://" + path}.LatestRelease(context.Background())
	if err != nil || rel.Tag != "v0.2.0" {
		t.Fatalf("file URL: tag %q err %v, want v0.2.0", rel.Tag, err)
	}

	_, err = ReleaseClient{APIURL: "file://" + filepath.Join(dir, "missing.json")}.LatestRelease(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("missing file: err = %v, want HTTP 404", err)
	}
}

// A lookup must never hang the modal or a cron poll: the Check timeout fills
// in whenever the client has none, and an explicit one is kept.
func TestReleaseClientTimeout(t *testing.T) {
	if got := (ReleaseClient{}).httpClient().Timeout; got != releaseCheckTimeout {
		t.Errorf("production timeout = %v, want %v", got, releaseCheckTimeout)
	}
	if got := (ReleaseClient{HTTP: &http.Client{}}).httpClient().Timeout; got != releaseCheckTimeout {
		t.Errorf("override without timeout = %v, want %v", got, releaseCheckTimeout)
	}
	if got := (ReleaseClient{HTTP: &http.Client{Timeout: time.Second}}).httpClient().Timeout; got != time.Second {
		t.Errorf("override timeout = %v, want 1s kept", got)
	}
}

func TestDefaultReleaseClientEnv(t *testing.T) {
	t.Setenv("MUXCODE_UPGRADE_API_URL", "")
	t.Setenv("MUXCODE_UPGRADE_TARBALL_URL", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "gh-token")
	c := DefaultReleaseClient()
	if c.APIURL != DefaultUpgradeAPIURL || c.TarballURL != "" || c.Token != "gh-token" {
		t.Errorf("defaults = %+v, want GitHub URL, no tarball override, GH_TOKEN", c)
	}

	t.Setenv("GITHUB_TOKEN", "github-token")
	t.Setenv("MUXCODE_UPGRADE_API_URL", "file:///scratch/latest.json")
	t.Setenv("MUXCODE_UPGRADE_TARBALL_URL", "file:///scratch/src.tar.gz")
	c = DefaultReleaseClient()
	if c.APIURL != "file:///scratch/latest.json" || c.TarballURL != "file:///scratch/src.tar.gz" || c.Token != "github-token" {
		t.Errorf("overrides = %+v, want both URLs and GITHUB_TOKEN ahead of GH_TOKEN", c)
	}
}

// A machine without a toolchain fails at Check with the tool named; with
// every tool present nothing is reported missing.
func TestCheckUpgradeProbesBuildTools(t *testing.T) {
	setInstalledVersion(t, "v0.1.19")
	dir := t.TempDir()
	for _, name := range []string{"go", "make"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	s := newPipeServer(releaseHandler("v0.1.20"))

	got, err := CheckUpgrade(context.Background(), pipeReleaseClient(s))
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	if !reflect.DeepEqual(got.MissingTools, []string{"tar"}) {
		t.Errorf("MissingTools = %v, want [tar]", got.MissingTools)
	}
	if terr := got.ToolsErr(); terr == nil || !strings.Contains(terr.Error(), "missing build tools: tar") {
		t.Errorf("ToolsErr() = %v, want it to name tar", terr)
	}

	if err := os.WriteFile(filepath.Join(dir, "tar"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = CheckUpgrade(context.Background(), pipeReleaseClient(s))
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	if got.MissingTools != nil || got.ToolsErr() != nil {
		t.Errorf("all tools present: MissingTools = %v, ToolsErr = %v, want none", got.MissingTools, got.ToolsErr())
	}
}

// TestDevBuildWarning replays 2026-10-07: v0.1.20-16-gfeb4a13-dirty carried
// an unreleased feature, read as older than v0.1.21, and was replaced
// silently. Exact release tags — a pre-release included — are the negative
// controls and must stay quiet.
func TestDevBuildWarning(t *testing.T) {
	for _, c := range []struct {
		installed string
		verdict   UpgradeVerdict
		warn      bool
	}{
		{"v0.1.20-16-gfeb4a13-dirty", UpgradeNewer, true},
		{"v0.1.20-16-gfeb4a13", UpgradeNewer, true},
		{"v0.1.21-dirty", UpgradeCurrent, true},
		{"v0.1.21-9-g7d339be", UpgradeAhead, true},
		{"devel", UpgradeUnknown, true},
		{"v0.1.20", UpgradeNewer, false},
		{"v0.1.21", UpgradeCurrent, false},
		{"v0.2.0-rc.1", UpgradeAhead, false},
	} {
		check := UpgradeCheck{Installed: Info{Version: c.installed}, Latest: Release{Tag: "v0.1.21"}, Verdict: c.verdict}
		w := check.DevBuildWarning()
		if got := w != ""; got != c.warn {
			t.Errorf("%s: warning %q, want warn=%v", c.installed, w, c.warn)
			continue
		}
		if c.warn && (!strings.Contains(w, c.installed) || !strings.Contains(w, "v0.1.21") || !strings.Contains(w, "unreleased dev build")) {
			t.Errorf("%s: warning must name the build, the release and what is lost: %q", c.installed, w)
		}
	}
}
