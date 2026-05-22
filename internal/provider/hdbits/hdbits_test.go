package hdbits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"subflux/internal/api"
	"subflux/internal/cache"
	"subflux/internal/httputil"
	"subflux/internal/provider/classify"
	"subflux/internal/provider/dlcache"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestHdbLangToISO(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"en maps to en", "en", "en"},
		{"fr maps to fr", "fr", "fr"},
		{"es maps to es", "es", "es"},
		{"de maps to de", "de", "de"},
		{"it maps to it", "it", "it"},
		{"pt maps to pt", "pt", "pt"},
		{"nl maps to nl", "nl", "nl"},
		{"ru maps to ru", "ru", "ru"},
		{"ja maps to ja", "ja", "ja"},
		{"zh maps to zh", "zh", "zh"},
		{"ko maps to ko", "ko", "ko"},
		{"pl maps to pl", "pl", "pl"},
		{"sv maps to sv", "sv", "sv"},
		{"no maps to no", "no", "no"},
		{"da maps to da", "da", "da"},
		{"fi maps to fi", "fi", "fi"},
		{"uk maps to uk", "uk", "uk"},
		{"br maps to pb", "br", "pb"},
		{"gr maps to el", "gr", "el"},
		{"cz maps to cs", "cz", "cs"},
		{"se maps to sv", "se", "sv"},
		{"dk maps to da", "dk", "da"},
		{"kr maps to ko", "kr", "ko"},
		{"cn maps to zh", "cn", "zh"},
		{"il maps to he", "il", "he"},
		{"si maps to sl", "si", "sl"},
		{"unknown code", "xx", ""},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hdbLangToISO(tt.input)
			if got != tt.want {
				t.Errorf("hdbLangToISO(%q) = %q, want %q",
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
		{"missing both", map[string]any{}, true},
		{"missing passkey", map[string]any{"username": "user"}, true},
		{"missing username", map[string]any{"passkey": "key"}, true},
		{"empty username", map[string]any{"username": "", "passkey": "key"}, true},
		{"empty passkey", map[string]any{"username": "user", "passkey": ""}, true},
		{"valid credentials", map[string]any{"username": "user", "passkey": "key"}, false},
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
			if p.Name() != api.ProviderNameHDBits {
				t.Errorf("Name() = %q, want %q", p.Name(), api.ProviderNameHDBits)
			}
		})
	}
}

// --- Subtitle Data Filtering ---

func TestFilterSubtitleData(t *testing.T) {
	t.Parallel()

	type assertFunc func(t *testing.T, got []api.Subtitle)

	tests := []struct {
		name      string
		data      []hdbSubtitleItem
		req       *api.SearchRequest
		wantCount int
		assertFn  assertFunc // optional: additional field checks
	}{
		{"nil data returns nil", nil, &api.SearchRequest{Languages: []string{"en"}}, 0, nil},
		{"empty data returns nil", []hdbSubtitleItem{}, &api.SearchRequest{Languages: []string{"en"}}, 0, nil},
		{"basic movie result mapped correctly",
			[]hdbSubtitleItem{{Title: "Test Sub", Filename: "test.srt", Language: "en", ID: 42}},
			&api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}, 1,
			func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].Provider != api.ProviderNameHDBits {
					t.Errorf("Provider = %q, want %q", got[0].Provider, api.ProviderNameHDBits)
				}
				if got[0].ID != "42" {
					t.Errorf("ID = %q, want %q", got[0].ID, "42")
				}
				if got[0].Language != "en" {
					t.Errorf("Language = %q, want %q", got[0].Language, "en")
				}
				if got[0].ReleaseName != "Test Sub" {
					t.Errorf("ReleaseName = %q, want %q", got[0].ReleaseName, "Test Sub")
				}
				if got[0].MatchedBy != api.MatchByIMDB {
					t.Errorf("MatchedBy = %q, want %q", got[0].MatchedBy, api.MatchByIMDB)
				}
			}},
		{"episode sets matched_by to tvdb_id",
			[]hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}, MediaType: "episode"}, 1,
			func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].MatchedBy != api.MatchByTVDB {
					t.Errorf("MatchedBy = %q, want %q", got[0].MatchedBy, api.MatchByTVDB)
				}
			}},
		{"unknown language skipped",
			[]hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "xx", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}}, 0, nil},
		{"language not in requested list skipped",
			[]hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "fr", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}}, 0, nil},
		{"br mapped to pb",
			[]hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "br", ID: 1}},
			&api.SearchRequest{Languages: []string{"pb"}}, 1,
			func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].Language != "pb" {
					t.Errorf("Language = %q, want %q", got[0].Language, "pb")
				}
			}},
		{"commentary title filtered",
			[]hdbSubtitleItem{{Title: "Director's Commentary", Filename: "sub.srt", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}}, 0, nil},
		{"commentary filename filtered",
			[]hdbSubtitleItem{{Title: "Normal Title", Filename: "commentary.srt", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}}, 0, nil},
		{"extras title filtered",
			[]hdbSubtitleItem{{Title: "Extras Disc", Filename: "sub.srt", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}}, 0, nil},
		{"extra without s not filtered",
			[]hdbSubtitleItem{{Title: "Extraordinary Movie", Filename: "sub.srt", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}, 1, nil},
		{"case insensitive commentary",
			[]hdbSubtitleItem{{Title: "COMMENTARY Track", Filename: "sub.srt", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}}, 0, nil},
		{"nil languages returns no results",
			[]hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: nil}, 0, nil},
		{"empty languages returns no results",
			[]hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: []string{}}, 0, nil},
		{"gr mapped to el",
			[]hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "gr", ID: 1}},
			&api.SearchRequest{Languages: []string{"el"}}, 1,
			func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].Language != "el" {
					t.Errorf("Language = %q, want %q", got[0].Language, "el")
				}
			}},
		{"non-subtitle extension filtered",
			[]hdbSubtitleItem{{Title: "Sub", Filename: "sub.mp4", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}, 0, nil},
		{"no extension accepted",
			[]hdbSubtitleItem{{Title: "Sub", Filename: "subtitle_no_ext", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}, 1, nil},
		{"lyrics title filtered",
			[]hdbSubtitleItem{{Title: "Song Lyrics", Filename: "sub.srt", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}}, 0, nil},
		{"forced in filename not filtered",
			[]hdbSubtitleItem{{Title: "Normal", Filename: "forced.srt", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}}, 1, nil},
		{"lyrics filename filtered",
			[]hdbSubtitleItem{{Title: "Normal", Filename: "lyrics.srt", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}}, 0, nil},
		{"multiple requested languages",
			[]hdbSubtitleItem{
				{Title: "English Sub", Filename: "en.srt", Language: "en", ID: 1},
				{Title: "French Sub", Filename: "fr.srt", Language: "fr", ID: 2},
				{Title: "German Sub", Filename: "de.srt", Language: "de", ID: 3},
			},
			&api.SearchRequest{Languages: []string{"en", "fr"}, MediaType: "movie"}, 2,
			func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].Language != "en" {
					t.Errorf("got[0].Language = %q, want %q", got[0].Language, "en")
				}
				if got[1].Language != "fr" {
					t.Errorf("got[1].Language = %q, want %q", got[1].Language, "fr")
				}
			}},
		{"season and episode propagated",
			[]hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "en", ID: 1}},
			&api.SearchRequest{Languages: []string{"en"}, MediaType: "episode", Season: 3, Episode: 7}, 1,
			func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].Season != 3 {
					t.Errorf("Season = %d, want 3", got[0].Season)
				}
				if got[0].Episode != 7 {
					t.Errorf("Episode = %d, want 7", got[0].Episode)
				}
			}},
		{"multiple results mixed filtering",
			[]hdbSubtitleItem{
				{Title: "Good Sub", Filename: "good.srt", Language: "en", ID: 1},
				{Title: "Commentary", Filename: "c.srt", Language: "en", ID: 2},
				{Title: "French Sub", Filename: "fr.srt", Language: "fr", ID: 3},
				{Title: "Also Good", Filename: "also.srt", Language: "en", ID: 4},
			},
			&api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}, 2,
			func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].ID != "1" {
					t.Errorf("got[0].ID = %q, want %q", got[0].ID, "1")
				}
				if got[1].ID != "4" {
					t.Errorf("got[1].ID = %q, want %q", got[1].ID, "4")
				}
			}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := filterSubtitleData(tt.data, tt.req)
			if len(got) != tt.wantCount {
				t.Fatalf("filterSubtitleData() = %d results, want %d", len(got), tt.wantCount)
			}
			if tt.assertFn != nil {
				tt.assertFn(t, got)
			}
		})
	}

	// Subtitle extensions sub-table.
	t.Run("subtitle extensions accepted", func(t *testing.T) {
		t.Parallel()
		for _, ext := range []string{".srt", ".ass", ".ssa", ".sub", ".vtt", ".zip", ".rar"} {
			t.Run(ext, func(t *testing.T) {
				t.Parallel()
				data := []hdbSubtitleItem{{Title: "Sub", Filename: "sub" + ext, Language: "en", ID: 1}}
				req := &api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}
				got := filterSubtitleData(data, req)
				if len(got) != 1 {
					t.Errorf("filterSubtitleData(filename=%q) = %d results, want 1", "sub"+ext, len(got))
				}
			})
		}
	})
}

func TestFlexInt_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"number", `42`, 42, false},
		{"zero", `0`, 0, true}, // non-positive IDs rejected
		{"quoted string", `"123"`, 123, false},
		{"quoted zero", `"0"`, 0, true}, // non-positive IDs rejected
		{"non-numeric string", `"abc"`, 0, true},
		{"empty string", `""`, 0, true},
		{"negative number", `-5`, 0, true}, // non-positive IDs rejected
		{"negative quoted string", `"-10"`, 0, true},
		{"large number", `2147483647`, 2147483647, false},
		{"float", `3.14`, 0, true},
		{"quoted float", `"3.14"`, 0, true},
		{"boolean", `true`, 0, true},
		{"null value", `null`, 0, true}, // null rejected: id=0 would build getdox.php?id=0&passkey=...
		{"array", `[1]`, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got flexInt
			err := json.Unmarshal([]byte(tt.input), &got)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Unmarshal(%s) expected error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Unmarshal(%s) unexpected error: %v", tt.input, err)
			}
			if int(got) != tt.want {
				t.Errorf("Unmarshal(%s) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestFlexInt_in_struct(t *testing.T) {
	t.Parallel()

	// Simulates the real HDBits API returning id as a string.
	input := `{"title":"Test","filename":"test.srt","language":"en","id":"79310"}`
	var item hdbSubtitleItem
	if err := json.Unmarshal([]byte(input), &item); err != nil {
		t.Fatalf("Unmarshal string id: %v", err)
	}
	if int(item.ID) != 79310 {
		t.Errorf("ID = %d, want 79310", item.ID)
	}

	// Normal numeric id still works.
	input = `{"title":"Test","filename":"test.srt","language":"en","id":42}`
	if err := json.Unmarshal([]byte(input), &item); err != nil {
		t.Fatalf("Unmarshal numeric id: %v", err)
	}
	if int(item.ID) != 42 {
		t.Errorf("ID = %d, want 42", item.ID)
	}
}

func TestFlexInt_round_trip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 1<<31-1).Draw(t, "n")
		// Round-trip via JSON number.
		numJSON := []byte(strconv.Itoa(n))
		var got flexInt
		if err := json.Unmarshal(numJSON, &got); err != nil {
			t.Fatalf("flexInt.UnmarshalJSON(%s) error: %v", numJSON, err)
		}
		if int(got) != n {
			t.Errorf("flexInt round-trip via number: got %d, want %d", got, n)
		}
		// Round-trip via JSON string.
		strJSON := fmt.Appendf(nil, "%q", strconv.Itoa(n))
		var got2 flexInt
		if err := json.Unmarshal(strJSON, &got2); err != nil {
			t.Fatalf("flexInt.UnmarshalJSON(%s) error: %v", strJSON, err)
		}
		if int(got2) != n {
			t.Errorf("flexInt round-trip via string: got %d, want %d", got2, n)
		}
	})
}

func TestClearCache_empties_both_caches(t *testing.T) {
	t.Parallel()
	p := &Provider{
		torrentCache: cache.New[[]int](1 * time.Hour),
		dlCache:      dlcache.New(100, 2<<20),
	}
	p.dlCache.Put("1", []byte{0x50, 0x4b}, nil)
	p.torrentCache.Set("key", []int{1, 2})
	p.ClearCache()

	if _, ok := p.dlCache.Get("1"); ok {
		t.Error("dlCache still has entries after ClearCache")
	}
	if _, ok := p.torrentCache.Get("key"); ok {
		t.Error("torrentCache still has entries after ClearCache")
	}
}

func TestFilterSubtitleData_forced_in_title_not_filtered(t *testing.T) {
	t.Parallel()
	data := []hdbSubtitleItem{
		{Title: "Forced Narrative", Filename: "sub.srt", Language: "en", ID: 1},
	}
	req := &api.SearchRequest{Languages: []string{"en"}}
	got := filterSubtitleData(data, req)
	if len(got) != 1 {
		t.Errorf("filterSubtitleData() = %d results, want 1 (forced not filtered at provider level)", len(got))
	}
}

func TestFilterSubtitleData_extras_word_boundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		title string
		want  int
	}{
		{"extras at end", "Bonus Extras", 0},
		{"extras mid-sentence", "The Extras Collection", 0},
		{"extra without s", "Extra Features", 1},
		{"extraordinary", "Extraordinary Movie", 1},
		{"extras case insensitive", "EXTRAS Disc", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := []hdbSubtitleItem{
				{Title: tt.title, Filename: "sub.srt", Language: "en", ID: 1},
			}
			req := &api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}
			got := filterSubtitleData(data, req)
			if len(got) != tt.want {
				t.Errorf("filterSubtitleData(title=%q) = %d results, want %d",
					tt.title, len(got), tt.want)
			}
		})
	}
}

func TestFilterSubtitleData_does_not_set_variant_fields(t *testing.T) {
	t.Parallel()
	data := []hdbSubtitleItem{
		{Title: "Sub", Filename: "sub.srt", Language: "en", ID: 1},
	}
	req := &api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}
	got := filterSubtitleData(data, req)
	if len(got) != 1 {
		t.Fatalf("filterSubtitleData() = %d results, want 1", len(got))
	}
	if got[0].HearingImp {
		t.Error("filterSubtitleData() set HearingImp = true, want false")
	}
	if got[0].Forced {
		t.Error("filterSubtitleData() set Forced = true, want false")
	}
	if got[0].DownloadURL != "" {
		t.Errorf("filterSubtitleData() DownloadURL = %q, want empty", got[0].DownloadURL)
	}
	if got[0].Score != 0 {
		t.Errorf("filterSubtitleData() Score = %d, want 0", got[0].Score)
	}
}

func TestHdbLangToISO_all_overrides_return_expected(t *testing.T) {
	t.Parallel()
	for code, want := range hdbLangOverrides {
		got := hdbLangToISO(code)
		if got != want {
			t.Errorf("hdbLangToISO(%q) = %q, want %q", code, got, want)
		}
		if got == "" {
			t.Errorf("hdbLangToISO(%q) returned empty for override code", code)
		}
	}
}

func TestHdbLangToISO_standard_codes_return_identity(t *testing.T) {
	t.Parallel()
	// Standard codes in LangRegistry should return themselves,
	// unless an override maps them to something else.
	for code := range classify.LangRegistry {
		got := hdbLangToISO(code)
		if override, ok := hdbLangOverrides[code]; ok {
			if got != override {
				t.Errorf("hdbLangToISO(%q) = %q, want %q (override)", code, got, override)
			}
		} else if got != code {
			t.Errorf("hdbLangToISO(%q) = %q, want %q (identity via LangRegistry)", code, got, code)
		}
	}
}

func TestHdbLangToISO_unmapped_returns_empty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		code := rapid.StringMatching(`[a-z]{2,4}`).Draw(t, "code")
		got := hdbLangToISO(code)
		_, isOverride := hdbLangOverrides[code]
		_, isRegistry := classify.LangRegistry[code]
		if !isOverride && !isRegistry && got != "" {
			t.Errorf("hdbLangToISO(%q) = %q, want empty for unmapped code",
				code, got)
		}
	})
}

func TestFactory_initializes_provider_correctly(t *testing.T) {
	t.Parallel()
	p, err := Factory(context.Background(), map[string]any{"username": "testuser", "passkey": "testkey"})
	if err != nil {
		t.Fatalf("Factory() error: %v", err)
	}
	hdb, ok := p.(*Provider)
	if !ok {
		t.Fatal("Factory() did not return *Provider")
	}
	if hdb.username != "testuser" {
		t.Errorf("username = %q, want %q", hdb.username, "testuser")
	}
	if hdb.passkey != "testkey" {
		t.Errorf("passkey = %q, want %q", hdb.passkey, "testkey")
	}
	if hdb.client == nil {
		t.Error("client is nil")
	}
	if hdb.torrentCache == nil {
		t.Error("torrentCache is nil")
	}
}

func TestFilterSubtitleData_title_filename_concatenation(t *testing.T) {
	t.Parallel()

	t.Run("word split across title and filename does not match", func(t *testing.T) {
		t.Parallel()
		// "com" + " " + "mentary.srt" = "com mentary.srt" which does NOT contain "commentary"
		data := []hdbSubtitleItem{
			{Title: "com", Filename: "mentary.srt", Language: "en", ID: 1},
		}
		req := &api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}
		got := filterSubtitleData(data, req)
		if len(got) != 1 {
			t.Errorf("filterSubtitleData() = %d results, want 1 (split word should not match)", len(got))
		}
	})

	t.Run("commentary in title boundary", func(t *testing.T) {
		t.Parallel()
		data := []hdbSubtitleItem{
			{Title: "Movie commentary", Filename: "sub.srt", Language: "en", ID: 1},
		}
		req := &api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}
		got := filterSubtitleData(data, req)
		if len(got) != 0 {
			t.Errorf("filterSubtitleData() = %d results, want 0 (commentary at end of title)", len(got))
		}
	})
}

// BenchmarkFilterSubtitleData measures per-torrent subtitle filtering
// performance with varying item counts.
func BenchmarkFilterSubtitleData(b *testing.B) {
	makeItems := func(n int) []hdbSubtitleItem {
		items := make([]hdbSubtitleItem, n)
		for i := range items {
			items[i] = hdbSubtitleItem{
				Title:    "Movie Subtitle " + strconv.Itoa(i),
				Filename: "sub_" + strconv.Itoa(i) + ".srt",
				Language: "en",
				ID:       flexInt(i + 1),
			}
		}
		return items
	}
	req := &api.SearchRequest{Languages: []string{"en", "fr"}, MediaType: "movie"}

	for _, n := range []int{10, 50, 100} {
		items := makeItems(n)
		b.Run(fmt.Sprintf("items=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				filterSubtitleData(items, req)
			}
		})
	}
}

// --- buildLookup ---

func TestBuildLookup(t *testing.T) {
	t.Parallel()

	p := &Provider{username: "user", passkey: "key"}

	tests := []struct {
		name         string
		req          *api.SearchRequest
		wantNil      bool
		wantCacheKey string
		assertParams func(t *testing.T, params map[string]any)
	}{
		// Valid inputs.
		{
			name:         "episode with valid tvdb",
			req:          &api.SearchRequest{MediaType: "episode", TvdbID: 12345, Season: 3},
			wantCacheKey: "torrents:tvdb:12345:s3",
			assertParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				if params["username"] != "user" {
					t.Errorf("params[username] = %v, want %q", params["username"], "user")
				}
				if params["passkey"] != "key" {
					t.Errorf("params[passkey] = %v, want %q", params["passkey"], "key")
				}
				tvdb, ok := params["tvdb"].(map[string]any)
				if !ok {
					t.Fatalf("params[tvdb] = %T, want map[string]any", params["tvdb"])
				}
				if tvdb["id"] != 12345 {
					t.Errorf("params[tvdb][id] = %v, want 12345", tvdb["id"])
				}
				if tvdb["season"] != 3 {
					t.Errorf("params[tvdb][season] = %v, want 3", tvdb["season"])
				}
				if _, hasImdb := params["imdb"]; hasImdb {
					t.Error("params[imdb] set on episode lookup, want absent")
				}
			},
		},
		{
			name:         "episode with season zero",
			req:          &api.SearchRequest{MediaType: "episode", TvdbID: 99, Season: 0},
			wantCacheKey: "torrents:tvdb:99:s0",
			assertParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				tvdb, ok := params["tvdb"].(map[string]any)
				if !ok {
					t.Fatalf("params[tvdb] = %T, want map[string]any", params["tvdb"])
				}
				if tvdb["id"] != 99 {
					t.Errorf("params[tvdb][id] = %v, want 99", tvdb["id"])
				}
			},
		},
		{
			name:         "movie with valid imdb",
			req:          &api.SearchRequest{MediaType: "movie", ImdbID: "tt1375666"},
			wantCacheKey: "torrents:imdb:tt1375666",
			assertParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				imdb, ok := params["imdb"].(map[string]any)
				if !ok {
					t.Fatalf("params[imdb] = %T, want map[string]any", params["imdb"])
				}
				if imdb["id"] != 1375666 {
					t.Errorf("params[imdb][id] = %v, want 1375666", imdb["id"])
				}
				if _, hasTvdb := params["tvdb"]; hasTvdb {
					t.Error("params[tvdb] set on movie lookup, want absent")
				}
			},
		},
		{
			name:         "movie strips tt prefix and leading zeros",
			req:          &api.SearchRequest{MediaType: "movie", ImdbID: "tt0000042"},
			wantCacheKey: "torrents:imdb:tt0000042",
			assertParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				imdb := params["imdb"].(map[string]any)
				if imdb["id"] != 42 {
					t.Errorf("params[imdb][id] = %v, want 42 (leading zeros stripped)", imdb["id"])
				}
			},
		},
		{
			name:         "non episode uses imdb branch",
			req:          &api.SearchRequest{MediaType: "movie", ImdbID: "tt500", TvdbID: 9999},
			wantCacheKey: "torrents:imdb:tt500",
			assertParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				if _, hasTvdb := params["tvdb"]; hasTvdb {
					t.Error("params[tvdb] set when MediaType=movie, want IMDB branch only")
				}
				imdb := params["imdb"].(map[string]any)
				if imdb["id"] != 500 {
					t.Errorf("params[imdb][id] = %v, want 500", imdb["id"])
				}
			},
		},
		{
			name:         "cache key distinguishes seasons",
			req:          &api.SearchRequest{MediaType: "episode", TvdbID: 77, Season: 1},
			wantCacheKey: "torrents:tvdb:77:s1",
			assertParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				req2 := &api.SearchRequest{MediaType: "episode", TvdbID: 77, Season: 2}
				_, k2 := p.buildLookup(req2)
				if k2 != "torrents:tvdb:77:s2" {
					t.Errorf("S2 cacheKey = %q, want %q", k2, "torrents:tvdb:77:s2")
				}
			},
		},
		// Invalid inputs (params should be nil).
		{name: "episode with zero tvdb", req: &api.SearchRequest{MediaType: "episode", TvdbID: 0, Season: 1}, wantNil: true},
		{name: "episode with negative tvdb", req: &api.SearchRequest{MediaType: "episode", TvdbID: -1, Season: 1}, wantNil: true},
		{name: "movie with empty imdb", req: &api.SearchRequest{MediaType: "movie", ImdbID: ""}, wantNil: true},
		{name: "movie with non-numeric imdb", req: &api.SearchRequest{MediaType: "movie", ImdbID: "ttabc"}, wantNil: true},
		{name: "movie with letters inside imdb", req: &api.SearchRequest{MediaType: "movie", ImdbID: "tt12x45"}, wantNil: true},
		{name: "empty media type with empty imdb", req: &api.SearchRequest{MediaType: "", ImdbID: ""}, wantNil: true},
		{name: "unknown media type with no imdb", req: &api.SearchRequest{MediaType: "other", ImdbID: ""}, wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			params, cacheKey := p.buildLookup(tt.req)
			if tt.wantNil {
				if params != nil {
					t.Errorf("buildLookup() params = %v, want nil", params)
				}
				if cacheKey != "" {
					t.Errorf("buildLookup() cacheKey = %q, want empty", cacheKey)
				}
				return
			}
			if params == nil {
				t.Fatal("buildLookup() params = nil, want non-nil")
			}
			if cacheKey != tt.wantCacheKey {
				t.Errorf("cacheKey = %q, want %q", cacheKey, tt.wantCacheKey)
			}
			if tt.assertParams != nil {
				tt.assertParams(t, params)
			}
		})
	}
}

// --- redactPasskey ---

func TestRedactPasskey_strips_secret_from_error_message(t *testing.T) {
	t.Parallel()

	p := &Provider{passkey: "supersecret32hex"}

	in := fmt.Errorf("Get https://hdbits.org/getdox.php?id=1&passkey=%s: dial tcp: i/o timeout", p.passkey)
	got := httputil.RedactSecret(in, p.passkey)
	if got == nil {
		t.Fatal("redactPasskey returned nil for non-nil input")
	}
	if strings.Contains(got.Error(), p.passkey) {
		t.Errorf("redactPasskey did not strip passkey: %q", got.Error())
	}
	if !strings.Contains(got.Error(), "REDACTED") {
		t.Errorf("redactPasskey did not insert REDACTED marker: %q", got.Error())
	}
}

func TestRedactPasskey_pass_through_when_passkey_absent_from_message(t *testing.T) {
	t.Parallel()

	p := &Provider{passkey: "supersecret32hex"}
	in := errors.New("some error that does not leak the secret")
	got := httputil.RedactSecret(in, p.passkey)
	if got.Error() != in.Error() {
		t.Errorf("redactPasskey mutated safe error: got %q, want %q", got.Error(), in.Error())
	}
}

func TestRedactPasskey_nil_and_empty_passkey(t *testing.T) {
	t.Parallel()

	p := &Provider{passkey: ""}
	if got := httputil.RedactSecret(nil, p.passkey); got != nil {
		t.Errorf("redactPasskey(nil) = %v, want nil", got)
	}

	in := errors.New("anything")
	if got := httputil.RedactSecret(in, p.passkey); got.Error() != in.Error() {
		t.Errorf("redactPasskey with empty passkey mutated error: got %q", got.Error())
	}
}
