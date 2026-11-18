// Command subflux finds, syncs and keeps subtitles for a media library.
package main

import (
	"os"

	"github.com/cplieger/subflux/internal/app"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "health" {
		app.ProbeHealth(app.OpsAddr(os.Getenv(app.OpsAddrSetting)))
	}
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
