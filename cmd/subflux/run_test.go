package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/app"
	"github.com/cplieger/subflux/internal/testchild"
	"github.com/cplieger/subflux/internal/testrig"
)

func swapRunApp(t *testing.T, f func(context.Context, app.Config) error) {
	t.Helper()
	prev := runApp
	runApp = f
	t.Cleanup(func() { runApp = prev })
}

func TestMainExitsOneOnRunError(t *testing.T) {
	var got app.Config
	swapRunApp(t, func(_ context.Context, cfg app.Config) error {
		got = cfg
		return errors.New("listen tcp :8374: bind: address already in use\nforged line")
	})
	var stdout, stderr strings.Builder
	if code := run(nil, nil, &stdout, &stderr); code != 1 {
		t.Errorf("run = %d, want 1", code)
	}
	if out := stderr.String(); !strings.Contains(out, "address already in use") || strings.Count(out, "\n") != 1 {
		t.Errorf("stderr %q; want the error on one line", out)
	}
	if got.Paths != app.DefaultPaths {
		t.Errorf("booted with %+v, want the default paths", got.Paths)
	}
	for value, want := range map[string]string{"": ":8375", " 127.0.0.1:9375\t": "127.0.0.1:9375"} {
		t.Setenv("OPS_LISTEN_ADDR", value)
		_ = run(nil, nil, &stdout, &stderr)
		if got.OpsAddr != want {
			t.Errorf("OPS_LISTEN_ADDR %q booted with OpsAddr %q, want %q", value, got.OpsAddr, want)
		}
	}
	stderr.Reset()

	swapRunApp(t, func(context.Context, app.Config) error { return nil })
	if code := run(nil, nil, &stdout, &stderr); code != 0 {
		t.Errorf("run after a clean Run = %d, want 0", code)
	}
	for _, args := range [][]string{{"bogus"}, {"health", "extra"}, {"--help", "extra"}} {
		stdout.Reset()
		stderr.Reset()
		if code := run(args, nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage") || stdout.Len() != 0 {
			t.Errorf("run(%q) = %d, stdout %q, stderr %q; want 2 with usage on stderr", args, code, stdout.String(), stderr.String())
		}
	}
	for _, arg := range []string{"-h", "--help", "help"} {
		stdout.Reset()
		stderr.Reset()
		if code := run([]string{arg}, nil, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "usage") || stderr.Len() != 0 {
			t.Errorf("run(%q) = %d, stdout %q, stderr %q; want 0 with usage on stdout", arg, code, stdout.String(), stderr.String())
		}
	}
}

// TestHealthHelper is the health subcommand's process when the parent test
// sets SUBFLUX_HEALTH_HELPER to a data directory, which it points every path
// at, and OPS_LISTEN_ADDR to the address to probe.
func TestHealthHelper(t *testing.T) {
	dir := os.Getenv("SUBFLUX_HEALTH_HELPER")
	if dir == "" {
		t.Skip("run as TestHealthProbe's child")
	}
	paths = app.Paths{Config: filepath.Join(dir, "config.yaml"), Store: filepath.Join(dir, "subflux.bolt"), SocketDir: dir, Socket: filepath.Join(dir, "admin.sock")}
	os.Args = []string{"subflux", "health"}
	main()
}

func TestHealthProbe(t *testing.T) {
	probe := func(t *testing.T, addr string) (int, string) {
		t.Helper()
		dir := t.TempDir()
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHealthHelper$")
		cmd.Env = append(os.Environ(), "SUBFLUX_HEALTH_HELPER="+dir, "OPS_LISTEN_ADDR="+addr, "GORACE="+testchild.GORACE())
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatal(err)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Errorf("health touched the data directory: %v, %v", entries, err)
		}
		return cmd.ProcessState.ExitCode(), stderr.String()
	}
	t.Run("serving", func(t *testing.T) {
		r := testrig.Start(t, testrig.Options{})
		if code, stderr := probe(t, r.OpsAddr); code != 0 || stderr != "" {
			t.Errorf("health = %d, %q; want 0 and no output", code, stderr)
		}
	})
	t.Run("settings_failed", func(t *testing.T) {
		r := testrig.Start(t, testrig.Options{Config: []byte("server:\n  log_level: info\n")})
		if code, stderr := probe(t, r.OpsAddr); code != 1 || !strings.Contains(stderr, "503") {
			t.Errorf("health = %d, %q; want 1 naming the 503", code, stderr)
		}
	})
	t.Run("draining", func(t *testing.T) {
		r, release := testrig.StartDraining(t)
		defer release()
		if code, stderr := probe(t, r.OpsAddr); code != 1 || !strings.Contains(stderr, "503") {
			t.Errorf("health while draining = %d, %q; want 1 naming the 503", code, stderr)
		}
	})
	t.Run("closed_listener", func(t *testing.T) {
		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		closed := ln.Addr().String()
		_ = ln.Close()
		if code, stderr := probe(t, closed); code != 1 || stderr == "" {
			t.Errorf("health with nothing listening = %d, %q; want 1 with a reason", code, stderr)
		}
	})
	t.Run("address_refused", func(t *testing.T) {
		if code, stderr := probe(t, "no-port"); code != 1 || !strings.Contains(stderr, `OPS_LISTEN_ADDR "no-port"`) {
			t.Errorf("health on a bad address = %d, %q; want 1 naming the setting and address", code, stderr)
		}
	})
	t.Run("probes_healthz", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" {
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(srv.Close)
		if code, _ := probe(t, srv.Listener.Addr().String()); code != 0 {
			t.Errorf("health against a server answering /healthz = %d, want 0", code)
		}
	})
}
