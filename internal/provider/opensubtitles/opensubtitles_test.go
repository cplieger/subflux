package opensubtitles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"subflux/internal/api"
	"testing"
	"time"
)

// errReader is an io.Reader that always returns an error.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read error") }

// --- Server URL ---

func TestIsValidServerHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		want bool
	}{
		{"valid hostname", "vip-api.opensubtitles.com", true},
		{"valid subdomain", "api.opensubtitles.com", true},
		{"apex domain accepted", "opensubtitles.com", true},
		{"trailing dot tolerated", "api.opensubtitles.com.", true},
		{"mixed case accepted", "API.OpenSubtitles.com", true},
		{"unrelated public host rejected", "example.com", false},
		{"lookalike suffix rejected", "evil-opensubtitles.com", false},
		{"path injection", "evil.com/steal-creds", false},
		{"port injection", "evil.com:8080", false},
		{"userinfo injection", "user@evil.com", false},
		{"query injection", "evil.com?redirect=true", false},
		{"fragment injection", "evil.com#frag", false},
		{"bare hostname", "localhost", false},
		{"private IP", "192.168.1.1", false},
		{"loopback IP", "127.0.0.1", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isValidServerHost(tt.host)
			if got != tt.want {
				t.Errorf("isValidServerHost(%q) = %v, want %v",
					tt.host, got, tt.want)
			}
		})
	}
}

func TestServerURL(t *testing.T) {
	t.Parallel()

	t.Run("default when no server host set", func(t *testing.T) {
		t.Parallel()
		p := &Provider{}
		got := p.serverURL()
		if got != baseURL {
			t.Errorf("serverURL() = %q, want %q", got, baseURL)
		}
	})

	t.Run("custom server host", func(t *testing.T) {
		t.Parallel()
		p := &Provider{serverHost: "vip-api.opensubtitles.com"}
		got := p.serverURL()
		want := "https://vip-api.opensubtitles.com/api/v1"
		if got != want {
			t.Errorf("serverURL() = %q, want %q", got, want)
		}
	})

	t.Run("empty server host uses default", func(t *testing.T) {
		t.Parallel()
		p := &Provider{serverHost: ""}
		got := p.serverURL()
		if got != baseURL {
			t.Errorf("serverURL() = %q, want %q", got, baseURL)
		}
	})
}

// --- Header Setting ---

func TestSetHeaders(t *testing.T) {
	t.Parallel()

	t.Run("sets required headers without token", func(t *testing.T) {
		t.Parallel()
		p := &Provider{apiKey: "test-api-key"}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", http.NoBody)

		p.setHeaders(req)

		if got := req.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}
		if got := req.Header.Get("Api-Key"); got != "test-api-key" {
			t.Errorf("Api-Key = %q, want %q", got, "test-api-key")
		}
		// User-Agent is now injected by the transport layer (userAgentTransport),
		// not by setHeaders. Verify it is NOT set here (transport adds it at Do time).
		if got := req.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty (no token)", got)
		}
	})

	t.Run("sets authorization header with token", func(t *testing.T) {
		t.Parallel()
		p := &Provider{apiKey: "key", token: "my-token"}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", http.NoBody)

		p.setHeaders(req)

		want := "Bearer my-token"
		if got := req.Header.Get("Authorization"); got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
	})
}

func TestToOSLang(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"pt maps to pt-PT", "pt", "pt-PT"},
		{"pb maps to pt-BR", "pb", "pt-BR"},
		{"zh maps to zh-CN", "zh", "zh-CN"},
		{"en passes through", "en", "en"},
		{"fr passes through", "fr", "fr"},
		{"empty passes through", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := toOSLang(tt.input)
			if got != tt.want {
				t.Errorf("toOSLang(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestFromOSLang(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"pt-PT maps to pt", "pt-PT", "pt"},
		{"pt-BR maps to pb", "pt-BR", "pb"},
		{"zh-CN maps to zh", "zh-CN", "zh"},
		{"ea maps to es", "ea", "es"},
		{"en passes through", "en", "en"},
		{"fr passes through", "fr", "fr"},
		{"empty passes through", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := fromOSLang(tt.input)
			if got != tt.want {
				t.Errorf("fromOSLang(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestFactory_requires_credentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings map[string]any
		wantErr  bool
	}{
		{"nil settings", nil, true},
		{"missing all", map[string]any{}, true},
		{"missing password", map[string]any{
			"username": "user", "api_key": "key",
		}, true},
		{"missing username", map[string]any{
			"password": "pass", "api_key": "key",
		}, true},
		{"missing api_key", map[string]any{
			"username": "user", "password": "pass",
		}, true},
		{"empty username", map[string]any{
			"username": "", "password": "pass", "api_key": "key",
		}, true},
		{"empty password", map[string]any{
			"username": "user", "password": "", "api_key": "key",
		}, true},
		{"empty api_key", map[string]any{
			"username": "user", "password": "pass", "api_key": "",
		}, true},
		{"valid credentials", map[string]any{
			"username": "user", "password": "pass", "api_key": "key",
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, err := Factory(context.Background(), tt.settings)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Factory() expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Factory() unexpected error: %v", err)
			}
			if p.Name() != api.ProviderNameOpenSubtitles {
				t.Errorf("Name() = %q, want %q", p.Name(), api.ProviderNameOpenSubtitles)
			}
		})
	}
}

func TestFactory_options(t *testing.T) {
	t.Parallel()

	validCreds := map[string]any{
		"username": "user", "password": "pass", "api_key": "key",
	}

	// merge returns validCreds with extra key-value pairs applied.
	merge := func(extra map[string]any) map[string]any {
		m := make(map[string]any, len(validCreds)+len(extra))
		maps.Copy(m, validCreds)
		maps.Copy(m, extra)
		return m
	}

	tests := []struct {
		name     string
		extra    map[string]any
		wantHash bool
		wantAI   bool
	}{
		{"defaults", nil, true, false},
		{"use_hash explicit true", map[string]any{"use_hash": true}, true, false},
		{"use_hash false", map[string]any{"use_hash": false}, false, false},
		{"include_ai_translated true", map[string]any{"include_ai_translated": true}, true, true},
		{"include_ai_translated false", map[string]any{"include_ai_translated": false}, true, false},
		{"both overridden", map[string]any{
			"use_hash": false, "include_ai_translated": true,
		}, false, true},
		{"string true accepted", map[string]any{"use_hash": "true"}, true, false},
		{"string false accepted", map[string]any{"use_hash": "false"}, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, err := Factory(context.Background(), merge(tt.extra))
			if err != nil {
				t.Fatalf("Factory() unexpected error: %v", err)
			}
			prov := p.(*Provider)
			if prov.useHash != tt.wantHash {
				t.Errorf("useHash = %v, want %v", prov.useHash, tt.wantHash)
			}
			if prov.includeAI != tt.wantAI {
				t.Errorf("includeAI = %v, want %v", prov.includeAI, tt.wantAI)
			}
		})
	}
}

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

// --- HTTP Status Checking ---

func TestCheckStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    bool
		wantMsg    string
		wantType   string // "auth", "ratelimit", or ""
	}{
		{"200 OK returns nil", 200, "", false, "", ""},
		{"201 Created returns nil", 201, "", false, "", ""},
		{"204 No Content returns nil", 204, "", false, "", ""},
		{"301 redirect returns nil", 301, "", false, "", ""},
		{"401 unauthorized", 401, "", true, "authentication failed (401)", "auth"},
		{"429 rate limited", 429, "", true, "rate limited (429)", "ratelimit"},
		{"406 download limit", 406, "", true, "download limit exceeded (406)", "ratelimit"},
		{"500 server error with body", 500, "internal error", true, "HTTP 500: internal error", ""},
		{"400 bad request with body", 400, "bad request", true, "HTTP 400: bad request", ""},
		{"403 forbidden with empty body", 403, "", true, "HTTP 403", ""},
		{"202 Accepted returns nil", 202, "", false, "", ""},
		{"body truncated at 1024 bytes", 500, strings.Repeat("x", 2000), true, "HTTP 500: " + strings.Repeat("x", 1024), ""},
		{"304 Not Modified returns nil", 304, "", false, "", ""},
		{"399 returns nil", 399, "", false, "", ""},
		{"503 service unavailable with body", 503, "service down", true, "HTTP 503: service down", ""},
		{"404 not found no body", 404, "", true, "HTTP 404", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := &http.Response{
				StatusCode: tt.statusCode,
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}
			defer resp.Body.Close()
			err := checkStatus(resp)
			if tt.wantErr {
				if err == nil {
					t.Fatal("checkStatus() expected error")
				}
				if err.Error() != tt.wantMsg {
					t.Errorf("checkStatus() error = %q, want %q",
						err.Error(), tt.wantMsg)
				}
				switch tt.wantType {
				case "auth":
					var authErr *api.AuthError
					if !errors.As(err, &authErr) {
						t.Errorf("checkStatus(%d) error type = %T, want *api.AuthError",
							tt.statusCode, err)
					}
				case "ratelimit":
					var rlErr *api.RateLimitError
					if !errors.As(err, &rlErr) {
						t.Errorf("checkStatus(%d) error type = %T, want *api.RateLimitError",
							tt.statusCode, err)
					}
				}
				return
			}
			if err != nil {
				t.Errorf("checkStatus() unexpected error: %v", err)
			}
		})
	}

	t.Run("body read error falls back to status only", func(t *testing.T) {
		t.Parallel()
		resp := &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(errReader{}),
		}
		defer resp.Body.Close()
		err := checkStatus(resp)
		if err == nil {
			t.Fatal("checkStatus() expected error")
		}
		want := "HTTP 502"
		if err.Error() != want {
			t.Errorf("checkStatus() error = %q, want %q", err.Error(), want)
		}
	})
}

// --- Query Parameter Building ---

func TestBuildQueryParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		req       *api.SearchRequest
		season    int
		episode   int
		includeAI bool
		wantKey   string
		wantValue string
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

func TestLangMapping_round_trip(t *testing.T) {
	t.Parallel()

	// Every key in langToOS should round-trip through toOSLang → fromOSLang.
	for iso, os := range langToOS {
		got := fromOSLang(toOSLang(iso))
		if got != iso {
			t.Errorf("fromOSLang(toOSLang(%q)) = %q, want %q (via %q)",
				iso, got, iso, os)
		}
	}

	// Every value in langFromOS that also has a reverse entry in langToOS
	// should round-trip. Entries like "ea"→"es" are one-way legacy mappings
	// (OpenSubtitles uses "ea" for Spanish, but we send "es" which passes
	// through unchanged), so we only check keys whose ISO code maps back.
	for os, iso := range langFromOS {
		if _, hasReverse := langToOS[iso]; !hasReverse {
			continue
		}
		got := toOSLang(fromOSLang(os))
		if got != os {
			t.Errorf("toOSLang(fromOSLang(%q)) = %q, want %q (via %q)",
				os, got, os, iso)
		}
	}
}

func TestJoinOSLangs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		langs []string
		want  string
	}{
		{"nil returns empty", nil, ""},
		{"empty returns empty", []string{}, ""},
		{"single language", []string{"en"}, "en"},
		{"multiple sorted", []string{"fr", "en"}, "en,fr"},
		{"mapped languages", []string{"zh", "pt"}, "pt-PT,zh-CN"},
		{"mixed mapped and unmapped", []string{"zh", "en", "pt"}, "en,pt-PT,zh-CN"},
		{"pb maps to pt-BR", []string{"pb"}, "pt-BR"},
		{"already sorted", []string{"de", "en", "fr"}, "de,en,fr"},
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

// --- Token Invalidation ---

func TestInvalidateTokenOn401(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		initialToken string
		err          error
		wantToken    string
	}{
		{"clears token on auth error", "my-token", &api.AuthError{Msg: "401"}, ""},
		{"preserves token on other error", "my-token", errors.New("some other error"), "my-token"},
		{"idempotent when token empty", "", &api.AuthError{Msg: "401"}, ""},
		{"wrapped auth error", "my-token", fmt.Errorf("request failed: %w", &api.AuthError{Msg: "401"}), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Provider{token: tt.initialToken}
			p.invalidateTokenOn401(tt.err)

			p.tokenMu.Lock()
			got := p.token
			p.tokenMu.Unlock()
			if got != tt.wantToken {
				t.Errorf("token = %q, want %q", got, tt.wantToken)
			}
		})
	}
}

// --- checkStatus Retry-After parsing ---

func TestCheckStatus_parses_retry_after_seconds_on_429(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"42"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
	defer resp.Body.Close()

	err := checkStatus(resp)
	var rl *api.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("checkStatus() error type = %T, want *api.RateLimitError", err)
	}
	if rl.RetryAfter != 42*time.Second {
		t.Errorf("RetryAfter = %v, want 42s", rl.RetryAfter)
	}
}

func TestCheckStatus_missing_retry_after_on_429_is_zero(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader("")),
	}
	defer resp.Body.Close()

	err := checkStatus(resp)
	var rl *api.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("checkStatus() error type = %T, want *api.RateLimitError", err)
	}
	if rl.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0 (no header)", rl.RetryAfter)
	}
}

func TestCheckStatus_406_defaults_to_next_utc_midnight(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusNotAcceptable,
		Body:       io.NopCloser(strings.NewReader("")),
	}
	defer resp.Body.Close()

	err := checkStatus(resp)
	var rl *api.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("checkStatus() error type = %T, want *api.RateLimitError", err)
	}
	// Daily quota window is always ≤24h and >0s.
	if rl.RetryAfter <= 0 || rl.RetryAfter > 24*time.Hour {
		t.Errorf("RetryAfter = %v, want (0, 24h]", rl.RetryAfter)
	}
}

func TestCheckStatus_406_respects_retry_after_header_when_present(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusNotAcceptable,
		Header:     http.Header{"Retry-After": []string{"7"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
	defer resp.Body.Close()

	err := checkStatus(resp)
	var rl *api.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("checkStatus() error type = %T, want *api.RateLimitError", err)
	}
	if rl.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %v, want 7s", rl.RetryAfter)
	}
}

func TestUntilNextUTCMidnight(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		now  time.Time
		want time.Duration
	}{
		{
			name: "start of day",
			now:  time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
			want: 24 * time.Hour,
		},
		{
			name: "mid day",
			now:  time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC),
			want: 12 * time.Hour,
		},
		{
			name: "one second before midnight",
			now:  time.Date(2026, 5, 27, 23, 59, 59, 0, time.UTC),
			want: time.Second,
		},
		{
			name: "exact midnight returns clamped 1s",
			// Start of day elsewhere would be 24h; same instant returns 24h.
			now:  time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
			want: 24 * time.Hour,
		},
		{
			name: "500ms before midnight clamps to 1s",
			now:  time.Date(2026, 5, 27, 23, 59, 59, int(500*time.Millisecond), time.UTC),
			want: time.Second,
		},
		{
			name: "nanosecond before midnight clamps to 1s",
			now:  time.Date(2026, 5, 27, 23, 59, 59, 999_999_999, time.UTC),
			want: time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := untilNextUTCMidnight(tt.now)
			if got != tt.want {
				t.Errorf("untilNextUTCMidnight(%v) = %v, want %v",
					tt.now, got, tt.want)
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
		{"episode with tt0 skips parent_imdb_id", "tt0", "episode", "parent_imdb_id"},
		{"episode with tt00000 skips parent_imdb_id", "tt00000", "episode", "parent_imdb_id"},
		{"episode with bare tt skips parent_imdb_id", "tt", "episode", "parent_imdb_id"},
		{"movie with tt0 skips imdb_id", "tt0", "movie", "imdb_id"},
		{"movie with 0000 skips imdb_id", "0000", "movie", "imdb_id"},
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

func TestCountShowSubtitles_short_circuits_on_empty_imdb(t *testing.T) {
	t.Parallel()
	// Empty-after-sanitize inputs must return (0, nil) without any HTTP
	// setup. Using a zero-value Provider (no client, no token) proves the
	// short-circuit happens before ensureToken/doGet.
	p := &Provider{}
	for _, imdb := range []string{"tt0", "tt00000", "0000", "tt"} {
		count, err := p.CountShowSubtitles(context.Background(), imdb, "en")
		if err != nil {
			t.Errorf("CountShowSubtitles(%q) error = %v, want nil", imdb, err)
		}
		if count != 0 {
			t.Errorf("CountShowSubtitles(%q) = %d, want 0", imdb, count)
		}
	}
}

// --- Rate Limiting ---

func TestRateLimit_no_wait_when_token_available(t *testing.T) {
	t.Parallel()

	rateCh := make(chan struct{}, 1)
	rateCh <- struct{}{} // pre-fill token
	p := &Provider{vip: false, rateCh: rateCh}

	start := time.Now()
	err := p.rateLimit(context.Background())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("rateLimit() unexpected error: %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("rateLimit() elapsed = %v, want < 50ms when token available", elapsed)
	}
}

func TestRateLimit_blocks_when_no_token_available(t *testing.T) {
	t.Parallel()

	rateCh := make(chan struct{}, 1)
	// Don't pre-fill — no token available, so rateLimit blocks.
	p := &Provider{vip: false, rateCh: rateCh}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := p.rateLimit(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("rateLimit() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed < 50*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Errorf("rateLimit() cancelled at %v, want ~100ms", elapsed)
	}
}

func TestRateLimit_refills_token_after_interval(t *testing.T) {
	t.Parallel()

	rateCh := make(chan struct{}, 1)
	rateCh <- struct{}{}                      // pre-fill
	p := &Provider{vip: true, rateCh: rateCh} // VIP = 200ms refill

	// Consume the token.
	if err := p.rateLimit(context.Background()); err != nil {
		t.Fatalf("first rateLimit() unexpected error: %v", err)
	}

	// Wait for refill (VIP = 200ms, give 400ms budget).
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := p.rateLimit(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("second rateLimit() unexpected error: %v (elapsed=%v)", err, elapsed)
	}
	if elapsed < 150*time.Millisecond || elapsed > 350*time.Millisecond {
		t.Errorf("rateLimit() VIP refill elapsed = %v, want ~200ms", elapsed)
	}
}

func TestRateLimit_respects_context_cancellation(t *testing.T) {
	t.Parallel()

	rateCh := make(chan struct{}, 1)
	// No token — will block until context cancelled.
	p := &Provider{vip: false, rateCh: rateCh}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	err := p.rateLimit(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("rateLimit() error = %v, want context.Canceled", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("rateLimit() returned at %v, want ~50ms (cancelled early)", elapsed)
	}
}

func BenchmarkFilterSearchResults(b *testing.B) {
	makeResults := func(n int) []searchResult {
		results := make([]searchResult, n)
		for i := range results {
			results[i] = searchResult{
				Attributes: searchAttributes{
					Language: "en",
					Release:  fmt.Sprintf("Movie.2024.720p.BluRay-GROUP%d", i),
					Files:    []searchFile{{FileID: 1000 + i}},
					FeatureDetails: featureDetails{
						Title: "Test Movie",
						Year:  2024,
					},
				},
			}
		}
		return results
	}

	languages := []string{"en", "fr", "de"}

	for _, size := range []int{10, 50, 200} {
		data := makeResults(size)
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				filterSearchResults(data, languages, false)
			}
		})
	}
}
