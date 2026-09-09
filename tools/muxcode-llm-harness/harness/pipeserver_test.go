package harness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
)

// pipeServer is an in-process stand-in for httptest.NewServer (MUX-153).
//
// httptest.NewServer binds a real loopback socket, and the Codex sandbox
// refuses that: a test agent on codex panics in newLocalListener before any
// assertion runs, and `set -e` in test.sh then hides every module after the
// failure (MUX-152), so 2900 tests silently never execute. Serving the handler
// through an http.RoundTripper needs no socket, so the suite runs under any
// sandbox — and is faster and fully hermetic besides.
//
// The shape deliberately mirrors httptest.Server (URL, Close, Client) so a call
// site converts by swapping the constructor and injecting Client() into
// whatever holds the *http.Client. URL is a sentinel host that resolves
// nowhere: a site that forgets the injection fails loudly on DNS rather than
// quietly reaching the network.
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
// simply blocks. Serving inline hung the bus module's timeout test for a full
// 10 minutes; this module has no such test today, and should not acquire the
// trap when it gains one.
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

// Client returns an http.Client whose requests reach the handler directly.
func (s *pipeServer) Client() *http.Client { return s.client }

// Close makes every later request fail, as closing an httptest.Server does.
// A no-op Close silently defeats any test that closes the server to simulate a
// dead one — it caught the bus module's TestOllamaModelLoaded.
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
