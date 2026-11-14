package clientaddr

import (
	"net/netip"
	"testing"

	"github.com/cplieger/access"
)

// FuzzLocal_untrustedHeadersNeverLocalize: whatever forwarding headers a
// public peer outside trusted_proxies sends, its client is the peer and is
// never local.
func FuzzLocal_untrustedHeadersNeverLocalize(f *testing.F) {
	public4 := []byte{203, 0, 113, 7}
	public6 := netip.MustParseAddr("2001:db8::7").AsSlice()
	f.Add(public4, "192.168.1.9", "127.0.0.1", "for=127.0.0.1", "https")
	f.Add(public4, "127.0.0.1, 10.0.0.2", "", "", "")
	f.Add(public6, "::1", "fd00::1", `for="[::1]:80"`, "")
	f.Add(public4, "[::ffff:192.168.1.9]", "unknown", "for=_hidden;proto=https", "https")
	f.Fuzz(func(t *testing.T, peerBytes []byte, xff, realIP, forwarded, proto string) {
		peer, ok := netip.AddrFromSlice(peerBytes)
		if !ok || !isPublic(peer) {
			t.Skip()
		}
		peer = peer.Unmap()
		r := New([]netip.Prefix{
			netip.MustParsePrefix("10.0.0.0/8"),
			netip.MustParsePrefix("192.168.0.0/16"),
			netip.MustParsePrefix("fd00::/8"),
		})
		req := request(netip.AddrPortFrom(peer, 443).String(),
			"X-Forwarded-For", xff, "X-Real-IP", realIP, "Forwarded", forwarded, "X-Forwarded-Proto", proto)
		c := r.Client(req)
		if c.IP() != peer || c.Via == access.ViaProxy {
			t.Fatalf("Client = %+v, want the peer %v unproxied", c, peer)
		}
		if Local(c, noForwarder) {
			t.Fatalf("public peer %v with headers %q %q %q is local", peer, xff, realIP, forwarded)
		}
	})
}
