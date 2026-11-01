package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	if code := run(nil, &stdout, &stderr); code != 1 {
		t.Errorf("run = %d, want 1", code)
	}
	if out := stderr.String(); !strings.Contains(out, "address already in use") || strings.Count(out, "\n") != 1 {
		t.Errorf("stderr %q; want the error on one line", out)
	}
	if got.Paths != app.DefaultPaths {
		t.Errorf("booted with %+v, want the default paths", got.Paths)
	}

	swapRunApp(t, func(context.Context, app.Config) error { return nil })
	if code := run(nil, &stdout, &stderr); code != 0 {
		t.Errorf("run after a clean Run = %d, want 0", code)
	}
	for _, args := range [][]string{{"bogus"}, {"health", "extra"}, {"--help", "extra"}} {
		stdout.Reset()
		stderr.Reset()
		if code := run(args, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage") || stdout.Len() != 0 {
			t.Errorf("run(%q) = %d, stdout %q, stderr %q; want 2 with usage on stderr", args, code, stdout.String(), stderr.String())
		}
	}
	for _, arg := range []string{"-h", "--help", "help"} {
		stdout.Reset()
		stderr.Reset()
		if code := run([]string{arg}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "usage") || stderr.Len() != 0 {
			t.Errorf("run(%q) = %d, stdout %q, stderr %q; want 0 with usage on stdout", arg, code, stdout.String(), stderr.String())
		}
	}
}

// TestHealthHelper is the health subcommand's process when the parent test
// sets SUBFLUX_HEALTH_HELPER to a marker path.
func TestHealthHelper(t *testing.T) {
	marker := os.Getenv("SUBFLUX_HEALTH_HELPER")
	if marker == "" {
		t.Skip("run as TestHealthSubcommand's child")
	}
	paths.Health = marker
	os.Args = []string{"subflux", "health"}
	main()
}

func TestHealthSubcommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "healthy")
	probe := func() (int, string) {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHealthHelper$")
		cmd.Env = append(os.Environ(), "SUBFLUX_HEALTH_HELPER="+marker)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatal(err)
		}
		return cmd.ProcessState.ExitCode(), stderr.String()
	}
	if code, stderr := probe(); code != 1 || !strings.Contains(stderr, "marker absent") {
		t.Errorf("health with no marker = %d, %q; want 1 with RunProbe's reason", code, stderr)
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, stderr := probe(); code != 0 || stderr != "" {
		t.Errorf("health with the marker = %d, %q; want 0 and no output", code, stderr)
	}
}
