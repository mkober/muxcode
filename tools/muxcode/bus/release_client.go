package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	// DefaultUpgradeAPIURL names the newest published release; GitHub never
	// reports a draft or a pre-release as "latest".
	DefaultUpgradeAPIURL = "https://api.github.com/repos/mkober/muxcode/releases/latest"

	upgradeTarballURLFormat = "https://github.com/mkober/muxcode/archive/refs/tags/%s.tar.gz"
	releaseCheckTimeout     = 10 * time.Second
	releaseBodyLimit        = 64 << 10
	releaseExcerptRunes     = 200
)

// releaseTagPattern admits a tag only when it is a single safe path
// component: the tag names the upgrade cache directory, so one carrying a
// separator or "+" build metadata must fail Check before it reaches disk.
var releaseTagPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

// Release is the latest published muxcode release. The JSON names are part
// of the `muxcode upgrade --check --json` output contract.
type Release struct {
	Tag         string `json:"tag"`
	TarballURL  string `json:"tarball_url"`
	PublishedAt string `json:"published_at"`
}

// ReleaseClient looks up the latest release.
//
// HTTP overrides the transport and is nil in production, where the client
// carries the 10 s Check timeout and also serves file:// URLs, so the
// integration script can point MUXCODE_UPGRADE_API_URL at a local JSON file.
// A test sets HTTP to reach a handler in-process instead of binding a socket,
// which the Codex sandbox refuses (MUX-153).
//
// Token is sent only to https://api.github.com: an overridden APIURL is any
// host the environment names, and a GitHub token must not travel there.
type ReleaseClient struct {
	APIURL     string
	TarballURL string
	Token      string
	HTTP       *http.Client
}

// DefaultReleaseClient resolves the client from the environment:
// MUXCODE_UPGRADE_API_URL and MUXCODE_UPGRADE_TARBALL_URL override the
// GitHub URLs, and GITHUB_TOKEN (else GH_TOKEN) lifts the unauthenticated
// rate limit.
func DefaultReleaseClient() ReleaseClient {
	c := ReleaseClient{
		APIURL:     os.Getenv("MUXCODE_UPGRADE_API_URL"),
		TarballURL: os.Getenv("MUXCODE_UPGRADE_TARBALL_URL"),
		Token:      os.Getenv("GITHUB_TOKEN"),
	}
	if c.APIURL == "" {
		c.APIURL = DefaultUpgradeAPIURL
	}
	if c.Token == "" {
		c.Token = os.Getenv("GH_TOKEN")
	}
	return c
}

// LatestRelease fetches the latest release. A transport failure, a non-200
// status, an undecodable body or a tag that is not a vMAJOR.MINOR.PATCH
// release is an error naming the URL. A status error carries an excerpt of
// the body, because GitHub explains a 403 — rate limit, bad token — only
// there. TarballURL is the MUXCODE_UPGRADE_TARBALL_URL override when set,
// else the tag's GitHub source archive.
func (c ReleaseClient) LatestRelease(ctx context.Context) (Release, error) {
	apiURL := c.APIURL
	if apiURL == "" {
		apiURL = DefaultUpgradeAPIURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return Release{}, fmt.Errorf("latest release: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "muxcode/"+BuildVersion())
	github := isGitHubAPI(req.URL)
	authed := c.Token != "" && github
	if authed {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("latest release %s: %w", apiURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, releaseBodyLimit))
	if err != nil {
		return Release{}, fmt.Errorf("latest release %s: reading body: %w", apiURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("latest release %s: HTTP %d: %s", apiURL, resp.StatusCode, bodyExcerpt(body))
		if github && !authed && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) {
			msg += " — set GITHUB_TOKEN or GH_TOKEN to lift the unauthenticated rate limit"
		}
		return Release{}, errors.New(msg)
	}

	var raw struct {
		TagName     string `json:"tag_name"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Release{}, fmt.Errorf("latest release %s: decoding: %v (body: %s)", apiURL, err, bodyExcerpt(body))
	}
	if !releaseTagPattern.MatchString(raw.TagName) {
		return Release{}, fmt.Errorf("latest release %s: tag %q is not a vMAJOR.MINOR.PATCH release", apiURL, raw.TagName)
	}
	tarball := c.TarballURL
	if tarball == "" {
		tarball = fmt.Sprintf(upgradeTarballURLFormat, raw.TagName)
	}
	return Release{Tag: raw.TagName, TarballURL: tarball, PublishedAt: raw.PublishedAt}, nil
}

// httpClient is the Check's client: c.HTTP copied with the Check timeout
// filled in when it has none, or the production client — a lookup must
// never hang the modal or a cron poll on an unresponsive API.
func (c ReleaseClient) httpClient() *http.Client {
	if c.HTTP != nil {
		hc := *c.HTTP
		if hc.Timeout == 0 {
			hc.Timeout = releaseCheckTimeout
		}
		return &hc
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.RegisterProtocol("file", http.NewFileTransport(http.Dir("/")))
	return &http.Client{Timeout: releaseCheckTimeout, Transport: t}
}

func isGitHubAPI(u *url.URL) bool {
	return u.Scheme == "https" && strings.EqualFold(u.Hostname(), "api.github.com")
}

// bodyExcerpt is the body on one line, cut to releaseExcerptRunes.
func bodyExcerpt(body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ")
	if s == "" {
		return "(empty body)"
	}
	return truncate(s, releaseExcerptRunes)
}
