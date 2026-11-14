package clientaddr

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/go/slogx/capture"
)

const (
	publicAddr   = "203.0.113.7"
	exposureWarn = "clientaddr: a public client reached subflux; see docs/hardening.md"
	peerWarn     = "clientaddr: ignored forwarding headers from a peer not in trusted_proxies"
)

// observeAll serves each request through Middleware over r and d.
func observeAll(r Resolver, d *Detector, reqs ...*http.Request) {
	h := Middleware(func() Resolver { return r }, d)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, req := range reqs {
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func reasonsNow(d *Detector) []Reason {
	e, _ := d.State(time.Now())
	return e.Reasons
}

func TestExposureDetector(t *testing.T) {
	tlsPublic := request(publicAddr + ":5000")
	tlsPublic.TLS = &tls.ConnectionState{}
	cases := []struct {
		name    string
		trusted Resolver
		req     *http.Request
		want    []Reason
	}{
		{"public client over plain HTTP", New(listedProxy), request(publicAddr + ":5000"), []Reason{PlainHTTP}},
		{"public client over TLS with no trusted proxies", New(nil), tlsPublic, []Reason{NoTrustedProxies}},
		{"public client over plain HTTP with no trusted proxies", New(nil), request(publicAddr + ":5000"), []Reason{PlainHTTP, NoTrustedProxies}},
		{"listed TLS proxy forwarding a public client", New(listedProxy), request(proxyAddr+":80", "X-Forwarded-For", publicAddr, "X-Forwarded-Proto", "https"), nil},
		{"listed plain proxy forwarding a public client", New(listedProxy), request(proxyAddr+":80", "X-Forwarded-For", publicAddr), []Reason{PlainHTTP}},
		{"unlisted TLS proxy forwarding a public client", New(listedProxy), request("172.17.0.1:80", "X-Forwarded-For", publicAddr, "X-Forwarded-Proto", "https"), []Reason{NoTrustedProxies}},
		{"unlisted TLS proxy with no trusted proxies", New(nil), request("172.17.0.1:80", "X-Forwarded-For", publicAddr, "X-Forwarded-Proto", "https"), []Reason{NoTrustedProxies}},
		{"unlisted plain proxy forwarding a public client", New(listedProxy), request("172.17.0.1:80", "X-Real-IP", publicAddr), []Reason{PlainHTTP}},
		{"zoned IPv4-mapped peer inside the list is not a listed TLS proxy", New(listedProxy), request("[::ffff:"+proxyAddr+"%eth0]:80", "X-Forwarded-For", publicAddr, "X-Forwarded-Proto", "https"), []Reason{NoTrustedProxies}},
		{"private client with no trusted proxies", New(nil), request("192.168.1.5:5000"), nil},
		{"listed proxy forwarding a private client", New(listedProxy), request(proxyAddr+":80", "X-Forwarded-For", "192.168.1.9"), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			capture.Default(t)
			synctest.Test(t, func(t *testing.T) {
				d := NewDetector()
				observeAll(c.trusted, d, c.req)
				if got := reasonsNow(d); !slices.Equal(got, c.want) {
					t.Errorf("reasons = %v, want %v", got, c.want)
				}
			})
		})
	}

	t.Run("an observation answers for 24 hours and not after", func(t *testing.T) {
		capture.Default(t)
		synctest.Test(t, func(t *testing.T) {
			d := NewDetector()
			first := time.Now()
			observeAll(New(listedProxy), d, request(publicAddr+":5000"))
			time.Sleep(time.Hour)
			second := time.Now()
			observeAll(New(listedProxy), d, request(publicAddr+":5000"))
			e, ok := d.State(time.Now())
			if !ok || !e.Since.Equal(first) || !e.LastSeen.Equal(second) {
				t.Errorf("State = %+v, %v; want Since %v, LastSeen %v", e, ok, first, second)
			}
			if _, ok := d.State(second.Add(exposureWindow - time.Nanosecond)); !ok {
				t.Error("State just inside 24 hours of the last request = false, want true")
			}
			if e, ok := d.State(second.Add(exposureWindow)); ok {
				t.Errorf("State 24 hours after the last request = %+v, true; want false", e)
			}
			time.Sleep(exposureWindow)
			third := time.Now()
			observeAll(New(listedProxy), d, request(publicAddr+":5000"))
			if e, _ := d.State(time.Now()); !e.Since.Equal(third) {
				t.Errorf("Since after a 24-hour gap = %v, want the new request's %v", e.Since, third)
			}
		})
	})

	t.Run("reasons from different episodes report the oldest start", func(t *testing.T) {
		capture.Default(t)
		synctest.Test(t, func(t *testing.T) {
			d := NewDetector()
			first := time.Now()
			observeAll(New(listedProxy), d, request(publicAddr+":5000"))
			time.Sleep(time.Hour)
			observeAll(New(nil), d, tlsPublic)
			e, _ := d.State(time.Now())
			if !slices.Equal(e.Reasons, []Reason{PlainHTTP, NoTrustedProxies}) || !e.Since.Equal(first) || !e.LastSeen.Equal(time.Now()) {
				t.Errorf("State = %+v; want both reasons since %v, last seen now", e, first)
			}
		})
	})

	t.Run("Reset clears", func(t *testing.T) {
		capture.Default(t)
		synctest.Test(t, func(t *testing.T) {
			d := NewDetector()
			observeAll(New(nil), d, request(publicAddr+":5000"))
			d.Reset()
			if e, ok := d.State(time.Now()); ok {
				t.Errorf("State after Reset = %+v, true; want false", e)
			}
		})
	})

	t.Run("WARN at most hourly per reason", func(t *testing.T) {
		rec := capture.Default(t)
		synctest.Test(t, func(t *testing.T) {
			d := NewDetector()
			observeAll(New(listedProxy), d, request(publicAddr+":5000"), request(publicAddr+":5001"))
			observeAll(New(nil), d, tlsPublic)
			if got := rec.CountLevel(slog.LevelWarn, exposureWarn); got != 2 {
				t.Errorf("WARNs after two plain requests and one with no trusted proxies = %d, want 2", got)
			}
			if !rec.HasAttr(exposureWarn, "reason", "plain_http") || !rec.HasAttr(exposureWarn, "reason", "no_trusted_proxies") {
				t.Errorf("WARNs %v do not name both reasons", rec.Records())
			}
			time.Sleep(warnInterval - time.Nanosecond)
			observeAll(New(listedProxy), d, request(publicAddr+":5000"))
			if got := rec.CountLevel(slog.LevelWarn, exposureWarn); got != 2 {
				t.Errorf("WARNs within the hour = %d, want 2", got)
			}
			time.Sleep(time.Nanosecond)
			observeAll(New(listedProxy), d, request(publicAddr+":5000"))
			if got := rec.CountLevel(slog.LevelWarn, exposureWarn); got != 3 {
				t.Errorf("WARNs after the hour = %d, want 3", got)
			}
		})
	})

	t.Run("WARN at most hourly per unlisted peer", func(t *testing.T) {
		rec := capture.Default(t)
		synctest.Test(t, func(t *testing.T) {
			d := NewDetector()
			fromA := func() *http.Request { return request("192.168.1.5:80", "X-Forwarded-For", "192.168.1.9") }
			observeAll(New(listedProxy), d, fromA(), fromA(), request("192.168.1.6:80", "Forwarded", "for=192.168.1.9"))
			if got := rec.CountLevel(slog.LevelWarn, peerWarn); got != 2 {
				t.Errorf("peer WARNs for two peers = %d, want 2", got)
			}
			if !rec.HasAttr(peerWarn, "peer", "192.168.1.5") || !rec.HasAttr(peerWarn, "peer", "192.168.1.6") {
				t.Errorf("peer WARNs %v do not name both peers", rec.Records())
			}
			if _, ok := d.State(time.Now()); ok {
				t.Error("private forwarders recorded exposure")
			}
			time.Sleep(warnInterval)
			observeAll(New(listedProxy), d, fromA())
			if got := rec.CountLevel(slog.LevelWarn, peerWarn); got != 3 {
				t.Errorf("peer WARNs after the hour = %d, want 3", got)
			}
		})
	})

	t.Run("remembered peers stay bounded", func(t *testing.T) {
		rec := capture.Default(t)
		synctest.Test(t, func(t *testing.T) {
			d := NewDetector()
			peer := func(i int) *http.Request {
				return request(fmt.Sprintf("[fd00::%x]:80", i+1), "X-Forwarded-For", "192.168.1.9")
			}
			for i := range maxWarnedPeers + 1 {
				observeAll(New(nil), d, peer(i))
			}
			if got := rec.CountLevel(slog.LevelWarn, peerWarn); got != maxWarnedPeers {
				t.Errorf("peer WARNs for %d peers = %d, want the bound %d", maxWarnedPeers+1, got, maxWarnedPeers)
			}
			if len(d.peers) != maxWarnedPeers {
				t.Errorf("remembered peers = %d, want %d", len(d.peers), maxWarnedPeers)
			}
			time.Sleep(warnInterval)
			observeAll(New(nil), d, peer(maxWarnedPeers))
			if got := rec.CountLevel(slog.LevelWarn, peerWarn); got != maxWarnedPeers+1 {
				t.Errorf("peer WARNs once the remembered peers expired = %d, want %d", got, maxWarnedPeers+1)
			}
			if len(d.peers) != 1 {
				t.Errorf("remembered peers after expiry = %d, want 1", len(d.peers))
			}
		})
	})
}

func TestReasonText(t *testing.T) {
	for _, c := range []struct {
		r    Reason
		text string
	}{{PlainHTTP, "plain_http"}, {NoTrustedProxies, "no_trusted_proxies"}} {
		t.Run(c.text, func(t *testing.T) {
			b, err := json.Marshal(c.r)
			if err != nil || string(b) != `"`+c.text+`"` {
				t.Errorf("Marshal(%d) = %s, %v; want %q", c.r, b, err, c.text)
			}
			var got Reason
			if err := json.Unmarshal(b, &got); err != nil || got != c.r {
				t.Errorf("Unmarshal(%s) = %d, %v; want %d", b, got, err, c.r)
			}
		})
	}
	t.Run("an unknown value is refused", func(t *testing.T) {
		if b, err := json.Marshal(Reason(0)); err == nil {
			t.Errorf("Marshal(0) = %s, want an error", b)
		}
		if b, err := json.Marshal(Reason(3)); err == nil {
			t.Errorf("Marshal(3) = %s, want an error", b)
		}
		for _, text := range []string{`"PLAIN_HTTP"`, `"plain-http"`, `""`, `"exposed"`} {
			var r Reason
			if err := json.Unmarshal([]byte(text), &r); err == nil {
				t.Errorf("Unmarshal(%s) = %d, want an error", text, r)
			}
		}
	})
}
