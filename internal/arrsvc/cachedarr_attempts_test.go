package arrsvc

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v2"
)

// transientThenOK answers the first request 500 and every later one 200 [],
// counting requests.
func transientThenOK(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(srv.Close)
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

func TestSingleAttempt_markedPassIssuesExactlyOneRequest(t *testing.T) {
	srv, calls := transientThenOK(t)
	c, err := NewCachedSonarr(srv.URL, "key", testGate(t.Context()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)

	// A transient-first-then-success upstream: the production wave client is
	// WithMaxAttempts(1), so the marked pass fails typed after ONE request
	// instead of retrying into the success.
	_, err = c.Series(WithRecovery(t.Context()))
	if !errors.Is(err, ErrRecoveryFailed) {
		t.Fatalf("marked read error = %v, want ErrRecoveryFailed", err)
	}
	if got := calls(); got != 1 {
		t.Errorf("upstream requests = %d, want exactly 1 (single-attempt wave client)", got)
	}
}

func TestSingleAttempt_plainPassKeepsTheShippedRetryPolicy(t *testing.T) {
	srv, calls := transientThenOK(t)
	// The shipped client's 3-attempt policy with a test-speed base delay
	// (production uses NewSonarr's 5 s; the policy, not the pacing, is the
	// subject — the wrapper must not clamp the shipped client's retries).
	shipped, err := arrapi.NewSonarr(srv.URL, "key",
		arrapi.WithMaxAttempts(3), arrapi.WithBaseDelay(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(shipped.Close)
	c := testCachedSonarr(&Sonarr{Sonarr: shipped}, &fakeSonarr{}, testGate(t.Context()))

	rows, err := c.Series(t.Context())
	if err != nil {
		t.Fatalf("plain read error = %v, want the retried success", err)
	}
	if rows == nil {
		t.Fatal("plain read returned no rows object")
	}
	if got := calls(); got != 2 {
		t.Errorf("upstream requests = %d, want 2 (transient retried by the shipped policy)", got)
	}
}
