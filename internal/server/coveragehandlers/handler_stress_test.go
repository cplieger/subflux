package coveragehandlers

// handler_stress_test.go — task 19's REFERENCE-SCALE movies payload evidence:
// the wire-cut measurement TestHandleCoverageMovies_payload_size_evidence
// records at 1,000 movies, re-run at the deterministic 4,360-movie reference
// fixture (internal/testsupport's reffixture, the shared cross-language
// stress library). EVIDENCE for the ≥60% gzip-reduction hypothesis, not a
// gate: the numbers land in the test log; the only hard assertion is that
// the cut payload stayed smaller.

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/server/coverage"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// refMoviesAndFiles materializes the reference fixture's movies as arr DTOs
// plus their subtitle rows. Row density mirrors the shipped-library shape the
// 1,000-movie test established: one external `en` per movie, an external `fr`
// where the fixture drew one, and 0–7 embedded tracks (coverage indexes
// EVERY embedded subtitle track, text and bitmap alike).
func refMoviesAndFiles() ([]arrapi.Movie, []subflux.SubtitleEntry) {
	items := testsupport.RefMovieItems()
	movies := make([]arrapi.Movie, 0, len(items))
	var files []subflux.SubtitleEntry
	embLangs := [...]string{"en", "fr", "de", "es", "ja", "pt", "it"}
	embCodecs := [...]string{"pgs", "ass", "srt"}
	for i, m := range items {
		movies = append(movies, arrapi.Movie{
			ID:               m.ArrID,
			Title:            m.Title,
			Year:             m.Year,
			TmdbID:           m.TmdbID,
			ImdbID:           fmt.Sprintf("tt%07d", 2000000+m.TmdbID),
			InCinemas:        fmt.Sprintf("%d-03-01", m.Year),
			DigitalRelease:   fmt.Sprintf("%d-06-01", m.Year),
			HasFile:          true,
			OriginalLanguage: &arrapi.Language{Name: testsupport.RefAudioNames[m.AudioIdx]},
			MovieFile: &arrapi.MovieFile{
				Path:      fmt.Sprintf("/movies/%s (%d)/movie.mkv", m.Title, m.Year),
				SceneName: m.SceneName,
			},
			Tags: []int{1, 3},
		})
		mediaID := "tmdb-" + strconv.Itoa(m.TmdbID)
		files = append(files, subflux.SubtitleEntry{
			MediaID: mediaID, Language: "en", Variant: "standard",
			Source: "opensubtitles", Codec: "srt", Score: 60 + i%40, OffsetMs: int64(i % 500),
		})
		if m.HasFR {
			files = append(files, subflux.SubtitleEntry{
				MediaID: mediaID, Language: "fr", Variant: "standard",
				Source: "subdl", Codec: "srt", Score: 55 + i%30, Ordinal: 1,
			})
		}
		for e := range m.EmbeddedTracks {
			files = append(files, subflux.SubtitleEntry{
				MediaID: mediaID, Language: embLangs[e%len(embLangs)], Variant: "standard",
				Source: "embedded", Codec: embCodecs[e%len(embCodecs)],
			})
		}
	}
	return movies, files
}

func TestHandleCoverageMovies_reference_scale_payload_evidence(t *testing.T) {
	t.Parallel()
	movies, files := refMoviesAndFiles()

	store := &mockCoverageStore{subtitleFiles: files}
	cfg := &fakeCoverageCfg{targets: []subflux.SubtitleTarget{{Code: "en"}, {Code: "fr"}}}
	h := newCoverageHandler(store, cfg, nil, &covRadarrFake{movies: movies})

	rec := httptest.NewRecorder()
	h.HandleCoverageMovies(rec, httptest.NewRequest(http.MethodGet, "/api/coverage/movies", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	after := rec.Body.Bytes()

	var result []MovieItem
	if err := json.Unmarshal(after, &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Every reference movie is file-bearing, so the collection serves all of
	// them — the "4,360-movie fixture" is 4,360 rows on the wire.
	if len(result) != testsupport.RefMovieCount {
		t.Fatalf("collection rows = %d, want %d", len(result), testsupport.RefMovieCount)
	}
	// One sampled row pins the audio code↔name correspondence through the
	// REAL name→code mapping: movie[0] drew AudioIdx 4 ("Spanish" → "es"),
	// matching the TS mirror's wire builder.
	if got := result[0].AudioLang; got != testsupport.RefAudioLangs[4] {
		t.Errorf("movie[0] audio_lang = %q, want %q", got, testsupport.RefAudioLangs[4])
	}

	// The pre-cut wire: each row plus its deduplicated inlined entries — the
	// exact attach the collection performed before A3.
	byMedia := make(map[string][]subflux.SubtitleEntry)
	for i := range files {
		byMedia[files[i].MediaID] = append(byMedia[files[i].MediaID], files[i])
	}
	type preCutMovieItem struct {
		MovieItem
		Subs []subflux.SubtitleEntry `json:"subs"`
	}
	preCut := make([]preCutMovieItem, 0, len(result))
	for _, item := range result {
		preCut = append(preCut, preCutMovieItem{
			MovieItem: item,
			Subs:      coverage.DeduplicateFileRows(byMedia["tmdb-"+strconv.Itoa(item.TmdbID)]),
		})
	}
	before, err := json.Marshal(preCut)
	if err != nil {
		t.Fatalf("marshal pre-cut payload: %v", err)
	}

	gzipSize := func(b []byte) int {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, werr := zw.Write(b); werr != nil {
			t.Fatalf("gzip write: %v", werr)
		}
		if cerr := zw.Close(); cerr != nil {
			t.Fatalf("gzip close: %v", cerr)
		}
		return buf.Len()
	}
	beforeGz, afterGz := gzipSize(before), gzipSize(after)
	pct := func(b, a int) float64 { return 100 * (1 - float64(a)/float64(b)) }
	t.Logf("movies collection payload at the %d-movie reference fixture (%d subtitle rows):",
		len(result), len(files))
	t.Logf("  raw:  %d -> %d bytes (-%.1f%%)", len(before), len(after), pct(len(before), len(after)))
	t.Logf("  gzip: %d -> %d bytes (-%.1f%%)", beforeGz, afterGz, pct(beforeGz, afterGz))
	t.Logf("  ≥60%% gzip-reduction hypothesis: %v (evidence, not a gate)",
		pct(beforeGz, afterGz) >= 60)

	if len(after) >= len(before) {
		t.Errorf("cut payload (%d bytes) is not smaller than the pre-cut payload (%d bytes)",
			len(after), len(before))
	}
}
