package clientaddr

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/access"
)

func TestMiddlewareResolvesOncePerRequest(t *testing.T) {
	calls := 0
	current := New(nil)
	getter := func() Resolver {
		calls++
		return current
	}
	var logged, handled access.ClientAddr
	var loggedOK, handledOK bool
	inner := http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		handled, handledOK = From(req.Context())
	})
	logger := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			logged, loggedOK = From(req.Context())
			next.ServeHTTP(w, req)
		})
	}
	h := Middleware(getter, NewDetector())(logger(inner))
	serve := func(t *testing.T) {
		t.Helper()
		calls = 0
		h.ServeHTTP(httptest.NewRecorder(), request(proxyAddr+":443", "X-Forwarded-For", "192.168.1.9"))
		if calls != 1 {
			t.Errorf("resolver getter ran %d times, want 1", calls)
		}
		if !loggedOK || !handledOK || logged != handled {
			t.Errorf("logger read %+v (%v), handler read %+v (%v), want one address", logged, loggedOK, handled, handledOK)
		}
	}

	t.Run("an untrusted forwarder is not local", func(t *testing.T) {
		serve(t)
		if Local(handled, noForwarder) || handled.Via != access.ViaUntrustedForwarding {
			t.Errorf("From = %+v, want an untrusted forwarder that is not local", handled)
		}
	})
	t.Run("a trusted_proxies change reaches the next request", func(t *testing.T) {
		current = New(listedProxy)
		serve(t)
		if Local(handled, noForwarder) || handled.Via != access.ViaProxy || handled.IP().String() != "192.168.1.9" {
			t.Errorf("From = %+v, want the forwarded private client, not local", handled)
		}
	})
	t.Run("outside the middleware From answers false", func(t *testing.T) {
		if a, ok := From(t.Context()); ok {
			t.Errorf("From(t.Context()) = %+v, true; want false", a)
		}
	})
}
