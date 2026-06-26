package opensubtitles

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// --- Search Parameter Building ---

func TestBuildSearchParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		req       *api.SearchRequest
		name      string
		wantKey   string
		wantValue string
		useHash   bool
		includeAI bool
	}{
		{
			name:      "languages sorted and mapped",
			req:       &api.SearchRequest{Languages: []string{"zh", "en", "pt"}},
			wantKey:   "languages",
			wantValue: "en,pt-PT,zh-CN",
		},
		{
			name:      "single language",
			req:       &api.SearchRequest{Languages: []string{"fr"}},
			wantKey:   "languages",
			wantValue: "fr",
		},
		{
			name:    "hash included when enabled and present",
			useHash: true,
			req: &api.SearchRequest{
				Languages: []string{"en"},
				VideoHash: "abc123",
			},
			wantKey:   "moviehash",
			wantValue: "abc123",
		},
		{
			name:    "hash omitted when disabled",
			useHash: false,
			req: &api.SearchRequest{
				Languages: []string{"en"},
				VideoHash: "abc123",
			},
			wantKey:   "moviehash",
			wantValue: "", // absent
		},
		{
			name:    "hash omitted when empty despite enabled",
			useHash: true,
			req: &api.SearchRequest{
				Languages: []string{"en"},
				VideoHash: "",
			},
			wantKey:   "moviehash",
			wantValue: "", // absent
		},
		{
			name: "imdb_id sanitized",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				ImdbID:    "tt1234567",
			},
			wantKey:   "imdb_id",
			wantValue: "1234567",
		},
		{
			name: "episode params set for episodes",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				MediaType: "episode",
				Season:    2,
				Episode:   5,
			},
			wantKey:   "episode_number",
			wantValue: "5",
		},
		{
			name: "season params set for episodes",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				MediaType: "episode",
				Season:    3,
				Episode:   1,
			},
			wantKey:   "season_number",
			wantValue: "3",
		},
		{
			name: "episode params omitted for movies",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				MediaType: "movie",
				Season:    1,
				Episode:   1,
			},
			wantKey:   "episode_number",
			wantValue: "", // absent
		},
		{
			name: "season params omitted for movies",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				MediaType: "movie",
				Season:    1,
				Episode:   1,
			},
			wantKey:   "season_number",
			wantValue: "", // absent
		},
		{
			name: "zero episode omitted",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				MediaType: "episode",
				Season:    1,
				Episode:   0,
			},
			wantKey:   "episode_number",
			wantValue: "", // absent
		},
		{
			name: "zero season omitted",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				MediaType: "episode",
				Season:    0,
				Episode:   1,
			},
			wantKey:   "season_number",
			wantValue: "", // absent
		},
		{
			name:      "ai_translated excluded by default",
			includeAI: false,
			req:       &api.SearchRequest{Languages: []string{"en"}},
			wantKey:   "ai_translated",
			wantValue: "exclude",
		},
		{
			name:      "ai_translated not excluded when included",
			includeAI: true,
			req:       &api.SearchRequest{Languages: []string{"en"}},
			wantKey:   "ai_translated",
			wantValue: "", // absent
		},
		{
			name:      "empty languages",
			req:       &api.SearchRequest{},
			wantKey:   "languages",
			wantValue: "",
		},
		{
			name: "imdb_id numeric passthrough",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				ImdbID:    "1234567",
			},
			wantKey:   "imdb_id",
			wantValue: "1234567",
		},
		{
			name: "imdb_id omitted when empty",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				ImdbID:    "",
			},
			wantKey:   "imdb_id",
			wantValue: "", // absent
		},
		{
			name: "imdb_id with leading zeros stripped",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				ImdbID:    "tt0012345",
			},
			wantKey:   "imdb_id",
			wantValue: "12345",
		},
		{
			name: "tmdb_id preferred for movies",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				MediaType: "movie",
				TmdbID:    550,
				ImdbID:    "tt0137523",
			},
			wantKey:   "tmdb_id",
			wantValue: "550",
		},
		{
			name: "tmdb_id omitted for episodes",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				MediaType: "episode",
				TmdbID:    550,
				ImdbID:    "tt0137523",
				Season:    1,
				Episode:   1,
			},
			wantKey:   "tmdb_id",
			wantValue: "", // absent — episodes use parent_imdb_id
		},
		{
			name: "parent_imdb_id used for episodes",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				MediaType: "episode",
				ImdbID:    "tt5923028",
				Season:    1,
				Episode:   1,
			},
			wantKey:   "parent_imdb_id",
			wantValue: "5923028",
		},
		{
			name: "imdb_id absent for episodes",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				MediaType: "episode",
				ImdbID:    "tt5923028",
				Season:    1,
				Episode:   1,
			},
			wantKey:   "imdb_id",
			wantValue: "", // absent — episodes use parent_imdb_id
		},
		{
			name: "imdb_id fallback when tmdb_id zero for movie",
			req: &api.SearchRequest{
				Languages: []string{"en"},
				MediaType: "movie",
				ImdbID:    "tt0137523",
			},
			wantKey:   "imdb_id",
			wantValue: "137523",
		},
		{
			name:    "no tmdb_id when both tmdb and imdb empty for movie",
			req:     &api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"},
			wantKey: "tmdb_id",
		},
		{
			name:    "no imdb_id when both tmdb and imdb empty for movie",
			req:     &api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"},
			wantKey: "imdb_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Provider{
				useHash:   tt.useHash,
				includeAI: tt.includeAI,
			}
			params := p.buildSearchParams(tt.req,
				tt.req.Season, tt.req.Episode)
			got := params.Get(tt.wantKey)
			if got != tt.wantValue {
				t.Errorf("buildSearchParams() %q = %q, want %q",
					tt.wantKey, got, tt.wantValue)
			}
		})
	}
}

// --- Query Parameter Building ---

func TestBuildQueryParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		req       *api.SearchRequest
		name      string
		wantKey   string
		wantValue string
		season    int
		episode   int
		includeAI bool
	}{
		{
			name:      "title set as query",
			req:       &api.SearchRequest{Title: "Breaking Bad", Languages: []string{"en"}},
			wantKey:   "query",
			wantValue: "Breaking Bad",
		},
		{
			name:      "languages sorted and mapped",
			req:       &api.SearchRequest{Title: "test", Languages: []string{"zh", "en", "pt"}},
			wantKey:   "languages",
			wantValue: "en,pt-PT,zh-CN",
		},
		{
			name:      "episode params set",
			req:       &api.SearchRequest{Title: "test", Languages: []string{"en"}, MediaType: "episode"},
			season:    3,
			episode:   7,
			wantKey:   "episode_number",
			wantValue: "7",
		},
		{
			name:      "season params set",
			req:       &api.SearchRequest{Title: "test", Languages: []string{"en"}, MediaType: "episode"},
			season:    3,
			episode:   7,
			wantKey:   "season_number",
			wantValue: "3",
		},
		{
			name:      "episode params omitted for movies",
			req:       &api.SearchRequest{Title: "test", Languages: []string{"en"}, MediaType: "movie"},
			season:    1,
			episode:   1,
			wantKey:   "episode_number",
			wantValue: "",
		},
		{
			name:      "zero episode omitted",
			req:       &api.SearchRequest{Title: "test", Languages: []string{"en"}, MediaType: "episode"},
			season:    1,
			episode:   0,
			wantKey:   "episode_number",
			wantValue: "",
		},
		{
			name:      "zero season omitted",
			req:       &api.SearchRequest{Title: "test", Languages: []string{"en"}, MediaType: "episode"},
			season:    0,
			episode:   1,
			wantKey:   "season_number",
			wantValue: "",
		},
		{
			name:      "ai_translated excluded by default",
			req:       &api.SearchRequest{Title: "test", Languages: []string{"en"}},
			includeAI: false,
			wantKey:   "ai_translated",
			wantValue: "exclude",
		},
		{
			name:      "ai_translated not excluded when included",
			req:       &api.SearchRequest{Title: "test", Languages: []string{"en"}},
			includeAI: true,
			wantKey:   "ai_translated",
			wantValue: "",
		},
		{
			name:      "no imdb_id or tmdb_id in query params",
			req:       &api.SearchRequest{Title: "test", Languages: []string{"en"}, ImdbID: "tt1234567", TmdbID: 550},
			wantKey:   "imdb_id",
			wantValue: "",
		},
		{
			name:      "no moviehash in query params",
			req:       &api.SearchRequest{Title: "test", Languages: []string{"en"}, VideoHash: "abc123"},
			wantKey:   "moviehash",
			wantValue: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Provider{includeAI: tt.includeAI}
			season := tt.season
			if season == 0 && tt.req.Season > 0 {
				season = tt.req.Season
			}
			episode := tt.episode
			if episode == 0 && tt.req.Episode > 0 {
				episode = tt.req.Episode
			}
			params := p.buildQueryParams(tt.req, season, episode)
			got := params.Get(tt.wantKey)
			if got != tt.wantValue {
				t.Errorf("buildQueryParams() %q = %q, want %q",
					tt.wantKey, got, tt.wantValue)
			}
		})
	}
}

func TestJoinOSLangs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		want  string
		langs []string
	}{
		{name: "nil returns empty", langs: nil, want: ""},
		{name: "empty returns empty", langs: []string{}, want: ""},
		{name: "single language", langs: []string{"en"}, want: "en"},
		{name: "multiple sorted", langs: []string{"fr", "en"}, want: "en,fr"},
		{name: "mapped languages", langs: []string{"zh", "pt"}, want: "pt-PT,zh-CN"},
		{name: "mixed mapped and unmapped", langs: []string{"zh", "en", "pt"}, want: "en,pt-PT,zh-CN"},
		{name: "pb maps to pt-BR", langs: []string{"pb"}, want: "pt-BR"},
		{name: "already sorted", langs: []string{"de", "en", "fr"}, want: "de,en,fr"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := joinOSLangs(tt.langs)
			if got != tt.want {
				t.Errorf("joinOSLangs(%v) = %q, want %q",
					tt.langs, got, tt.want)
			}
		})
	}
}

// --- Empty-sanitized-IMDB guard ---

func TestBuildSearchParams_skips_empty_sanitized_imdb(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		imdb      string
		mediaType string
		absentKey string
	}{
		{name: "episode with tt0 skips parent_imdb_id", imdb: "tt0", mediaType: "episode", absentKey: "parent_imdb_id"},
		{name: "episode with tt00000 skips parent_imdb_id", imdb: "tt00000", mediaType: "episode", absentKey: "parent_imdb_id"},
		{name: "episode with bare tt skips parent_imdb_id", imdb: "tt", mediaType: "episode", absentKey: "parent_imdb_id"},
		{name: "movie with tt0 skips imdb_id", imdb: "tt0", mediaType: "movie", absentKey: "imdb_id"},
		{name: "movie with 0000 skips imdb_id", imdb: "0000", mediaType: "movie", absentKey: "imdb_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Provider{}
			req := &api.SearchRequest{
				ImdbID:    tt.imdb,
				MediaType: api.MediaType(tt.mediaType),
				Languages: []string{"en"},
			}
			params := p.buildSearchParams(req, 1, 1)
			if got := params.Get(tt.absentKey); got != "" {
				t.Errorf("buildSearchParams() set %q=%q, want unset",
					tt.absentKey, got)
			}
		})
	}
}

func TestBuildSearchParams_episode_with_valid_imdb_sets_parent(t *testing.T) {
	t.Parallel()
	p := &Provider{}
	req := &api.SearchRequest{
		ImdbID:    "tt1234567",
		MediaType: "episode",
		Languages: []string{"en"},
	}
	params := p.buildSearchParams(req, 1, 1)
	if got := params.Get("parent_imdb_id"); got != "1234567" {
		t.Errorf("parent_imdb_id = %q, want %q", got, "1234567")
	}
	if got := params.Get("imdb_id"); got != "" {
		t.Errorf("imdb_id = %q, want unset for episode", got)
	}
}
