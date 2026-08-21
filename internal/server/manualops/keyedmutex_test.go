package manualops

import (
	"strings"
	"testing"

	"github.com/cplieger/keyenc"
	"github.com/cplieger/subflux/internal/subflux"
)

// oldDownloadQuadKey is the NUL-joined form downloadQuadKey replaced, kept as
// the collision oracle for the pairs below.
func oldDownloadQuadKey(mt subflux.MediaType, mediaID, lang string, variant subflux.Variant) string {
	return strings.Join([]string{string(mt), mediaID, lang, string(variant)}, "\x00")
}

// TestDownloadQuadKeyCannotBeForged pins that two DIFFERENT quads never share
// one gate entry. A merge puts two unrelated media items behind ONE mutex, so an
// ordinal allocation plus its atomic write and history insert serialize against
// a download that has nothing to do with them.
//
// Exactly one of the four components can carry a separator: mediaID is
// mediaid.Episode/BuildMovieID output, which falls back to the arr's raw imdbId
// when TVDB/TMDB is absent. mediaType and variant are closed constant sets, and
// lang has passed langcode.Valid, whose whole vocabulary is two ASCII letters.
//
// So neither table's pairs are reachable through the HTTP boundary: both need a
// separator inside lang, and no validator admits one. That is what they are for.
// They record that the old NUL-joined form's injectivity rested on the language
// validator rather than on the encoding — a dependency the gate has no business
// holding — while keyenc escaping keeps the key injective for whatever alphabet
// the arrs, or a future code space, turn out to admit.
func TestDownloadQuadKeyCannotBeForged(t *testing.T) {
	t.Parallel()

	type quad struct {
		mediaID string
		lang    string
		mt      subflux.MediaType
		variant subflux.Variant
	}
	key := func(q quad) string { return downloadQuadKey(q.mt, q.mediaID, q.lang, q.variant) }
	oldKey := func(q quad) string { return oldDownloadQuadKey(q.mt, q.mediaID, q.lang, q.variant) }

	nulCases := map[string][2]quad{
		"control separator moves from the media id into the language": {
			{mt: subflux.MediaTypeEpisode, mediaID: "tt1\x00fr", lang: "en", variant: subflux.VariantStandard},
			{mt: subflux.MediaTypeEpisode, mediaID: "tt1", lang: "fr\x00en", variant: subflux.VariantStandard},
		},
	}
	for name, pair := range nulCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if oldKey(pair[0]) != oldKey(pair[1]) {
				t.Fatalf("case no longer exercises a real collision: %q vs %q", oldKey(pair[0]), oldKey(pair[1]))
			}
			if left, right := key(pair[0]), key(pair[1]); left == right {
				t.Errorf("distinct quads share the gate key %q", left)
			}
		})
	}

	sepCases := map[string][2]quad{
		"colon moves from the media id into the language": {
			{mt: subflux.MediaTypeEpisode, mediaID: "tt1:fr", lang: "en", variant: subflux.VariantStandard},
			{mt: subflux.MediaTypeEpisode, mediaID: "tt1", lang: "fr:en", variant: subflux.VariantStandard},
		},
		"colon in the media id reaches the variant": {
			{mt: subflux.MediaTypeMovie, mediaID: "tt1:en:forced", lang: "de", variant: subflux.VariantHI},
			{mt: subflux.MediaTypeMovie, mediaID: "tt1", lang: "en:forced:de", variant: subflux.VariantHI},
		},
	}
	for name, pair := range sepCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// A plain ':' join — what the naive port of this key would have been.
			naive := func(q quad) string {
				return strings.Join([]string{string(q.mt), q.mediaID, q.lang, string(q.variant)}, ":")
			}
			if naive(pair[0]) != naive(pair[1]) {
				t.Fatalf("case does not exercise a separator collision: %q vs %q", naive(pair[0]), naive(pair[1]))
			}
			if left, right := key(pair[0]), key(pair[1]); left == right {
				t.Errorf("distinct quads share the gate key %q", left)
			}
		})
	}
}

// TestDownloadQuadKeyIsUnescapedForOrdinaryInput pins the shape of the key for
// real quads. The BYTES deliberately changed (NUL joins became ':'), which is
// free because the gate map is process-local and rebuilt every run — what must
// hold is that an ordinary quad still encodes as the plain separator-joined
// form, and that each component still separates quads that differ only in it.
func TestDownloadQuadKeyIsUnescapedForOrdinaryInput(t *testing.T) {
	t.Parallel()

	got := downloadQuadKey(subflux.MediaTypeEpisode, "tvdb-121361-s01e02", "fr", subflux.VariantForced)
	if want := "episode:tvdb-121361-s01e02:fr:forced"; got != want {
		t.Errorf("downloadQuadKey() = %q, want %q", got, want)
	}
	if keyenc.IsHashed(got) {
		t.Error("an ordinary quad key must not be reduced to a hashed identity")
	}

	base := downloadQuadKey(subflux.MediaTypeEpisode, "tvdb-1-s01e02", "fr", subflux.VariantStandard)
	others := map[string]string{
		"media type": downloadQuadKey(subflux.MediaTypeMovie, "tvdb-1-s01e02", "fr", subflux.VariantStandard),
		"media id":   downloadQuadKey(subflux.MediaTypeEpisode, "tvdb-2-s01e02", "fr", subflux.VariantStandard),
		"language":   downloadQuadKey(subflux.MediaTypeEpisode, "tvdb-1-s01e02", "en", subflux.VariantStandard),
		"variant":    downloadQuadKey(subflux.MediaTypeEpisode, "tvdb-1-s01e02", "fr", subflux.VariantForced),
	}
	for field, other := range others {
		if other == base {
			t.Errorf("quads differing only in %s share the gate key %q", field, base)
		}
	}
}

// TestDownloadQuadGateSerializesOnlyTheSameQuad pins the gate behavior the key
// feeds: one quad's holder blocks a second acquirer of the SAME quad, while a
// different quad proceeds immediately.
func TestDownloadQuadGateSerializesOnlyTheSameQuad(t *testing.T) {
	t.Parallel()

	g := newQuadGate()
	a := downloadQuadKey(subflux.MediaTypeEpisode, "tvdb-1-s01e02", "fr", subflux.VariantStandard)
	b := downloadQuadKey(subflux.MediaTypeEpisode, "tvdb-1-s01e02", "fr", subflux.VariantForced)

	unlockA := g.lock(a)

	done := make(chan struct{})
	go func() {
		unlockB := g.lock(b)
		unlockB()
		close(done)
	}()
	<-done // a different quad must not wait on A's holder

	blocked := make(chan struct{})
	go func() {
		unlockA2 := g.lock(a)
		unlockA2()
		close(blocked)
	}()
	select {
	case <-blocked:
		t.Error("a second acquirer of the same quad was not serialized")
	default:
	}
	unlockA()
	<-blocked
}

// gateEntries reports how many keys the gate currently holds.
func gateEntries(g *quadGate) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.locks)
}

// The gate map is bounded by IN-FLIGHT work rather than by library size: each
// entry is reference-counted and dropped when its last holder releases, so a
// server that has downloaded for thousands of quads still holds none.
func TestDownloadQuadGate_forgets_a_quad_once_its_last_holder_releases(t *testing.T) {
	t.Parallel()

	g := newQuadGate()
	a := downloadQuadKey(subflux.MediaTypeEpisode, "tvdb-1-s01e02", "fr", subflux.VariantStandard)
	b := downloadQuadKey(subflux.MediaTypeMovie, "tmdb-27205", "en", subflux.VariantForced)

	unlockA := g.lock(a)
	if got := gateEntries(g); got != 1 {
		t.Fatalf("gate entries after locking one quad = %d, want 1", got)
	}
	unlockB := g.lock(b)
	if got := gateEntries(g); got != 2 {
		t.Fatalf("gate entries after locking a second quad = %d, want 2", got)
	}

	unlockA()
	if got := gateEntries(g); got != 1 {
		t.Errorf("gate entries after releasing the first quad = %d, want 1 (its entry must be dropped)", got)
	}
	unlockB()
	if got := gateEntries(g); got != 0 {
		t.Errorf("gate entries after releasing every quad = %d, want 0 (the map must not grow with library size)", got)
	}
}
