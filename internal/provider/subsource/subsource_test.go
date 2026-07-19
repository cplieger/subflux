package subsource

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cplieger/httpx/v3"
	"github.com/cplieger/subflux/internal/api"
)

func TestFactory_requires_api_key(t *testing.T) {
	t.Parallel()

	_, err := Factory(context.Background(), nil)
	if err == nil {
		t.Fatal("Factory(context.Background(), nil) expected error for missing api_key")
	}

	_, err = Factory(context.Background(), map[string]any{"api_key": ""})
	if err == nil {
		t.Fatal("Factory(empty key) expected error")
	}
}

func TestFactory_with_api_key(t *testing.T) {
	t.Parallel()

	p, err := Factory(context.Background(), map[string]any{"api_key": "test-key"})
	if err != nil {
		t.Fatalf("Factory() unexpected error: %v", err)
	}
	if p.Name() != api.ProviderNameSubSource {
		t.Errorf("Name() = %q, want %q", p.Name(), api.ProviderNameSubSource)
	}
}

func TestIso2ToSubSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, input, want string
	}{
		{"English", "en", "English"},
		{"French", "fr", "French"},
		{"Spanish", "es", "Spanish"},
		{"German", "de", "German"},
		{"Persian", "fa", "Farsi_persian"},
		{"Chinese", "zh", "Chinese BG code"},
		{"Hindi", "hi", "Hindi"},
		{"unknown", "xx", ""},
		{"empty", "", ""},
		{"alpha3 English", "eng", "English"},
		{"alpha3 French", "fre", "French"},
		{"alpha3 unknown", "xxx", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := iso2ToSubSource(tt.input)
			if got != tt.want {
				t.Errorf("iso2ToSubSource(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

// --- matchTitle ---

func TestMatchTitle(t *testing.T) {
	t.Parallel()

	data := []searchResult{
		{MovieID: 100, Title: "Breaking Bad", ReleaseYear: FlexInt(2008)},
		{MovieID: 200, Title: "The Wire", ReleaseYear: FlexInt(2002)},
		{MovieID: 300, Title: "Better Call Saul", ReleaseYear: FlexInt(2015)},
	}

	tests := []struct {
		name  string
		title string
		year  int
		want  int
	}{
		{"exact match no year", "Breaking Bad", 0, 100},
		{"exact match with year", "Breaking Bad", 2008, 100},
		{"wrong year", "Breaking Bad", 2020, 0},
		{"case insensitive", "breaking bad", 0, 100},
		{"substring match", "Wire", 0, 200},
		{"no match", "Dexter", 0, 0},
		{"empty title matches all", "", 0, 100},
		{"year from string field", "The Wire", 2002, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := matchTitle(data, tt.title, tt.year)
			if got != tt.want {
				t.Errorf("matchTitle(data, %q, %d) = %d, want %d",
					tt.title, tt.year, got, tt.want)
			}
		})
	}
}

func TestMatchTitle_year_disambiguates(t *testing.T) {
	t.Parallel()

	data := []searchResult{
		{MovieID: 1, Title: "The Matrix", ReleaseYear: FlexInt(1999)},
		{MovieID: 2, Title: "The Matrix Reloaded", ReleaseYear: FlexInt(2003)},
		{MovieID: 3, Title: "The Matrix Resurrections", ReleaseYear: FlexInt(2021)},
	}

	tests := []struct {
		name  string
		title string
		year  int
		want  int
	}{
		{"year picks correct sequel", "Matrix", 2003, 2},
		{"year picks original", "Matrix", 1999, 1},
		{"no year picks first match", "Matrix", 0, 1},
		{"wrong year no match", "Matrix", 2010, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := matchTitle(data, tt.title, tt.year)
			if got != tt.want {
				t.Errorf("matchTitle(data, %q, %d) = %d, want %d",
					tt.title, tt.year, got, tt.want)
			}
		})
	}
}

func TestMatchTitle_nil_release_year(t *testing.T) {
	t.Parallel()

	data := []searchResult{
		{MovieID: 5, Title: "Test Movie", ReleaseYear: FlexInt(0)},
	}

	// With year=0, zero ReleaseYear should still match (FlexInt(0) == 0).
	if got := matchTitle(data, "Test", 0); got != 5 {
		t.Errorf("matchTitle(nil year, year=0) = %d, want 5", got)
	}
	// With a specific year, nil ReleaseYear should not match.
	if got := matchTitle(data, "Test", 2020); got != 0 {
		t.Errorf("matchTitle(nil year, year=2020) = %d, want 0", got)
	}
}

func TestMatchTitle_empty_data(t *testing.T) {
	t.Parallel()

	if got := matchTitle(nil, "test", 0); got != 0 {
		t.Errorf("matchTitle(nil) = %d, want 0", got)
	}
	if got := matchTitle([]searchResult{}, "test", 0); got != 0 {
		t.Errorf("matchTitle([]) = %d, want 0", got)
	}
}

// --- releaseYear ---

func TestFlexInt_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  FlexInt
	}{
		{"number", `2024`, 2024},
		{"string", `"2015"`, 2015},
		{"invalid string", `"abc"`, 0},
		{"empty string", `""`, 0},
		{"null", `null`, 0},
		{"zero", `0`, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got FlexInt
			if err := got.UnmarshalJSON([]byte(tt.input)); err != nil {
				t.Fatalf("UnmarshalJSON(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("UnmarshalJSON(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// --- buildSubtitles ---

func TestBuildSubtitles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		check   func(t *testing.T, got []api.Subtitle)
		name    string
		lang    string
		items   []subtitleItem
		season  int
		episode int
		wantLen int
	}{
		{
			name:    "empty items",
			items:   nil,
			lang:    "en",
			season:  1,
			episode: 1,
			wantLen: 0,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got != nil {
					t.Errorf("buildSubtitles(nil) = %d items, want nil", len(got))
				}
			},
		},
		{
			name: "skips foreign parts",
			items: []subtitleItem{
				{SubtitleID: 1, ForeignParts: true, ReleaseInfo: []string{"rel"}},
				{SubtitleID: 2, ForeignParts: false, ReleaseInfo: []string{"rel"}},
			},
			lang: "en", season: 1, episode: 1, wantLen: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].ID != "2" {
					t.Errorf("got[0].ID = %q, want %q", got[0].ID, "2")
				}
			},
		},
		{
			name: "skips forced commentary",
			items: []subtitleItem{
				{SubtitleID: 1, Commentary: "forced subtitle", ReleaseInfo: []string{"rel"}},
				{SubtitleID: 2, Commentary: "normal", ReleaseInfo: []string{"rel"}},
			},
			lang: "en", season: 1, episode: 1, wantLen: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].ID != "2" {
					t.Errorf("got[0].ID = %q, want %q", got[0].ID, "2")
				}
			},
		},
		{
			name: "detects hi",
			items: []subtitleItem{
				{SubtitleID: 1, HearingImp: true, ReleaseInfo: []string{"rel"}},
				{SubtitleID: 2, Commentary: "SDH version", ReleaseInfo: []string{"rel"}},
				{SubtitleID: 3, ReleaseInfo: []string{"rel"}},
			},
			lang: "en", season: 1, episode: 1, wantLen: 3,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if !got[0].HearingImp {
					t.Error("got[0].HearingImp = false, want true (struct field)")
				}
				if !got[1].HearingImp {
					t.Error("got[1].HearingImp = false, want true (commentary)")
				}
				if got[2].HearingImp {
					t.Error("got[2].HearingImp = true, want false")
				}
			},
		},
		{
			name: "expands multiple releases",
			items: []subtitleItem{
				{SubtitleID: 1, ReleaseInfo: []string{"SPARKS", "YIFY", "FGT"}},
			},
			lang: "en", season: 1, episode: 1, wantLen: 3,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				for i, want := range []string{"SPARKS", "YIFY", "FGT"} {
					if got[i].ReleaseName != want {
						t.Errorf("got[%d].ReleaseName = %q, want %q", i, got[i].ReleaseName, want)
					}
				}
			},
		},
		{
			name: "empty release info",
			items: []subtitleItem{
				{SubtitleID: 1, ReleaseInfo: nil},
			},
			lang: "en", season: 1, episode: 1, wantLen: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].ReleaseName != "" {
					t.Errorf("got[0].ReleaseName = %q, want empty", got[0].ReleaseName)
				}
			},
		},
		{
			name: "sets metadata",
			items: []subtitleItem{
				{SubtitleID: 42, ReleaseInfo: []string{"rel"}},
			},
			lang: "fr", season: 3, episode: 7, wantLen: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				s := got[0]
				if s.Provider != providerName {
					t.Errorf("Provider = %q, want %q", s.Provider, providerName)
				}
				if s.Language != "fr" {
					t.Errorf("Language = %q, want %q", s.Language, "fr")
				}
				if s.Season != 3 {
					t.Errorf("Season = %d, want 3", s.Season)
				}
				if s.Episode != 7 {
					t.Errorf("Episode = %d, want 7", s.Episode)
				}
				if s.MatchedBy != matchedByIMDB {
					t.Errorf("MatchedBy = %q, want %q", s.MatchedBy, matchedByIMDB)
				}
			},
		},
		{
			name: "sets download url and id",
			items: []subtitleItem{
				{SubtitleID: 42, ReleaseInfo: []string{"rel"}},
			},
			lang: "en", season: 1, episode: 1, wantLen: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				wantURL := "https://api.subsource.net/api/v1/subtitles/42/download"
				if got[0].DownloadURL != wantURL {
					t.Errorf("DownloadURL = %q, want %q", got[0].DownloadURL, wantURL)
				}
				if got[0].ID != "42" {
					t.Errorf("ID = %q, want %q", got[0].ID, "42")
				}
			},
		},
		{
			name: "mixed forced and normal",
			items: []subtitleItem{
				{SubtitleID: 1, Commentary: "foreign parts only", ReleaseInfo: []string{"rel"}},
				{SubtitleID: 2, ForeignParts: true, Commentary: "normal", ReleaseInfo: []string{"rel"}},
				{SubtitleID: 3, Commentary: "good quality", ReleaseInfo: []string{"rel"}},
			},
			lang: "en", season: 1, episode: 1, wantLen: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].ID != "3" {
					t.Errorf("got[0].ID = %q, want %q", got[0].ID, "3")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildSubtitles(tt.items, tt.lang, tt.season, tt.episode)
			if len(got) != tt.wantLen {
				t.Fatalf("buildSubtitles() = %d items, want %d", len(got), tt.wantLen)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// --- redactAPIKey ---

func TestRedactAPIKey_strips_secret_from_error_message(t *testing.T) {
	t.Parallel()

	p := &Provider{apiKey: "supersecret32hex"}
	in := fmt.Errorf("Get https://api.subsource.net/api/v1/subtitles?api_key=%s: dial tcp: i/o timeout", p.apiKey)
	got := httpx.RedactSecret(in, p.apiKey)
	if got == nil {
		t.Fatal("redactAPIKey returned nil for non-nil input")
	}
	if strings.Contains(got.Error(), p.apiKey) {
		t.Errorf("redactAPIKey did not strip api_key: %q", got.Error())
	}
	if !strings.Contains(got.Error(), "REDACTED") {
		t.Errorf("redactAPIKey did not insert REDACTED marker: %q", got.Error())
	}
}

func TestRedactAPIKey_pass_through_when_apikey_absent_from_message(t *testing.T) {
	t.Parallel()

	p := &Provider{apiKey: "supersecret32hex"}
	in := errors.New("some error that does not leak the secret")
	got := httpx.RedactSecret(in, p.apiKey)
	if got.Error() != in.Error() {
		t.Errorf("redactAPIKey mutated safe error: got %q, want %q", got.Error(), in.Error())
	}
}

func TestRedactAPIKey_nil_and_empty_apikey(t *testing.T) {
	t.Parallel()

	p := &Provider{apiKey: ""}
	if got := httpx.RedactSecret(nil, p.apiKey); got != nil {
		t.Errorf("redactAPIKey(nil) = %v, want nil", got)
	}

	in := errors.New("anything")
	if got := httpx.RedactSecret(in, p.apiKey); got.Error() != in.Error() {
		t.Errorf("redactAPIKey with empty apiKey mutated error: got %q", got.Error())
	}
}
