package betaseries

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

func TestBetaLangToISO(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "vo maps to en", input: "vo", want: "en"},
		{name: "vf maps to fr", input: "vf", want: "fr"},
		{name: "en maps to en", input: "en", want: "en"},
		{name: "fr maps to fr", input: "fr", want: "fr"},
		{name: "uppercase VO", input: "VO", want: "en"},
		{name: "uppercase VF", input: "VF", want: "fr"},
		{name: "mixed case Vo", input: "Vo", want: "en"},
		{name: "unknown code", input: "de", want: ""},
		{name: "empty string", input: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := betaLangToISO(tt.input)
			if got != tt.want {
				t.Errorf("betaLangToISO(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestFactory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		settings map[string]any
		name     string
		wantErr  bool
	}{
		{name: "nil settings", settings: nil, wantErr: true},
		{name: "missing token", settings: map[string]any{}, wantErr: true},
		{name: "empty token", settings: map[string]any{"token": ""}, wantErr: true},
		{name: "non-string token", settings: map[string]any{"token": 123}, wantErr: true},
		{name: "valid token", settings: map[string]any{"token": "test-key"}, wantErr: false},
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
			if p.Name() != api.ProviderNameBetaSeries {
				t.Errorf("Name() = %q, want %q", p.Name(), api.ProviderNameBetaSeries)
			}
		})
	}
}

// --- Subtitle Entry Filtering ---

func TestFilterSubtitleEntries(t *testing.T) {
	t.Parallel()

	t.Run("nil entries returns nil", func(t *testing.T) {
		t.Parallel()
		got := filterSubtitleEntries(nil, []string{"en"}, 1, 1)
		if got != nil {
			t.Errorf("filterSubtitleEntries(nil) = %v, want nil", got)
		}
	})

	t.Run("empty entries returns nil", func(t *testing.T) {
		t.Parallel()
		got := filterSubtitleEntries([]subtitleEntry{}, []string{"en"}, 1, 1)
		if got != nil {
			t.Errorf("filterSubtitleEntries([]) = %v, want nil", got)
		}
	})

	t.Run("basic entry mapped correctly", func(t *testing.T) {
		t.Parallel()
		entries := []subtitleEntry{
			{ID: 42, Language: "vo", Source: "addic7ed", File: "Show.S01E01.srt", URL: "https://example.com/42.srt"},
		}
		got := filterSubtitleEntries(entries, []string{"en"}, 1, 1)
		if len(got) != 1 {
			t.Fatalf("filterSubtitleEntries() = %d results, want 1", len(got))
		}
		if got[0].Provider != "betaseries" {
			t.Errorf("Provider = %q, want %q", got[0].Provider, "betaseries")
		}
		if got[0].ID != "42" {
			t.Errorf("ID = %q, want %q", got[0].ID, "42")
		}
		if got[0].Language != "en" {
			t.Errorf("Language = %q, want %q", got[0].Language, "en")
		}
		if got[0].ReleaseName != "Show.S01E01.srt" {
			t.Errorf("ReleaseName = %q, want %q", got[0].ReleaseName, "Show.S01E01.srt")
		}
		if got[0].DownloadURL != "https://example.com/42.srt" {
			t.Errorf("DownloadURL = %q, want %q", got[0].DownloadURL, "https://example.com/42.srt")
		}
		if got[0].MatchedBy != api.MatchByTVDB {
			t.Errorf("MatchedBy = %q, want %q", got[0].MatchedBy, api.MatchByTVDB)
		}
		if got[0].Season != 1 {
			t.Errorf("Season = %d, want 1", got[0].Season)
		}
		if got[0].Episode != 1 {
			t.Errorf("Episode = %d, want 1", got[0].Episode)
		}
	})

	t.Run("vf maps to fr", func(t *testing.T) {
		t.Parallel()
		entries := []subtitleEntry{
			{ID: 1, Language: "vf", Source: "addic7ed", File: "sub.srt", URL: "https://example.com/1.srt"},
		}
		got := filterSubtitleEntries(entries, []string{"fr"}, 2, 3)
		if len(got) != 1 {
			t.Fatalf("filterSubtitleEntries() = %d results, want 1", len(got))
		}
		if got[0].Language != "fr" {
			t.Errorf("Language = %q, want %q", got[0].Language, "fr")
		}
	})

	t.Run("unknown language skipped", func(t *testing.T) {
		t.Parallel()
		entries := []subtitleEntry{
			{ID: 1, Language: "de", Source: "addic7ed", File: "sub.srt", URL: "https://example.com/1.srt"},
		}
		got := filterSubtitleEntries(entries, []string{"en"}, 1, 1)
		if len(got) != 0 {
			t.Errorf("filterSubtitleEntries() = %d results, want 0", len(got))
		}
	})

	t.Run("language not in requested list skipped", func(t *testing.T) {
		t.Parallel()
		entries := []subtitleEntry{
			{ID: 1, Language: "vo", Source: "addic7ed", File: "sub.srt", URL: "https://example.com/1.srt"},
		}
		got := filterSubtitleEntries(entries, []string{"fr"}, 1, 1)
		if len(got) != 0 {
			t.Errorf("filterSubtitleEntries() = %d results, want 0", len(got))
		}
	})

	t.Run("seriessub source skipped", func(t *testing.T) {
		t.Parallel()
		entries := []subtitleEntry{
			{ID: 1, Language: "vo", Source: "seriessub", File: "sub.srt", URL: "https://example.com/1.srt"},
		}
		got := filterSubtitleEntries(entries, []string{"en"}, 1, 1)
		if len(got) != 0 {
			t.Errorf("filterSubtitleEntries() = %d results, want 0 (seriessub)", len(got))
		}
	})

	t.Run("multiple entries mixed filtering", func(t *testing.T) {
		t.Parallel()
		entries := []subtitleEntry{
			{ID: 1, Language: "vo", Source: "addic7ed", File: "en.srt", URL: "https://example.com/1.srt"},
			{ID: 2, Language: "vf", Source: "seriessub", File: "fr.srt", URL: "https://example.com/2.srt"},
			{ID: 3, Language: "de", Source: "addic7ed", File: "de.srt", URL: "https://example.com/3.srt"},
			{ID: 4, Language: "vf", Source: "addic7ed", File: "fr2.srt", URL: "https://example.com/4.srt"},
		}
		got := filterSubtitleEntries(entries, []string{"en", "fr"}, 1, 5)
		if len(got) != 2 {
			t.Fatalf("filterSubtitleEntries() = %d results, want 2", len(got))
		}
		if got[0].ID != "1" {
			t.Errorf("got[0].ID = %q, want %q", got[0].ID, "1")
		}
		if got[1].ID != "4" {
			t.Errorf("got[1].ID = %q, want %q", got[1].ID, "4")
		}
	})

	t.Run("empty language in entry skipped", func(t *testing.T) {
		t.Parallel()
		entries := []subtitleEntry{
			{ID: 1, Language: "", Source: "addic7ed", File: "sub.srt", URL: "https://example.com/1.srt"},
		}
		got := filterSubtitleEntries(entries, []string{"en", "fr"}, 1, 1)
		if len(got) != 0 {
			t.Errorf("filterSubtitleEntries() = %d results, want 0 (empty language)", len(got))
		}
	})

	t.Run("nil languages returns no results", func(t *testing.T) {
		t.Parallel()
		entries := []subtitleEntry{
			{ID: 1, Language: "vo", Source: "addic7ed", File: "sub.srt", URL: "https://example.com/1.srt"},
		}
		got := filterSubtitleEntries(entries, nil, 1, 1)
		if len(got) != 0 {
			t.Errorf("filterSubtitleEntries() = %d results, want 0", len(got))
		}
	})
}

// --- classifyBadRequest ---

func TestClassifyBadRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		wantErrType string
		wantErrMsg  string
		wantBody    string
		wantErr     bool
	}{
		{
			name:     "not found 4001",
			body:     `{"errors":[{"code":4001,"text":"Show not found"}]}`,
			wantBody: `{"episodes":[]}`,
		},
		{
			name:        "auth error 1001",
			body:        `{"errors":[{"code":1001,"text":"Invalid API key"}]}`,
			wantErr:     true,
			wantErrType: "auth",
		},
		{
			name:       "unknown code 9999",
			body:       `{"errors":[{"code":9999,"text":"Unknown"}]}`,
			wantErr:    true,
			wantErrMsg: "HTTP 400",
		},
		{
			name:       "empty errors array",
			body:       `{"errors":[]}`,
			wantErr:    true,
			wantErrMsg: "HTTP 400",
		},
		{
			name:       "invalid json",
			body:       `not json`,
			wantErr:    true,
			wantErrMsg: "HTTP 400",
		},
		{
			name:       "empty body",
			body:       ``,
			wantErr:    true,
			wantErrMsg: "HTTP 400",
		},
		{
			name:     "multiple errors uses first",
			body:     `{"errors":[{"code":4001},{"code":1001}]}`,
			wantBody: `{"episodes":[]}`,
		},
		{
			name:       "unrecognized code 2001",
			body:       `{"errors":[{"code":2001}]}`,
			wantErr:    true,
			wantErrMsg: "HTTP 400",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rc, err := classifyBadRequest([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatal("classifyBadRequest() error = nil, want error")
				}
				if tt.wantErrType == "auth" {
					var authErr *api.AuthError
					if !errors.As(err, &authErr) {
						t.Errorf("error type = %T, want *api.AuthError", err)
					}
				}
				if tt.wantErrMsg != "" && err.Error() != tt.wantErrMsg {
					t.Errorf("error = %q, want %q", err.Error(), tt.wantErrMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("classifyBadRequest() unexpected error: %v", err)
			}
			defer rc.Close()
			if tt.wantBody != "" {
				data, _ := io.ReadAll(rc)
				if string(data) != tt.wantBody {
					t.Errorf("body = %q, want %q", string(data), tt.wantBody)
				}
			} else {
				rc.Close()
			}
		})
	}
}

func FuzzClassifyBadRequest(f *testing.F) {
	f.Add([]byte(`{"errors":[{"code":4001,"text":"Show not found"}]}`))
	f.Add([]byte(`{"errors":[{"code":1001,"text":"Invalid API key"}]}`))
	f.Add([]byte(`{"errors":[{"code":9999,"text":"Unknown"}]}`))
	f.Add([]byte(`{"errors":[]}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(``))

	f.Fuzz(func(t *testing.T, body []byte) {
		rc, err := classifyBadRequest(body)
		if err != nil {
			return
		}
		if rc != nil {
			_, _ = io.ReadAll(rc)
			rc.Close()
		}
	})
}
