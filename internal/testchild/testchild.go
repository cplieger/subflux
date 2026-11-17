// Package testchild shapes the environment of a child process a test binary
// starts from its own executable.
package testchild

import (
	"os"
	"strings"
)

// GORACE is the GORACE value for such a child: the inherited options plus
// atexit_sleep_ms=0. A race-built child otherwise sleeps atexit_sleep_ms, 1 s
// by default, before it exits
// (https://go.dev/doc/articles/race_detector#Options).
func GORACE() string {
	return strings.TrimSpace(os.Getenv("GORACE") + " atexit_sleep_ms=0")
}
