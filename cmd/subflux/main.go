// Command subflux finds, syncs and keeps subtitles for a media library.
package main

import (
	"os"

	"github.com/cplieger/health"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "health" {
		health.RunProbe(paths.Health)
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
