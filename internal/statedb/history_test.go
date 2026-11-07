package statedb

import (
	"slices"
	"testing"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

func recordAttempts(t *testing.T, s *Store, ds ...Download) {
	t.Helper()
	if err := s.RecordAttempts(t.Context(), ds); err != nil {
		t.Fatalf("RecordAttempts: %v", err)
	}
}

func history(t *testing.T, s *Store, scope HistoryScope, c ListCursor, limit int) ([]Download, ListCursor) {
	t.Helper()
	ds, next, err := s.History(t.Context(), scope, c, limit)
	if err != nil {
		t.Fatalf("History(%q, %d): %v", c, limit, err)
	}
	return ds, next
}

func TestHistoryExactKeys(t *testing.T) {
	s, _ := newLibrary(t, "radarr", "sonarr")
	passOver(t, s, "radarr",
		report{movie("12", 12), []mediasource.Item{film("12", "/m/12.mkv")}},
		report{movie("120", 120), []mediasource.Item{film("120", "/m/120.mkv")}},
	)
	passOver(t, s, "sonarr",
		report{series("12", 12), []mediasource.Item{episode("121", 1, 1, "/tv/12/E01.mkv")}},
		report{series("120", 120), []mediasource.Item{episode("1201", 1, 1, "/tv/120/E01.mkv")}},
	)
	m12, m120 := movieKey(t, 12), movieKey(t, 120)
	e12, e120 := epKey(t, 12, 1, 1), epKey(t, 120, 1, 1)
	recordAttempts(t, s, attemptOf(instOf(t, s, m120), false, m120), attemptOf(instOf(t, s, e120), false, e120))

	t.Run("items tmdb-12 and tmdb-120", func(t *testing.T) {
		if row, _ := itemByKey(t, s, m12); row.HasHistory {
			t.Error("tmdb-12 HasHistory, from tmdb-120's download")
		}
		if row, _ := itemByKey(t, s, m120); !row.HasHistory {
			t.Error("tmdb-120 has no history")
		}
		if ds, _ := history(t, s, ItemHistory(m12), "", 10); len(ds) != 0 {
			t.Errorf("History(tmdb-12) = %+v, want none", ds)
		}
		if ds, _ := history(t, s, ItemHistory(m120), "", 10); len(ds) != 1 {
			t.Errorf("History(tmdb-120) has %d rows, want 1", len(ds))
		}
	})
	t.Run("roots tvdb-12 and tvdb-120", func(t *testing.T) {
		if row, _ := rootByKey(t, s, e12.Root()); row.HasHistory {
			t.Error("tvdb-12 HasHistory, from tvdb-120's download")
		}
		if row, _ := rootByKey(t, s, e120.Root()); !row.HasHistory {
			t.Error("tvdb-120 has no history")
		}
		if ds, _ := history(t, s, RootHistory(e12.Root()), "", 10); len(ds) != 0 {
			t.Errorf("History(tvdb-12) = %+v, want none", ds)
		}
		if ds, _ := history(t, s, RootHistory(e120.Root()), "", 10); len(ds) != 1 {
			t.Errorf("History(tvdb-120) has %d rows, want 1", len(ds))
		}
	})
}

// historyLog records one download of tmdb-8 (attempt 100), then seven of
// tmdb-7 with attempts 1 to 7.
func historyLog(t *testing.T) (*Store, media.Key) {
	t.Helper()
	s, _ := newLibrary(t, "radarr")
	k, other := movieKey(t, 7), movieKey(t, 8)
	d := attemptOf(1, false, other)
	d.Attempt = 100
	recordAttempts(t, s, d)
	for n := 1; n <= 7; n++ {
		d := attemptOf(1, false, k)
		d.Attempt = n
		recordAttempts(t, s, d)
	}
	return s, k
}

// deleteAttempt deletes k's download row of attempt n, which no statedb
// write does: a cursor naming it must still position the next page.
func deleteAttempt(t *testing.T, s *Store, k media.Key, n int) {
	t.Helper()
	var seq uint64
	if err := s.db.View(t.Context(), func(r *bolttable.ReadTx) error {
		return s.t.downloads.Scan(r, bolttable.Range{}, func(q uint64, v *downloadRow) (bool, error) {
			if v.Attempt == n && v.Items[0].k == k {
				seq = q
			}
			return true, nil
		})
	}); err != nil || seq == 0 {
		t.Fatalf("attempt %d of %s: seq %d, %v", n, k, seq, err)
	}
	update(t, s, func(tx *bolttable.Tx) error {
		_, err := s.t.downloads.Delete(tx, seq)
		return err
	})
}

func TestHistoryPagesExact(t *testing.T) {
	// Deleting attempt 6, the row the first page's cursor names, changes no
	// page: each row still comes back exactly once.
	scopes := []struct {
		scope func(media.Key) HistoryScope
		name  string
		pages [][]int
	}{
		{ItemHistory, "item", [][]int{{7, 6}, {5, 4}, {3, 2}, {1}}},
		{func(k media.Key) HistoryScope { return RootHistory(k.Root()) }, "root", [][]int{{7, 6}, {5, 4}, {3, 2}, {1}}},
		{func(media.Key) HistoryScope { return HistoryScope{} }, "whole log", [][]int{{7, 6}, {5, 4}, {3, 2}, {1, 100}}},
	}
	for _, sc := range scopes {
		for _, deleted := range []bool{false, true} {
			name := sc.name
			if deleted {
				name += " with the cursor's row deleted"
			}
			t.Run(name, func(t *testing.T) {
				s, k := historyLog(t)
				var got [][]int
				var c ListCursor
				for range 10 {
					ds, next := history(t, s, sc.scope(k), c, 2)
					var page []int
					for _, d := range ds {
						page = append(page, d.Attempt)
					}
					got = append(got, page)
					if deleted && c == "" {
						deleteAttempt(t, s, k, 6)
					}
					if c = next; c == "" {
						break
					}
				}
				if !slices.EqualFunc(got, sc.pages, slices.Equal) {
					t.Errorf("pages = %v, want %v", got, sc.pages)
				}
			})
		}
	}
}
