package seriesorder

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediasource"
)

func reading(fetched time.Time, title string) mediasource.SeriesEvidence {
	return mediasource.SeriesEvidence{
		Catalogue: mediasource.Catalogue{
			Fetched:  fetched,
			Episodes: []mediasource.CatalogueEpisode{{AirDate: "2002-09-20", ID: 1, Season: 1, Episode: 1}},
			TVDB:     4242,
		},
		Titles: []mediasource.TitleSet{{Language: english, Found: true, Titles: []mediasource.EpisodeTitle{{Title: title, ID: 1}}}},
	}
}

func TestEvidenceReadsBound(t *testing.T) {
	r := newRig()
	q := query("sonarr", 4242, dvdOrder, language.Tag{}, nil)
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	bound, held := reading(t0, "Bound title"), reading(t0.Add(time.Hour), "Held title")
	r.store.observe(t, q, &mediasource.RootEvidence{Bound: bound, Held: &held})
	got, ok, err := r.svc.Evidence(t.Context(), q.Source, q.Root)
	if err != nil || !ok {
		t.Fatalf("Evidence = %v, %v", ok, err)
	}
	want, _ := json.Marshal(bound)
	if b, _ := json.Marshal(got); !bytes.Equal(b, want) {
		t.Errorf("Evidence = %s, want the Bound reading %s", b, want)
	}
	if sent := r.sky.take(); len(sent) != 0 {
		t.Errorf("Evidence sent %v", sent)
	}
	other := query("sonarr", 4243, dvdOrder, language.Tag{}, nil)
	if _, ok, err := r.svc.Evidence(t.Context(), other.Source, other.Root); ok || err != nil {
		t.Errorf("Evidence of a root with none = %v, %v; want false", ok, err)
	}
}
