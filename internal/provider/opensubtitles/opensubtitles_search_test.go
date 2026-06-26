package opensubtitles

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// --- Episode Numberings ---

func TestEpisodeNumberings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *api.SearchRequest
		want []numbering
	}{
		{
			name: "movie returns single aired entry",
			req:  &api.SearchRequest{MediaType: "movie", Season: 0, Episode: 0},
			want: []numbering{{scheme: "aired", season: 0, episode: 0}},
		},
		{
			name: "episode with no alternates returns single aired",
			req:  &api.SearchRequest{MediaType: "episode", Season: 2, Episode: 5},
			want: []numbering{{scheme: "aired", season: 2, episode: 5}},
		},
		{
			name: "episode with scene numbering adds scene entry",
			req: &api.SearchRequest{
				MediaType: "episode",
				Season:    2, Episode: 5,
				SceneSeason: 3, SceneEpisode: 10,
			},
			want: []numbering{
				{scheme: "aired", season: 2, episode: 5},
				{scheme: "scene", season: 3, episode: 10},
			},
		},
		{
			name: "duplicate scene numbering deduped",
			req: &api.SearchRequest{
				MediaType: "episode",
				Season:    2, Episode: 5,
				SceneSeason: 2, SceneEpisode: 5,
			},
			want: []numbering{
				{scheme: "aired", season: 2, episode: 5},
			},
		},
		{
			name: "absolute episode with scene season",
			req: &api.SearchRequest{
				MediaType: "episode",
				Season:    1, Episode: 3,
				SceneSeason:     2,
				AbsoluteEpisode: 50,
			},
			want: []numbering{
				{scheme: "aired", season: 1, episode: 3},
				{scheme: "absolute", season: 2, episode: 50},
			},
		},
		{
			name: "absolute episode defaults to season 1 when no scene season",
			req: &api.SearchRequest{
				MediaType: "episode",
				Season:    1, Episode: 3,
				AbsoluteEpisode: 50,
			},
			want: []numbering{
				{scheme: "aired", season: 1, episode: 3},
				{scheme: "absolute", season: 1, episode: 50},
			},
		},
		{
			// Absolute-only request (aired episode is 0, so no aired entry):
			// the absolute scheme must default its season to 1, NOT inherit
			// the aired season (5). add() only substitutes req.Season for a
			// season <= 0, and absSeason is defaulted to 1 before that.
			name: "absolute-only defaults to season 1 not aired season",
			req: &api.SearchRequest{
				MediaType:       "episode",
				Season:          5,
				Episode:         0,
				SceneSeason:     0,
				SceneEpisode:    0,
				AbsoluteEpisode: 99,
			},
			want: []numbering{
				{scheme: "absolute", season: 1, episode: 99},
			},
		},
		{
			name: "zero episode skipped for scene",
			req: &api.SearchRequest{
				MediaType: "episode",
				Season:    1, Episode: 5,
				SceneSeason: 2, SceneEpisode: 0,
			},
			want: []numbering{
				{scheme: "aired", season: 1, episode: 5},
			},
		},
		{
			name: "zero season defaults to aired season",
			req: &api.SearchRequest{
				MediaType: "episode",
				Season:    3, Episode: 5,
				SceneSeason: 0, SceneEpisode: 10,
			},
			want: []numbering{
				{scheme: "aired", season: 3, episode: 5},
				{scheme: "scene", season: 3, episode: 10},
			},
		},
		{
			name: "all three numbering schemes",
			req: &api.SearchRequest{
				MediaType: "episode",
				Season:    1, Episode: 1,
				SceneSeason: 2, SceneEpisode: 3,
				AbsoluteEpisode: 100,
			},
			want: []numbering{
				{scheme: "aired", season: 1, episode: 1},
				{scheme: "scene", season: 2, episode: 3},
				{scheme: "absolute", season: 2, episode: 100},
			},
		},
		{
			name: "zero aired episode still included",
			req:  &api.SearchRequest{MediaType: "episode", Season: 1, Episode: 0},
			want: nil,
		},
		{
			name: "zero aired episode but valid scene episode",
			req: &api.SearchRequest{
				MediaType:    "episode",
				Season:       1,
				Episode:      0,
				SceneSeason:  2,
				SceneEpisode: 5,
			},
			want: []numbering{
				{scheme: "scene", season: 2, episode: 5},
			},
		},
		{
			name: "negative episode values skipped",
			req: &api.SearchRequest{
				MediaType:       "episode",
				Season:          1,
				Episode:         -1,
				SceneSeason:     2,
				SceneEpisode:    -5,
				AbsoluteEpisode: -10,
			},
			want: nil,
		},
		{
			name: "negative season defaults to aired season",
			req: &api.SearchRequest{
				MediaType:    "episode",
				Season:       3,
				Episode:      5,
				SceneSeason:  -1,
				SceneEpisode: 10,
			},
			want: []numbering{
				{scheme: "aired", season: 3, episode: 5},
				{scheme: "scene", season: 3, episode: 10},
			},
		},
		{
			name: "season 0 specials skip absolute episode",
			req: &api.SearchRequest{
				MediaType:       "episode",
				Season:          0,
				Episode:         1,
				AbsoluteEpisode: 6,
			},
			want: []numbering{
				{scheme: "aired", season: 0, episode: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := episodeNumberings(tt.req)
			if len(got) != len(tt.want) {
				t.Fatalf("episodeNumberings() = %d entries, want %d: %+v",
					len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("episodeNumberings()[%d] = %+v, want %+v",
						i, got[i], tt.want[i])
				}
			}
		})
	}
}

// --- Search Result Filtering ---

func TestFilterSearchResults(t *testing.T) {
	t.Parallel()

	makeResult := func(lang, release string, fileID int, opts ...func(*searchAttributes)) searchResult {
		attr := searchAttributes{
			Language: lang,
			Release:  release,
			Files:    []searchFile{{FileID: fileID}},
		}
		for _, o := range opts {
			o(&attr)
		}
		return searchResult{Attributes: attr}
	}
	withAI := func(a *searchAttributes) { a.AITranslated = true }
	withMachine := func(a *searchAttributes) { a.MachineTranslated = true }
	withHash := func(a *searchAttributes) { a.MoviehashMatch = true }
	withHI := func(a *searchAttributes) { a.HearingImpaired = true }
	withForeign := func(a *searchAttributes) { a.ForeignPartsOnly = true }
	withNoFiles := func(a *searchAttributes) { a.Files = nil }

	t.Run("nil data returns nil", func(t *testing.T) {
		t.Parallel()
		got := filterSearchResults(nil, []string{"en"}, false)
		if got != nil {
			t.Errorf("filterSearchResults(nil) = %v, want nil", got)
		}
	})

	t.Run("empty data returns nil", func(t *testing.T) {
		t.Parallel()
		got := filterSearchResults([]searchResult{}, []string{"en"}, false)
		if got != nil {
			t.Errorf("filterSearchResults([]) = %v, want nil", got)
		}
	})

	t.Run("basic result mapped correctly", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("en", "Test.Release", 42)}
		got := filterSearchResults(data, []string{"en"}, false)
		if len(got) != 1 {
			t.Fatalf("filterSearchResults() = %d results, want 1", len(got))
		}
		if got[0].Provider != api.ProviderNameOpenSubtitles {
			t.Errorf("Provider = %q, want %q", got[0].Provider, api.ProviderNameOpenSubtitles)
		}
		if got[0].ID != "42" {
			t.Errorf("ID = %q, want %q", got[0].ID, "42")
		}
		if got[0].Language != "en" {
			t.Errorf("Language = %q, want %q", got[0].Language, "en")
		}
		if got[0].ReleaseName != "Test.Release" {
			t.Errorf("ReleaseName = %q, want %q", got[0].ReleaseName, "Test.Release")
		}
		if got[0].MatchedBy != "title" {
			t.Errorf("MatchedBy = %q, want %q", got[0].MatchedBy, "title")
		}
	})

	t.Run("hash match sets matched_by to hash", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("en", "rel", 1, withHash)}
		got := filterSearchResults(data, []string{"en"}, false)
		if len(got) != 1 {
			t.Fatalf("filterSearchResults() = %d results, want 1", len(got))
		}
		if got[0].MatchedBy != "hash" {
			t.Errorf("MatchedBy = %q, want %q", got[0].MatchedBy, "hash")
		}
	})

	t.Run("AI translated excluded when includeAI false", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("en", "rel", 1, withAI)}
		got := filterSearchResults(data, []string{"en"}, false)
		if len(got) != 0 {
			t.Errorf("filterSearchResults() = %d results, want 0", len(got))
		}
	})

	t.Run("AI translated included when includeAI true", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("en", "rel", 1, withAI)}
		got := filterSearchResults(data, []string{"en"}, true)
		if len(got) != 1 {
			t.Errorf("filterSearchResults() = %d results, want 1", len(got))
		}
	})

	t.Run("machine translated always excluded", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("en", "rel", 1, withMachine)}
		got := filterSearchResults(data, []string{"en"}, true)
		if len(got) != 0 {
			t.Errorf("filterSearchResults() = %d results, want 0", len(got))
		}
	})

	t.Run("no files skipped", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("en", "rel", 1, withNoFiles)}
		got := filterSearchResults(data, []string{"en"}, false)
		if len(got) != 0 {
			t.Errorf("filterSearchResults() = %d results, want 0", len(got))
		}
	})

	t.Run("language not in requested list skipped", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("fr", "rel", 1)}
		got := filterSearchResults(data, []string{"en"}, false)
		if len(got) != 0 {
			t.Errorf("filterSearchResults() = %d results, want 0", len(got))
		}
	})

	t.Run("OS language mapped before filtering", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("pt-PT", "rel", 1)}
		got := filterSearchResults(data, []string{"pt"}, false)
		if len(got) != 1 {
			t.Fatalf("filterSearchResults() = %d results, want 1", len(got))
		}
		if got[0].Language != "pt" {
			t.Errorf("Language = %q, want %q", got[0].Language, "pt")
		}
	})

	t.Run("hearing impaired flag preserved", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("en", "rel", 1, withHI)}
		got := filterSearchResults(data, []string{"en"}, false)
		if len(got) != 1 {
			t.Fatalf("filterSearchResults() = %d results, want 1", len(got))
		}
		if !got[0].HearingImp {
			t.Error("HearingImp = false, want true")
		}
		if got[0].Forced {
			t.Error("Forced = true, want false (HI suppresses forced)")
		}
	})

	t.Run("foreign parts only sets forced when not HI", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("en", "rel", 1, withForeign)}
		got := filterSearchResults(data, []string{"en"}, false)
		if len(got) != 1 {
			t.Fatalf("filterSearchResults() = %d results, want 1", len(got))
		}
		if !got[0].Forced {
			t.Error("Forced = false, want true")
		}
	})

	t.Run("foreign parts only suppressed by HI", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("en", "rel", 1, withForeign, withHI)}
		got := filterSearchResults(data, []string{"en"}, false)
		if len(got) != 1 {
			t.Fatalf("filterSearchResults() = %d results, want 1", len(got))
		}
		if got[0].Forced {
			t.Error("Forced = true, want false (HI suppresses forced)")
		}
	})

	t.Run("ea language mapped to es through filter", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("ea", "spanish-rel", 10)}
		got := filterSearchResults(data, []string{"es"}, false)
		if len(got) != 1 {
			t.Fatalf("filterSearchResults() = %d results, want 1", len(got))
		}
		if got[0].Language != "es" {
			t.Errorf("Language = %q, want %q", got[0].Language, "es")
		}
	})

	t.Run("zh-CN language mapped to zh through filter", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("zh-CN", "chinese-rel", 11)}
		got := filterSearchResults(data, []string{"zh"}, false)
		if len(got) != 1 {
			t.Fatalf("filterSearchResults() = %d results, want 1", len(got))
		}
		if got[0].Language != "zh" {
			t.Errorf("Language = %q, want %q", got[0].Language, "zh")
		}
	})

	t.Run("nil languages returns no results", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("en", "rel", 1)}
		got := filterSearchResults(data, nil, false)
		if len(got) != 0 {
			t.Errorf("filterSearchResults() = %d results, want 0", len(got))
		}
	})

	t.Run("empty languages returns no results", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("en", "rel", 1)}
		got := filterSearchResults(data, []string{}, false)
		if len(got) != 0 {
			t.Errorf("filterSearchResults() = %d results, want 0", len(got))
		}
	})

	t.Run("feature details mapped correctly", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{{
			Attributes: searchAttributes{
				Language: "en",
				Release:  "Test.Release",
				Files:    []searchFile{{FileID: 99}},
				FeatureDetails: featureDetails{
					Title:         "Breaking Bad",
					Year:          2008,
					SeasonNumber:  5,
					EpisodeNumber: 16,
				},
			},
		}}
		got := filterSearchResults(data, []string{"en"}, false)
		if len(got) != 1 {
			t.Fatalf("filterSearchResults() = %d results, want 1", len(got))
		}
		if got[0].Title != "Breaking Bad" {
			t.Errorf("Title = %q, want %q", got[0].Title, "Breaking Bad")
		}
		if got[0].Year != 2008 {
			t.Errorf("Year = %d, want %d", got[0].Year, 2008)
		}
		if got[0].Season != 5 {
			t.Errorf("Season = %d, want %d", got[0].Season, 5)
		}
		if got[0].Episode != 16 {
			t.Errorf("Episode = %d, want %d", got[0].Episode, 16)
		}
	})

	t.Run("multiple results filtered correctly", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{
			makeResult("en", "good", 1),
			makeResult("en", "ai", 2, withAI),
			makeResult("fr", "wrong-lang", 3),
			makeResult("en", "also-good", 4),
		}
		got := filterSearchResults(data, []string{"en"}, false)
		if len(got) != 2 {
			t.Fatalf("filterSearchResults() = %d results, want 2", len(got))
		}
		if got[0].ID != "1" {
			t.Errorf("got[0].ID = %q, want %q", got[0].ID, "1")
		}
		if got[1].ID != "4" {
			t.Errorf("got[1].ID = %q, want %q", got[1].ID, "4")
		}
	})

	t.Run("machine translated excluded even when AI included", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("en", "rel", 1, withAI, withMachine)}
		got := filterSearchResults(data, []string{"en"}, true)
		if len(got) != 0 {
			t.Errorf("filterSearchResults() = %d results, want 0 (machine always excluded)", len(got))
		}
	})

	t.Run("multiple requested languages all matched", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{
			makeResult("en", "english-rel", 1),
			makeResult("fr", "french-rel", 2),
			makeResult("de", "german-rel", 3),
		}
		got := filterSearchResults(data, []string{"en", "fr"}, false)
		if len(got) != 2 {
			t.Fatalf("filterSearchResults() = %d results, want 2", len(got))
		}
		if got[0].Language != "en" {
			t.Errorf("got[0].Language = %q, want %q", got[0].Language, "en")
		}
		if got[1].Language != "fr" {
			t.Errorf("got[1].Language = %q, want %q", got[1].Language, "fr")
		}
	})

	t.Run("pt-BR language mapped to pb through filter", func(t *testing.T) {
		t.Parallel()
		data := []searchResult{makeResult("pt-BR", "brazilian-rel", 12)}
		got := filterSearchResults(data, []string{"pb"}, false)
		if len(got) != 1 {
			t.Fatalf("filterSearchResults() = %d results, want 1", len(got))
		}
		if got[0].Language != "pb" {
			t.Errorf("Language = %q, want %q", got[0].Language, "pb")
		}
	})
}
