package search

import (
	"context"
	"fmt"
	"testing"

	"subflux/internal/api"
	"subflux/internal/scorer"
	"subflux/internal/search/syncing"
)

func BenchmarkSearchProviders(b *testing.B) {
	for _, n := range []int{1, 5, 10} {
		providers := make([]api.Provider, n)
		for i := range providers {
			providers[i] = &mockProvider{name: fmt.Sprintf("prov%d", i)}
		}
		e := New(providers,
			WithStore(&mockStore{}),
			WithConfig(&mockConfig{}),
			WithScorer(scorer.New(&api.DefaultScores)),
			WithSyncer(syncing.Syncer{}),
			WithTracks(noopDetector{}),
			WithTimeout(noopHealth{}),
		)
		req := &api.SearchRequest{
			MediaType: api.MediaTypeEpisode,
			Title:     "Breaking Bad",
			Season:    1,
			Episode:   3,
			ImdbID:    "tt0903747",
			Languages: []string{"en"},
			VideoPath: "/media/tv/breaking.bad.s01e03.mkv",
		}
		b.Run(fmt.Sprintf("providers=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			ctx := context.Background()
			for b.Loop() {
				_ = e.searchProvidersFilteredInner(ctx, req, providers)
			}
		})
	}
}

func BenchmarkBuildSearchKey(b *testing.B) {
	for _, n := range []int{1, 5, 10} {
		providers := make([]api.Provider, n)
		for i := range providers {
			providers[i] = &mockProvider{name: fmt.Sprintf("prov%d", i)}
		}
		req := &api.SearchRequest{
			MediaType: api.MediaTypeEpisode,
			Title:     "Breaking Bad",
			Season:    1,
			Episode:   3,
			ImdbID:    "tt0903747",
			Languages: []string{"en", "fr", "de"},
			VideoPath: "/media/tv/breaking.bad.s01e03.mkv",
			VideoHash: "abc123def456",
		}
		b.Run(fmt.Sprintf("providers=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = buildSearchKey(req, providers)
			}
		})
	}
}
