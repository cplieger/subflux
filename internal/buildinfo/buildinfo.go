// Package buildinfo answers which subflux build is running.
package buildinfo

import "runtime/debug"

// stamped is the release tag the image build sets with -X.
var stamped string

// Version is the release tag the build was stamped with, else the module
// version, else "unknown".
func Version() string {
	if stamped != "" {
		return stamped
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "unknown"
}
