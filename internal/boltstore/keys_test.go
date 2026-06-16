package boltstore

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	boltkv "github.com/cplieger/subflux/internal/store/kv"
)

// TestAttemptKey_layoutAndRoundTrip asserts the search_attempts key is exactly
// mt 0x00 mid 0x00 lang 0x00 provider and splits back into its components.
func TestAttemptKey_layoutAndRoundTrip(t *testing.T) {
	key := attemptKey(api.MediaTypeEpisode, "tvdb-99-s01e02", "fr", api.ProviderNameOpenSubtitles)
	want := []byte("episode\x00tvdb-99-s01e02\x00fr\x00opensubtitles")
	if !bytes.Equal(key, want) {
		t.Fatalf("attemptKey = %q, want %q", key, want)
	}
	got := boltkv.Split(key)
	wantParts := []string{"episode", "tvdb-99-s01e02", "fr", "opensubtitles"}
	if !slices.Equal(got, wantParts) {
		t.Errorf("Split(attemptKey) = %q, want %q", got, wantParts)
	}
}

// TestStateKey_orderingAndParse asserts surrogate-id keys sort in numeric
// (insertion) order and parse back exactly. This is the be64 numeric-sort
// requirement applied to subtitle_state primary keys.
func TestStateKey_orderingAndParse(t *testing.T) {
	ids := []int64{1, 2, 9, 10, 255, 256, 1 << 20, 1 << 40}
	for i := range len(ids) - 1 {
		a, b := stateKey(ids[i]), stateKey(ids[i+1])
		if bytes.Compare(a, b) >= 0 {
			t.Errorf("stateKey(%d) should sort before stateKey(%d)", ids[i], ids[i+1])
		}
	}
	for _, id := range ids {
		got, ok := parseStateKey(stateKey(id))
		if !ok || got != id {
			t.Errorf("parseStateKey(stateKey(%d)) = (%d, %v), want (%d, true)", id, got, ok, id)
		}
	}
	if _, ok := parseStateKey([]byte{1, 2, 3}); ok {
		t.Error("parseStateKey on a 3-byte key: ok = true, want false")
	}
}

// TestTriplePrefix_boundarySafety is the central component-boundary guard: a
// prefix scan for media id "tt1" must NOT match a key for "tt12".
func TestTriplePrefix_boundarySafety(t *testing.T) {
	short := triplePrefix(api.MediaTypeEpisode, "tt1", "fr")
	longKey := attemptKey(api.MediaTypeEpisode, "tt12", "fr", api.ProviderNameSubDL)
	shortKey := attemptKey(api.MediaTypeEpisode, "tt1", "fr", api.ProviderNameSubDL)

	if !bytes.HasPrefix(shortKey, short) {
		t.Errorf("expected key for tt1 %q to carry triplePrefix %q", shortKey, short)
	}
	if bytes.HasPrefix(longKey, short) {
		t.Errorf("key for tt12 %q must not match the tt1 prefix %q", longKey, short)
	}

	// mediaPrefix has the same boundary guarantee on the media id alone.
	mp := mediaPrefix(api.MediaTypeEpisode, "tt1")
	if !bytes.HasPrefix(shortKey, mp) {
		t.Errorf("expected key for tt1 %q to carry mediaPrefix %q", shortKey, mp)
	}
	if bytes.HasPrefix(longKey, mp) {
		t.Errorf("key for tt12 %q must not match the tt1 mediaPrefix %q", longKey, mp)
	}
}

// TestStateTripleKey_prefixAndID asserts the triple index key carries
// triplePrefix (so it prefix-scans by triple and by media) and that the
// trailing surrogate id parses back.
func TestStateTripleKey_prefixAndID(t *testing.T) {
	const id int64 = 4242
	key := stateTripleKey(api.MediaTypeMovie, "tmdb-27205", "en", id)

	if !bytes.HasPrefix(key, triplePrefix(api.MediaTypeMovie, "tmdb-27205", "en")) {
		t.Error("stateTripleKey must carry its triplePrefix")
	}
	if !bytes.HasPrefix(key, mediaPrefix(api.MediaTypeMovie, "tmdb-27205")) {
		t.Error("stateTripleKey must carry its mediaPrefix")
	}
	gotID, ok := stateTripleKeyID(key)
	if !ok || gotID != id {
		t.Errorf("stateTripleKeyID = (%d, %v), want (%d, true)", gotID, ok, id)
	}

	// Two ids under the same triple sort by id (insertion order).
	if bytes.Compare(stateTripleKey(api.MediaTypeMovie, "tmdb-27205", "en", 1),
		stateTripleKey(api.MediaTypeMovie, "tmdb-27205", "en", 2)) >= 0 {
		t.Error("stateTripleKey id=1 should sort before id=2 under the same triple")
	}
}

// TestStateVideoKey_roundTripAndBoundary covers the video-path reverse index:
// build/parse round-trip plus the prefix-boundary guard for shared-prefix
// paths.
func TestStateVideoKey_roundTripAndBoundary(t *testing.T) {
	const path = "/media/movies/Inception (2010)/Inception.mkv"
	const id int64 = 77
	key := stateVideoKey(path, id)

	gotPath, gotID, ok := splitStateVideoKey(key)
	if !ok || gotPath != path || gotID != id {
		t.Fatalf("splitStateVideoKey = (%q, %d, %v), want (%q, %d, true)", gotPath, gotID, ok, path, id)
	}

	if !bytes.HasPrefix(key, videoPrefix(path)) {
		t.Error("stateVideoKey must carry its videoPrefix")
	}
	// A path that shares a prefix of another path must not match its prefix.
	other := stateVideoKey(path+".bak", id)
	if bytes.HasPrefix(other, videoPrefix(path)) {
		t.Error("a longer path must not match the shorter path's videoPrefix")
	}

	if _, _, ok := splitStateVideoKey([]byte{1, 2, 3}); ok {
		t.Error("splitStateVideoKey on a too-short key: ok = true, want false")
	}
}

// TestSubtitleFileKey_roundTrip asserts the six-component subtitle_files key
// builds in order and splits back, with lang/variant recoverable from the key
// for key-only coverage walks.
func TestSubtitleFileKey_roundTrip(t *testing.T) {
	key := subtitleFileKey(api.MediaTypeEpisode, "tvdb-1-s01e01", "es", api.Variant("hi"),
		api.SourceExternal, "/media/tv/Show/Show.S01E01.es.hi.srt")
	got := boltkv.Split(key)
	want := []string{
		"episode", "tvdb-1-s01e01", "es", "hi", "external",
		"/media/tv/Show/Show.S01E01.es.hi.srt",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Split(subtitleFileKey) = %q, want %q", got, want)
	}
	// Per-media prefix walk must hit the file key.
	if !bytes.HasPrefix(key, mediaPrefix(api.MediaTypeEpisode, "tvdb-1-s01e01")) {
		t.Error("subtitleFileKey must carry its mediaPrefix for key-only coverage walks")
	}
}

// TestScanStateKey_roundTrip asserts scan_state is keyed by mt 0x00 mid.
func TestScanStateKey_roundTrip(t *testing.T) {
	key := scanStateKey(api.MediaTypeMovie, "tmdb-603")
	want := []string{"movie", "tmdb-603"}
	if got := boltkv.Split(key); !slices.Equal(got, want) {
		t.Errorf("Split(scanStateKey) = %q, want %q", got, want)
	}
}

// TestBareKeys asserts sync_offsets and poll_state keep their bare-string keys.
func TestBareKeys(t *testing.T) {
	if got := syncOffsetKey("/media/x.fr.srt"); !bytes.Equal(got, []byte("/media/x.fr.srt")) {
		t.Errorf("syncOffsetKey = %q, want the bare path", got)
	}
	if got := pollStateKey(api.PollKey("sonarr")); !bytes.Equal(got, []byte("sonarr")) {
		t.Errorf("pollStateKey = %q, want %q", got, "sonarr")
	}
}

// TestTimeIndexKeys_chronologicalOrder asserts the time-ordered index helpers
// (attempts-due, state-imported, scan-at) sort chronologically and seek to a
// cutoff exactly.
func TestTimeIndexKeys_chronologicalOrder(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	earlier, later := base, base.Add(time.Hour)

	attemptPrimary := attemptKey(api.MediaTypeEpisode, "tt1", "fr", api.ProviderNameGestdown)
	if bytes.Compare(attemptsDueKey(earlier, attemptPrimary), attemptsDueKey(later, attemptPrimary)) >= 0 {
		t.Error("attemptsDueKey: earlier next_retry must sort before later")
	}

	if bytes.Compare(stateImportedKey(earlier, 1), stateImportedKey(later, 1)) >= 0 {
		t.Error("stateImportedKey: earlier media_imported must sort before later")
	}
	// Same import time, ascending id sorts ascending (reverse walk = id DESC).
	if bytes.Compare(stateImportedKey(base, 1), stateImportedKey(base, 2)) >= 0 {
		t.Error("stateImportedKey: with equal time, id=1 must sort before id=2")
	}

	scanPrimary := scanStateKey(api.MediaTypeMovie, "tmdb-1")
	k1 := scanAtKey(earlier, scanPrimary)
	k2 := scanAtKey(later, scanPrimary)
	if bytes.Compare(k1, k2) >= 0 {
		t.Error("scanAtKey: earlier scanned_at must sort before later")
	}
	// A seek to the later cutoff excludes the earlier entry, includes the later.
	cutoff := boltkv.Be64(uint64(later.UnixNano()))
	if bytes.Compare(k1[:8], cutoff) >= 0 {
		t.Error("earlier scanAtKey timestamp should be < later cutoff")
	}
	if bytes.Compare(k2[:8], cutoff) < 0 {
		t.Error("later scanAtKey timestamp should be >= later cutoff")
	}
}
