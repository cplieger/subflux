package manualops

import (
	"strings"
	"sync"

	"github.com/cplieger/subflux/internal/api"
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

// downloadQuadKey builds the gate key for a quad. The NUL joins cannot
// collide with field content (media IDs and validated language codes never
// contain control characters).
func downloadQuadKey(mt api.MediaType, mediaID, lang string, variant api.Variant) string {
	return strings.Join([]string{string(mt), mediaID, lang, string(variant)}, "\x00")
}
