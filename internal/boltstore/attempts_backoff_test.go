package boltstore

import (
	"context"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/cplieger/subflux/internal/api"
)

// This file covers the task-3.2 backoff listing behaviour: GetBackoffItems
// (ascending next_retry via the due index, Requirement 2.3) and
// GetBackoffByPrefix (provider rows only, ordered media id then ascending
// next_retry, Requirement 15.4), plus prefix isolation/inclusion semantics.

// TestGetBackoffItems_ascendingNextRetry asserts GetBackoffItems returns every
// row ordered by ascending next_retry, regardless of insertion order
// (Requirement 2.3).
func TestGetBackoffItems_ascendingNextRetry(t *testing.T) {
	db, _ := openTemp(t)
	base := time.Now()

	// Insert three rows out of next_retry order.
	putAttemptRow(t, db, api.MediaTypeMovie, "tt300", "en", testProv,
		attemptRec{LastTried: base, NextRetry: base.Add(3 * time.Hour), Failures: 3})
	putAttemptRow(t, db, api.MediaTypeMovie, "tt100", "en", testProv,
		attemptRec{LastTried: base, NextRetry: base.Add(1 * time.Hour), Failures: 1})
	putAttemptRow(t, db, api.MediaTypeMovie, "tt200", "en", testProv,
		attemptRec{LastTried: base, NextRetry: base.Add(2 * time.Hour), Failures: 2})

	got, err := db.GetBackoffItems(context.Background())
	if err != nil {
		t.Fatalf("GetBackoffItems: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].NextRetry.Before(got[i-1].NextRetry) {
			t.Errorf("entry %d next_retry %v is before entry %d %v (not ascending)",
				i, got[i].NextRetry, i-1, got[i-1].NextRetry)
		}
	}
	// Verify the full populated entry, not just ordering.
	if got[0].MediaID != "tt100" || got[0].Failures != 1 || got[0].Provider != testProv {
		t.Errorf("first entry = %+v, want tt100/failures=1/%s", got[0], testProv)
	}
}

// TestGetBackoffItems_excludesEmptyProvider asserts rows with an empty provider
// component are excluded (mirrors the old `WHERE provider != ”`).
func TestGetBackoffItems_excludesEmptyProvider(t *testing.T) {
	db, _ := openTemp(t)
	future := time.Now().Add(time.Hour)
	putAttemptRow(t, db, api.MediaTypeMovie, "tt1", "en", testProv,
		attemptRec{NextRetry: future, Failures: 1})
	putAttemptRow(t, db, api.MediaTypeMovie, "tt1", "en", api.ProviderID(""),
		attemptRec{NextRetry: future, Failures: 1})

	got, err := db.GetBackoffItems(context.Background())
	if err != nil {
		t.Fatalf("GetBackoffItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1 (empty-provider row excluded)", len(got))
	}
	if got[0].Provider != testProv {
		t.Errorf("provider = %q, want %q", got[0].Provider, testProv)
	}
}

// TestGetBackoffItems_empty asserts a store with no attempts returns no items
// without error.
func TestGetBackoffItems_empty(t *testing.T) {
	db, _ := openTemp(t)
	got, err := db.GetBackoffItems(context.Background())
	if err != nil {
		t.Fatalf("GetBackoffItems: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0", len(got))
	}
}

// TestGetBackoffByPrefix_mediaIDThenNextRetry asserts the by-prefix listing is
// ordered by media id then ascending next_retry (Requirement 15.4).
func TestGetBackoffByPrefix_mediaIDThenNextRetry(t *testing.T) {
	db, _ := openTemp(t)
	base := time.Now()

	// Two media ids, each with two providers at different next_retry, inserted
	// in an order that is neither media-id nor next_retry sorted.
	putAttemptRow(t, db, api.MediaTypeMovie, "ttB", "en", api.ProviderNameGestdown,
		attemptRec{LastTried: base, NextRetry: base.Add(4 * time.Hour), Failures: 1})
	putAttemptRow(t, db, api.MediaTypeMovie, "ttA", "en", api.ProviderNameGestdown,
		attemptRec{LastTried: base, NextRetry: base.Add(3 * time.Hour), Failures: 1})
	putAttemptRow(t, db, api.MediaTypeMovie, "ttA", "en", testProv,
		attemptRec{LastTried: base, NextRetry: base.Add(1 * time.Hour), Failures: 1})
	putAttemptRow(t, db, api.MediaTypeMovie, "ttB", "en", testProv,
		attemptRec{LastTried: base, NextRetry: base.Add(2 * time.Hour), Failures: 1})

	got, err := db.GetBackoffByPrefix(context.Background(), api.MediaTypeMovie, "")
	if err != nil {
		t.Fatalf("GetBackoffByPrefix: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d entries, want 4", len(got))
	}

	// Expected: ttA rows first (ascending next_retry within ttA), then ttB rows.
	type mp struct {
		mid  string
		next time.Duration
	}
	wantOrder := []mp{
		{"ttA", 1 * time.Hour},
		{"ttA", 3 * time.Hour},
		{"ttB", 2 * time.Hour},
		{"ttB", 4 * time.Hour},
	}
	for i, w := range wantOrder {
		if got[i].MediaID != w.mid || !got[i].NextRetry.Equal(base.Add(w.next)) {
			t.Errorf("entry %d = (%s, +%v), want (%s, +%v)",
				i, got[i].MediaID, got[i].NextRetry.Sub(base), w.mid, w.next)
		}
	}
}

// TestGetBackoffByPrefix_prefixInclusionAndType asserts the media-id prefix is a
// starts-with match (so "tt1" includes both "tt1" and "tt12") and that the
// media-type filter excludes other types (Requirement 15.4).
func TestGetBackoffByPrefix_prefixInclusionAndType(t *testing.T) {
	db, _ := openTemp(t)
	future := time.Now().Add(time.Hour)
	putAttemptRow(t, db, api.MediaTypeMovie, "tt1", "en", testProv, attemptRec{NextRetry: future, Failures: 1})
	putAttemptRow(t, db, api.MediaTypeMovie, "tt12", "en", testProv, attemptRec{NextRetry: future, Failures: 1})
	putAttemptRow(t, db, api.MediaTypeMovie, "tt2", "en", testProv, attemptRec{NextRetry: future, Failures: 1})
	// Same media id under a different media type must not match the movie query.
	putAttemptRow(t, db, api.MediaTypeEpisode, "tt1", "en", testProv, attemptRec{NextRetry: future, Failures: 1})

	got, err := db.GetBackoffByPrefix(context.Background(), api.MediaTypeMovie, "tt1")
	if err != nil {
		t.Fatalf("GetBackoffByPrefix: %v", err)
	}
	gotIDs := map[string]int{}
	for _, e := range got {
		gotIDs[e.MediaID]++
		if e.MediaType != api.MediaTypeMovie {
			t.Errorf("entry media type = %q, want movie", e.MediaType)
		}
	}
	// LIKE 'tt1%' matches tt1 and tt12, not tt2; the episode tt1 row is filtered
	// out by the media-type clause.
	if gotIDs["tt1"] != 1 || gotIDs["tt12"] != 1 {
		t.Errorf("want one tt1 and one tt12, got %v", gotIDs)
	}
	if _, bleed := gotIDs["tt2"]; bleed {
		t.Errorf("tt2 leaked into a tt1 prefix query: %v", gotIDs)
	}
	if len(got) != 2 {
		t.Errorf("got %d entries, want 2", len(got))
	}
}

// TestGetBackoffByPrefix_excludesEmptyProvider asserts provider-rows-only
// filtering (Requirement 15.4: entries for rows that have a provider).
func TestGetBackoffByPrefix_excludesEmptyProvider(t *testing.T) {
	db, _ := openTemp(t)
	future := time.Now().Add(time.Hour)
	putAttemptRow(t, db, api.MediaTypeMovie, "tt1", "en", testProv, attemptRec{NextRetry: future, Failures: 1})
	putAttemptRow(t, db, api.MediaTypeMovie, "tt1", "en", api.ProviderID(""), attemptRec{NextRetry: future, Failures: 1})

	got, err := db.GetBackoffByPrefix(context.Background(), api.MediaTypeMovie, "")
	if err != nil {
		t.Fatalf("GetBackoffByPrefix: %v", err)
	}
	if len(got) != 1 || got[0].Provider != testProv {
		t.Fatalf("got %+v, want exactly one row for provider %q", got, testProv)
	}
}

// byPrefixPool is a small media-id pool for the property test, chosen to force
// prefix collisions ("tt1" vs "tt12") and grouping.
var byPrefixPool = []string{"tt1", "tt12", "tt2", "tt20"}

// TestGetBackoffByPrefix_orderingProperty is a property test asserting that for
// any randomly populated set of provider rows, GetBackoffByPrefix returns
// exactly the provider rows whose media id starts with the queried prefix,
// ordered by (media id, next_retry) (Requirements 15.4, 8.3).
func TestGetBackoffByPrefix_orderingProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db := openPropDB(rt)
		base := time.Now()

		type row struct {
			mid      string
			provider api.ProviderID
			next     time.Time
		}
		var inserted []row
		n := rapid.IntRange(0, 12).Draw(rt, "rows")
		for i := 0; i < n; i++ {
			mid := rapid.SampledFrom(byPrefixPool).Draw(rt, "mid")
			prov := rapid.SampledFrom(providerSetPool).Draw(rt, "prov")
			offsetMin := rapid.IntRange(0, 600).Draw(rt, "offsetMin")
			next := base.Add(time.Duration(offsetMin) * time.Minute)
			// One row per (mid, provider): a duplicate overwrites, matching a
			// single search_attempts row per triple+provider (lang fixed).
			putAttemptRow(t, db, api.MediaTypeMovie, mid, testLang, prov,
				attemptRec{LastTried: base, NextRetry: next, Failures: 1})
			// Track the latest write for each (mid, provider).
			replaced := false
			for j := range inserted {
				if inserted[j].mid == mid && inserted[j].provider == prov {
					inserted[j].next = next
					replaced = true
					break
				}
			}
			if !replaced {
				inserted = append(inserted, row{mid: mid, provider: prov, next: next})
			}
		}

		prefix := rapid.SampledFrom([]string{"", "tt1", "tt12", "tt2", "tt20", "ttX"}).Draw(rt, "prefix")

		got, err := db.GetBackoffByPrefix(context.Background(), api.MediaTypeMovie, prefix)
		if err != nil {
			rt.Fatalf("GetBackoffByPrefix: %v", err)
		}

		// Reference: filter by media-id starts-with prefix. This is the exact
		// set the listing must return (provider rows only; one row per
		// (mid, provider) after overwrites).
		var want []row
		for _, r := range inserted {
			if len(prefix) == 0 || hasStringPrefix(r.mid, prefix) {
				want = append(want, r)
			}
		}

		// 1. Set equality: the listing returns exactly the matching rows, no
		//    more, no fewer.
		if len(got) != len(want) {
			rt.Fatalf("got %d entries, want %d (prefix=%q, inserted=%v)", len(got), len(want), prefix, inserted)
		}
		gotSet := map[string]int{}
		for _, e := range got {
			gotSet[e.MediaID+"|"+string(e.Provider)]++
		}
		for _, w := range want {
			if gotSet[w.mid+"|"+string(w.provider)] == 0 {
				rt.Fatalf("missing (%s,%s) in %v (prefix=%q)", w.mid, w.provider, got, prefix)
			}
		}

		// 2. Ordering invariant: media id non-decreasing, and within a media-id
		//    group next_retry non-decreasing. Ties on next_retry have an
		//    unspecified order (SQL ORDER BY media_id, next_retry leaves them
		//    arbitrary), so the assertion checks the invariant, not a fixed
		//    permutation.
		for i := 1; i < len(got); i++ {
			if got[i].MediaID < got[i-1].MediaID {
				rt.Fatalf("media id order violated at %d: %q before %q", i, got[i-1].MediaID, got[i].MediaID)
			}
			if got[i].MediaID == got[i-1].MediaID && got[i].NextRetry.Before(got[i-1].NextRetry) {
				rt.Fatalf("next_retry order violated within %q at %d: %v before %v",
					got[i].MediaID, i, got[i-1].NextRetry, got[i].NextRetry)
			}
		}
	})
}

// hasStringPrefix is a tiny local helper to keep the property reference
// readable (avoids importing strings for one call).
func hasStringPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
