package manualops

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

func TestValidateDownloadRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		wantErr error
		name    string
		wantMT  api.MediaType
		req     DownloadRequest
	}{
		{
			name:    "valid request defaults media type to movie",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", FilePath: "/f", Language: "en"},
			wantErr: nil,
			wantMT:  api.MediaTypeMovie,
		},
		{
			name:    "valid request preserves explicit media type",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", FilePath: "/f", Language: "en", MediaType: api.MediaTypeEpisode},
			wantErr: nil,
			wantMT:  api.MediaTypeEpisode,
		},
		{
			name:    "missing provider",
			req:     DownloadRequest{SubtitleID: "1", FilePath: "/f", Language: "en"},
			wantErr: ErrMissingRequired,
		},
		{
			name:    "missing subtitle_id",
			req:     DownloadRequest{Provider: "os", FilePath: "/f", Language: "en"},
			wantErr: ErrMissingRequired,
		},
		{
			name:    "missing file_path",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", Language: "en"},
			wantErr: ErrMissingRequired,
		},
		{
			name:    "missing language",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", FilePath: "/f"},
			wantErr: ErrMissingRequired,
		},
		{
			name:    "invalid language code",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", FilePath: "/f", Language: "en/../.."},
			wantErr: ErrInvalidLangCode,
		},
		{
			name:    "invalid media type",
			req:     DownloadRequest{Provider: "os", SubtitleID: "1", FilePath: "/f", Language: "en", MediaType: "invalid"},
			wantErr: ErrInvalidMediaType,
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

func TestIsValidLangCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		lang string
		want bool
	}{
		{"eng", true},
		{"pt-BR", true},
		{"zh-Hans", true},
		{"", false},
		{strings.Repeat("a", MaxLangCodeLen), true},    // exactly the limit is valid
		{strings.Repeat("a", MaxLangCodeLen+1), false}, // one over the limit
		{"en/gb", false},
		{"en\\gb", false},
		{"en..gb", false},
		{"en\x00gb", false},
		{"en\tUS", false}, // tab (0x09) is a control char
		{"en US", true},   // space (0x20) is not a control char
	}
	for _, tt := range tests {
		if got := IsValidLangCode(tt.lang); got != tt.want {
			t.Errorf("IsValidLangCode(%q) = %v, want %v", tt.lang, got, tt.want)
		}
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
		if got := api.MediaType(tt.mt).Valid(); got != tt.want {
			t.Errorf("MediaType(%q).Valid() = %v, want %v", tt.mt, got, tt.want)
		}
	}
}

func TestBuildSearchResults_caps_at_MaxResults(t *testing.T) {
	t.Parallel()
	scored := make([]api.ScoredResult, MaxResults+10)
	for i := range scored {
		scored[i] = api.ScoredResult{Sub: api.Subtitle{Provider: "p", Language: "eng"}, Score: i}
	}
	results := BuildSearchResults(scored, nil)
	if len(results) != MaxResults {
		t.Errorf("len(results) = %d, want %d", len(results), MaxResults)
	}
}

func TestBuildSearchResults_marks_on_disk(t *testing.T) {
	t.Parallel()
	scored := []api.ScoredResult{
		{Sub: api.Subtitle{Provider: "os", ReleaseName: "Movie.2024", Language: "eng"}, Score: 80},
		{Sub: api.Subtitle{Provider: "os", ReleaseName: "Other.2024", Language: "eng"}, Score: 70},
	}
	refs := []api.DownloadedRef{{Provider: "os", ReleaseName: "Movie.2024"}}
	results := BuildSearchResults(scored, refs)
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
		name         string
		query        string
		wantLang     string
		wantType     api.MediaType
		wantFilePath string
		wantTitle    string
		wantImdb     string
		wantSeason   int
		wantEpisode  int
		wantYear     int
		wantRelease  string
	}{
		{
			name:        "explicit movie with all fields",
			query:       "title=The+Matrix&imdb=tt0133093&lang=fr&type=movie&year=1999&release=Matrix.1999.1080p",
			wantLang:    "fr",
			wantType:    api.MediaTypeMovie,
			wantTitle:   "The Matrix",
			wantImdb:    "tt0133093",
			wantYear:    1999,
			wantRelease: "Matrix.1999.1080p",
		},
		{
			name:      "missing lang defaults to en",
			query:     "title=X&type=movie",
			wantLang:  "en",
			wantType:  api.MediaTypeMovie,
			wantTitle: "X",
		},
		{
			name:        "no type with season and episode infers episode",
			query:       "title=Show&season=1&episode=2",
			wantLang:    "en",
			wantType:    api.MediaTypeEpisode,
			wantTitle:   "Show",
			wantSeason:  1,
			wantEpisode: 2,
		},
		{
			name:       "no type without episode infers movie",
			query:      "title=Show&season=1",
			wantLang:   "en",
			wantType:   api.MediaTypeMovie,
			wantTitle:  "Show",
			wantSeason: 1,
		},
		{
			name:         "file populates release when release absent",
			query:        "type=movie&file=/media/Movie.2024.mkv",
			wantLang:     "en",
			wantType:     api.MediaTypeMovie,
			wantFilePath: "/media/Movie.2024.mkv",
			wantRelease:  "/media/Movie.2024.mkv",
		},
		{
			name:         "explicit release is not overwritten by file",
			query:        "type=movie&file=/media/Movie.mkv&release=Real.Release",
			wantLang:     "en",
			wantType:     api.MediaTypeMovie,
			wantFilePath: "/media/Movie.mkv",
			wantRelease:  "Real.Release",
		},
		{
			name:        "negative and non-numeric ints clamp to zero",
			query:       "type=movie&year=-5&season=abc&episode=2",
			wantLang:    "en",
			wantType:    api.MediaTypeMovie,
			wantYear:    0,
			wantSeason:  0,
			wantEpisode: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodGet, "/api/search?"+tt.query, nil)
			req, lang, mediaType, filePath := ParseSearchQuery(r)
			if lang != tt.wantLang {
				t.Errorf("lang = %q, want %q", lang, tt.wantLang)
			}
			if mediaType != tt.wantType {
				t.Errorf("mediaType = %q, want %q", mediaType, tt.wantType)
			}
			if filePath != tt.wantFilePath {
				t.Errorf("filePath = %q, want %q", filePath, tt.wantFilePath)
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
