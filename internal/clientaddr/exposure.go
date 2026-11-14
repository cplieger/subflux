package clientaddr

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/cplieger/go/httpserver"
)

// Reason is why a public client's request counts as exposure.
type Reason uint8

const (
	// PlainHTTP is a public client reaching subflux without HTTPS.
	PlainHTTP Reason = iota + 1
	// NoTrustedProxies is a public client reaching subflux with no trusted
	// proxy listed, or through a TLS proxy that is not listed.
	NoTrustedProxies
)

const reasonCount = int(NoTrustedProxies)

// MarshalText answers plain_http or no_trusted_proxies and refuses any other
// value, so the wire enum stays closed.
func (r Reason) MarshalText() ([]byte, error) {
	switch r {
	case PlainHTTP:
		return []byte("plain_http"), nil
	case NoTrustedProxies:
		return []byte("no_trusted_proxies"), nil
	}
	return nil, fmt.Errorf("clientaddr: unknown exposure reason %d", uint8(r))
}

// UnmarshalText reads MarshalText's spellings and refuses every other text.
func (r *Reason) UnmarshalText(b []byte) error {
	switch string(b) {
	case "plain_http":
		*r = PlainHTTP
	case "no_trusted_proxies":
		*r = NoTrustedProxies
	default:
		return fmt.Errorf("clientaddr: unknown exposure reason %q", b)
	}
	return nil
}

// Exposure is the exposure seen in the window State reads: its reasons in
// ascending order, when the oldest of their episodes began and when a request
// last counted.
type Exposure struct {
	Since    time.Time
	LastSeen time.Time
	Reasons  []Reason
}

const (
	// exposureWindow is how long one observation keeps its reason shown.
	exposureWindow = 24 * time.Hour
	// warnInterval bounds the WARN lines to one per reason and one per
	// unlisted peer each hour.
	warnInterval = time.Hour
	// maxWarnedPeers bounds the peers remembered for warnInterval, so a flood
	// of distinct peers sending forwarding headers cannot grow memory; past
	// it a new peer's WARN is dropped until the remembered ones expire.
	maxWarnedPeers = 1024
)

type episode struct{ since, last time.Time }

// Detector records public clients' requests (DECISIONS 18); its zero value is
// not usable, NewDetector builds one.
type Detector struct {
	peers  map[netip.Addr]time.Time
	seen   [reasonCount]episode
	warned [reasonCount]time.Time
	mu     sync.Mutex
}

// NewDetector returns a Detector that has seen nothing.
func NewDetector() *Detector {
	return &Detector{peers: make(map[netip.Addr]time.Time)}
}

// State answers the exposure seen in the 24 hours before now; false: none.
func (d *Detector) State(now time.Time) (Exposure, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var e Exposure
	for i, ep := range d.seen {
		if ep.last.IsZero() || now.Sub(ep.last) >= exposureWindow {
			continue
		}
		e.Reasons = append(e.Reasons, Reason(i+1))
		if e.Since.IsZero() || ep.since.Before(e.Since) {
			e.Since = ep.since
		}
		if ep.last.After(e.LastSeen) {
			e.LastSeen = ep.last
		}
	}
	return e, len(e.Reasons) > 0
}

// Reset forgets every observation and WARN; a Save changing the server
// section calls it.
func (d *Detector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen = [reasonCount]episode{}
	d.warned = [reasonCount]time.Time{}
	clear(d.peers)
}

// observe records req, resolved by r as res. A public client is the resolved
// address, or an address in a forwarding header the walk ignored.
func (d *Detector) observe(r Resolver, req *http.Request, res httpserver.ClientResolution) {
	public := isPublic(res.IP) || res.IgnoredPublic
	if !public && !res.UntrustedForwarding {
		return
	}
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if res.UntrustedForwarding {
		d.warnPeer(now, res.Peer)
	}
	if !public {
		return
	}
	// An unlisted peer's X-Forwarded-Proto: https says a TLS proxy sits in
	// front that subflux does not trust, which is not plain HTTP.
	if !res.PeerTrusted && forwardedHTTPS(req) {
		d.record(now, NoTrustedProxies, res.IP)
		return
	}
	if !https(req, res) {
		d.record(now, PlainHTTP, res.IP)
	}
	if len(r.trusted) == 0 {
		d.record(now, NoTrustedProxies, res.IP)
	}
}

func (d *Detector) record(now time.Time, r Reason, client netip.Addr) {
	ep := &d.seen[r-1]
	if ep.last.IsZero() || now.Sub(ep.last) >= exposureWindow {
		ep.since = now
	}
	ep.last = now
	if w := d.warned[r-1]; !w.IsZero() && now.Sub(w) < warnInterval {
		return
	}
	d.warned[r-1] = now
	reason, _ := r.MarshalText()
	slog.Warn("clientaddr: a public client reached subflux; see docs/hardening.md",
		"reason", string(reason), "client", client.String())
}

func (d *Detector) warnPeer(now time.Time, peer netip.Addr) {
	if w, ok := d.peers[peer]; ok && now.Sub(w) < warnInterval {
		return
	}
	if len(d.peers) >= maxWarnedPeers {
		for p, w := range d.peers {
			if now.Sub(w) >= warnInterval {
				delete(d.peers, p)
			}
		}
		if len(d.peers) >= maxWarnedPeers {
			return
		}
	}
	d.peers[peer] = now
	slog.Warn("clientaddr: ignored forwarding headers from a peer not in trusted_proxies",
		"peer", peer.String())
}

func isPublic(ip netip.Addr) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate()
}
