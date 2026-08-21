package manualops

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subtitlefile"
)

func TestValidateDownloadRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		wantErr error
		name    string
		wantMT  subflux.MediaType
		req     DownloadRequest
	}{
		{
			name:    "valid request defaults media type to movie",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", ArrID: 42, Language: "en"},
			wantErr: nil,
			wantMT:  subflux.MediaTypeMovie,
		},
		{
			name:    "valid episode request preserves explicit media type",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", ArrID: 42, Language: "en", MediaType: subflux.MediaTypeEpisode, Season: 1, Episode: 2},
			wantErr: nil,
			wantMT:  subflux.MediaTypeEpisode,
		},
		{
			name:    "missing provider",
			req:     DownloadRequest{SubtitleID: "1", ArrID: 42, Language: "en"},
			wantErr: ErrMissingRequired,
		},
		{
			name:    "missing subtitle_id",
			req:     DownloadRequest{Provider: "os", ArrID: 42, Language: "en"},
			wantErr: ErrMissingRequired,
		},
		{
			name:    "missing media_id (arr ref replaces file_path)",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", Language: "en"},
			wantErr: ErrMissingRequired,
		},
		{
			name:    "missing language",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", ArrID: 42},
			wantErr: ErrMissingRequired,
		},
		{
			name:    "invalid language code",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", ArrID: 42, Language: "en/../.."},
			wantErr: ErrInvalidLangCode,
		},
		{
			name:    "language code outside the internal space",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", ArrID: 42, Language: "eng"},
			wantErr: ErrInvalidLangCode,
		},
		{
			name:    "subflux's own Brazilian Portuguese code is accepted",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", ArrID: 42, Language: "pb"},
			wantErr: nil,
			wantMT:  subflux.MediaTypeMovie,
		},
		{
			name:    "invalid media type",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", ArrID: 42, Language: "en", MediaType: "invalid"},
			wantErr: ErrInvalidMediaType,
		},
		{
			name:    "episode without episode number",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", ArrID: 42, Language: "en", MediaType: subflux.MediaTypeEpisode},
			wantErr: ErrMissingEpisode,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := tt.req
			err := ValidateDownloadRequest(&req)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidateDownloadRequest() error = %v, want %v", err, tt.wantErr)
			}
			if err == nil && req.MediaType != tt.wantMT {
				t.Errorf("ValidateDownloadRequest() MediaType = %q, want %q", req.MediaType, tt.wantMT)
			}
		})
	}
}

// POST /api/search/download is the ONE path by which an untrusted language code
// reaches a filename: the automated path resolves its targets from validated
// config, but this one takes the code from the request body and hands it to
// subtitlefile.ManualPath, which writes it as a dot segment next to the media
// file. Those directories are shared over SMB and NFS and read by Windows
// clients, so a character POSIX allows and Win32 does not is a portability
// break, not a cosmetic one: ':' opens an NTFS alternate data stream, the Win32
// name grammar bars < > " | ? *, and a trailing dot or space is stripped, which
// collapses two codes onto one file.
//
// A character blocklist cannot hold this line — a trailing dot is legal on its
// own and composes into ".." in the rendered path. The gate is the closed
// vocabulary instead, the same one config.validateLangCode applies.
func TestValidateDownloadRequest_rejectsLangCodesUnsafeInAFilename(t *testing.T) {
	t.Parallel()
	for _, lang := range []string{
		"a:b", "en<", "en>", `en"`, "en|", "en?", "en*",
		"en.", ".en", "en ", " en", "en\x00", "en/fr", `en\fr`, "en..fr",
	} {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			req := DownloadRequest{Provider: "os", SubtitleID: "1", ArrID: 42, Language: lang}
			if err := ValidateDownloadRequest(&req); !errors.Is(err, ErrInvalidLangCode) {
				t.Errorf("ValidateDownloadRequest(language=%q) error = %v, want ErrInvalidLangCode; "+
					"it reaches the share as %q", lang, err,
					subtitlefile.ManualPath("/media/movie.mkv", 1, subtitlefile.Tags{Lang: lang}))
			}
		})
	}
}

func TestValidMediaType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mt   string
		want bool
	}{
		{"episode", true},
		{"movie", true},
		{"series", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := subflux.MediaType(tt.mt).Valid(); got != tt.want {
			t.Errorf("MediaType(%q).Valid() = %v, want %v", tt.mt, got, tt.want)
		}
	}
}

func TestBuildSearchResults_caps_at_MaxResults(t *testing.T) {
	t.Parallel()
	scored := make([]subflux.ScoredResult, MaxResults+10)
	for i := range scored {
		scored[i] = subflux.ScoredResult{Sub: subflux.Subtitle{Provider: "p", Language: "eng"}, Score: i}
	}
	results := BuildSearchResults(scored, nil, nil)
	if len(results) != MaxResults {
		t.Errorf("len(results) = %d, want %d", len(results), MaxResults)
	}
}

// BuildSearchResults computes each result's tier server-side via the
// injected scorer; a nil scorer (pre-wire state) leaves tiers empty.
func TestBuildSearchResults_computes_tier(t *testing.T) {
	t.Parallel()
	scored := []subflux.ScoredResult{
		{Sub: subflux.Subtitle{Provider: "os", ReleaseName: "A"}, Score: 85},
		{Sub: subflux.Subtitle{Provider: "os", ReleaseName: "B"}, Score: 0},
	}
	sc := scorer.New(&subflux.DefaultScores)
	results := BuildSearchResults(scored, nil, sc)
	if results[0].Tier != subflux.TierExcellent {
		t.Errorf("Tier for score 85 = %q, want %q", results[0].Tier, subflux.TierExcellent)
	}
	if results[1].Tier != subflux.TierNone {
		t.Errorf("Tier for score 0 = %q, want %q", results[1].Tier, subflux.TierNone)
	}

	noScorer := BuildSearchResults(scored, nil, nil)
	if noScorer[0].Tier != "" {
		t.Errorf("Tier with nil scorer = %q, want empty", noScorer[0].Tier)
	}
}

func TestBuildSearchResults_marks_on_disk(t *testing.T) {
	t.Parallel()
	scored := []subflux.ScoredResult{
		{Sub: subflux.Subtitle{Provider: "os", ReleaseName: "Movie.2024", Language: "eng"}, Score: 80},
		{Sub: subflux.Subtitle{Provider: "os", ReleaseName: "Other.2024", Language: "eng"}, Score: 70},
	}
	refs := []subflux.DownloadedRef{{Provider: "os", ReleaseName: "Movie.2024"}}
	results := BuildSearchResults(scored, refs, nil)
	if !results[0].OnDisk {
		t.Error("first result should be marked OnDisk")
	}
	if results[1].OnDisk {
		t.Error("second result should not be marked OnDisk")
	}
}

func TestQueryInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		val  string
		want int
	}{
		{"42", 42},
		{"", 0},
		{"-1", 0},
		{"abc", 0},
		{"0", 0},
	}
	for _, tt := range tests {
		q := mockQuery{val: tt.val}
		if got := QueryInt(q, "key"); got != tt.want {
			t.Errorf("QueryInt(%q) = %d, want %d", tt.val, got, tt.want)
		}
	}
}

func TestParseSearchQuery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		query       string
		wantLang    string
		wantType    subflux.MediaType
		wantTitle   string
		wantImdb    string
		wantSeason  int
		wantEpisode int
		wantYear    int
		wantArrID   int
		wantRelease string
	}{
		{
			name:        "explicit movie with all fields",
			query:       "title=The+Matrix&imdb=tt0133093&lang=fr&type=movie&year=1999&release=Matrix.1999.1080p",
			wantLang:    "fr",
			wantType:    subflux.MediaTypeMovie,
			wantTitle:   "The Matrix",
			wantImdb:    "tt0133093",
			wantYear:    1999,
			wantRelease: "Matrix.1999.1080p",
		},
		{
			name:      "missing lang defaults to en",
			query:     "title=X&type=movie",
			wantLang:  "en",
			wantType:  subflux.MediaTypeMovie,
			wantTitle: "X",
		},
		{
			name:        "no type with season and episode infers episode",
			query:       "title=Show&season=1&episode=2",
			wantLang:    "en",
			wantType:    subflux.MediaTypeEpisode,
			wantTitle:   "Show",
			wantSeason:  1,
			wantEpisode: 2,
		},
		{
			name:       "no type without episode infers movie",
			query:      "title=Show&season=1",
			wantLang:   "en",
			wantType:   subflux.MediaTypeMovie,
			wantTitle:  "Show",
			wantSeason: 1,
		},
		{
			name:      "media_id (arr id) parsed for server-side resolution",
			query:     "type=movie&media_id=42&title=X",
			wantLang:  "en",
			wantType:  subflux.MediaTypeMovie,
			wantTitle: "X",
			wantArrID: 42,
		},
		{
			name:        "file param is gone: ignored, never a path",
			query:       "type=movie&file=/media/Movie.mkv&release=Real.Release",
			wantLang:    "en",
			wantType:    subflux.MediaTypeMovie,
			wantRelease: "Real.Release",
		},
		{
			name:        "negative and non-numeric ints clamp to zero",
			query:       "type=movie&year=-5&season=abc&episode=2",
			wantLang:    "en",
			wantType:    subflux.MediaTypeMovie,
			wantYear:    0,
			wantSeason:  0,
			wantEpisode: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodGet, "/api/search?"+tt.query, nil)
			req, lang, mediaType, arrID := ParseSearchQuery(r)
			if lang != tt.wantLang {
				t.Errorf("lang = %q, want %q", lang, tt.wantLang)
			}
			if mediaType != tt.wantType {
				t.Errorf("mediaType = %q, want %q", mediaType, tt.wantType)
			}
			if arrID != tt.wantArrID {
				t.Errorf("arrID = %d, want %d", arrID, tt.wantArrID)
			}
			if req.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", req.Title, tt.wantTitle)
			}
			if req.ImdbID != tt.wantImdb {
				t.Errorf("ImdbID = %q, want %q", req.ImdbID, tt.wantImdb)
			}
			if req.Season != tt.wantSeason {
				t.Errorf("Season = %d, want %d", req.Season, tt.wantSeason)
			}
			if req.Episode != tt.wantEpisode {
				t.Errorf("Episode = %d, want %d", req.Episode, tt.wantEpisode)
			}
			if req.Year != tt.wantYear {
				t.Errorf("Year = %d, want %d", req.Year, tt.wantYear)
			}
			if req.ReleaseName != tt.wantRelease {
				t.Errorf("ReleaseName = %q, want %q", req.ReleaseName, tt.wantRelease)
			}
		})
	}
}

type mockQuery struct{ val string }

func (m mockQuery) Get(_ string) string { return m.val }
