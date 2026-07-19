package server

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/ssrf/v3"
)

type posterRoundTripFunc func(*http.Request) (*http.Response, error)

func (f posterRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func posterTestResponse(req *http.Request, status int, location string) *http.Response {
	header := make(http.Header)
	if location != "" {
		header.Set("Location", location)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader("test response")),
		Request:    req,
	}
}

func testPosterClient(arr, public http.RoundTripper) *posterClient {
	return &posterClient{
		arr: &http.Client{
			Transport:     arr,
			CheckRedirect: posterArrRedirectPolicy,
		},
		public: &http.Client{
			Transport:     public,
			CheckRedirect: newPosterPublicRedirectPolicy(),
		},
	}
}

func posterRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet,
		"http://192.168.1.10:8989/api/v3/mediacover/12/poster.jpg", http.NoBody)
	if err != nil {
		t.Fatalf("build poster request: %v", err)
	}
	req.Header.Set("X-Api-Key", "arr-secret")
	return req
}

// TestPosterClient verifies the two trust zones: same-origin arr redirects keep
// the key, crossing to a public CDN strips it permanently, unsafe public
// targets are rejected before the public transport runs, and same-host but
// different-port redirects are not mistaken for the trusted arr origin.
func TestPosterClient(t *testing.T) {
	t.Run("same-origin arr redirect keeps API key", func(t *testing.T) {
		arrCalls := 0
		arr := posterRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			arrCalls++
			if got := req.Header.Get("X-Api-Key"); got != "arr-secret" {
				t.Errorf("arr request API key = %q, want preserved", got)
			}
			if arrCalls == 1 {
				return posterTestResponse(req, http.StatusFound, "/MediaCover/12/poster-500.jpg"), nil
			}
			return posterTestResponse(req, http.StatusOK, ""), nil
		})
		publicCalled := false
		public := posterRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			publicCalled = true
			return posterTestResponse(req, http.StatusOK, ""), nil
		})

		resp, err := testPosterClient(arr, public).Do(posterRequest(t))
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		_ = resp.Body.Close()
		if arrCalls != 2 {
			t.Errorf("arr requests = %d, want 2", arrCalls)
		}
		if publicCalled {
			t.Error("same-origin redirect used public transport")
		}
	})

	t.Run("cross-origin refetch strips API key permanently", func(t *testing.T) {
		arr := posterRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return posterTestResponse(req, http.StatusFound, "https://cdn.example/poster.jpg"), nil
		})
		publicCalls := 0
		public := posterRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			publicCalls++
			if got := req.Header.Get("X-Api-Key"); got != "" {
				t.Errorf("public request leaked X-Api-Key %q", got)
			}
			if publicCalls == 1 {
				return posterTestResponse(req, http.StatusFound, "https://images.example/final.jpg"), nil
			}
			return posterTestResponse(req, http.StatusOK, ""), nil
		})

		resp, err := testPosterClient(arr, public).Do(posterRequest(t))
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		_ = resp.Body.Close()
		if publicCalls != 2 {
			t.Errorf("public requests = %d, want 2", publicCalls)
		}
	})

	t.Run("private cross-origin target is blocked before dial", func(t *testing.T) {
		arr := posterRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return posterTestResponse(req, http.StatusFound,
				"https://169.254.169.254/latest/meta-data/"), nil
		})
		publicCalled := false
		public := posterRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			publicCalled = true
			return posterTestResponse(req, http.StatusOK, ""), nil
		})

		resp, err := testPosterClient(arr, public).Do(posterRequest(t))
		if resp != nil {
			_ = resp.Body.Close()
		}
		var ssrfErr *ssrf.Error
		if !errors.As(err, &ssrfErr) || ssrfErr.Kind != ssrf.KindNonPublicIP {
			t.Errorf("Do() error = %v, want wrapped ssrf.KindNonPublicIP", err)
		}
		if publicCalled {
			t.Error("unsafe redirect reached public transport")
		}
	})

	t.Run("same host on different port is not the arr origin", func(t *testing.T) {
		arr := posterRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return posterTestResponse(req, http.StatusFound,
				"http://192.168.1.10:8990/internal"), nil
		})
		publicCalled := false
		public := posterRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			publicCalled = true
			return posterTestResponse(req, http.StatusOK, ""), nil
		})

		resp, err := testPosterClient(arr, public).Do(posterRequest(t))
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			t.Fatal("Do() = nil error, want different-port redirect blocked")
		}
		if publicCalled {
			t.Error("different-port private redirect reached public transport")
		}
	})
}

// TestPosterClient_hopCap verifies that the cap covers the complete chain even
// when every redirect remains on the trusted arr origin.
func TestPosterClient_hopCap(t *testing.T) {
	arrCalls := 0
	arr := posterRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		arrCalls++
		return posterTestResponse(req, http.StatusFound, "/next.jpg"), nil
	})
	public := posterRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return posterTestResponse(req, http.StatusOK, ""), nil
	})

	resp, err := testPosterClient(arr, public).Do(posterRequest(t))
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatalf("Do() after %d requests = nil, want redirect-cap error", arrCalls)
	}
	if arrCalls != posterMaxRedirects+1 {
		t.Errorf("arr requests = %d, want %d", arrCalls, posterMaxRedirects+1)
	}
}
