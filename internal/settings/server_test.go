package settings

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFailedServerSectionFailsClosed(t *testing.T) {
	type want struct {
		paths   []string
		admit   []string
		refuse  []string
		level   slog.Level
		proxies int
	}
	cases := []struct {
		name, text string
		want       want
	}{
		{
			"a bad log_format keeps the host list", "server: {allowed_hosts: [nas.lan], log_format: yaml}\n",
			want{paths: []string{"server.log_format"}, admit: []string{"nas.lan"}, refuse: []string{"evil.example"}},
		},
		{
			"b only an invalid entry", "server: {allowed_hosts: [http://nas.lan]}\n",
			want{paths: []string{"server.allowed_hosts[0]"}, refuse: []string{"nas.lan", "evil.example", "localhost"}},
		},
		{
			"b a valid entry beside an invalid one", "server: {allowed_hosts: [nas.lan, http://x]}\n",
			want{paths: []string{"server.allowed_hosts[1]"}, admit: []string{"nas.lan"}, refuse: []string{"x", "evil.example"}},
		},
		{
			"c not a list", "server: {allowed_hosts: nas.lan}\n",
			want{paths: []string{"server.allowed_hosts"}, refuse: []string{"nas.lan", "evil.example"}},
		},
		{
			"c a mapping", "server: {allowed_hosts: {nas.lan: 1}}\n",
			want{paths: []string{"server.allowed_hosts"}, refuse: []string{"nas.lan"}},
		},
		{
			"d one bad proxy trusts none", "server: {trusted_proxies: [10.0.0.1, bogus]}\n",
			want{paths: []string{"server.trusted_proxies[1]"}, admit: []string{"evil.example"}},
		},
		{
			"e a valid level beside a bad format", "server: {log_level: debug, log_format: yaml}\n",
			want{paths: []string{"server.log_format"}, level: slog.LevelDebug, admit: []string{"evil.example"}},
		},
		{
			"e a bad level keeps the default", "server: {log_level: loud}\n",
			want{paths: []string{"server.log_level"}, admit: []string{"evil.example"}},
		},
		{
			"f empty string alone", "server: {allowed_hosts: [\"\"]}\n",
			want{paths: []string{"server.allowed_hosts[0]"}, refuse: []string{"nas.lan", "evil.example"}},
		},
		{
			"f blank beside a bad format", "server: {allowed_hosts: [\" \"], log_format: yaml}\n",
			want{paths: []string{"server.allowed_hosts[0]", "server.log_format"}, refuse: []string{"nas.lan", "evil.example"}},
		},
		{
			"f null element", "server: {allowed_hosts: [~]}\n",
			want{paths: []string{"server.allowed_hosts[0]"}, refuse: []string{"evil.example"}},
		},
		{
			"f mapping element", "server: {allowed_hosts: [{}]}\n",
			want{paths: []string{"server.allowed_hosts[0]"}, refuse: []string{"evil.example"}},
		},
		{
			"f blank element beside a valid one", "server: {allowed_hosts: [nas.lan, \"\"]}\n",
			want{paths: []string{"server.allowed_hosts[1]"}, admit: []string{"nas.lan"}, refuse: []string{"evil.example"}},
		},
		{
			"f blank proxy", "server: {trusted_proxies: [\"\"]}\n",
			want{paths: []string{"server.trusted_proxies[0]"}, admit: []string{"evil.example"}},
		},
		{
			"f null proxy", "server: {trusted_proxies: [10.0.0.1, ~]}\n",
			want{paths: []string{"server.trusted_proxies[1]"}, admit: []string{"evil.example"}},
		},
		{
			"f mapping proxy", "server: {trusted_proxies: [{}]}\n",
			want{paths: []string{"server.trusted_proxies[0]"}, admit: []string{"evil.example"}},
		},
		{
			"server held twice", "server: {allowed_hosts: []}\nserver: {log_level: debug}\n",
			want{paths: []string{"server"}, refuse: []string{"evil.example"}},
		},
		{
			"server not a mapping", "server: [nas.lan]\n",
			want{paths: []string{"server"}, refuse: []string{"nas.lan"}},
		},
		{
			"server an alias", "t: &s {allowed_hosts: [nas.lan]}\nserver: *s\n",
			want{paths: []string{"t", "server", "t"}, refuse: []string{"nas.lan"}},
		},
		{
			"allowed_hosts held twice", "server: {allowed_hosts: [nas.lan], allowed_hosts: [x]}\n",
			want{paths: []string{"server.allowed_hosts"}, refuse: []string{"nas.lan", "x"}},
		},
		{
			"unresolved host reference", "server: {allowed_hosts: [nas.lan, '${SUBFLUX_UNSET}']}\n",
			want{paths: []string{"server.allowed_hosts[1]"}, admit: []string{"nas.lan"}, refuse: []string{"evil.example"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := loadText(t, c.text+validLanguages)
			if got := failurePaths(t, s); !slices.Equal(got, c.want.paths) {
				t.Errorf("paths %v, want %v", got, c.want.paths)
			}
			srv, ok := s.Server()
			if ok {
				t.Error("Server() answered true for a failed section")
			}
			for _, h := range c.want.admit {
				if !admits(srv, h) {
					t.Errorf("Host %s refused", h)
				}
			}
			for _, h := range c.want.refuse {
				if admits(srv, h) {
					t.Errorf("Host %s admitted", h)
				}
			}
			if srv.LogLevel() != c.want.level || srv.LogFormat() != "json" {
				t.Errorf("log %v %s, want %v json", srv.LogLevel(), srv.LogFormat(), c.want.level)
			}
			if len(srv.TrustedProxies()) != 0 {
				t.Errorf("trusted proxies %v, want none", srv.TrustedProxies())
			}
			if loopback(srv) {
				t.Error("the fail-closed value exempts loopback")
			}
		})
	}
	t.Run("e a valid text format is kept", func(t *testing.T) {
		srv, _ := loadText(t, "server: {log_format: text, log_level: loud}\n"+validLanguages).Server()
		if srv.LogFormat() != "text" {
			t.Errorf("format %s, want text", srv.LogFormat())
		}
	})
}

func TestRootFailureDeniesEveryHost(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.yaml")
	if err := os.WriteFile(big, []byte("server: {allowed_hosts: [nas.lan]}\n#"+strings.Repeat("x", maxFileBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	roots := map[string]func(t *testing.T) Snapshot{
		"second document":  func(t *testing.T) Snapshot { return loadText(t, "server: {allowed_hosts: [nas.lan]}\n---\nx: 1\n") },
		"over 1 MiB":       func(*testing.T) Snapshot { return Load(big, Registries{}) },
		"non-mapping root": func(t *testing.T) Snapshot { return loadText(t, "[server]\n") },
		"directory":        func(*testing.T) Snapshot { return Load(dir, Registries{}) },
	}
	for name, load := range roots {
		t.Run(name, func(t *testing.T) {
			srv, ok := load(t).Server()
			if ok || !srv.Hosts().Active() || admits(srv, "nas.lan") || admits(srv, "evil.example") || loopback(srv) {
				t.Errorf("Server() = %v, active %v; want false, denying every Host", ok, srv.Hosts().Active())
			}
			if len(srv.TrustedProxies()) != 0 || srv.LogLevel() != slog.LevelInfo || srv.LogFormat() != "json" {
				t.Errorf("server %+v, want defaults and no proxy", srv)
			}
		})
	}
	t.Run("a shape refusal elsewhere keeps the server section", func(t *testing.T) {
		s := loadText(t, "server: {allowed_hosts: [nas.lan]}\nx: &a 1\n"+validLanguages)
		srv, ok := s.Server()
		if s.Mode() != Failed || !ok || !admits(srv, "nas.lan") || admits(srv, "evil.example") {
			t.Errorf("mode %v, Server() %v", s.Mode(), ok)
		}
	})
}

func TestServerSection(t *testing.T) {
	t.Run("proxies as CIDR and bare address", func(t *testing.T) {
		s := loadText(t, "server: {trusted_proxies: [10.0.0.0/8, 192.168.1.5, '::1', 172.16.3.4/12]}\n"+validLanguages)
		a, ok := s.Active()
		want := []netip.Prefix{
			netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.168.1.5/32"),
			netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("172.16.0.0/12"),
		}
		if !ok || !slices.Equal(a.Server.TrustedProxies(), want) {
			t.Errorf("proxies %v, want %v", a.Server.TrustedProxies(), want)
		}
	})
	t.Run("pasted URL", func(t *testing.T) {
		errs, _ := loadText(t, "server: {allowed_hosts: [http://nas.lan]}\n"+validLanguages).Failure()
		if len(errs) != 1 || errs[0].Path != "server.allowed_hosts[0]" || !strings.Contains(errs[0].Message, "host name") {
			t.Errorf("Failure() = %v", errs)
		}
	})
	t.Run("allowed hosts canonical once each in file order", func(t *testing.T) {
		a, _ := loadText(t, "server: {allowed_hosts: [Nas.LAN, 192.168.1.5, 'nas.lan:8374']}\n"+validLanguages).Active()
		if got := a.Server.AllowedHosts(); !slices.Equal(got, []string{"nas.lan", "192.168.1.5"}) {
			t.Errorf("AllowedHosts() = %v", got)
		}
		if !loopback(a.Server) {
			t.Error("a parsed list does not exempt loopback")
		}
	})
	t.Run("accepted entries of a failed list", func(t *testing.T) {
		srv, _ := loadText(t, "server: {allowed_hosts: [nas.lan, http://x]}\n"+validLanguages).Server()
		if got := srv.AllowedHosts(); !slices.Equal(got, []string{"nas.lan"}) {
			t.Errorf("AllowedHosts() = %v, want [nas.lan]", got)
		}
	})
	for name, text := range map[string]string{"absent": "server: {}\n", "null": "server: {allowed_hosts: ~}\n", "empty": "server: {allowed_hosts: []}\n", "no section": ""} {
		t.Run(name+" list allows every host", func(t *testing.T) {
			a, ok := loadText(t, text+validLanguages).Active()
			if !ok || a.Server.AllowedHosts() != nil || a.Server.Hosts().Active() || !admits(a.Server, "evil.example") {
				t.Errorf("Active %v, AllowedHosts %v", ok, a.Server.AllowedHosts())
			}
		})
	}
	t.Run("defaults and levels", func(t *testing.T) {
		a, _ := loadText(t, "server: {}\n"+validLanguages).Active()
		if a.Server.LogLevel() != slog.LevelInfo || a.Server.LogFormat() != "json" {
			t.Errorf("defaults %v %s", a.Server.LogLevel(), a.Server.LogFormat())
		}
		for text, want := range map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError} {
			a, _ := loadText(t, "server: {log_level: "+text+", log_format: text}\n").Active()
			if a.Server.LogLevel() != want || a.Server.LogFormat() != "text" {
				t.Errorf("%s: %v %s", text, a.Server.LogLevel(), a.Server.LogFormat())
			}
		}
	})
	t.Run("the zero Snapshot denies every host", func(t *testing.T) {
		var snap Snapshot
		srv, ok := snap.Server()
		if _, active := snap.Active(); ok || active || snap.Mode() != 0 || !srv.Hosts().Active() || admits(srv, "nas.lan") {
			t.Error("the zero Snapshot is not fail-closed")
		}
		if errs, failed := snap.Failure(); failed || errs != nil {
			t.Errorf("Failure() = %v, %v", errs, failed)
		}
	})
	t.Run("the zero Server allows every host", func(t *testing.T) {
		var srv Server
		if srv.Hosts().Active() || srv.LogLevel() != slog.LevelInfo || srv.LogFormat() != "json" || srv.TrustedProxies() != nil {
			t.Error("the zero Server is not the documented default")
		}
	})
}

// loopback reports whether s's policy admits a loopback peer naming
// localhost.
func loopback(s Server) bool {
	r := httptest.NewRequest(http.MethodGet, "http://localhost:8374/", nil)
	r.RemoteAddr = "127.0.0.1:50000"
	return s.Hosts().Allows(r) && s.Hosts().Active()
}
