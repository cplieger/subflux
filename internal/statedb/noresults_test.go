package statedb

import (
	"slices"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

func putNoResults(t *testing.T, s *Store, rows ...NoResult) {
	t.Helper()
	if err := s.PutNoResults(t.Context(), rows); err != nil {
		t.Fatalf("PutNoResults: %v", err)
	}
}

func noResults(t *testing.T, s *Store, root media.RootKey, season uint16) []NoResult {
	t.Helper()
	rows, err := s.NoResults(t.Context(), root, season)
	if err != nil {
		t.Fatalf("NoResults: %v", err)
	}
	return rows
}

func noResultKeysOf(rows []NoResult) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Key.String()+" "+r.Tag.String()+" "+r.Provider)
	}
	return out
}

func TestNoResultsCascadeWithItem(t *testing.T) {
	s, _ := newLibrary(t, "sonarr", "radarr")
	fr := mustTag(t, "fr")
	e1, e2, e3 := epKey(t, showTVDB, 1, 1), epKey(t, showTVDB, 1, 2), epKey(t, showTVDB, 2, 1)
	all := []mediasource.Item{episode("e1", 1, 1, "/tv/Show/S01E01.mkv"), episode("e2", 1, 2, "/tv/Show/S01E02.mkv"), episode("e3", 2, 1, "/tv/Show/S02E01.mkv")}
	passOver(t, s, "sonarr", report{show, all})
	film := mediasource.Root{Native: mediasource.NativeRef{ID: "m1", Kind: media.Movie}, Title: "Film", External: media.ExternalIDs{TMDB: 603}}
	passOver(t, s, "radarr", report{film, []mediasource.Item{{Native: "m1", VideoPath: "/movies/Film.mkv"}}})
	movie, unrecorded := movieKey(t, 603), epKey(t, showTVDB, 1, 9)
	next := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	putNoResults(t, s,
		NoResult{Key: e2, Tag: enTag, Provider: "subdl", Count: 1, Next: next},
		NoResult{Key: e1, Tag: fr, Provider: "subdl", Count: 2, Next: next},
		NoResult{Key: e1, Tag: enTag, Provider: "subdl", Count: 3, Next: next},
		NoResult{Key: e3, Tag: enTag, Provider: "subdl", Count: 1, Next: next},
		NoResult{Key: movie, Tag: enTag, Provider: "assrt", Count: 4, Next: next},
		NoResult{Key: unrecorded, Tag: enTag, Provider: "subdl", Count: 1, Next: next},
	)
	t.Run("one season of a root reads its items' rows only", func(t *testing.T) {
		got := noResultKeysOf(noResults(t, s, show1Root(t), 1))
		want := []string{e1.String() + " en subdl", e1.String() + " fr subdl", e2.String() + " en subdl"}
		if !slices.Equal(got, want) {
			t.Errorf("NoResults(season 1) = %v, want %v", got, want)
		}
	})
	t.Run("season 0 reads a movie's", func(t *testing.T) {
		rows := noResults(t, s, movie.Root(), 0)
		if len(rows) != 1 || rows[0].Key != movie || rows[0].Count != 4 || !rows[0].Next.Equal(next) {
			t.Errorf("NoResults(movie) = %+v", rows)
		}
	})
	t.Run("a key with no item record stores nothing", func(t *testing.T) {
		if got := noResults(t, s, show1Root(t), 1); slices.ContainsFunc(got, func(r NoResult) bool { return r.Key == unrecorded }) {
			t.Errorf("NoResults kept a row for an unrecorded key: %+v", got)
		}
	})
	t.Run("count 0 deletes the row", func(t *testing.T) {
		putNoResults(t, s, NoResult{Key: e1, Tag: fr, Provider: "subdl"})
		got := noResultKeysOf(noResults(t, s, show1Root(t), 1))
		if slices.Contains(got, e1.String()+" fr subdl") || len(got) != 2 {
			t.Errorf("after a count-0 put, NoResults = %v", got)
		}
	})
	t.Run("deleting the item record deletes its rows in the same write", func(t *testing.T) {
		before := headRev(t, s)
		passOver(t, s, "sonarr", report{show, []mediasource.Item{all[0], all[2]}})
		if _, _, found := holderOf(t, s, e2); found {
			t.Fatal("e2 kept its item record")
		}
		got := noResultKeysOf(noResults(t, s, show1Root(t), 1))
		if !slices.Equal(got, []string{e1.String() + " en subdl"}) {
			t.Errorf("after e2's record went, NoResults = %v", got)
		}
		if headRev(t, s) != before+1 {
			t.Errorf("the retiring pass took revisions %d to %d, want one", before, headRev(t, s))
		}
	})
	t.Run("a negative count is refused, writing nothing", func(t *testing.T) {
		if err := s.PutNoResults(t.Context(), []NoResult{{Key: e3, Tag: fr, Provider: "x", Count: 1}, {Key: e3, Tag: enTag, Provider: "subdl", Count: -1}}); err == nil {
			t.Fatal("accepted")
		}
		if got := noResults(t, s, show1Root(t), 2); len(got) != 1 || got[0].Count != 1 {
			t.Errorf("NoResults(season 2) = %+v after a refused put", got)
		}
	})
}

func show1Root(t *testing.T) media.RootKey {
	t.Helper()
	return epKey(t, showTVDB, 1, 1).Root()
}
