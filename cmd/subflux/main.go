// Command subflux finds, syncs and keeps subtitles for a media library.
package main

import (
	"os"

	"github.com/cplieger/subflux/internal/app"
)

// healthAddr is the listener the health subcommand probes; tests point it
// at their own server.
var healthAddr = app.ListenAddr

func main() {
	if len(os.Args) == 2 && os.Args[1] == "health" {
		app.ProbeHealth(healthAddr)
	}
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
