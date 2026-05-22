package animetosho

import (
	"bytes"
	"compress/gzip"
	"context"
	"testing"

	"pgregory.net/rapid"

	"github.com/ulikunitz/xz"

	"subflux/internal/api"
	"subflux/internal/provider/archive"
)

func TestFactory(t *testing.T) {
	t.Parallel()

	p, err := Factory(context.Background(), nil)
	if err != nil {
		t.Fatalf("Factory(context.Background(), nil) unexpected error: %v", err)
	}
	if p.Name() != api.ProviderNameAnimeTosho {
		t.Errorf("Name() = %q, want %q", p.Name(), api.ProviderNameAnimeTosho)
	}
}

func TestFactory_accepts_any_settings(t *testing.T) {
	t.Parallel()

	p, err := Factory(context.Background(), map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("Factory(settings) unexpected error: %v", err)
	}
	if p.Name() != api.ProviderNameAnimeTosho {
		t.Errorf("Name() = %q, want %q", p.Name(), api.ProviderNameAnimeTosho)
	}
}

// --- Search early-return paths ---

func TestSearch_skips_non_episode(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), nil)
	req := &api.SearchRequest{MediaType: "movie", Title: "Akira", Languages: []string{"en"}}
	got, err := p.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("Search() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("Search(movie) = %v, want nil", got)
	}
}

func TestSearch_skips_empty_title(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), nil)
	req := &api.SearchRequest{MediaType: "episode", Title: "", Languages: []string{"en"}}
	got, err := p.Search(context.Background(), req)
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
		got := filterAttachments(entryDetail{}, []string{"en"}, 1, 1)
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
		got := filterAttachments(detail, []string{"en"}, 1, 1)
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
		got := filterAttachments(detail, []string{"en"}, 1, 1)
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
		got := filterAttachments(detail, []string{"en"}, 1, 1)
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
		got := filterAttachments(detail, []string{"en"}, 1, 1)
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
		got := filterAttachments(detail, []string{"en"}, 1, 1)
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
		got := filterAttachments(detail, []string{"fr"}, 1, 1)
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
		got := filterAttachments(detail, []string{"ja"}, 1, 1)
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
		got := filterAttachments(detail, []string{"en"}, 1, 1)
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
		got := filterAttachments(detail, []string{"en", "ja"}, 1, 1)
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
		got := filterAttachments(detail, nil, 1, 1)
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
		got := filterAttachments(detail, []string{"en"}, 1, 1)
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
		got := filterAttachments(detail, []string{"en"}, 1, 1)
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
		got := filterAttachments(detail, []string{"en"}, 1, 1)
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
		got := filterAttachments(detail, []string{}, 1, 1)
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
		got := filterAttachments(detail, []string{"pb"}, 1, 1)
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
		got := filterAttachments(detail, []string{"pt"}, 1, 1)
		if len(got) != 1 {
			t.Fatalf("filterAttachments() = %d results, want 1", len(got))
		}
		if got[0].Language != "pt" {
			t.Errorf("Language = %q, want %q", got[0].Language, "pt")
		}
	})
}

// --- Decompression ---

func TestDecompressData(t *testing.T) {
	t.Parallel()

	t.Run("plain data returned unchanged", func(t *testing.T) {
		t.Parallel()
		data := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")
		got := archive.Decompress(data)
		if !bytes.Equal(got, data) {
			t.Errorf("decompressData(plain) = %q, want %q", got, data)
		}
	})

	t.Run("nil data returned as nil", func(t *testing.T) {
		t.Parallel()
		got := archive.Decompress(nil)
		if got != nil {
			t.Errorf("decompressData(nil) = %v, want nil", got)
		}
	})

	t.Run("empty data returned as empty", func(t *testing.T) {
		t.Parallel()
		got := archive.Decompress([]byte{})
		if len(got) != 0 {
			t.Errorf("decompressData([]) = %v, want empty", got)
		}
	})

	t.Run("gzip data decompressed", func(t *testing.T) {
		t.Parallel()
		original := []byte("subtitle content here")
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		if _, err := gw.Write(original); err != nil {
			t.Fatalf("gzip.Write: %v", err)
		}
		if err := gw.Close(); err != nil {
			t.Fatalf("gzip.Close: %v", err)
		}
		got := archive.Decompress(buf.Bytes())
		if !bytes.Equal(got, original) {
			t.Errorf("decompressData(gzip) = %q, want %q", got, original)
		}
	})

	t.Run("xz invalid header returned as-is", func(t *testing.T) {
		t.Parallel()
		// xz magic bytes: FD 37 7A 58 5A 00 + invalid payload
		xzData := []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00, 0x01, 0x02, 0x03}
		got := archive.Decompress(xzData)
		if !bytes.Equal(got, xzData) {
			t.Errorf("decompressData(invalid xz) should return data as-is")
		}
	})

	t.Run("valid xz data decompressed", func(t *testing.T) {
		t.Parallel()
		original := []byte("subtitle content for xz test")
		var buf bytes.Buffer
		w, err := xz.NewWriter(&buf)
		if err != nil {
			t.Fatalf("xz.NewWriter: %v", err)
		}
		if _, err := w.Write(original); err != nil {
			t.Fatalf("xz.Write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("xz.Close: %v", err)
		}
		got := archive.Decompress(buf.Bytes())
		if !bytes.Equal(got, original) {
			t.Errorf("decompressData(valid xz) = %q, want %q", got, original)
		}
	})

	t.Run("short data with gzip magic returned as-is", func(t *testing.T) {
		t.Parallel()
		// Only 2 bytes matching gzip magic but too short to be valid gzip
		data := []byte{0x1f, 0x8b}
		got := archive.Decompress(data)
		if !bytes.Equal(got, data) {
			t.Errorf("decompressData(short gzip magic) should return data as-is")
		}
	})

	t.Run("invalid gzip data returned as-is", func(t *testing.T) {
		t.Parallel()
		// Gzip magic bytes but invalid content
		data := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0xFF, 0xFF}
		got := archive.Decompress(data)
		if !bytes.Equal(got, data) {
			t.Errorf("decompressData(invalid gzip) should return data as-is")
		}
	})

	t.Run("short xz magic not detected", func(t *testing.T) {
		t.Parallel()
		// Only 6 bytes with xz-like prefix but len <= 6
		data := []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00}
		got := archive.Decompress(data)
		// len(data) == 6, not > 6, so xz branch not taken; returned as-is
		if !bytes.Equal(got, data) {
			t.Errorf("decompressData(short xz) should return data as-is")
		}
	})

	t.Run("truncated gzip header returned as-is", func(t *testing.T) {
		t.Parallel()
		// Gzip magic + method byte but header truncated (< 10 bytes needed).
		// gzip.NewReader fails on incomplete header → data returned as-is.
		data := []byte{0x1f, 0x8b, 0x08}
		got := archive.Decompress(data)
		if !bytes.Equal(got, data) {
			t.Errorf("decompressData(truncated gzip header) = %v, want %v", got, data)
		}
	})

	t.Run("empty gzip stream decompresses to empty", func(t *testing.T) {
		t.Parallel()
		// Valid gzip with zero-length payload.
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		if err := gw.Close(); err != nil {
			t.Fatalf("gzip.Close: %v", err)
		}
		got := archive.Decompress(buf.Bytes())
		if len(got) != 0 {
			t.Errorf("decompressData(empty gzip) = %d bytes, want 0", len(got))
		}
	})

	t.Run("xz truncated stream returns raw data", func(t *testing.T) {
		t.Parallel()
		// Build a valid xz stream, then truncate it mid-payload to trigger
		// a read error (not a header error).
		original := bytes.Repeat([]byte("subtitle data for truncation test\n"), 100)
		var buf bytes.Buffer
		w, err := xz.NewWriter(&buf)
		if err != nil {
			t.Fatalf("xz.NewWriter: %v", err)
		}
		if _, err := w.Write(original); err != nil {
			t.Fatalf("xz.Write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("xz.Close: %v", err)
		}
		full := buf.Bytes()
		// Keep the header + some stream data, but chop before the footer
		// so decompression fails mid-read.
		truncated := full[:len(full)/2]
		got := archive.Decompress(truncated)
		// Should return raw data on read error.
		if !bytes.Equal(got, truncated) {
			t.Errorf("decompressData(truncated xz) should return raw data, got %d bytes", len(got))
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
		{"e-prefix at filename start", "e08 - Show Title.mkv", 1, 8, true},
		{"padded number match", "Show 08 [720p].mkv", 1, 8, true},
		{"padded number wrong episode", "Show 03 [720p].mkv", 1, 8, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := fileMatchesEpisode(tt.filename, tt.season, tt.episode)
			if got != tt.want {
				t.Errorf("fileMatchesEpisode(%q, %d, %d) = %v, want %v",
					tt.filename, tt.season, tt.episode, got, tt.want)
			}
		})
	}
}

func TestMatchFiles(t *testing.T) {
	t.Parallel()

	t.Run("single file returned as-is", func(t *testing.T) {
		t.Parallel()
		files := []entryFile{{Filename: "whatever.mkv"}}
		got := matchFiles(files, 1, 1)
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
		got := matchFiles(files, 1, 2)
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
		got := matchFiles(files, 1, 5)
		if got != nil {
			t.Errorf("matchFiles(no match) = %v, want nil", got)
		}
	})

	t.Run("empty files returns nil", func(t *testing.T) {
		t.Parallel()
		got := matchFiles(nil, 1, 1)
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

	got := filterAttachments(detail, []string{"en"}, 1, 2)
	if len(got) != 1 {
		t.Fatalf("filterAttachments(season pack, E02) = %d, want 1", len(got))
	}
	if got[0].ID != "20" {
		t.Errorf("ID = %q, want %q", got[0].ID, "20")
	}
}

func TestDecompressData_gzip_round_trip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		original := rapid.SliceOfN(rapid.Byte(), 1, 4096).Draw(t, "data")
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		if _, err := gw.Write(original); err != nil {
			t.Fatalf("gzip.Write: %v", err)
		}
		if err := gw.Close(); err != nil {
			t.Fatalf("gzip.Close: %v", err)
		}
		got := archive.Decompress(buf.Bytes())
		if !bytes.Equal(got, original) {
			t.Fatalf("decompressData(gzip(%d bytes)) = %d bytes, want %d",
				len(original), len(got), len(original))
		}
	})
}

func TestDecompressData_xz_round_trip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		original := rapid.SliceOfN(rapid.Byte(), 1, 4096).Draw(t, "data")
		var buf bytes.Buffer
		w, err := xz.NewWriter(&buf)
		if err != nil {
			t.Fatalf("xz.NewWriter: %v", err)
		}
		if _, err := w.Write(original); err != nil {
			t.Fatalf("xz.Write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("xz.Close: %v", err)
		}
		got := archive.Decompress(buf.Bytes())
		if !bytes.Equal(got, original) {
			t.Fatalf("decompressData(xz(%d bytes)) = %d bytes, want %d",
				len(original), len(got), len(original))
		}
	})
}

func TestDecompressData_non_compressed_passthrough(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		data := rapid.SliceOfN(rapid.Byte(), 0, 4096).Draw(t, "data")
		// Ensure data doesn't start with gzip or xz magic.
		if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
			data[0] = 0x00
		}
		if len(data) > 6 && data[0] == 0xFD && data[1] == 0x37 &&
			data[2] == 0x7A && data[3] == 0x58 &&
			data[4] == 0x5A && data[5] == 0x00 {
			data[0] = 0x00
		}
		got := archive.Decompress(data)
		if !bytes.Equal(got, data) {
			t.Fatalf("decompressData(non-compressed %d bytes) modified the data", len(data))
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
		_ = fileMatchesEpisode(filename, season, episode)
	})
}
