package arrsvc

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestEpisodeStore_residentSetStaysWithinTheBound(t *testing.T) {
	s := newEpisodeStore()
	for i := range maxResidentEpisodeEntries * 4 {
		s.put(episodesKey(i), readEntry{payload: []int{i}, readBegin: time.Now()})
		if got := s.resident(); got > maxResidentEpisodeEntries {
			t.Fatalf("after %d distinct puts: resident = %d, want <= %d", i+1, got, maxResidentEpisodeEntries)
		}
	}
}

func TestEpisodeStore_overwritingAHeldKeyNeverForcesAReclaim(t *testing.T) {
	s := newEpisodeStore()
	for i := range maxResidentEpisodeEntries {
		s.put(episodesKey(i), readEntry{payload: i, readBegin: time.Now()})
	}
	if got := s.resident(); got != maxResidentEpisodeEntries {
		t.Fatalf("resident = %d, want the full %d", got, maxResidentEpisodeEntries)
	}

	// The map is exactly full and every entry is live, so a fresh key would
	// have to drop the generation. Rewriting a key already held must not.
	s.put(episodesKey(0), readEntry{payload: "refreshed", readBegin: time.Now()})
	if got := s.resident(); got != maxResidentEpisodeEntries {
		t.Errorf("resident after an overwrite = %d, want the untouched %d", got, maxResidentEpisodeEntries)
	}
	e, ok := s.get(episodesKey(maxResidentEpisodeEntries - 1))
	if !ok {
		t.Fatal("an unrelated live entry was dropped by an overwrite")
	}
	if e.payload != maxResidentEpisodeEntries-1 {
		t.Errorf("unrelated entry payload = %v, want %d", e.payload, maxResidentEpisodeEntries-1)
	}
}

func TestEpisodeStore_reclaimsExpiredEntriesBeforeDroppingLiveOnes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newEpisodeStore()
		for i := range maxResidentEpisodeEntries - 1 {
			s.put(episodesKey(i), readEntry{payload: i, readBegin: time.Now()})
		}

		time.Sleep(arrCacheTTL + time.Millisecond) // every entry so far is now a miss
		live := episodesKey(maxResidentEpisodeEntries)
		s.put(live, readEntry{payload: "live", readBegin: time.Now()}) // fills the map

		// The next new key finds the map full: the expired entries pay for it,
		// so the live one survives and nothing is dropped wholesale.
		s.put(episodesKey(maxResidentEpisodeEntries+1), readEntry{payload: "newest", readBegin: time.Now()})
		if got := s.resident(); got != 2 {
			t.Errorf("resident after the reclaim = %d, want 2 (the live entry and the newest)", got)
		}
		if _, ok := s.get(live); !ok {
			t.Error("the live entry was discarded while expired entries were reclaimable")
		}
	})
}

func TestEpisodeStore_expiredEntryReadsAsAMiss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newEpisodeStore()
		s.put(episodesKey(1), readEntry{payload: "v", readBegin: time.Now()})
		if _, ok := s.get(episodesKey(1)); !ok {
			t.Fatal("a fresh entry read as a miss")
		}
		time.Sleep(arrCacheTTL + time.Millisecond)
		if _, ok := s.get(episodesKey(1)); ok {
			t.Errorf("an entry past %v was served", arrCacheTTL)
		}
	})
}

func TestIsEpisodesKey_routesOnlyTheEpisodesFamily(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{key: episodesKey(7), want: true},
		{key: keySeriesList, want: false},
		{key: keyMovieList, want: false},
		{key: tagsKey([]string{"anime"}), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			if got := isEpisodesKey(tc.key); got != tc.want {
				t.Errorf("isEpisodesKey(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

// The table routes by family both ways: an episodes entry lands in the bounded
// store and every other family in the shared cache, so neither can be read out
// of the other's.
func TestReadTable_routesEachFamilyToItsOwnStore(t *testing.T) {
	tbl := newTestTable(t.Context())
	epKey := episodesKey(3)
	tbl.put(epKey, readEntry{payload: "episodes", readBegin: time.Now()})
	tbl.put(keySeriesList, readEntry{payload: "series", readBegin: time.Now()})

	if got := tbl.episodes.resident(); got != 1 {
		t.Errorf("bounded store holds %d entries, want just the episodes one", got)
	}
	if _, ok := tbl.cache.Get(epKey); ok {
		t.Error("the episodes entry also landed in the shared cache")
	}
	if e, ok := tbl.lookup(epKey); !ok || e.payload != "episodes" {
		t.Errorf("lookup(%q) = %v (ok=%v), want the bounded store's entry", epKey, e.payload, ok)
	}
	if e, ok := tbl.lookup(keySeriesList); !ok || e.payload != "series" {
		t.Errorf("lookup(series) = %v (ok=%v), want the shared cache's entry", e.payload, ok)
	}
}

func TestEpisodeStore_concurrentPutsRespectTheBound(t *testing.T) {
	s := newEpisodeStore()
	const writers, perWriter = 8, 64
	done := make(chan struct{})
	for w := range writers {
		go func() {
			for i := range perWriter {
				s.put(episodesKey(w*perWriter+i), readEntry{payload: i, readBegin: time.Now()})
			}
			done <- struct{}{}
		}()
	}
	for range writers {
		<-done
	}
	if got := s.resident(); got > maxResidentEpisodeEntries {
		t.Errorf("resident after %d concurrent puts = %d, want <= %d",
			writers*perWriter, got, maxResidentEpisodeEntries)
	}
}
