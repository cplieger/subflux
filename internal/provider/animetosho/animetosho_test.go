package animetosho

import (
	"bytes"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/cplieger/ssrf/v4"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/subflux"
	"pgregory.net/rapid"
)

func TestFactory(t *testing.T) {
	t.Parallel()

	p, err := Factory(t.Context(), nil)
	if err != nil {
		t.Fatalf("Factory(t.Context(), nil) unexpected error: %v", err)
	}
	if p.Name() != subflux.ProviderNameAnimeTosho {
		t.Errorf("Name() = %q, want %q", p.Name(), subflux.ProviderNameAnimeTosho)
	}
}

func TestFactory_accepts_any_settings(t *testing.T) {
	t.Parallel()

	p, err := Factory(t.Context(), map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("Factory(settings) unexpected error: %v", err)
	}
	if p.Name() != subflux.ProviderNameAnimeTosho {
		t.Errorf("Name() = %q, want %q", p.Name(), subflux.ProviderNameAnimeTosho)
	}
}

// --- Search early-return paths ---

func TestSearch_skips_non_episode(t *testing.T) {
	t.Parallel()
	p, _ := Factory(t.Context(), nil)
	req := &subflux.SearchRequest{MediaType: "movie", Title: "Akira", Languages: []string{"en"}}
	got, err := p.Search(t.Context(), req)
	if err != nil {
		t.Fatalf("Search() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("Search(movie) = %v, want nil", got)
	}
}

func TestSearch_skips_empty_title(t *testing.T) {
	t.Parallel()
	p, _ := Factory(t.Context(), nil)
	req := &subflux.SearchRequest{MediaType: "episode", Title: "", Languages: []string{"en"}}
	got, err := p.Search(t.Context(), req)
	if err != nil {
		t.Fatalf("Search() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("Search(empty title) = %v, want nil", got)
	}
}

// --- Download SSRF validation ---

func TestDownload_rejects_ssrf_url(t *testing.T) {
	t.Parallel()
	p, _ := Factory(t.Context(), nil)
	sub := &subflux.Subtitle{DownloadURL: "http://127.0.0.1/evil"}
	_, err := p.Download(t.Context(), sub)
	if err == nil {
		t.Fatal("Download(loopback URL) expected error, got nil")
	}
}

func TestDownload_rejects_internal_ip(t *testing.T) {
	t.Parallel()
	p, _ := Factory(t.Context(), nil)
	sub := &subflux.Subtitle{DownloadURL: "http://192.168.1.1/sub.srt"}
	_, err := p.Download(t.Context(), sub)
	if err == nil {
		t.Fatal("Download(private IP) expected error, got nil")
	}
	// The refusal must come from the URL validation, not from the transport
	// blocking the dial: a hostile URL in an API response never gets dialled.
	if _, isTransport := errors.AsType[*url.Error](err); isTransport {
		t.Errorf("Download(private IP) error = %v, want a pre-dial refusal, not a transport error", err)
	}
	if _, isSSRF := errors.AsType[*ssrf.Error](err); !isSSRF {
		t.Errorf("Download(private IP) error = %v, want an *ssrf.Error from the URL validation", err)
	}
}

// --- Diagnostics ---

// captureLogs routes the default logger into a buffer for the rest of the test
// and restores it afterwards. The default logger is process-global, so a test
// that calls this must not run in parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestFactory_reportsDisabledEpisodeIDResolution asserts the missing-key notice
// is emitted only when no anidb_client_key is configured — it is how an operator
// finds out episode ID resolution is off, so a copy on every start would be
// noise and silence on a keyless start would hide it.
// Not parallel: it swaps the process-wide default logger.
func TestFactory_reportsDisabledEpisodeIDResolution(t *testing.T) {
	const notice = "animetosho: no anidb_client_key, episode ID resolution disabled"

	t.Run("no key", func(t *testing.T) {
		logs := captureLogs(t)
		if _, err := Factory(t.Context(), nil); err != nil {
			t.Fatalf("Factory(nil): %v", err)
		}
		if got := logs.String(); !strings.Contains(got, notice) {
			t.Errorf("Factory(no key) log = %q, want it to contain %q", got, notice)
		}
	})

	t.Run("key configured", func(t *testing.T) {
		logs := captureLogs(t)
		if _, err := Factory(t.Context(), map[string]any{
			string(provider.KeyAniDBClientKey): "abc123",
		}); err != nil {
			t.Fatalf("Factory(with key): %v", err)
		}
		if got := logs.String(); strings.Contains(got, notice) {
			t.Errorf("Factory(with key) log = %q, want no disabled notice", got)
		}
	})
}

// TestMatchFiles_reportsAnUnmatchedPack asserts a season pack whose filenames
// carry no recognizable episode marker says so: the entry is skipped, and the
// line is the only trace of why nothing was downloaded.
// Not parallel: it swaps the process-wide default logger.
func TestMatchFiles_reportsAnUnmatchedPack(t *testing.T) {
	logs := captureLogs(t)
	files := []entryFile{{Filename: "pack disc a.mkv"}, {Filename: "pack disc b.mkv"}}

	if got := matchFiles(files, 1, 3, 0); got != nil {
		t.Errorf("matchFiles(unmatched pack) = %+v, want nil", got)
	}
	const want = `msg="animetosho: no file matched target episode in pack" season=1 episode=3 files=2`
	if got := logs.String(); !strings.Contains(got, want) {
		t.Errorf("matchFiles(unmatched pack) log = %q, want it to contain %q", got, want)
	}
}

// --- Entry Filtering ---

func TestFilterCompleteEntries(t *testing.T) {
	t.Parallel()

	t.Run("nil entries returns nil", func(t *testing.T) {
		t.Parallel()
		got := filterCompleteEntries(nil)
		if got != nil {
			t.Errorf("filterCompleteEntries(nil) = %v, want nil", got)
		}
	})

	t.Run("empty entries returns nil", func(t *testing.T) {
		t.Parallel()
		got := filterCompleteEntries([]feedEntry{})
		if got != nil {
			t.Errorf("filterCompleteEntries([]) = %v, want nil", got)
		}
	})

	t.Run("filters only complete entries", func(t *testing.T) {
		t.Parallel()
		entries := []feedEntry{
			{Title: "A", Status: "complete", ID: 1},
			{Title: "B", Status: "processing", ID: 2},
			{Title: "C", Status: "complete", ID: 3},
			{Title: "D", Status: "error", ID: 4},
		}
		got := filterCompleteEntries(entries)
		if len(got) != 2 {
			t.Fatalf("filterCompleteEntries() = %d entries, want 2", len(got))
		}
		if got[0].ID != 1 {
			t.Errorf("got[0].ID = %d, want 1", got[0].ID)
		}
		if got[1].ID != 3 {
			t.Errorf("got[1].ID = %d, want 3", got[1].ID)
		}
	})

	t.Run("caps at maxSearchEntries", func(t *testing.T) {
		t.Parallel()
		entries := make([]feedEntry, 20)
		for i := range entries {
			entries[i] = feedEntry{Title: "entry", Status: "complete", ID: i + 1}
		}
		got := filterCompleteEntries(entries)
		if len(got) != maxSearchEntries {
			t.Errorf("filterCompleteEntries() = %d entries, want %d (maxSearchEntries)",
				len(got), maxSearchEntries)
		}
	})

	t.Run("no complete entries returns nil", func(t *testing.T) {
		t.Parallel()
		entries := []feedEntry{
			{Title: "A", Status: "processing", ID: 1},
			{Title: "B", Status: "error", ID: 2},
		}
		got := filterCompleteEntries(entries)
		if got != nil {
			t.Errorf("filterCompleteEntries() = %v, want nil", got)
		}
	})

	t.Run("preserves entry fields", func(t *testing.T) {
		t.Parallel()
		entries := []feedEntry{
			{Title: "My Anime S01E01", Status: "complete", ID: 42},
		}
		got := filterCompleteEntries(entries)
		if len(got) != 1 {
			t.Fatalf("filterCompleteEntries() = %d entries, want 1", len(got))
		}
		if got[0].Title != "My Anime S01E01" {
			t.Errorf("Title = %q, want %q", got[0].Title, "My Anime S01E01")
		}
		if got[0].ID != 42 {
			t.Errorf("ID = %d, want 42", got[0].ID)
		}
	})
}

// --- Attachment Filtering ---

func TestFilterAttachments(t *testing.T) {
	t.Parallel()

	makeDetail := func(attachments ...entryAttachment) entryDetail {
		return entryDetail{
			Files: []entryFile{{Attachments: attachments}},
		}
	}

	t.Run("empty files returns nil", func(t *testing.T) {
		t.Parallel()
		got := filterAttachments(entryDetail{}, []string{"en"}, 1, 1, 0)
		if got != nil {
			t.Errorf("filterAttachments(empty) = %v, want nil", got)
		}
	})

	t.Run("basic subtitle mapped correctly", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   12345,
			Info: attachmentInfo{Lang: "eng"},
		})
		got := filterAttachments(detail, []string{"en"}, 1, 1, 0)
		if len(got) != 1 {
			t.Fatalf("filterAttachments() = %d results, want 1", len(got))
		}
		if got[0].Provider != "animetosho" {
			t.Errorf("Provider = %q, want %q", got[0].Provider, "animetosho")
		}
		if got[0].ID != "12345" {
			t.Errorf("ID = %q, want %q", got[0].ID, "12345")
		}
		if got[0].Language != "en" {
			t.Errorf("Language = %q, want %q", got[0].Language, "en")
		}
		if got[0].MatchedBy != "title" {
			t.Errorf("MatchedBy = %q, want %q", got[0].MatchedBy, "title")
		}
		wantURL := "https://animetosho.org/storage/attach/00003039/12345.xz"
		if got[0].DownloadURL != wantURL {
			t.Errorf("DownloadURL = %q, want %q", got[0].DownloadURL, wantURL)
		}
	})

	t.Run("non-subtitle type skipped", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "font",
			ID:   1,
			Info: attachmentInfo{Lang: "eng"},
		})
		got := filterAttachments(detail, []string{"en"}, 1, 1, 0)
		if len(got) != 0 {
			t.Errorf("filterAttachments() = %d results, want 0 (non-subtitle)", len(got))
		}
	})

	t.Run("zero ID skipped", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   0,
			Info: attachmentInfo{Lang: "eng"},
		})
		got := filterAttachments(detail, []string{"en"}, 1, 1, 0)
		if len(got) != 0 {
			t.Errorf("filterAttachments() = %d results, want 0 (zero ID)", len(got))
		}
	})

	t.Run("negative ID skipped", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   -5,
			Info: attachmentInfo{Lang: "eng"},
		})
		got := filterAttachments(detail, []string{"en"}, 1, 1, 0)
		if len(got) != 0 {
			t.Errorf("filterAttachments() = %d results, want 0 (negative ID)", len(got))
		}
	})

	t.Run("unknown language defaults to en", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   1,
			Info: attachmentInfo{Lang: ""},
		})
		got := filterAttachments(detail, []string{"en"}, 1, 1, 0)
		if len(got) != 1 {
			t.Fatalf("filterAttachments() = %d results, want 1", len(got))
		}
		if got[0].Language != "en" {
			t.Errorf("Language = %q, want %q (default)", got[0].Language, "en")
		}
	})

	t.Run("unknown language default not in requested list skipped", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   1,
			Info: attachmentInfo{Lang: ""},
		})
		got := filterAttachments(detail, []string{"fr"}, 1, 1, 0)
		if len(got) != 0 {
			t.Errorf("filterAttachments() = %d results, want 0", len(got))
		}
	})

	t.Run("alpha3 language mapped correctly", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   1,
			Info: attachmentInfo{Lang: "jpn"},
		})
		got := filterAttachments(detail, []string{"ja"}, 1, 1, 0)
		if len(got) != 1 {
			t.Fatalf("filterAttachments() = %d results, want 1", len(got))
		}
		if got[0].Language != "ja" {
			t.Errorf("Language = %q, want %q", got[0].Language, "ja")
		}
	})

	t.Run("language not in requested list skipped", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   1,
			Info: attachmentInfo{Lang: "fre"},
		})
		got := filterAttachments(detail, []string{"en"}, 1, 1, 0)
		if len(got) != 0 {
			t.Errorf("filterAttachments() = %d results, want 0", len(got))
		}
	})

	t.Run("multiple files with mixed attachments", func(t *testing.T) {
		t.Parallel()
		detail := entryDetail{
			Files: []entryFile{
				{Filename: "Show S01E01.mkv", Attachments: []entryAttachment{
					{Type: "subtitle", ID: 1, Info: attachmentInfo{Lang: "eng"}},
					{Type: "font", ID: 2, Info: attachmentInfo{Lang: "eng"}},
				}},
				{Filename: "Show S01E01.mkv", Attachments: []entryAttachment{
					{Type: "subtitle", ID: 3, Info: attachmentInfo{Lang: "jpn"}},
					{Type: "subtitle", ID: 0, Info: attachmentInfo{Lang: "eng"}},
				}},
			},
		}
		got := filterAttachments(detail, []string{"en", "ja"}, 1, 1, 0)
		if len(got) != 2 {
			t.Fatalf("filterAttachments() = %d results, want 2", len(got))
		}
		if got[0].ID != "1" {
			t.Errorf("got[0].ID = %q, want %q", got[0].ID, "1")
		}
		if got[1].ID != "3" {
			t.Errorf("got[1].ID = %q, want %q", got[1].ID, "3")
		}
	})

	t.Run("nil languages returns no results", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   1,
			Info: attachmentInfo{Lang: "eng"},
		})
		got := filterAttachments(detail, nil, 1, 1, 0)
		if len(got) != 0 {
			t.Errorf("filterAttachments() = %d results, want 0", len(got))
		}
	})

	t.Run("two-char language code passes through", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   1,
			Info: attachmentInfo{Lang: "en"},
		})
		got := filterAttachments(detail, []string{"en"}, 1, 1, 0)
		if len(got) != 1 {
			t.Fatalf("filterAttachments() = %d results, want 1", len(got))
		}
		if got[0].Language != "en" {
			t.Errorf("Language = %q, want %q", got[0].Language, "en")
		}
	})

	t.Run("download URL hex format correct", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   255,
			Info: attachmentInfo{Lang: "eng"},
		})
		got := filterAttachments(detail, []string{"en"}, 1, 1, 0)
		if len(got) != 1 {
			t.Fatalf("filterAttachments() = %d results, want 1", len(got))
		}
		wantURL := "https://animetosho.org/storage/attach/000000ff/255.xz"
		if got[0].DownloadURL != wantURL {
			t.Errorf("DownloadURL = %q, want %q", got[0].DownloadURL, wantURL)
		}
	})

	t.Run("large ID hex format correct", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   16777215, // 0x00ffffff
			Info: attachmentInfo{Lang: "eng"},
		})
		got := filterAttachments(detail, []string{"en"}, 1, 1, 0)
		if len(got) != 1 {
			t.Fatalf("filterAttachments() = %d results, want 1", len(got))
		}
		wantURL := "https://animetosho.org/storage/attach/00ffffff/16777215.xz"
		if got[0].DownloadURL != wantURL {
			t.Errorf("DownloadURL = %q, want %q", got[0].DownloadURL, wantURL)
		}
	})

	t.Run("empty languages returns no results", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   1,
			Info: attachmentInfo{Lang: "eng"},
		})
		got := filterAttachments(detail, []string{}, 1, 1, 0)
		if len(got) != 0 {
			t.Errorf("filterAttachments() = %d results, want 0", len(got))
		}
	})

	t.Run("Brazilian Portuguese detected from subtitle name", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   1,
			Info: attachmentInfo{Lang: "por", Name: "Brazilian Portuguese"},
		})
		got := filterAttachments(detail, []string{"pb"}, 1, 1, 0)
		if len(got) != 1 {
			t.Fatalf("filterAttachments() = %d results, want 1", len(got))
		}
		if got[0].Language != "pb" {
			t.Errorf("Language = %q, want %q", got[0].Language, "pb")
		}
	})

	t.Run("Portuguese without brazil stays pt", func(t *testing.T) {
		t.Parallel()
		detail := makeDetail(entryAttachment{
			Type: "subtitle",
			ID:   1,
			Info: attachmentInfo{Lang: "por", Name: "European Portuguese"},
		})
		got := filterAttachments(detail, []string{"pt"}, 1, 1, 0)
		if len(got) != 1 {
			t.Fatalf("filterAttachments() = %d results, want 1", len(got))
		}
		if got[0].Language != "pt" {
			t.Errorf("Language = %q, want %q", got[0].Language, "pt")
		}
	})
}

// --- File Matching ---

func TestFileMatchesEpisode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filename string
		season   int
		episode  int
		want     bool
	}{
		{"standard S01E01", "Show S01E01.mkv", 1, 1, true},
		{"standard S02E08", "Show S02E08 Title.mkv", 2, 8, true},
		{"wrong episode", "Show S01E02.mkv", 1, 1, false},
		{"wrong season", "Show S02E01.mkv", 1, 1, false},
		{"case insensitive", "show s01e01.mkv", 1, 1, true},
		{"sonarr format", "Mob Psycho 100 (2016) - S01E12 - Title [Bluray].mkv", 1, 12, true},
		{"empty filename", "", 1, 1, false},
		{"no episode pattern", "some random file.mkv", 1, 1, false},
		{"absolute episode fallback", "Show - 08.mkv", 1, 8, true},
		{"absolute with dash", "Show - 01 [720p].mkv", 1, 1, true},
		{"absolute e prefix", "Show e08.mkv", 1, 8, true},
		{"absolute no false positive", "Show - 100.mkv", 1, 1, false},
		{"e-prefix inside word", "Release01.mkv", 1, 1, false},
		{"e-prefix inside word premiere", "Premiere08.mkv", 1, 8, false},
		// The letter test is a range: 'a' and 'z' are both letters, so an e##
		// directly after either is still inside a word.
		{"e-prefix after the letter a", "Sae08.mkv", 1, 8, false},
		{"e-prefix after the letter z", "Blaze08.mkv", 1, 8, false},
		// A multibyte title character is not a letter as far as the ASCII range
		// test goes, so it is a word boundary like a space or a dot.
		{"e-prefix after a multibyte character", "日e08.mkv", 1, 8, true},
		{"e-prefix at filename start", "e08 - Show Title.mkv", 1, 8, true},
		{"padded number match", "Show 08 [720p].mkv", 1, 8, true},
		{"padded number wrong episode", "Show 03 [720p].mkv", 1, 8, false},
		{"multiple SxxExx, a later one matches", "Show.S09E09.S01E05.mkv", 1, 5, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := fileMatchesEpisode(tt.filename, tt.season, tt.episode, 0)
			if got != tt.want {
				t.Errorf("fileMatchesEpisode(%q, %d, %d) = %v, want %v",
					tt.filename, tt.season, tt.episode, got, tt.want)
			}
		})
	}
}

func TestFileMatchesEpisodeAbsoluteNumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		filename   string
		season     int
		episode    int
		absEpisode int
		want       bool
	}{
		{"batch named by absolute number", "[Group] Show - 26 [1080p].mkv", 2, 1, 26, true},
		{"absolute with e prefix", "Show e26.mkv", 2, 1, 26, true},
		{"absolute dash pad", "Show - 26.mkv", 2, 1, 26, true},
		{"aired number still matches first", "Show - 01.mkv", 2, 1, 26, true},
		{"neither number present", "Show - 99.mkv", 2, 1, 26, false},
		{"absolute ignored when equal to aired", "Show - 05.mkv", 1, 5, 5, true},
		{"absolute zero never probes", "Show - 00.mkv", 2, 1, 0, false},
		{"SxxExx present disables fallback", "Show S01E01 - 26.mkv", 2, 1, 26, false},
		// The fallback gate is marker SHAPE, not a readable marker: a season
		// number too wide to parse is still an explicit S##E## numbering, so
		// the absolute-number probe must stay off.
		{
			"unreadable SxxExx also disables fallback",
			"Show S99999999999999999999E01 - 26.mkv", 2, 1, 26, false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := fileMatchesEpisode(tt.filename, tt.season, tt.episode, tt.absEpisode)
			if got != tt.want {
				t.Errorf("fileMatchesEpisode(%q, %d, %d, abs=%d) = %v, want %v",
					tt.filename, tt.season, tt.episode, tt.absEpisode, got, tt.want)
			}
		})
	}
}

func TestMatchFiles(t *testing.T) {
	t.Parallel()

	t.Run("single file returned as-is", func(t *testing.T) {
		t.Parallel()
		files := []entryFile{{Filename: "whatever.mkv"}}
		got := matchFiles(files, 1, 1, 0)
		if len(got) != 1 {
			t.Errorf("matchFiles(single) = %d, want 1", len(got))
		}
	})

	t.Run("season pack filters to matching episode", func(t *testing.T) {
		t.Parallel()
		files := []entryFile{
			{Filename: "Show S01E01.mkv", Attachments: []entryAttachment{
				{Type: "subtitle", ID: 1, Info: attachmentInfo{Lang: "eng"}},
			}},
			{Filename: "Show S01E02.mkv", Attachments: []entryAttachment{
				{Type: "subtitle", ID: 2, Info: attachmentInfo{Lang: "eng"}},
			}},
			{Filename: "Show S01E03.mkv", Attachments: []entryAttachment{
				{Type: "subtitle", ID: 3, Info: attachmentInfo{Lang: "eng"}},
			}},
		}
		got := matchFiles(files, 1, 2, 0)
		if len(got) != 1 {
			t.Fatalf("matchFiles(pack, S01E02) = %d, want 1", len(got))
		}
		if got[0].Filename != "Show S01E02.mkv" {
			t.Errorf("matched file = %q, want S01E02", got[0].Filename)
		}
	})

	t.Run("season pack no match returns nil", func(t *testing.T) {
		t.Parallel()
		files := []entryFile{
			{Filename: "Show S01E01.mkv"},
			{Filename: "Show S01E02.mkv"},
		}
		got := matchFiles(files, 1, 5, 0)
		if got != nil {
			t.Errorf("matchFiles(no match) = %v, want nil", got)
		}
	})

	t.Run("empty files returns nil", func(t *testing.T) {
		t.Parallel()
		got := matchFiles(nil, 1, 1, 0)
		if got != nil {
			t.Errorf("matchFiles(nil) = %v, want nil", got)
		}
	})
}

func TestFilterAttachments_season_pack(t *testing.T) {
	t.Parallel()

	detail := entryDetail{
		Files: []entryFile{
			{Filename: "Show S01E01.mkv", Attachments: []entryAttachment{
				{Type: "subtitle", ID: 10, Info: attachmentInfo{Lang: "eng"}},
			}},
			{Filename: "Show S01E02.mkv", Attachments: []entryAttachment{
				{Type: "subtitle", ID: 20, Info: attachmentInfo{Lang: "eng"}},
			}},
			{Filename: "Show S01E03.mkv", Attachments: []entryAttachment{
				{Type: "subtitle", ID: 30, Info: attachmentInfo{Lang: "eng"}},
			}},
		},
	}

	got := filterAttachments(detail, []string{"en"}, 1, 2, 0)
	if len(got) != 1 {
		t.Fatalf("filterAttachments(season pack, E02) = %d, want 1", len(got))
	}
	if got[0].ID != "20" {
		t.Errorf("ID = %q, want %q", got[0].ID, "20")
	}
}

func TestFileMatchesEpisode_case_insensitive(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Restrict to ASCII so ToLower/ToUpper round-trip cleanly. The matcher
		// lowercases internally and uses a case-insensitive regex, so its
		// verdict must never depend on the filename's letter case.
		name := rapid.StringMatching(`[A-Za-z0-9 .-]{0,40}`).Draw(t, "filename")
		season := rapid.IntRange(0, 30).Draw(t, "season")
		episode := rapid.IntRange(0, 300).Draw(t, "episode")

		lower := fileMatchesEpisode(strings.ToLower(name), season, episode, 0)
		upper := fileMatchesEpisode(strings.ToUpper(name), season, episode, 0)
		if lower != upper {
			t.Fatalf("fileMatchesEpisode(%q, %d, %d) case mismatch: lower=%v upper=%v",
				name, season, episode, lower, upper)
		}
	})
}

func FuzzFileMatchesEpisode(f *testing.F) {
	// Seed corpus with known anime filename patterns.
	f.Add("[SubGroup] Anime Title - 05 [1080p].mkv", 1, 5)
	f.Add("Show.S01E05.720p.WEB-DL.mkv", 1, 5)
	f.Add("[Group] Title - S2E12 [720p].ass", 2, 12)
	f.Add("e05.srt", 1, 5)
	f.Add("Title - 105.srt", 1, 105)
	f.Add("Season Pack S01 Complete", 1, 1)
	f.Add("", 0, 0)
	f.Add("random garbage !@#$%", 3, 99)
	f.Fuzz(func(t *testing.T, filename string, season, episode int) {
		// Must not panic on any input.
		_ = fileMatchesEpisode(filename, season, episode, 0)
	})
}
