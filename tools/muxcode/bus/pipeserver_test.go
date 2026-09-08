package bus

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// pipeServer is an in-process stand-in for httptest.NewServer (MUX-153).
//
// httptest.NewServer binds a real loopback socket, and the Codex sandbox
// refuses that: a test agent on codex panics in newLocalListener before any
// assertion runs, and `set -e` in test.sh then hides every module after the
// failure (MUX-152). Serving the handler through an http.RoundTripper needs no
// socket, so the suite runs under any sandbox — and is faster and hermetic
// besides.
//
// The shape mirrors httptest.Server (URL, Close, Client) so a call site
// converts by swapping the constructor and routing Client() into whatever holds
// the *http.Client. URL is a sentinel host that resolves nowhere, so a site
// that forgets the injection fails loudly rather than quietly reaching out.
type pipeServer struct {
	URL     string
	client  *http.Client
	handler http.Handler
	mu      sync.Mutex
	closed  bool
}

func newPipeServer(h http.Handler) *pipeServer {
	s := &pipeServer{URL: "http://pipe.invalid", handler: h}
	s.client = &http.Client{Transport: roundTripFunc(s.roundTrip)}
	return s
}

// roundTrip serves the handler off-goroutine and abandons it when the request
// context is cancelled.
//
// Cancellation is what makes http.Client.Timeout work: the client enforces its
// timeout by cancelling the request context, and a transport that ignores it
// simply blocks. Serving inline instead hung TestCheckOllamaInference_Timeout
// for the full 10-minute test timeout — the handler sleeps to simulate a slow
// server, and with no cancellation the client waited for it forever.
func (s *pipeServer) roundTrip(r *http.Request) (*http.Response, error) {
	if s.isClosed() {
		return nil, fmt.Errorf("pipeServer: connection refused (server closed)")
	}
	done := make(chan *http.Response, 1)
	go func() {
		rec := httptest.NewRecorder()
		s.handler.ServeHTTP(rec, r)
		done <- rec.Result()
	}()
	select {
	case resp := <-done:
		return resp, nil
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
}

// clientWithTimeout returns the pipe client carrying d, so production code that
// asked for a timeout still gets one.
func (s *pipeServer) clientWithTimeout(d time.Duration) *http.Client {
	c := *s.client
	c.Timeout = d
	return &c
}

// Client returns an http.Client whose requests reach the handler directly.
func (s *pipeServer) Client() *http.Client { return s.client }

// Close makes every later request fail, as closing an httptest.Server does.
//
// A no-op Close is not good enough: tests close the server to simulate a dead
// one and then assert the caller reports it unreachable. With the handler still
// answering, TestOllamaModelLoaded's "dead server must report responsive=false"
// failed — and any close-to-kill test that lacked such an assertion would have
// passed while proving nothing.
func (s *pipeServer) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *pipeServer) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// newAtlassianPipeServer is newPipeServer wired into the Atlassian path, whose
// requests go through the package-level atlassianHTTPClient rather than a
// caller-supplied one. Swapping that var is the only injection seam, so the
// swap and its restore live here instead of being repeated at every call site.
func newAtlassianPipeServer(t *testing.T, h http.Handler) *pipeServer {
	t.Helper()
	s := newPipeServer(h)
	orig := atlassianHTTPClient
	atlassianHTTPClient = s.Client()
	t.Cleanup(func() { atlassianHTTPClient = orig })
	return s
}

// newHealthPipeServer is newPipeServer wired into the Ollama health probes,
// which build their own client per call. Overriding the healthHTTPClient
// constructor is the seam; the restore lives here so no call site repeats it.
func newHealthPipeServer(t *testing.T, h http.Handler) *pipeServer {
	t.Helper()
	s := newPipeServer(h)
	orig := healthHTTPClient
	healthHTTPClient = s.clientWithTimeout
	t.Cleanup(func() { healthHTTPClient = orig })
	return s
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
