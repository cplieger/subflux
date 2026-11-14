package clientaddr

import (
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cplieger/access"
	"github.com/cplieger/go/httpserver"
)

const proxyAddr = "10.0.0.2"

var listedProxy = []netip.Prefix{netip.MustParsePrefix(proxyAddr + "/32")}

func request(remote string, header ...string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = remote
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Add(header[i], header[i+1])
	}
	return req
}

// noForwarder is a host with no forwarders.
func noForwarder(netip.Addr) bool { return false }

func TestLocalIsTheDirectSocketPeer(t *testing.T) {
	r := New(listedProxy)
	cases := []struct {
		name  string
		req   *http.Request
		local bool
	}{
		{"loopback with no header", request("127.0.0.1:5000"), true},
		{"IPv6 loopback with no header", request("[::1]:5000"), true},
		{"private with no header", request("192.168.1.5:5000"), true},
		{"unique local with no header", request("[fd00::5]:5000"), true},
		{"unlisted proxy adding no header", request("172.17.0.1:5000"), true},
		{"IPv4-mapped private peer", request("[::ffff:192.168.1.5]:5000"), true},
		{"listed proxy forwarding a private client", request(proxyAddr+":443", "X-Forwarded-For", "192.168.1.9"), false},
		{"listed proxy forwarding a loopback client", request(proxyAddr+":443", "X-Forwarded-For", "127.0.0.1"), false},
		{"IPv4-mapped listed proxy forwarding a private client", request("[::ffff:"+proxyAddr+"]:443", "X-Forwarded-For", "192.168.1.9"), false},
		{"listed proxy forwarding a public client", request(proxyAddr+":443", "X-Forwarded-For", "203.0.113.7"), false},
		{"listed proxy forwarding nothing", request(proxyAddr + ":443"), false},
		{"listed proxy forwarding only listed hops", request(proxyAddr+":443", "X-Forwarded-For", proxyAddr), false},
		{"listed proxy forwarding no usable address", request(proxyAddr+":443", "X-Forwarded-For", "unknown"), false},
		{"unlisted peer sending X-Forwarded-For", request("192.168.1.5:5000", "X-Forwarded-For", "192.168.1.9"), false},
		{"unlisted peer sending X-Real-IP", request("192.168.1.5:5000", "X-Real-IP", "127.0.0.1"), false},
		{"unlisted peer sending Forwarded", request("192.168.1.5:5000", "Forwarded", "for=127.0.0.1"), false},
		{"public client", request("203.0.113.7:5000"), false},
		{"IPv4-mapped public peer", request("[::ffff:203.0.113.7]:5000"), false},
		{"carrier-grade NAT peer", request("100.64.0.1:5000"), false},
		{"IPv4 link-local peer", request("169.254.1.1:5000"), false},
		{"IPv6 link-local peer", request("[fe80::1]:5000"), false},
		{"peer that is not an address", request("@"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Local(r.Client(c.req), noForwarder); got != c.local {
				t.Errorf("Local(Client) = %v, want %v (client %+v)", got, c.local, r.Client(c.req))
			}
		})
	}
	loop := netip.MustParseAddr("127.0.0.1")
	t.Run("only ViaDirect is local", func(t *testing.T) {
		for _, c := range []access.ClientAddr{
			{Peer: loop},
			{Peer: loop, Via: access.ViaProxy, Forwarded: loop},
			{Peer: loop, Via: access.ViaProxyNoClient},
			{Peer: loop, Via: access.ViaUntrustedForwarding},
		} {
			if Local(c, noForwarder) {
				t.Errorf("Local(%+v) = true, want false", c)
			}
		}
		if !Local(access.ClientAddr{Peer: loop, Via: access.ViaDirect}, noForwarder) {
			t.Error("Local(a direct loopback peer) = false, want true")
		}
	})
	t.Run("a direct peer that is a forwarder is not local", func(t *testing.T) {
		gateway := netip.MustParseAddr("172.17.0.1")
		var asked []netip.Addr
		forwarder := func(a netip.Addr) bool {
			asked = append(asked, a)
			return a == gateway
		}
		if Local(r.Client(request("172.17.0.1:5000")), forwarder) {
			t.Error("Local(the host's bridge gateway) = true, want false")
		}
		if !Local(r.Client(request("192.168.1.5:5000")), forwarder) {
			t.Error("Local(a private peer that is no forwarder) = false, want true")
		}
		if want := []netip.Addr{gateway, netip.MustParseAddr("192.168.1.5")}; !slices.Equal(asked, want) {
			t.Errorf("forwarder asked %v, want %v", asked, want)
		}
	})
	t.Run("a failed forwarder read is not local", func(t *testing.T) {
		quiet := httpserver.WithForwarderLogger(slog.New(slog.DiscardHandler))
		noIfaces := httpserver.WithInterfaceAddrs(func() ([]httpserver.InterfaceAddrs, error) { return nil, nil })
		direct := access.ClientAddr{Peer: netip.MustParseAddr("192.168.1.5"), Via: access.ViaDirect}
		failed := httpserver.NewForwarderCheck(httpserver.WithRouteFiles(filepath.Join(t.TempDir(), "absent"), ""), noIfaces, quiet)
		if Local(direct, failed.IsForwarder) {
			t.Error("Local with an unreadable route table = true, want false")
		}
		routes := filepath.Join(t.TempDir(), "route")
		header := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n"
		if err := os.WriteFile(routes, []byte(header), 0o600); err != nil {
			t.Fatal(err)
		}
		read := httpserver.NewForwarderCheck(httpserver.WithRouteFiles(routes, filepath.Join(t.TempDir(), "absent6")), noIfaces, quiet)
		if !Local(direct, read.IsForwarder) {
			t.Error("Local with a readable host that forwards nothing = false, want true")
		}
	})
	t.Run("a nil forwarder makes no client local", func(t *testing.T) {
		if Local(access.ClientAddr{Peer: loop, Via: access.ViaDirect}, nil) {
			t.Error("Local(direct loopback, nil) = true, want false")
		}
	})
	t.Run("the zero resolver trusts no proxy", func(t *testing.T) {
		var zero Resolver
		c := zero.Client(request(proxyAddr+":443", "X-Forwarded-For", "192.168.1.9"))
		if c.Via != access.ViaUntrustedForwarding || Local(c, noForwarder) {
			t.Errorf("zero resolver: %+v, want an untrusted forwarding, non-local client", c)
		}
	})
}

func TestClientCarriesProvenance(t *testing.T) {
	r := New(append(slices.Clone(listedProxy), netip.MustParsePrefix("10.0.0.3/32")))
	proxy, peer := netip.MustParseAddr(proxyAddr), netip.MustParseAddr("192.168.1.5")
	cases := []struct {
		name string
		req  *http.Request
		want access.ClientAddr
	}{
		{
			name: "a listed proxy forwarding a client",
			req:  request(proxyAddr+":443", "X-Forwarded-For", "203.0.113.7"),
			want: access.ClientAddr{Peer: proxy, Forwarded: netip.MustParseAddr("203.0.113.7"), Via: access.ViaProxy},
		},
		{
			name: "a listed proxy forwarding nothing",
			req:  request(proxyAddr + ":443"),
			want: access.ClientAddr{Peer: proxy, Via: access.ViaProxyNoClient},
		},
		{
			name: "a listed proxy forwarding only listed hops",
			req:  request(proxyAddr+":443", "X-Forwarded-For", "10.0.0.3"),
			want: access.ClientAddr{Peer: proxy, Via: access.ViaProxyNoClient},
		},
		{
			name: "an unlisted peer forwarding",
			req:  request("192.168.1.5:5000", "X-Forwarded-For", "203.0.113.7"),
			want: access.ClientAddr{Peer: peer, Via: access.ViaUntrustedForwarding},
		},
		{
			name: "an unlisted peer sending nothing",
			req:  request("192.168.1.5:5000"),
			want: access.ClientAddr{Peer: peer, Via: access.ViaDirect},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := r.Client(c.req); got != c.want {
				t.Errorf("Client = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestNewCopiesTrusted(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix(proxyAddr + "/32")}
	r := New(trusted)
	trusted[0] = netip.MustParsePrefix("192.0.2.0/24")
	if r.Client(request(proxyAddr+":443", "X-Forwarded-For", "192.168.1.9")).Via != access.ViaProxy {
		t.Error("a change to the caller's slice reached the resolver")
	}
}

func TestHTTPSPredicate(t *testing.T) {
	r := New(listedProxy)
	tlsReq := request("203.0.113.7:5000")
	tlsReq.TLS = &tls.ConnectionState{}
	cases := []struct {
		name string
		req  *http.Request
		want bool
	}{
		{"TLS connection", tlsReq, true},
		{"plain connection", request("203.0.113.7:5000"), false},
		{"listed peer forwarding https", request(proxyAddr+":80", "X-Forwarded-Proto", "https"), true},
		{"listed peer forwarding HTTPS first", request(proxyAddr+":80", "X-Forwarded-Proto", " HTTPS , http"), true},
		{"listed peer forwarding http first", request(proxyAddr+":80", "X-Forwarded-Proto", "http, https"), false},
		{"listed peer's first header field counts", request(proxyAddr+":80", "X-Forwarded-Proto", "http", "X-Forwarded-Proto", "https"), false},
		{"listed peer forwarding nothing", request(proxyAddr + ":80"), false},
		{"unlisted peer forwarding https", request("192.168.1.5:80", "X-Forwarded-Proto", "https"), false},
		{"zoned IPv4-mapped peer inside the list forwarding https", request("[::ffff:"+proxyAddr+"%eth0]:80", "X-Forwarded-Proto", "https"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := https(c.req, r.resolve(c.req)); got != c.want {
				t.Errorf("https = %v, want %v", got, c.want)
			}
		})
	}
}
