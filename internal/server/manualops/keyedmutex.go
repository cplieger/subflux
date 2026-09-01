package manualops

import (
	"sync"

	"github.com/cplieger/keyenc"
	"github.com/cplieger/subflux/internal/subflux"
)

// quadGate serializes numbered-path allocation per subtitle-state quad
// (media_type, media_id, language, variant). Manual downloads run in
// background goroutines; two concurrent downloads for the SAME quad would
// otherwise both read NextManualNumber before either records its history
// row, claim the same ordinal, and the second atomic write would silently
// overwrite the first file. The gate is keyed by the quad, so unrelated
// downloads never contend — deliberately a keyed gate, not a global lock
// (mirrors internal/search's mediaGate).
type quadGate struct {
	locks map[string]*quadGateEntry
	mu    sync.Mutex
}

type quadGateEntry struct {
	mu   sync.Mutex
	refs int
}

func newQuadGate() *quadGate {
	return &quadGate{locks: make(map[string]*quadGateEntry)}
}

// lock acquires the per-key mutex, blocking while another holder runs, and
// returns the release func. Entries are reference-counted and removed at
// zero, so the map stays bounded by in-flight work, not library size.
func (g *quadGate) lock(key string) (unlock func()) {
	g.mu.Lock()
	e, ok := g.locks[key]
	if !ok {
		e = &quadGateEntry{}
		g.locks[key] = e
	}
	e.refs++
	g.mu.Unlock()

	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		g.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(g.locks, key)
		}
		g.mu.Unlock()
	}
}

// downloadPathGate is the process-wide reservation over manual-download
// path allocation: numbered subtitle paths live in one filesystem
// namespace, so the gate is package-scoped rather than per-handler.
var downloadPathGate = newQuadGate()

// downloadQuadKey builds the gate key for a quad. keyenc.Join escapes each
// component, so the key stays injective even though mediaID (Sonarr/Radarr's
// own alphabet, via mediaid.Episode/BuildMovieID) is the one component whose
// character set this package does not control; a merge would serialize an
// unrelated download's ordinal allocation behind the same mutex.
func downloadQuadKey(mt subflux.MediaType, mediaID, lang string, variant subflux.Variant) string {
	return keyenc.Join(string(mt), mediaID, lang, string(variant))
}
