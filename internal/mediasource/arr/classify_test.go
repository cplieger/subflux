package arr_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/sonarr"
	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr"
	"go.yaml.in/yaml/v4"
)

func TestClassify(t *testing.T) {
	codes := map[mediasource.Cause][]int{
		mediasource.Unauthorized: {401, 403},
		mediasource.NotFound:     {404},
		mediasource.Protocol:     {204, 206, 399, 400, 407, 409, 428, 430, 499, 600},
		mediasource.Unreachable:  {408, 429, 500, 501, 502, 503, 504, 599},
	}
	for want, list := range codes {
		for _, code := range list {
			if got := arr.Classify(&arrapi.StatusError{Code: code, Path: "/api/v5/series"}).Cause; got != want {
				t.Errorf("status %d: %v, want %v", code, got, want)
			}
		}
	}
	rows := map[string]struct {
		err  error
		want mediasource.Cause
	}{
		"too large":         {&arrapi.ResponseTooLargeError{Path: "/api/v5/series", Limit: 64 << 20}, mediasource.Protocol},
		"refused dial":      {fmt.Errorf("arrapi: request /api: %w", errors.New("dial tcp: connection refused")), mediasource.Unreachable},
		"passed deadline":   {fmt.Errorf("arrapi: request /api: %w", context.DeadlineExceeded), mediasource.Unreachable},
		"a wrapped refusal": {fmt.Errorf("arrapi: request /api: %w", &httpclient.RedirectRefusedError{Origin: "http://other:8989", Reason: httpclient.RedirectOtherOrigin}), mediasource.Protocol},
	}
	for name, r := range rows {
		if got := arr.Classify(r.err); got.Cause != r.want || !errors.Is(got, r.err) {
			t.Errorf("%s: %v, want %v wrapping the error", name, got.Cause, r.want)
		}
	}
	t.Run("the hop cap names the loop", func(t *testing.T) {
		got := arr.Classify(&httpclient.RedirectRefusedError{Origin: "http://sonarr:8989", Reason: httpclient.RedirectTooMany})
		if got.Cause != mediasource.Protocol || !strings.Contains(got.Err.Error(), "redirect loop") {
			t.Errorf("hop cap: %v %q, want protocol naming the loop", got.Cause, got.Err)
		}
	})
}

func TestClassifyServerChangedBeforeCode(t *testing.T) {
	cases := map[string]error{
		"a 404":     &arrapi.ServerChangedError{Status: &arrapi.StatusError{Code: 404}, From: v5},
		"a 401":     &arrapi.ServerChangedError{Status: &arrapi.StatusError{Code: 401}, From: v5},
		"a 503":     &arrapi.ServerChangedError{Status: &arrapi.StatusError{Code: 503}, From: v5},
		"no status": &arrapi.ServerChangedError{From: v5, To: "5.1.0.1"},
	}
	for name, err := range cases {
		if got := arr.Classify(err); got.Cause != mediasource.Unreachable || errors.Is(got, mediasource.ErrRescanOutcomeUnknown) {
			t.Errorf("server changed with %s: %v, want unreachable and no outcome-unknown", name, got.Cause)
		}
	}
	unknown := map[string]error{
		"over a server change":   fmt.Errorf("%w: %w", arrapi.ErrCommandOutcomeUnknown, &arrapi.ServerChangedError{From: v5}),
		"over no version header": fmt.Errorf("%w: %w", arrapi.ErrCommandOutcomeUnknown, &arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNoVersionHeader, Want: "Sonarr"}),
	}
	for name, err := range unknown {
		got := arr.Classify(err)
		if got.Cause != mediasource.Unreachable || !errors.Is(got, mediasource.ErrRescanOutcomeUnknown) || !errors.Is(got, arrapi.ErrCommandOutcomeUnknown) {
			t.Errorf("outcome unknown %s: %v, want unreachable naming ErrRescanOutcomeUnknown", name, got.Cause)
		}
	}
}

func TestClassifyUnsupportedAPI(t *testing.T) {
	notAPI := &arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNotAPI, Want: "Sonarr", Path: "/api/v3/system/status"}
	if got := arr.Classify(notAPI); got.Cause != mediasource.NotFound || !strings.Contains(got.Err.Error(), "check the URL's base path") {
		t.Errorf("not the API: %v %q, want not_found with arrapi's base-path sentence", got.Cause, got.Err)
	}
	for _, r := range []arrapi.UnsupportedReason{arrapi.ReasonV5Unreachable, arrapi.ReasonUnsupportedVersion, arrapi.ReasonUnknownGeneration, arrapi.ReasonWrongApp, arrapi.ReasonNoVersionHeader} {
		err := &arrapi.UnsupportedAPIError{Reason: r, Want: "Sonarr", App: "Radarr"}
		if got := arr.Classify(err); got.Cause != mediasource.Protocol || got.Err.Error() != err.Error() {
			t.Errorf("reason %d: %v %q, want protocol with arrapi's text", r, got.Cause, got.Err)
		}
	}
	t.Run("nothing answers at the base path", func(t *testing.T) {
		u := newUpstream(t)
		u.on("/api", http.NotFound)
		u.on("/api/v5/system/status", http.NotFound)
		u.on("/api/v3/system/status", http.NotFound)
		u.on("/api/v5/series", http.NotFound)
		_, err := sonarrClient(t, u.srv.URL).Series(t.Context())
		if got := classified(t, err); got.Cause != mediasource.NotFound {
			t.Errorf("every path 404: %v (%v), want not_found", got.Cause, got.Err)
		}
	})
}

func TestClassifyTwoHundredCodeProtocol(t *testing.T) {
	for _, code := range []int{204, 206} {
		if got := arr.Classify(&arrapi.StatusError{Code: code}); got.Cause != mediasource.Protocol {
			t.Errorf("a GET answered %d: %v, want protocol", code, got.Cause)
		}
	}
}

// arrEntry is ParseConfigs' view of an arr kind whose New is the adapters'
// shape: ReadValues, then the arrapi client.
var arrEntry = mediasource.AdapterKind{Type: "sonarr", Schema: arr.Schema("Sonarr"), New: func(cfg mediasource.Config, _ mediasource.Deps) (mediasource.Adapter, error) {
	v, err := arr.ReadValues(cfg.Values)
	if err != nil {
		return nil, err
	}
	if _, err := sonarr.New(v.URL, arrapi.APIKey(v.APIKey)); err != nil {
		return nil, err
	}
	return nil, errors.New("not built in this test")
}}

func TestNewErrorsNeedNoRedaction(t *testing.T) {
	const key = "test-key-in-query"
	entries := map[string]string{
		"a key in the url's query": "url: 'http://sonarr:8989/?apikey=" + key + "'",
		"no scheme":                "url: 'sonarr:8989/" + key + "'",
		"a public_url query":       "url: 'http://sonarr:8989'\n  public_url: 'https://s.example/?k=" + key + "'",
		"a public_url scheme":      "url: 'http://sonarr:8989'\n  public_url: 'ftp://" + key + "'",
	}
	for name, e := range entries {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte("- id: s\n  type: sonarr\n  api_key: "+key+"\n  "+e+"\n"), &doc); err != nil {
			t.Fatal(err)
		}
		_, errs := mediasource.ParseConfigs(doc.Content[0], []mediasource.AdapterKind{arrEntry})
		if len(errs) != 1 || errs[0].Path != "sources[0]" {
			t.Errorf("%s: %v, want one refusal at sources[0]", name, errs)
			continue
		}
		if msg := errs[0].Message; strings.Contains(msg, key) || strings.Contains(msg, "sonarr:8989") || strings.Contains(msg, "s.example") {
			t.Errorf("%s: refusal %q renders the URL or the key", name, msg)
		}
	}
}

func TestKeyRedactedInErrors(t *testing.T) {
	u := newUpstream(t)
	u.on("/api/v5/series", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"bad key `+testKey+`"}`)
	})
	_, err := sonarrClient(t, u.srv.URL).Series(t.Context())
	got := classified(t, err)
	if got.Cause != mediasource.Protocol || strings.Contains(got.Error(), testKey) {
		t.Errorf("a 400 echoing the key: %v %q, want protocol with the key redacted", got.Cause, got.Error())
	}
}
