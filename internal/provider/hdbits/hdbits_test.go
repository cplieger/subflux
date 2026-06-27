package hdbits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/cache"
	"github.com/cplieger/subflux/internal/provider/classify"
	"github.com/cplieger/subflux/internal/provider/dlcache"
	"pgregory.net/rapid"
)

func TestHdbLangToISO(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "en maps to en", input: "en", want: "en"},
		{name: "fr maps to fr", input: "fr", want: "fr"},
		{name: "es maps to es", input: "es", want: "es"},
		{name: "de maps to de", input: "de", want: "de"},
		{name: "it maps to it", input: "it", want: "it"},
		{name: "pt maps to pt", input: "pt", want: "pt"},
		{name: "nl maps to nl", input: "nl", want: "nl"},
		{name: "ru maps to ru", input: "ru", want: "ru"},
		{name: "ja maps to ja", input: "ja", want: "ja"},
		{name: "zh maps to zh", input: "zh", want: "zh"},
		{name: "ko maps to ko", input: "ko", want: "ko"},
		{name: "pl maps to pl", input: "pl", want: "pl"},
		{name: "sv maps to sv", input: "sv", want: "sv"},
		{name: "no maps to no", input: "no", want: "no"},
		{name: "da maps to da", input: "da", want: "da"},
		{name: "fi maps to fi", input: "fi", want: "fi"},
		{name: "uk maps to uk", input: "uk", want: "uk"},
		{name: "br maps to pb", input: "br", want: "pb"},
		{name: "gr maps to el", input: "gr", want: "el"},
		{name: "cz maps to cs", input: "cz", want: "cs"},
		{name: "se maps to sv", input: "se", want: "sv"},
		{name: "dk maps to da", input: "dk", want: "da"},
		{name: "kr maps to ko", input: "kr", want: "ko"},
		{name: "cn maps to zh", input: "cn", want: "zh"},
		{name: "il maps to he", input: "il", want: "he"},
		{name: "si maps to sl", input: "si", want: "sl"},
		{name: "unknown code", input: "xx", want: ""},
		{name: "empty string", input: "", want: ""},
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
		settings map[string]any
		name     string
		wantErr  bool
	}{
		{name: "nil settings", settings: nil, wantErr: true},
		{name: "missing both", settings: map[string]any{}, wantErr: true},
		{name: "missing passkey", settings: map[string]any{"username": "user"}, wantErr: true},
		{name: "missing username", settings: map[string]any{"passkey": "key"}, wantErr: true},
		{name: "empty username", settings: map[string]any{"username": "", "passkey": "key"}, wantErr: true},
		{name: "empty passkey", settings: map[string]any{"username": "user", "passkey": ""}, wantErr: true},
		{name: "valid credentials", settings: map[string]any{"username": "user", "passkey": "key"}, wantErr: false},
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

// TestFactory_torrentCacheUsesOneHourTTL verifies the torrent-ID cache the
// factory builds keeps entries alive well beyond a single request: a value
// stored and read back a few milliseconds later must still be present, which
// fails if the cache were constructed with a zero (or near-zero) TTL.
func TestFactory_torrentCacheUsesOneHourTTL(t *testing.T) {
	p, err := Factory(context.Background(), map[string]any{
		settingUsername: "user",
		settingPasskey:  "passkey",
	})
	if err != nil {
		t.Fatalf("Factory() error = %v, want nil", err)
	}
	prov, ok := p.(*Provider)
	if !ok {
		t.Fatalf("Factory() returned %T, want *Provider", p)
	}

	const key = "torrents:tvdb:1:s1"
	want := []int{11, 22, 33}
	prov.torrentCache.Set(key, want)

	// Far below the real 1h TTL, but past the Set instant a zero TTL would
	// expire entries at.
	time.Sleep(5 * time.Millisecond)

	got, found := prov.torrentCache.Get(key)
	if !found {
		t.Fatalf("torrentCache.Get(%q) found = false, want true", key)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("torrentCache.Get(%q) = %v, want %v", key, got, want)
	}
}

// --- Subtitle Data Filtering ---

func TestFilterSubtitleData(t *testing.T) {
	t.Parallel()

	type assertFunc func(t *testing.T, got []api.Subtitle)

	tests := []struct {
		req       *api.SearchRequest
		assertFn  assertFunc
		name      string
		data      []hdbSubtitleItem
		wantCount int
	}{
		{name: "nil data returns nil", data: nil, req: &api.SearchRequest{Languages: []string{"en"}}, wantCount: 0, assertFn: nil},
		{name: "empty data returns nil", data: []hdbSubtitleItem{}, req: &api.SearchRequest{Languages: []string{"en"}}, wantCount: 0, assertFn: nil},
		{name: "basic movie result mapped correctly", data: []hdbSubtitleItem{{Title: "Test Sub", Filename: "test.srt", Language: "en", ID: 42}}, req: &api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}, wantCount: 1, assertFn: func(t *testing.T, got []api.Subtitle) {
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
		{name: "episode sets matched_by to tvdb_id", data: []hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}, MediaType: "episode"}, wantCount: 1, assertFn: func(t *testing.T, got []api.Subtitle) {
			t.Helper()
			if got[0].MatchedBy != api.MatchByTVDB {
				t.Errorf("MatchedBy = %q, want %q", got[0].MatchedBy, api.MatchByTVDB)
			}
		}},
		{name: "unknown language skipped", data: []hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "xx", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}}, wantCount: 0, assertFn: nil},
		{name: "language not in requested list skipped", data: []hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "fr", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}}, wantCount: 0, assertFn: nil},
		{name: "br mapped to pb", data: []hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "br", ID: 1}}, req: &api.SearchRequest{Languages: []string{"pb"}}, wantCount: 1, assertFn: func(t *testing.T, got []api.Subtitle) {
			t.Helper()
			if got[0].Language != "pb" {
				t.Errorf("Language = %q, want %q", got[0].Language, "pb")
			}
		}},
		{name: "commentary title filtered", data: []hdbSubtitleItem{{Title: "Director's Commentary", Filename: "sub.srt", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}}, wantCount: 0, assertFn: nil},
		{name: "commentary filename filtered", data: []hdbSubtitleItem{{Title: "Normal Title", Filename: "commentary.srt", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}}, wantCount: 0, assertFn: nil},
		{name: "extras title filtered", data: []hdbSubtitleItem{{Title: "Extras Disc", Filename: "sub.srt", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}}, wantCount: 0, assertFn: nil},
		{name: "extra without s not filtered", data: []hdbSubtitleItem{{Title: "Extraordinary Movie", Filename: "sub.srt", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}, wantCount: 1, assertFn: nil},
		{name: "case insensitive commentary", data: []hdbSubtitleItem{{Title: "COMMENTARY Track", Filename: "sub.srt", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}}, wantCount: 0, assertFn: nil},
		{name: "nil languages returns no results", data: []hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: nil}, wantCount: 0, assertFn: nil},
		{name: "empty languages returns no results", data: []hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: []string{}}, wantCount: 0, assertFn: nil},
		{name: "gr mapped to el", data: []hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "gr", ID: 1}}, req: &api.SearchRequest{Languages: []string{"el"}}, wantCount: 1, assertFn: func(t *testing.T, got []api.Subtitle) {
			t.Helper()
			if got[0].Language != "el" {
				t.Errorf("Language = %q, want %q", got[0].Language, "el")
			}
		}},
		{name: "non-subtitle extension filtered", data: []hdbSubtitleItem{{Title: "Sub", Filename: "sub.mp4", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}, wantCount: 0, assertFn: nil},
		{name: "no extension accepted", data: []hdbSubtitleItem{{Title: "Sub", Filename: "subtitle_no_ext", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}, wantCount: 1, assertFn: nil},
		{name: "lyrics title filtered", data: []hdbSubtitleItem{{Title: "Song Lyrics", Filename: "sub.srt", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}}, wantCount: 0, assertFn: nil},
		{name: "forced in filename not filtered", data: []hdbSubtitleItem{{Title: "Normal", Filename: "forced.srt", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}}, wantCount: 1, assertFn: nil},
		{name: "lyrics filename filtered", data: []hdbSubtitleItem{{Title: "Normal", Filename: "lyrics.srt", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}}, wantCount: 0, assertFn: nil},
		{name: "multiple requested languages", data: []hdbSubtitleItem{
			{Title: "English Sub", Filename: "en.srt", Language: "en", ID: 1},
			{Title: "French Sub", Filename: "fr.srt", Language: "fr", ID: 2},
			{Title: "German Sub", Filename: "de.srt", Language: "de", ID: 3},
		}, req: &api.SearchRequest{Languages: []string{"en", "fr"}, MediaType: "movie"}, wantCount: 2, assertFn: func(t *testing.T, got []api.Subtitle) {
			t.Helper()
			if got[0].Language != "en" {
				t.Errorf("got[0].Language = %q, want %q", got[0].Language, "en")
			}
			if got[1].Language != "fr" {
				t.Errorf("got[1].Language = %q, want %q", got[1].Language, "fr")
			}
		}},
		{name: "season and episode propagated", data: []hdbSubtitleItem{{Title: "Sub", Filename: "sub.srt", Language: "en", ID: 1}}, req: &api.SearchRequest{Languages: []string{"en"}, MediaType: "episode", Season: 3, Episode: 7}, wantCount: 1, assertFn: func(t *testing.T, got []api.Subtitle) {
			t.Helper()
			if got[0].Season != 3 {
				t.Errorf("Season = %d, want 3", got[0].Season)
			}
			if got[0].Episode != 7 {
				t.Errorf("Episode = %d, want 7", got[0].Episode)
			}
		}},
		{name: "multiple results mixed filtering", data: []hdbSubtitleItem{
			{Title: "Good Sub", Filename: "good.srt", Language: "en", ID: 1},
			{Title: "Commentary", Filename: "c.srt", Language: "en", ID: 2},
			{Title: "French Sub", Filename: "fr.srt", Language: "fr", ID: 3},
			{Title: "Also Good", Filename: "also.srt", Language: "en", ID: 4},
		}, req: &api.SearchRequest{Languages: []string{"en"}, MediaType: "movie"}, wantCount: 2, assertFn: func(t *testing.T, got []api.Subtitle) {
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
		{name: "number", input: `42`, want: 42, wantErr: false},
		{name: "zero", input: `0`, want: 0, wantErr: true}, // non-positive IDs rejected
		{name: "quoted string", input: `"123"`, want: 123, wantErr: false},
		{name: "quoted zero", input: `"0"`, want: 0, wantErr: true}, // non-positive IDs rejected
		{name: "non-numeric string", input: `"abc"`, want: 0, wantErr: true},
		{name: "empty string", input: `""`, want: 0, wantErr: true},
		{name: "negative number", input: `-5`, want: 0, wantErr: true}, // non-positive IDs rejected
		{name: "negative quoted string", input: `"-10"`, want: 0, wantErr: true},
		{name: "large number", input: `2147483647`, want: 2147483647, wantErr: false},
		{name: "float", input: `3.14`, want: 0, wantErr: true},
		{name: "quoted float", input: `"3.14"`, want: 0, wantErr: true},
		{name: "boolean", input: `true`, want: 0, wantErr: true},
		{name: "null value", input: `null`, want: 0, wantErr: true}, // null rejected: id=0 would build getdox.php?id=0&passkey=...
		{name: "array", input: `[1]`, want: 0, wantErr: true},
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
		{name: "extras at end", title: "Bonus Extras", want: 0},
		{name: "extras mid-sentence", title: "The Extras Collection", want: 0},
		{name: "extra without s", title: "Extra Features", want: 1},
		{name: "extraordinary", title: "Extraordinary Movie", want: 1},
		{name: "extras case insensitive", title: "EXTRAS Disc", want: 0},
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
		req          *api.SearchRequest
		assertParams func(t *testing.T, params map[string]any)
		name         string
		wantCacheKey string
		wantNil      bool
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

// --- capTorrentIDs ---

func TestCapTorrentIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ids    []int
		maxCap int
		want   []int
	}{
		{name: "nil stays nil", ids: nil, maxCap: 5, want: nil},
		{name: "empty stays empty", ids: []int{}, maxCap: 5, want: []int{}},
		{name: "under cap unchanged", ids: []int{1, 2, 3}, maxCap: 5, want: []int{1, 2, 3}},
		{name: "at cap unchanged", ids: []int{1, 2, 3, 4, 5}, maxCap: 5, want: []int{1, 2, 3, 4, 5}},
		{name: "over cap truncated to first maxCap", ids: []int{1, 2, 3, 4, 5, 6, 7}, maxCap: 5, want: []int{1, 2, 3, 4, 5}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := capTorrentIDs(tt.ids, tt.maxCap, "torrents:imdb:tt1")
			if !slices.Equal(got, tt.want) {
				t.Errorf("capTorrentIDs(%v, %d) = %v, want %v", tt.ids, tt.maxCap, got, tt.want)
			}
		})
	}
}

// --- secret redaction (provider wiring) ---

// roundTripFunc adapts a function to http.RoundTripper so a test can drive
// the provider's HTTP client without a real network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestDownload_redactsPasskeyFromTransportError verifies the public Download
// path strips the passkey from transport errors: the getdox URL carries the
// passkey as a query parameter, so a failed request surfaces it inside the
// wrapping *url.Error unless the provider redacts it.
func TestDownload_redactsPasskeyFromTransportError(t *testing.T) {
	t.Parallel()
	const passkey = "supersecret32hex"
	p := &Provider{
		passkey: passkey,
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: i/o timeout")
		})},
		dlCache: dlcache.New(10, 1<<20),
	}

	_, err := p.Download(context.Background(), &api.Subtitle{ID: "123"})
	if err == nil {
		t.Fatal("Download() with a failing transport expected an error")
	}
	if strings.Contains(err.Error(), passkey) {
		t.Errorf("Download() leaked passkey in error: %q", err.Error())
	}
	// Redaction unwraps the *url.Error but must preserve the underlying cause.
	if !strings.Contains(err.Error(), "i/o timeout") {
		t.Errorf("Download() error lost the transport cause: %q", err.Error())
	}
}
