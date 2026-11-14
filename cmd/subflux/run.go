package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/cplieger/go/textsafe"
	"github.com/cplieger/subflux/internal/adminsock"
	"github.com/cplieger/subflux/internal/app"
)

// Test seams: the boot path and the container layout.
var (
	runApp = app.Run
	paths  = app.DefaultPaths
)

// usage lists the commands; each local one calls the admin socket of the
// subflux running in this container.
var usage = func() string {
	var b strings.Builder
	b.WriteString("usage: subflux [health | <command>]\ncommands:")
	for _, w := range adminsock.Words() {
		var c adminsock.Command
		_ = c.UnmarshalText([]byte(w))
		b.WriteString(strings.TrimRight("\n  "+w+" "+commandUsage[c], " "))
	}
	return b.String()
}()

// run dispatches every command but health, which main hands to
// health.RunProbe, and answers the exit code: 0, 1 on a failure, 2 on a
// usage error. Asked-for help goes to stdout with 0.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	switch {
	case len(args) == 0:
		if err := runApp(ctx, app.Config{Paths: paths}); err != nil {
			_, _ = fmt.Fprintln(stderr, "subflux: "+textsafe.SanitizeSingleLine(err.Error()))
			return 1
		}
		return 0
	case len(args) == 1 && slices.Contains([]string{"-h", "--help", "help"}, args[0]):
		_, _ = fmt.Fprintln(stdout, usage)
		return 0
	}
	if code, ok := command(ctx, args, stdin, stdout, stderr); ok {
		return code
	}
	_, _ = fmt.Fprintln(stderr, usage)
	return 2
}
