package manualops

import (
	"errors"
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
		{"", false},
		{"aaaaaaaaaaaaaaaaaaaaaa", false}, // >MaxLangCodeLen
		{"en/gb", false},
		{"en\\gb", false},
		{"en..gb", false},
		{"en\x00gb", false},
		{"zh-Hans", true},
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

type mockQuery struct{ val string }

func (m mockQuery) Get(_ string) string { return m.val }
