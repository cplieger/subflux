package auth

import (
	"context"
	"crypto/sha1" // mirrors the production HIBP k-anonymity API path
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hibpRoundTripper redirects every request from the production HIBP host to a
// test server, captures the request headers, and returns a canned body keyed
// by URL path. Lets the unit test verify both the outgoing Add-Padding header
// and the parser's behaviour against arbitrary response bodies.
type hibpRoundTripper struct {
	t                *testing.T
	server           *httptest.Server
	respByPathSuffix map[string]string // last 5 chars of path -> body
	gotAddPadding    string
}

func (rt *hibpRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.t.Helper()
	rt.gotAddPadding = req.Header.Get("Add-Padding")
	rebased, _ := http.NewRequest(req.Method, rt.server.URL+req.URL.Path, http.NoBody)
	rebased.Header = req.Header
	return rt.server.Client().Do(rebased) //nolint:wrapcheck // test rt
}

func newHibpFake(t *testing.T, respByPathSuffix map[string]string) *http.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path is /range/<5 hex chars>; key the canned body by that prefix.
		key := r.URL.Path[len("/range/"):]
		body, ok := respByPathSuffix[strings.ToUpper(key)]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	rt := &hibpRoundTripper{t: t, server: srv, respByPathSuffix: respByPathSuffix}
	return &http.Client{Transport: rt}
}

// hibpFakeWithCapture is the same as newHibpFake but exposes the captured
// request-header value so the test can assert on it.
func hibpFakeWithCapture(
	t *testing.T,
	respByPathSuffix map[string]string,
) (*http.Client, *hibpRoundTripper) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path[len("/range/"):]
		body, ok := respByPathSuffix[strings.ToUpper(key)]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	rt := &hibpRoundTripper{t: t, server: srv, respByPathSuffix: respByPathSuffix}
	return &http.Client{Transport: rt}, rt
}

// TestCheckBreachedPassword_sets_add_padding_header verifies the privacy
// hardening header is on every outbound HIBP request. Without it, response
// sizes leak partial info about the password's hash-prefix bucket.
func TestCheckBreachedPassword_sets_add_padding_header(t *testing.T) {
	t.Parallel()
	hash := sha1.Sum([]byte("anything"))
	prefix := fmt.Sprintf("%X", hash)[:5]
	client, rt := hibpFakeWithCapture(t, map[string]string{prefix: ""})

	_, _ = CheckBreachedPassword(context.Background(), client, "anything")

	if rt.gotAddPadding != "true" {
		t.Errorf("Add-Padding header = %q, want %q", rt.gotAddPadding, "true")
	}
}

// TestCheckBreachedPassword_filters_zero_count_padding verifies that an entry
// matching the user's hash suffix BUT with count=0 (a synthetic padding
// entry) is NOT treated as a breach hit.
func TestCheckBreachedPassword_filters_zero_count_padding(t *testing.T) {
	t.Parallel()
	password := "MySecretPassword!"
	hash := sha1.Sum([]byte(password))
	hex := fmt.Sprintf("%X", hash)
	prefix, suffix := hex[:5], hex[5:]

	// Body where the user's actual suffix appears as a padding entry (count=0).
	// Real HIBP padding uses random suffixes, but this case proves the filter:
	// even an exact suffix collision must be ignored when count is 0.
	body := suffix + ":0\nDEADBEEF" + strings.Repeat("0", len(suffix)-8) + ":7\n"
	client := newHibpFake(t, map[string]string{prefix: body})

	breached, err := CheckBreachedPassword(context.Background(), client, password)
	if err != nil {
		t.Fatalf("CheckBreachedPassword unexpected error: %v", err)
	}
	if breached {
		t.Error("count=0 padding entry treated as breach; should be filtered")
	}
}

// TestCheckBreachedPassword_real_hit_still_matches verifies the filter doesn't
// over-correct: a genuine hit (count > 0) must still return true.
func TestCheckBreachedPassword_real_hit_still_matches(t *testing.T) {
	t.Parallel()
	password := "Password123!"
	hash := sha1.Sum([]byte(password))
	hex := fmt.Sprintf("%X", hash)
	prefix, suffix := hex[:5], hex[5:]

	body := "00" + strings.Repeat("F", len(suffix)-2) + ":0\n" + // padding
		suffix + ":42\n" + // real hit (count > 0)
		"FF" + strings.Repeat("0", len(suffix)-2) + ":3\n" // unrelated
	client := newHibpFake(t, map[string]string{prefix: body})

	breached, err := CheckBreachedPassword(context.Background(), client, password)
	if err != nil {
		t.Fatalf("CheckBreachedPassword unexpected error: %v", err)
	}
	if !breached {
		t.Error("real hit (count=42) not detected; filter is too aggressive")
	}
}

// TestCheckBreachedPassword_no_match_returns_false verifies the negative path:
// none of the returned suffixes match the password's hash and none are real
// hits — must return false (not breached).
func TestCheckBreachedPassword_no_match_returns_false(t *testing.T) {
	t.Parallel()
	password := "UntestedPassword99"
	hash := sha1.Sum([]byte(password))
	prefix := fmt.Sprintf("%X", hash)[:5]

	body := "DEADBEEFCAFE0000111122223333444455556666:5\n" +
		"AAAABBBBCCCCDDDDEEEEFFFFAAAABBBBCCCCDDDD:1\n"
	client := newHibpFake(t, map[string]string{prefix: body})

	breached, err := CheckBreachedPassword(context.Background(), client, password)
	if err != nil {
		t.Fatalf("CheckBreachedPassword unexpected error: %v", err)
	}
	if breached {
		t.Error("no-match response treated as breach")
	}
}
