package gestdown

import (
	"context"
	"fmt"
	"testing"

	"subflux/internal/api"

	"pgregory.net/rapid"
)

// --- Factory ---

func TestFactory(t *testing.T) {
	t.Parallel()

	p, err := Factory(context.Background(), nil)
	if err != nil {
		t.Fatalf("Factory(context.Background(), nil) unexpected error: %v", err)
	}
	if p.Name() != api.ProviderNameGestdown {
		t.Errorf("Name() = %q, want %q", p.Name(), api.ProviderNameGestdown)
	}
}

// --- Search early-return paths ---

func TestSearch_skips_non_episode(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), nil)
	req := &api.SearchRequest{MediaType: "movie", TvdbID: 12345, Languages: []string{"en"}}
	got, err := p.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("Search() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("Search(movie) = %v, want nil", got)
	}
}

func TestSearch_skips_zero_tvdb_id(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), nil)
	req := &api.SearchRequest{MediaType: "episode", TvdbID: 0, Languages: []string{"en"}}
	got, err := p.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("Search() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("Search(tvdbID=0) = %v, want nil", got)
	}
}

// --- Download SSRF validation ---

func TestDownload_rejects_ssrf_url(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), nil)
	sub := &api.Subtitle{DownloadURL: "http://127.0.0.1/evil"}
	_, err := p.Download(context.Background(), sub)
	if err == nil {
		t.Fatal("Download(loopback URL) expected error, got nil")
	}
}

func TestDownload_rejects_internal_ip(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), nil)
	sub := &api.Subtitle{DownloadURL: "http://192.168.1.1/sub.srt"}
	_, err := p.Download(context.Background(), sub)
	if err == nil {
		t.Fatal("Download(private IP) expected error, got nil")
	}
}

// --- buildSubtitles ---

func TestBuildSubtitles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		isoLang  string
		episodes []seasonEpisode
		season   int
		want     int
	}{
		{
			name: "normal completed subtitle",
			episodes: []seasonEpisode{{
				Number: 3,
				Subtitles: []subtitleResult{{
					SubtitleID:  "abc",
					Version:     "LOL",
					DownloadURI: "/subtitles/abc",
					Completed:   true,
				}},
			}},
			season:  1,
			isoLang: "en",
			want:    1,
		},
		{
			name: "incomplete subtitle filtered",
			episodes: []seasonEpisode{{
				Number: 1,
				Subtitles: []subtitleResult{{
					SubtitleID:  "inc",
					Version:     "LOL",
					DownloadURI: "/subtitles/inc",
					Completed:   false,
				}},
			}},
			season:  1,
			isoLang: "en",
			want:    0,
		},
		{
			name: "invalid URI filtered",
			episodes: []seasonEpisode{{
				Number: 1,
				Subtitles: []subtitleResult{{
					SubtitleID:  "bad",
					Version:     "LOL",
					DownloadURI: "http://evil.com/sub",
					Completed:   true,
				}},
			}},
			season:  1,
			isoLang: "en",
			want:    0,
		},
		{
			name: "multi-version comma-separated",
			episodes: []seasonEpisode{{
				Number: 5,
				Subtitles: []subtitleResult{{
					SubtitleID:  "mv",
					Version:     "LOL, DIMENSION, FUM",
					DownloadURI: "/subtitles/mv",
					Completed:   true,
				}},
			}},
			season:  2,
			isoLang: "fr",
			want:    1,
		},
		{
			name:     "empty episodes",
			episodes: nil,
			season:   1,
			isoLang:  "en",
			want:     0,
		},
		{
			name: "multiple episodes multiple subs",
			episodes: []seasonEpisode{
				{Number: 1, Subtitles: []subtitleResult{
					{SubtitleID: "a", Version: "LOL", DownloadURI: "/a", Completed: true},
					{SubtitleID: "b", Version: "FLEET", DownloadURI: "/b", Completed: true},
				}},
				{Number: 2, Subtitles: []subtitleResult{
					{SubtitleID: "c", Version: "NTb", DownloadURI: "/c", Completed: true},
				}},
			},
			season:  1,
			isoLang: "en",
			want:    3,
		},
		{
			name: "empty version string",
			episodes: []seasonEpisode{{
				Number: 1,
				Subtitles: []subtitleResult{{
					SubtitleID:  "ev",
					Version:     "",
					DownloadURI: "/subtitles/ev",
					Completed:   true,
				}},
			}},
			season:  1,
			isoLang: "en",
			want:    1,
		},
		{
			name: "whitespace-only version",
			episodes: []seasonEpisode{{
				Number: 1,
				Subtitles: []subtitleResult{{
					SubtitleID:  "ws",
					Version:     "  ",
					DownloadURI: "/subtitles/ws",
					Completed:   true,
				}},
			}},
			season:  1,
			isoLang: "en",
			want:    1,
		},
		{
			name: "empty download URI filtered",
			episodes: []seasonEpisode{{
				Number: 1,
				Subtitles: []subtitleResult{{
					SubtitleID:  "eu",
					Version:     "LOL",
					DownloadURI: "",
					Completed:   true,
				}},
			}},
			season:  1,
			isoLang: "en",
			want:    0,
		},
		{
			name: "episode with empty subtitles slice",
			episodes: []seasonEpisode{{
				Number:    1,
				Subtitles: nil,
			}},
			season:  1,
			isoLang: "en",
			want:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildSubtitles(tt.episodes, tt.season, tt.isoLang)
			if len(got) != tt.want {
				t.Errorf("buildSubtitles() returned %d subs, want %d", len(got), tt.want)
			}
		})
	}
}

func TestBuildSubtitles_fields(t *testing.T) {
	t.Parallel()

	episodes := []seasonEpisode{{
		Number: 7,
		Subtitles: []subtitleResult{{
			SubtitleID:  "sub123",
			Version:     " LOL , DIMENSION ",
			DownloadURI: "/subtitles/sub123",
			Completed:   true,
			HearingImp:  true,
		}},
	}}

	subs := buildSubtitles(episodes, 3, "fr")
	if len(subs) != 1 {
		t.Fatalf("buildSubtitles() returned %d subs, want 1", len(subs))
	}
	s := subs[0]
	if s.Provider != "gestdown" {
		t.Errorf("Provider = %q, want %q", s.Provider, "gestdown")
	}
	if s.ID != "sub123" {
		t.Errorf("ID = %q, want %q", s.ID, "sub123")
	}
	if s.Language != "fr" {
		t.Errorf("Language = %q, want %q", s.Language, "fr")
	}
	if s.ReleaseName != "LOL DIMENSION" {
		t.Errorf("ReleaseName = %q, want %q", s.ReleaseName, "LOL DIMENSION")
	}
	if s.DownloadURL != baseURL+"/subtitles/sub123" {
		t.Errorf("DownloadURL = %q, want %q", s.DownloadURL, baseURL+"/subtitles/sub123")
	}
	if !s.HearingImp {
		t.Error("HearingImp = false, want true")
	}
	if s.MatchedBy != "tvdb" {
		t.Errorf("MatchedBy = %q, want %q", s.MatchedBy, "tvdb")
	}
	if s.Episode != 7 {
		t.Errorf("Episode = %d, want 7", s.Episode)
	}
	if s.Season != 3 {
		t.Errorf("Season = %d, want 3", s.Season)
	}
}

// --- filterByEpisode ---

func TestFilterByEpisode(t *testing.T) {
	t.Parallel()

	subs := []api.Subtitle{
		{ID: "a", Episode: 1},
		{ID: "b", Episode: 2},
		{ID: "c", Episode: 1},
		{ID: "d", Episode: 3},
		{ID: "e", Episode: 0},
	}

	tests := []struct {
		name    string
		episode int
		want    int
	}{
		{"matching episode", 1, 2},
		{"single match", 2, 1},
		{"no match", 99, 0},
		{"episode zero (specials)", 0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := filterByEpisode(subs, tt.episode)
			if len(got) != tt.want {
				t.Errorf("filterByEpisode(_, %d) = %d subs, want %d",
					tt.episode, len(got), tt.want)
			}
		})
	}
}

func TestFilterByEpisode_empty(t *testing.T) {
	t.Parallel()
	got := filterByEpisode(nil, 1)
	if got != nil {
		t.Errorf("filterByEpisode(nil, 1) = %v, want nil", got)
	}
}

// --- Language Mapping ---

func TestIso2ToGestdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, input, want string
	}{
		{"English", "en", "English"},
		{"French", "fr", "French"},
		{"Spanish", "es", "Spanish"},
		{"German", "de", "German"},
		{"Portuguese", "pt", "Portuguese"},
		{"Dutch", "nl", "Dutch"},
		{"Russian", "ru", "Russian"},
		{"Arabic", "ar", "Arabic"},
		{"Japanese", "ja", "Japanese"},
		{"Korean", "ko", "Korean"},
		{"Swedish", "sv", "Swedish"},
		{"Norwegian", "no", "Norwegian"},
		{"Polish", "pl", "Polish"},
		{"Czech", "cs", "Czech"},
		{"Hungarian", "hu", "Hungarian"},
		{"Turkish", "tr", "Turkish"},
		{"Greek", "el", "Greek"},
		{"Hebrew", "he", "Hebrew"},
		{"Bulgarian", "bg", "Bulgarian"},
		{"Croatian", "hr", "Croatian"},
		{"Slovak", "sk", "Slovak"},
		{"Slovenian", "sl", "Slovenian"},
		{"Ukrainian", "uk", "Ukrainian"},
		{"Serbian", "sr", "Serbian"},
		{"Persian", "fa", "Persian"},
		{"unknown code", "xx", ""},
		{"empty string", "", ""},
		// Alpha-3 codes (via Alpha2FromAlpha3 conversion).
		{"English from alpha-3", "eng", "English"},
		{"French from alpha-3", "fre", "French"},
		{"German from alpha-3", "ger", "German"},
		{"unknown alpha-3", "zzz", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := iso2ToGestdown(tt.input)
			if got != tt.want {
				t.Errorf("iso2ToGestdown(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildSubtitles_release_name_edge_cases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		version     string
		wantRelease string
	}{
		{"empty version", "", ""},
		{"whitespace only", "  ", ""},
		{"single group", "LOL", "LOL"},
		{"trailing comma", "LOL,", "LOL "},
		{"leading comma", ",LOL", " LOL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			episodes := []seasonEpisode{{
				Number: 1,
				Subtitles: []subtitleResult{{
					SubtitleID:  "x",
					Version:     tt.version,
					DownloadURI: "/subtitles/x",
					Completed:   true,
				}},
			}}
			subs := buildSubtitles(episodes, 1, "en")
			if len(subs) != 1 {
				t.Fatalf("buildSubtitles() returned %d subs, want 1", len(subs))
			}
			if subs[0].ReleaseName != tt.wantRelease {
				t.Errorf("buildSubtitles(version=%q).ReleaseName = %q, want %q",
					tt.version, subs[0].ReleaseName, tt.wantRelease)
			}
		})
	}
}

func TestBuildSubtitles_count_invariant(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		nEps := rapid.IntRange(0, 5).Draw(t, "numEpisodes")
		var episodes []seasonEpisode
		wantCount := 0

		for i := range nEps {
			nSubs := rapid.IntRange(0, 4).Draw(t, fmt.Sprintf("numSubs_%d", i))
			var subs []subtitleResult
			for j := range nSubs {
				completed := rapid.Bool().Draw(t, fmt.Sprintf("completed_%d_%d", i, j))
				validURI := rapid.Bool().Draw(t, fmt.Sprintf("validURI_%d_%d", i, j))
				uri := "/subtitles/test"
				if !validURI {
					uri = "http://evil.com/sub"
				}
				subs = append(subs, subtitleResult{
					SubtitleID:  fmt.Sprintf("sub_%d_%d", i, j),
					Version:     "LOL",
					DownloadURI: uri,
					Completed:   completed,
				})
				if completed && validURI {
					wantCount++
				}
			}
			episodes = append(episodes, seasonEpisode{
				Number:    i + 1,
				Subtitles: subs,
			})
		}

		got := buildSubtitles(episodes, 1, "en")
		if len(got) != wantCount {
			t.Fatalf("buildSubtitles() returned %d subs, want %d (completed+validURI)",
				len(got), wantCount)
		}
	})
}

func TestFilterByEpisode_invariant(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "numSubs")
		target := rapid.IntRange(1, 10).Draw(t, "targetEpisode")

		var subs []api.Subtitle
		wantCount := 0
		for i := range n {
			ep := rapid.IntRange(1, 10).Draw(t, fmt.Sprintf("ep_%d", i))
			subs = append(subs, api.Subtitle{
				ID:      fmt.Sprintf("s%d", i),
				Episode: ep,
			})
			if ep == target {
				wantCount++
			}
		}

		got := filterByEpisode(subs, target)
		if len(got) != wantCount {
			t.Fatalf("filterByEpisode(_, %d) returned %d subs, want %d",
				target, len(got), wantCount)
		}
		for _, s := range got {
			if s.Episode != target {
				t.Fatalf("filterByEpisode(_, %d) returned sub with Episode=%d",
					target, s.Episode)
			}
		}
	})
}
