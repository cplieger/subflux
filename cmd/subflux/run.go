package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/cplieger/runesafe/v3"
	"github.com/cplieger/subflux/internal/app"
)

// Test seams: the boot path and the container layout.
var (
	runApp = app.Run
	paths  = app.DefaultPaths
)

const usage = "usage: subflux [health]"

// run dispatches every command but health, which main hands to
// health.RunProbe, and answers the exit code: 0, 1 on a failure, 2 on a
// usage error. Asked-for help goes to stdout with 0.
func run(args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 0:
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
		defer stop()
		if err := runApp(ctx, app.Config{Paths: paths}); err != nil {
			_, _ = fmt.Fprintln(stderr, "subflux: "+runesafe.SanitizeSingleLine(err.Error()))
			return 1
		}
		return 0
	case len(args) == 1 && slices.Contains([]string{"-h", "--help", "help"}, args[0]):
		_, _ = fmt.Fprintln(stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
}
