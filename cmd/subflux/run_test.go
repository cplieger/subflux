package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/app"
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
// sets SUBFLUX_HEALTH_HELPER to a listen address.
func TestHealthHelper(t *testing.T) {
	addr := os.Getenv("SUBFLUX_HEALTH_HELPER")
	if addr == "" {
		t.Skip("run as TestHealthSubcommand's child")
	}
	healthAddr = addr
	os.Args = []string{"subflux", "health"}
	main()
}

func TestHealthSubcommand(t *testing.T) {
	probe := func(addr string) (int, string) {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHealthHelper$")
		cmd.Env = append(os.Environ(), "SUBFLUX_HEALTH_HELPER="+addr)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatal(err)
		}
		return cmd.ProcessState.ExitCode(), stderr.String()
	}
	serve := func(code int) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/health" {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(code)
		}))
		t.Cleanup(srv.Close)
		return srv.Listener.Addr().String()
	}
	if code, stderr := probe(serve(http.StatusOK)); code != 0 || stderr != "" {
		t.Errorf("health against a 200 = %d, %q; want 0 and no output", code, stderr)
	}
	if code, stderr := probe(serve(http.StatusServiceUnavailable)); code != 1 || !strings.Contains(stderr, "503") {
		t.Errorf("health against a 503 = %d, %q; want 1 naming the status", code, stderr)
	}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	_ = ln.Close()
	if code, stderr := probe(closed); code != 1 || stderr == "" {
		t.Errorf("health with nothing listening = %d, %q; want 1 with a reason", code, stderr)
	}
	if code, stderr := probe("no-port"); code != 1 || !strings.Contains(stderr, "health:") {
		t.Errorf("health on a bad address = %d, %q; want 1 naming the address error", code, stderr)
	}
}
