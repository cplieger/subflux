package store

import (
	"context"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

func TestRecordSubtitleFiles_inserts_and_retrieves(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	files := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: "/media/movie.en.srt"},
		{Language: "fr", Variant: "hi", Source: "embedded", Codec: "ass", Path: ""},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", files); err != nil {
		t.Fatalf("RecordSubtitleFiles() unexpected error: %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-123")
	if err != nil {
		t.Fatalf("GetSubtitleFiles() unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("GetSubtitleFiles() returned %d rows, want 2", len(rows))
	}

	if rows[0].Language != "en" {
		t.Errorf("rows[0].Language = %q, want %q", rows[0].Language, "en")
	}
	if rows[0].Variant != "standard" {
		t.Errorf("rows[0].Variant = %q, want %q", rows[0].Variant, "standard")
	}
	if rows[0].Source != "external" {
		t.Errorf("rows[0].Source = %q, want %q", rows[0].Source, "external")
	}
	if rows[0].Codec != "subrip" {
		t.Errorf("rows[0].Codec = %q, want %q", rows[0].Codec, "subrip")
	}
	if rows[0].Path != "/media/movie.en.srt" {
		t.Errorf("rows[0].Path = %q, want %q", rows[0].Path, "/media/movie.en.srt")
	}

	if rows[1].Language != "fr" {
		t.Errorf("rows[1].Language = %q, want %q", rows[1].Language, "fr")
	}
	if rows[1].Source != "embedded" {
		t.Errorf("rows[1].Source = %q, want %q", rows[1].Source, "embedded")
	}
}

func TestRecordSubtitleFiles_replaces_existing(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	initial := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/old.srt"},
		{Language: "fr", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/old-fr.srt"},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", initial); err != nil {
		t.Fatalf("RecordSubtitleFiles(initial): %v", err)
	}

	replacement := []api.SubtitleFile{
		{Language: "de", Variant: "hi", Source: "embedded", Codec: "ass", Path: ""},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", replacement); err != nil {
		t.Fatalf("RecordSubtitleFiles(replacement): %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-123")
	if err != nil {
		t.Fatalf("GetSubtitleFiles() unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("GetSubtitleFiles() returned %d rows, want 1 (old rows replaced)", len(rows))
	}
	if rows[0].Language != "de" {
		t.Errorf("rows[0].Language = %q, want %q (replacement)", rows[0].Language, "de")
	}
}

func TestRecordSubtitleFiles_empty_clears_existing(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	files := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/sub.srt"},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", files); err != nil {
		t.Fatalf("RecordSubtitleFiles(initial): %v", err)
	}

	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", nil); err != nil {
		t.Fatalf("RecordSubtitleFiles(empty): %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-123")
	if err != nil {
		t.Fatalf("GetSubtitleFiles() unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("GetSubtitleFiles() returned %d rows, want 0 (cleared)", len(rows))
	}
}

func TestRecordSubtitleFiles_does_not_affect_other_media(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	filesA := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/a.srt"},
	}
	filesB := []api.SubtitleFile{
		{Language: "fr", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/b.srt"},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-111", filesA); err != nil {
		t.Fatalf("RecordSubtitleFiles(A): %v", err)
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-222", filesB); err != nil {
		t.Fatalf("RecordSubtitleFiles(B): %v", err)
	}

	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-111", nil); err != nil {
		t.Fatalf("RecordSubtitleFiles(clear A): %v", err)
	}

	rowsA, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-111")
	if err != nil {
		t.Fatalf("GetSubtitleFiles(A): %v", err)
	}
	if len(rowsA) != 0 {
		t.Errorf("GetSubtitleFiles(A) returned %d rows, want 0", len(rowsA))
	}

	rowsB, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-222")
	if err != nil {
		t.Fatalf("GetSubtitleFiles(B): %v", err)
	}
	if len(rowsB) != 1 {
		t.Errorf("GetSubtitleFiles(B) returned %d rows, want 1 (unaffected)", len(rowsB))
	}
}

func TestGetSubtitleFiles_empty(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "")
	if err != nil {
		t.Fatalf("GetSubtitleFiles() unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("GetSubtitleFiles() returned %d rows, want 0", len(rows))
	}
}

func TestGetSubtitleFiles_prefix_filter(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	for _, id := range []string{"tvdb-111-s01e01", "tvdb-111-s01e02", "tvdb-222-s01e01"} {
		files := []api.SubtitleFile{
			{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/" + id + ".srt"},
		}
		if _, err := db.RecordSubtitleFiles(context.Background(), "episode", id, files); err != nil {
			t.Fatalf("RecordSubtitleFiles(%q): %v", id, err)
		}
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "episode", "tvdb-111-")
	if err != nil {
		t.Fatalf("GetSubtitleFiles(prefix) unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("GetSubtitleFiles(tvdb-111-) returned %d rows, want 2", len(rows))
	}
}

func TestGetSubtitleFiles_ordered_by_media_id_language_variant_source(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	files := []api.SubtitleFile{
		{Language: "fr", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/fr.srt"},
		{Language: "en", Variant: "hi", Source: "embedded", Codec: "ass", Path: ""},
		{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/en.srt"},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", files); err != nil {
		t.Fatalf("RecordSubtitleFiles(): %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "")
	if err != nil {
		t.Fatalf("GetSubtitleFiles() unexpected error: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("GetSubtitleFiles() returned %d rows, want 3", len(rows))
	}

	if rows[0].Language != "en" || rows[0].Variant != "hi" {
		t.Errorf("rows[0] = {%q, %q}, want {en, hi}", rows[0].Language, rows[0].Variant)
	}
	if rows[1].Language != "en" || rows[1].Variant != "standard" {
		t.Errorf("rows[1] = {%q, %q}, want {en, standard}", rows[1].Language, rows[1].Variant)
	}
	if rows[2].Language != "fr" || rows[2].Variant != "standard" {
		t.Errorf("rows[2] = {%q, %q}, want {fr, standard}", rows[2].Language, rows[2].Variant)
	}
}

// Verifies the LEFT JOIN between subtitle_files and subtitle_state:
// files with a matching auto download get the download score; files
// without a match get score 0 (COALESCE).
func TestGetSubtitleFiles_score_from_download_state(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tmdb-123",
		Language: "en", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: "/p/movie.en.srt",
		Score: 180, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	files := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/movie.en.srt"},
		{Language: "fr", Variant: "standard", Source: "embedded", Codec: "ass", Path: ""},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", files); err != nil {
		t.Fatalf("RecordSubtitleFiles() unexpected error: %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-123")
	if err != nil {
		t.Fatalf("GetSubtitleFiles() unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("GetSubtitleFiles() returned %d rows, want 2", len(rows))
	}

	if rows[0].Language != "en" {
		t.Fatalf("rows[0].Language = %q, want %q", rows[0].Language, "en")
	}
	if rows[0].Score != 180 {
		t.Errorf("GetSubtitleFiles() rows[0].Score = %d, want 180 (from subtitle_state join)",
			rows[0].Score)
	}

	if rows[1].Language != "fr" {
		t.Fatalf("rows[1].Language = %q, want %q", rows[1].Language, "fr")
	}
	if rows[1].Score != 0 {
		t.Errorf("GetSubtitleFiles() rows[1].Score = %d, want 0 (no matching download)",
			rows[1].Score)
	}
}

// Verifies that manual downloads are excluded from the score join
// (only auto downloads with manual=0 contribute scores).
func TestGetSubtitleFiles_score_ignores_manual_downloads(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	meta := &api.DownloadMeta{Manual: true}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tmdb-456",
		Language: "en", ProviderName: "os",
		ReleaseName: "Movie-Manual", Path: "/p/manual.srt",
		Score: 250, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload(manual) unexpected error: %v", err)
	}

	files := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/sub.srt"},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-456", files); err != nil {
		t.Fatalf("RecordSubtitleFiles() unexpected error: %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-456")
	if err != nil {
		t.Fatalf("GetSubtitleFiles() unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("GetSubtitleFiles() returned %d rows, want 1", len(rows))
	}

	if rows[0].Score != 0 {
		t.Errorf("GetSubtitleFiles() rows[0].Score = %d, want 0 (manual download excluded from join)",
			rows[0].Score)
	}
}

func TestUpsertSubtitleFile_inserts_new_record(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	f := &api.SubtitleFile{
		Language: "en",
		Variant:  "standard",
		Source:   "opensubtitles",
		Codec:    "subrip",
		Path:     "/media/movie.en.srt",
	}
	if err := db.UpsertSubtitleFile(context.Background(), "movie", "tmdb-123", f); err != nil {
		t.Fatalf("UpsertSubtitleFile() error = %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-123")
	if err != nil {
		t.Fatalf("GetSubtitleFiles() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("GetSubtitleFiles() got %d rows, want 1", len(rows))
	}
	if rows[0].Language != "en" || rows[0].Variant != "standard" || rows[0].Source != "opensubtitles" {
		t.Errorf("unexpected row: %+v", rows[0])
	}
}

func TestUpsertSubtitleFile_updates_existing_record(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	f := &api.SubtitleFile{
		Language: "en",
		Variant:  "standard",
		Source:   "opensubtitles",
		Codec:    "subrip",
		Path:     "/media/movie.en.srt",
	}
	if err := db.UpsertSubtitleFile(context.Background(), "movie", "tmdb-123", f); err != nil {
		t.Fatalf("first UpsertSubtitleFile() error = %v", err)
	}

	f.Codec = "ass"
	if err := db.UpsertSubtitleFile(context.Background(), "movie", "tmdb-123", f); err != nil {
		t.Fatalf("second UpsertSubtitleFile() error = %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-123")
	if err != nil {
		t.Fatalf("GetSubtitleFiles() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("GetSubtitleFiles() got %d rows, want 1 (upsert should not duplicate)", len(rows))
	}
	if rows[0].Codec != "ass" {
		t.Errorf("codec = %q, want %q", rows[0].Codec, "ass")
	}
}

func TestUpsertSubtitleFile_different_path_creates_new_row(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	f1 := &api.SubtitleFile{
		Language: "en", Variant: "standard", Source: "external",
		Codec: "subrip", Path: "/media/movie.en.srt",
	}
	f2 := &api.SubtitleFile{
		Language: "en", Variant: "standard", Source: "external",
		Codec: "subrip", Path: "/media/movie.en.1.srt",
	}
	if err := db.UpsertSubtitleFile(context.Background(), "movie", "tmdb-123", f1); err != nil {
		t.Fatalf("UpsertSubtitleFile(f1) error = %v", err)
	}
	if err := db.UpsertSubtitleFile(context.Background(), "movie", "tmdb-123", f2); err != nil {
		t.Fatalf("UpsertSubtitleFile(f2) error = %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-123")
	if err != nil {
		t.Fatalf("GetSubtitleFiles() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("GetSubtitleFiles() got %d rows, want 2 (different paths = different rows)", len(rows))
	}
}

func TestUpsertSubtitleFile_does_not_affect_other_records(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	f1 := &api.SubtitleFile{Language: "en", Variant: "standard", Source: "opensubtitles", Path: "/a.srt"}
	f2 := &api.SubtitleFile{Language: "fr", Variant: "standard", Source: "opensubtitles", Path: "/b.srt"}
	if err := db.UpsertSubtitleFile(context.Background(), "movie", "tmdb-1", f1); err != nil {
		t.Fatalf("UpsertSubtitleFile(en) error = %v", err)
	}
	if err := db.UpsertSubtitleFile(context.Background(), "movie", "tmdb-1", f2); err != nil {
		t.Fatalf("UpsertSubtitleFile(fr) error = %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-1")
	if err != nil {
		t.Fatalf("GetSubtitleFiles() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("GetSubtitleFiles() got %d rows, want 2", len(rows))
	}
}

func TestRecordSubtitleFiles_multiple_media_independent(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	files1 := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "os", Path: "/a.srt"},
	}
	files2 := []api.SubtitleFile{
		{Language: "fr", Variant: "forced", Source: "bs", Path: "/b.srt"},
		{Language: "de", Variant: "standard", Source: "bs", Path: "/c.srt"},
	}

	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tt111", files1); err != nil {
		t.Fatalf("RecordSubtitleFiles(tt111): %v", err)
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tt222", files2); err != nil {
		t.Fatalf("RecordSubtitleFiles(tt222): %v", err)
	}

	got, _ := db.TotalSubtitleFiles(context.Background())
	if got != 3 {
		t.Errorf("TotalSubtitleFiles() = %d, want 3", got)
	}

	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tt111", nil); err != nil {
		t.Fatalf("RecordSubtitleFiles(tt111, nil): %v", err)
	}

	got, _ = db.TotalSubtitleFiles(context.Background())
	if got != 2 {
		t.Errorf("TotalSubtitleFiles() after clearing tt111 = %d, want 2", got)
	}
}

func TestDeleteSubtitleFile_removes_matching_record(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	files := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "opensubtitles", Codec: "subrip", Path: "/p/en.srt"},
		{Language: "fr", Variant: "standard", Source: "opensubtitles", Codec: "subrip", Path: "/p/fr.srt"},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", files); err != nil {
		t.Fatalf("RecordSubtitleFiles(): %v", err)
	}

	if err := db.DeleteSubtitleFile(context.Background(), "movie", "tmdb-123", "en", api.VariantStandard, "opensubtitles", "/p/en.srt"); err != nil {
		t.Fatalf("DeleteSubtitleFile() unexpected error: %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-123")
	if err != nil {
		t.Fatalf("GetSubtitleFiles(): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("GetSubtitleFiles() returned %d rows, want 1 (en deleted)", len(rows))
	}
	if rows[0].Language != "fr" {
		t.Errorf("remaining row Language = %q, want %q", rows[0].Language, "fr")
	}
}

func TestDeleteSubtitleFile_no_match_is_noop(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	files := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "opensubtitles", Codec: "subrip", Path: "/p/en.srt"},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", files); err != nil {
		t.Fatalf("RecordSubtitleFiles(): %v", err)
	}

	if err := db.DeleteSubtitleFile(context.Background(), "movie", "tmdb-123", "de", api.VariantStandard, "opensubtitles", "/p/de.srt"); err != nil {
		t.Fatalf("DeleteSubtitleFile(no match) unexpected error: %v", err)
	}

	if got, _ := db.TotalSubtitleFiles(context.Background()); got != 1 {
		t.Errorf("TotalSubtitleFiles() = %d, want 1 (no match deleted)", got)
	}
}

func TestDeleteSubtitleFile_requires_all_key_fields(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	files := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "opensubtitles", Codec: "subrip", Path: "/p/en.srt"},
		{Language: "en", Variant: "hi", Source: "opensubtitles", Codec: "subrip", Path: "/p/en-hi.srt"},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", files); err != nil {
		t.Fatalf("RecordSubtitleFiles(): %v", err)
	}

	if err := db.DeleteSubtitleFile(context.Background(), "movie", "tmdb-123", "en", api.VariantHI, "opensubtitles", "/p/en-hi.srt"); err != nil {
		t.Fatalf("DeleteSubtitleFile(hi) unexpected error: %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-123")
	if err != nil {
		t.Fatalf("GetSubtitleFiles(): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("GetSubtitleFiles() returned %d rows, want 1", len(rows))
	}
	if rows[0].Variant != "standard" {
		t.Errorf("remaining row Variant = %q, want %q", rows[0].Variant, "standard")
	}
}

func TestSyncOffset(t *testing.T) {
	t.Parallel()
	cases := []struct {
		extraCheck func(t *testing.T, db *DB)
		name       string
		seedMedia  string
		seedID     string
		setPath    string
		getPath    string
		seedFiles  []api.SubtitleFile
		setOffset  int64
		wantOffset int64
	}{
		{
			name:      "updates_offset",
			seedFiles: []api.SubtitleFile{{Language: "en", Variant: "standard", Source: "opensubtitles", Codec: "subrip", Path: "/media/movie.en.srt"}},
			seedMedia: "movie", seedID: "tmdb-123",
			setPath: "/media/movie.en.srt", setOffset: 1500,
			getPath: "/media/movie.en.srt", wantOffset: 1500,
		},
		{
			name:      "no_matching_path_is_noop",
			seedMedia: "", seedID: "", // no seed
			setPath: "/nonexistent/path.srt", setOffset: 500,
			getPath: "/nonexistent/path.srt", wantOffset: 0,
		},
		{
			name:      "negative_offset",
			seedFiles: []api.SubtitleFile{{Language: "fr", Variant: "standard", Source: "gestdown", Codec: "subrip", Path: "/media/movie.fr.srt"}},
			seedMedia: "movie", seedID: "tmdb-456",
			setPath: "/media/movie.fr.srt", setOffset: -2000,
			getPath: "/media/movie.fr.srt", wantOffset: -2000,
		},
		{
			name: "updates_only_matching_path",
			seedFiles: []api.SubtitleFile{
				{Language: "en", Variant: "standard", Source: "os", Codec: "subrip", Path: "/media/a.srt"},
				{Language: "fr", Variant: "standard", Source: "os", Codec: "subrip", Path: "/media/b.srt"},
			},
			seedMedia: "movie", seedID: "tmdb-789",
			setPath: "/media/a.srt", setOffset: 300,
			getPath: "/media/a.srt", wantOffset: 300,
			extraCheck: func(t *testing.T, db *DB) {
				rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-789")
				if err != nil {
					t.Fatalf("GetSubtitleFiles(): %v", err)
				}
				for _, r := range rows {
					if r.Path == "/media/b.srt" && r.OffsetMs != 0 {
						t.Errorf("path %q OffsetMs = %d, want 0 (unaffected)", r.Path, r.OffsetMs)
					}
				}
			},
		},
		{
			name:      "get_no_record_returns_zero",
			seedMedia: "", seedID: "",
			setPath: "", setOffset: 0, // no set
			getPath: "/nonexistent.srt", wantOffset: 0,
		},
		{
			name:      "round_trip",
			seedMedia: "movie", seedID: "tmdb-1",
			setPath: "/media/movie.en.srt", setOffset: 500,
			getPath: "/media/movie.en.srt", wantOffset: 500,
		},
		{
			name:      "not_found_returns_zero",
			seedMedia: "", seedID: "",
			setPath: "", setOffset: 0,
			getPath: "/nonexistent/path.srt", wantOffset: 0,
		},
		{
			name:      "returns_stored_value",
			seedMedia: "movie", seedID: "tt123",
			setPath: "/media/movie.en.srt", setOffset: -250,
			getPath: "/media/movie.en.srt", wantOffset: -250,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			ctx := context.Background()

			// Seed files if needed.
			if tc.seedMedia != "" && len(tc.seedFiles) > 0 {
				if _, err := db.RecordSubtitleFiles(ctx, api.MediaType(tc.seedMedia), tc.seedID, tc.seedFiles); err != nil {
					t.Fatalf("RecordSubtitleFiles(): %v", err)
				}
			} else if tc.seedMedia != "" && tc.name == "round_trip" {
				if err := db.UpsertSubtitleFile(ctx, api.MediaType(tc.seedMedia), tc.seedID,
					&api.SubtitleFile{Language: "en", Variant: "standard", Source: "external", Path: tc.setPath}); err != nil {
					t.Fatalf("UpsertSubtitleFile(): %v", err)
				}
			} else if tc.seedMedia != "" && tc.name == "returns_stored_value" {
				if err := db.UpsertSubtitleFile(ctx, api.MediaType(tc.seedMedia), tc.seedID,
					&api.SubtitleFile{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: tc.setPath}); err != nil {
					t.Fatalf("UpsertSubtitleFile(): %v", err)
				}
			}

			// Set offset if path is specified.
			if tc.setPath != "" {
				if err := db.SetSyncOffset(ctx, tc.setPath, tc.setOffset); err != nil {
					t.Fatalf("SetSyncOffset() error: %v", err)
				}
			}

			// Get and verify.
			got, err := db.GetSyncOffset(ctx, tc.getPath)
			if err != nil {
				t.Fatalf("GetSyncOffset() error: %v", err)
			}
			if got != tc.wantOffset {
				t.Errorf("GetSyncOffset(%q) = %d, want %d", tc.getPath, got, tc.wantOffset)
			}

			if tc.extraCheck != nil {
				tc.extraCheck(t, db)
			}
		})
	}
}

func TestGetSubtitleFiles_video_path_from_download_state(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	meta := &api.DownloadMeta{VideoPath: "/media/movie.mkv"}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tmdb-555",
		Language: "en", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: "/p/movie.en.srt",
		Score: 150, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	files := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/movie.en.srt"},
		{Language: "en", Variant: "standard", Source: "embedded", Codec: "subrip", Path: ""},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-555", files); err != nil {
		t.Fatalf("RecordSubtitleFiles() unexpected error: %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-555")
	if err != nil {
		t.Fatalf("GetSubtitleFiles() unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("GetSubtitleFiles() returned %d rows, want 2", len(rows))
	}

	if rows[0].Source != "embedded" {
		t.Fatalf("rows[0].Source = %q, want %q", rows[0].Source, "embedded")
	}
	if rows[0].VideoPath != "" {
		t.Errorf("GetSubtitleFiles() embedded VideoPath = %q, want empty (JOIN excluded)",
			rows[0].VideoPath)
	}

	if rows[1].Source != "external" {
		t.Fatalf("rows[1].Source = %q, want %q", rows[1].Source, "external")
	}
	if rows[1].VideoPath != "/media/movie.mkv" {
		t.Errorf("GetSubtitleFiles() external VideoPath = %q, want %q",
			rows[1].VideoPath, "/media/movie.mkv")
	}
}

func TestRecordSubtitleFiles_no_change_returns_false(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	files := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/en.srt"},
		{Language: "fr", Variant: "hi", Source: "embedded", Codec: "ass", Path: ""},
	}
	changed, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", files)
	if err != nil {
		t.Fatalf("RecordSubtitleFiles(initial): %v", err)
	}
	if !changed {
		t.Error("RecordSubtitleFiles(initial) = false, want true (first insert)")
	}

	changed, err = db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", files)
	if err != nil {
		t.Fatalf("RecordSubtitleFiles(same): %v", err)
	}
	if changed {
		t.Error("RecordSubtitleFiles(same data) = true, want false (no changes)")
	}
}

func TestRecordSubtitleFiles_codec_change_triggers_update(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	files := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/en.srt"},
	}
	changed, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", files)
	if err != nil {
		t.Fatalf("RecordSubtitleFiles(initial): %v", err)
	}
	if !changed {
		t.Error("RecordSubtitleFiles(initial) = false, want true")
	}

	files[0].Codec = "ass"
	changed, err = db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-123", files)
	if err != nil {
		t.Fatalf("RecordSubtitleFiles(codec change): %v", err)
	}
	if !changed {
		t.Error("RecordSubtitleFiles(codec change) = false, want true (codec updated)")
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-123")
	if err != nil {
		t.Fatalf("GetSubtitleFiles(): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("GetSubtitleFiles() returned %d rows, want 1", len(rows))
	}
	if rows[0].Codec != "ass" {
		t.Errorf("codec = %q, want %q", rows[0].Codec, "ass")
	}
}
