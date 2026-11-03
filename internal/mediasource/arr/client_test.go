package arr_test

import (
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/mediasource"
)

// TestRedirectKeepsKeyOnOrigin pins the exact-origin guarantee subflux's
// adapters get from arrapi v3's own redirect policy, for reads and detection
// probes alike, and Classify's answer to each refusal.
func TestRedirectKeepsKeyOnOrigin(t *testing.T) {
	other := newUpstream(t)
	otherPort := other.srv.Listener.Addr().String()[strings.LastIndex(other.srv.Listener.Addr().String(), ":"):]
	targets := map[string]struct{ location, origin string }{
		"same host, another port": {other.srv.URL + "/api/v5/series", other.srv.URL},
		"another host":            {"http://localhost" + otherPort + "/api/v5/series", "http://localhost" + otherPort},
	}
	for name, tc := range targets {
		t.Run(name, func(t *testing.T) {
			origin := newUpstream(t)
			origin.on("/api/v5/series", redirectTo(tc.location))
			_, err := sonarrClient(t, origin.srv.URL).Series(t.Context())
			se := classified(t, err)
			if se.Cause != mediasource.Protocol || se.Err.Error() != "redirects to "+tc.origin+"; set url to that address" {
				t.Errorf("Series = %v (%v), want protocol naming %s", se.Err, se.Cause, tc.origin)
			}
			for _, r := range other.keys() {
				if r.key != "" {
					t.Errorf("the key reached %s at %s", tc.origin, r.path)
				}
			}
		})
	}
	t.Run("http to https", func(t *testing.T) {
		origin := newUpstream(t)
		https := "https://" + origin.srv.Listener.Addr().String()
		origin.on("/api/v5/series", redirectTo(https+"/api/v5/series"))
		_, err := sonarrClient(t, origin.srv.URL).Series(t.Context())
		if se := classified(t, err); se.Cause != mediasource.Protocol || !strings.Contains(se.Err.Error(), "redirects to "+https+"; set url") {
			t.Errorf("Series = %v (%v), want protocol naming %s", se.Err, se.Cause, https)
		}
	})
	t.Run("a detection probe's redirect", func(t *testing.T) {
		origin := newUpstream(t)
		origin.on("/api", redirectTo(other.srv.URL+"/api"))
		origin.on("/api/v5/system/status", redirectTo(other.srv.URL+"/api/v5/system/status"))
		_, err := sonarrClient(t, origin.srv.URL).Info(t.Context())
		if se := classified(t, err); se.Cause != mediasource.Protocol || !strings.Contains(se.Err.Error(), "redirects to "+other.srv.URL) {
			t.Errorf("Info = %v (%v), want protocol naming the other origin", se.Err, se.Cause)
		}
		for _, r := range other.keys() {
			if r.key != "" {
				t.Errorf("a probe's key reached the other origin at %s", r.path)
			}
		}
	})
	t.Run("same origin is followed with the key", func(t *testing.T) {
		origin := newUpstream(t)
		origin.on("/api/v5/series", redirectTo(origin.srv.URL+"/moved"))
		if _, err := sonarrClient(t, origin.srv.URL).Series(t.Context()); err != nil {
			t.Fatalf("Series = %v, want the same-origin hop followed", err)
		}
		got := origin.keys()
		if last := got[len(got)-1]; last.path != "/moved" || last.key != testKey {
			t.Errorf("last request %+v, want /moved with the key", last)
		}
	})
}
