package anidb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/keyenc"
)

// oldEpisodeCacheKey is the fmt.Sprintf form both episodeCache builders used,
// kept as the byte-identity oracle. The TrimSpace mirrors the original: only the
// join changed.
func oldEpisodeCacheKey(seriesID int, epNo string) string {
	epNo = strings.TrimSpace(epNo)
	if n, err := strconv.Atoi(epNo); err == nil {
		return fmt.Sprintf("%d:%d", seriesID, n)
	}
	return fmt.Sprintf("%d:%s", seriesID, epNo)
}

// TestEpisodeCacheKeyBuildersAgree is the site's load-bearing test: this key is
// built in TWO places — buildEpisodeCacheKey on the WRITE side (from AniDB's
// episodes XML) and getEpisodeID on the READ side (from the resolved episode
// number) — and if they disagree by one byte every lookup misses, degrading the
// cache to one API call per episode against AniDB's 1-req-per-2s limit.
//
// It asserts agreement through the real read path rather than by re-deriving
// the key: a cache seeded exactly as cacheEpisodes seeds it must be HIT by
// getEpisodeID, which returns from its fast path before any HTTP.
func TestEpisodeCacheKeyBuildersAgree(t *testing.T) {
	t.Parallel()

	const (
		seriesID = 4521
		episode  = 12
		epID     = 98765
	)

	// Every spelling of episode 12 the XML can carry must land on the key the
	// read side looks up.
	for _, xmlEpNo := range []string{"12", "012", "0012", "  12  "} {
		t.Run("xml epno "+strconv.Quote(xmlEpNo), func(t *testing.T) {
			t.Parallel()
			m := NewMapper("")
			m.episodeCache[buildEpisodeCacheKey(seriesID, xmlEpNo)] = epID

			got, err := m.getEpisodeID(context.Background(), seriesID, episode)
			if err != nil {
				t.Fatalf("getEpisodeID() error = %v; the write-side key %q did not match the read-side lookup",
					err, buildEpisodeCacheKey(seriesID, xmlEpNo))
			}
			if got != epID {
				t.Errorf("getEpisodeID() = %d, want %d", got, epID)
			}
		})
	}
}

// TestEpisodeCacheKeyKeepsBytesForOrdinaryInput pins that adopting keyenc did
// not change the key for what AniDB actually returns — plain numbers and the
// S1/C1/T1 specials — so the cache is not silently invalidated and the existing
// normalization contract (leading zeros, whitespace) still holds.
func TestEpisodeCacheKeyKeepsBytesForOrdinaryInput(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		epNo     string
		want     string
		seriesID int
	}{
		"numeric":              {seriesID: 123, epNo: "5", want: "123:5"},
		"numeric leading zero": {seriesID: 123, epNo: "007", want: "123:7"},
		"whitespace trimmed":   {seriesID: 123, epNo: "  7  ", want: "123:7"},
		"special S1":           {seriesID: 123, epNo: "S1", want: "123:S1"},
		"special C1":           {seriesID: 42, epNo: "C1", want: "42:C1"},
		"empty":                {seriesID: 123, epNo: "", want: "123:"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := buildEpisodeCacheKey(tc.seriesID, tc.epNo)
			if got != tc.want {
				t.Errorf("buildEpisodeCacheKey(%d, %q) = %q, want %q", tc.seriesID, tc.epNo, got, tc.want)
			}
			if old := oldEpisodeCacheKey(tc.seriesID, tc.epNo); got != old {
				t.Errorf("key bytes changed: %q, previously %q", got, old)
			}
			if keyenc.IsHashed(got) {
				t.Error("an ordinary episode key must not be reduced to a hashed identity")
			}
		})
	}
}

// TestEpisodeCacheKeyIsInjective pins that distinct (series, episode) pairs stay
// distinct. No tuple pair the old form collapsed exists here and none is
// asserted: seriesID is an int, so it can never carry the separator and the
// boundary was already pinned from the left. epNo CAN carry it — it is chardata
// straight out of AniDB's episodes XML and the non-numeric branch stores it
// verbatim — so what the test pins is that a ':' or '\' arriving in epNo cannot
// make one episode's entry answer for another's. A collision does not miss, it
// answers: animetosho searches by the AniDB episode id this map returns, so the
// WRONG episode's subtitle is scored and written next to the video, where it
// then counts as covered and is never retried.
func TestEpisodeCacheKeyIsInjective(t *testing.T) {
	t.Parallel()

	keys := map[string]string{
		"plain episode":          buildEpisodeCacheKey(1, "2"),
		"longer series id":       buildEpisodeCacheKey(12, "2"),
		"special":                buildEpisodeCacheKey(1, "S2"),
		"epno carrying colon":    buildEpisodeCacheKey(1, "2:3"),
		"epno carrying escape":   buildEpisodeCacheKey(1, `2\3`),
		"epno colon at the head": buildEpisodeCacheKey(1, ":2"),
		"other series same epno": buildEpisodeCacheKey(2, "2"),
	}
	seen := make(map[string]string, len(keys))
	for name, key := range keys {
		if prev, dup := seen[key]; dup {
			t.Errorf("%q and %q share the episode cache key %q", prev, name, key)
		}
		seen[key] = name
	}

	// A ':'-carrying epNo must also stay clear of every key the READ side can
	// produce, since the read side always joins two integers.
	readSide := buildEpisodeCacheKey(1, "23")
	if forged := buildEpisodeCacheKey(1, "2:3"); forged == readSide {
		t.Errorf("an XML episode number forged the numeric lookup key %q", readSide)
	}
}
