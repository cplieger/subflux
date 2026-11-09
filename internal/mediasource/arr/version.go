package arr

import (
	"log/slog"
	"sync"

	"github.com/cplieger/arrapi/v3"
)

// Version logs the server version a Ping found, once per change. Its zero
// value is ready; it is safe for concurrent use.
type Version struct {
	last string
	mu   sync.Mutex
}

// Note logs version and api at INFO when version differs from the last one
// noted.
func (v *Version) Note(log *slog.Logger, version string, api arrapi.API) {
	v.mu.Lock()
	changed := v.last != version
	v.last = version
	v.mu.Unlock()
	if changed {
		log.Info("source server version", "version", version, "api", string(api))
	}
}
