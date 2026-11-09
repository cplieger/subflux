package seriesorder

import (
	"log/slog"
	"testing"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr/arrtest"
	"github.com/cplieger/subflux/internal/mediasource/sonarr"
)

// TestNonAiredSeriesKeyedAired drives a Set over the Sonarr adapter on the
// recorded Sonarr v5 (API v5) Firefly, in DVD order, joined through this
// service over SkyHook's recorded Firefly and Monogatari lists.
func TestNonAiredSeriesKeyedAired(t *testing.T) {
	const source media.SourceID = "tv"
	r := newRig()
	r.sky.record(t, firefly)
	r.sky.record(t, monogatari)
	srv := arrtest.Load(t, "../mediasource/sonarr/testdata/v5-api-v5")
	cfg := arrtest.Config(t, "Sonarr", "http://sonarr.test", "")
	cfg.ID = source
	set, err := mediasource.NewSet([]mediasource.AdapterKind{sonarr.Kind}, []mediasource.Config{cfg}, mediasource.Deps{HTTP: srv.Client(), Logger: slog.New(slog.DiscardHandler)}, r.svc)
	if err != nil {
		t.Fatal(err)
	}
	var (
		root  mediasource.Root
		items []mediasource.Item
	)
	err = set.Items(t.Context(), source, func(got mediasource.Root, its []mediasource.Item) error {
		if got.External.TVDB == firefly {
			root, items = got, its
		}
		return nil
	}, func(ref mediasource.NativeRef, err *mediasource.SourceError) error {
		t.Errorf("unread %v: %v", ref, err)
		return nil
	})
	if err != nil || len(items) == 0 {
		t.Fatalf("Items = %v with %d Firefly items", err, len(items))
	}
	if want := (mediasource.Order{Type: "dvd", Name: "DVD Order"}); root.Order != want {
		t.Errorf("Order = %+v, want %+v", root.Order, want)
	}
	var aired int
	for _, e := range readRecording(t, firefly, english, "official").Episodes {
		if e.Title == "Serenity" && e.Season == 1 {
			aired = e.Absolute
		}
	}
	dvd := media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}
	for _, it := range items {
		if it.SeriesNumbering != dvd {
			continue
		}
		if want := (media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 11}); it.Title != "Serenity" || it.Numbering != want || it.Absolute != aired || aired == 0 {
			t.Errorf("DVD S01E01 = %q Numbering %+v Absolute %d, want Serenity at aired %+v, absolute %d", it.Title, it.Numbering, it.Absolute, want, aired)
		}
		return
	}
	t.Errorf("no item lists DVD S01E01")
}
