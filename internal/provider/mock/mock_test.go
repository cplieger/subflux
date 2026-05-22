package mock

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"subflux/internal/api"
)

func TestFactory_defaults(t *testing.T) {
	t.Parallel()
	p, err := Factory(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "mock" {
		t.Errorf("Name() = %q, want mock", p.Name())
	}
}

func TestSearch_static_returns_results(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), map[string]any{"result_count": "5"})
	req := &api.SearchRequest{
		Title:     "Test Movie",
		Year:      2024,
		MediaType: "movie",
		Languages: []string{"en"},
	}
	subs, err := p.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 5 {
		t.Errorf("got %d results, want 5", len(subs))
	}
	for _, s := range subs {
		if s.Language != "en" {
			t.Errorf("language = %q, want en", s.Language)
		}
		if s.Provider != "mock" {
			t.Errorf("provider = %q, want mock", s.Provider)
		}
	}
}

func TestSearch_modes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		config      map[string]any
		wantErr     bool
		errContains string
		errType     string // "auth", "rate_limit", "deadline"
		wantCount   int
	}{
		{
			name:        "error",
			config:      map[string]any{"mode": "error", "error_message": "boom"},
			wantErr:     true,
			errContains: "boom",
		},
		{
			name:    "auth_error",
			config:  map[string]any{"mode": "auth_error"},
			wantErr: true,
			errType: "auth",
		},
		{
			name:    "rate_limit",
			config:  map[string]any{"mode": "rate_limit"},
			wantErr: true,
			errType: "rate_limit",
		},
		{
			name:      "empty",
			config:    map[string]any{"mode": "empty"},
			wantCount: 0,
		},
		{
			name:    "timeout",
			config:  map[string]any{"mode": "timeout"},
			wantErr: true,
			errType: "deadline",
		},
		{
			name:      "season_pack",
			config:    map[string]any{"mode": "season_pack"},
			wantCount: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, _ := Factory(context.Background(), tc.config)
			req := &api.SearchRequest{
				Title:     "Breaking Bad",
				Year:      2008,
				Season:    1,
				Episode:   1,
				MediaType: "episode",
				Languages: []string{"en"},
			}
			subs, err := p.Search(context.Background(), req)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tc.errContains)
				}
				if tc.errType != "" {
					switch tc.errType {
					case "auth":
						var authErr *api.AuthError
						if !errors.As(err, &authErr) {
							t.Errorf("expected AuthError, got %T", err)
						}
					case "rate_limit":
						var rlErr *api.RateLimitError
						if !errors.As(err, &rlErr) {
							t.Errorf("expected RateLimitError, got %T", err)
						}
					case "deadline":
						if !errors.Is(err, context.DeadlineExceeded) {
							t.Errorf("error = %v, want context.DeadlineExceeded", err)
						}
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(subs) != tc.wantCount {
				t.Errorf("got %d results, want %d", len(subs), tc.wantCount)
			}
		})
	}
}

func TestSearch_language_filter(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), map[string]any{"languages": "fr,de"})
	subs, err := p.Search(context.Background(), &api.SearchRequest{
		Languages: []string{"en", "fr", "de"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range subs {
		if s.Language != "fr" && s.Language != "de" {
			t.Errorf("unexpected language %q", s.Language)
		}
	}
}

func TestSearch_hash_match(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), map[string]any{"include_hash": true})
	subs, err := p.Search(context.Background(), &api.SearchRequest{
		Title:     "Test",
		MediaType: "movie",
		Languages: []string{"en"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) == 0 {
		t.Fatal("no results")
	}
	if subs[0].MatchedBy != "hash" {
		t.Errorf("first result MatchedBy = %q, want hash", subs[0].MatchedBy)
	}
}

func TestDownload_returns_srt(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), nil)
	data, err := p.Download(context.Background(), &api.Subtitle{
		Language:    "en",
		ReleaseName: "Test.2024.1080p.WEB-DL",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Mock subtitle") {
		t.Error("download data missing mock marker")
	}
}

func TestDownload_error(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), map[string]any{"download_error": "disk full"})
	_, err := p.Download(context.Background(), &api.Subtitle{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error = %q, want to contain 'disk full'", err.Error())
	}
}

func TestSearch_episode_release_name(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), map[string]any{"result_count": "1"})
	subs, err := p.Search(context.Background(), &api.SearchRequest{
		Title:     "Breaking Bad",
		Year:      2008,
		Season:    1,
		Episode:   1,
		MediaType: "episode",
		Languages: []string{"en"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d results, want 1", len(subs))
	}
	if !strings.Contains(subs[0].ReleaseName, "S01E01") {
		t.Errorf("release name %q missing S01E01", subs[0].ReleaseName)
	}
}

func TestSearch_slow_mode_respects_context_cancellation(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), map[string]any{"mode": "slow"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Search(ctx, &api.SearchRequest{
		Languages: []string{"en"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestDownload_custom_content(t *testing.T) {
	t.Parallel()
	content := "1\n00:00:01,000 --> 00:00:02,000\nCustom\n"
	p, _ := Factory(context.Background(), map[string]any{"subtitle_content": content})
	data, err := p.Download(context.Background(), &api.Subtitle{
		Language:    "en",
		ReleaseName: "Test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("Download() = %q, want %q", data, content)
	}
}

func TestFactory_invalid_numeric_settings_use_defaults(t *testing.T) {
	t.Parallel()
	p, err := Factory(context.Background(), map[string]any{
		"delay_ms":     "not-a-number",
		"result_count": "bad",
		"flaky_rate":   "invalid",
		"score_base":   "nope",
	})
	if err != nil {
		t.Fatal(err)
	}
	mock := p.(*mockProvider)
	if mock.delay != 0 {
		t.Errorf("delay = %v, want 0 (default)", mock.delay)
	}
	if mock.resultCount != 3 {
		t.Errorf("resultCount = %d, want 3 (default)", mock.resultCount)
	}
	if mock.flakyRate != 0.5 {
		t.Errorf("flakyRate = %f, want 0.5 (default)", mock.flakyRate)
	}
	if mock.scoreBase != 50 {
		t.Errorf("scoreBase = %d, want 50 (default)", mock.scoreBase)
	}
}

func TestSearch_season_pack_multiple_languages(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), map[string]any{"mode": "season_pack"})
	req := &api.SearchRequest{
		Title:     "Test Show",
		Season:    2,
		MediaType: "episode",
		Languages: []string{"en", "fr"},
	}
	subs, err := p.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 {
		t.Fatalf("Search(season_pack, 2 langs) = %d results, want 2", len(subs))
	}
	langs := map[string]bool{subs[0].Language: true, subs[1].Language: true}
	if !langs["en"] || !langs["fr"] {
		t.Errorf("languages = %v, want en and fr", langs)
	}
}

func TestSearch_season_pack_language_filter(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), map[string]any{"mode": "season_pack", "languages": "fr"})
	req := &api.SearchRequest{
		Title:     "Test Show",
		Season:    1,
		MediaType: "episode",
		Languages: []string{"en", "fr"},
	}
	subs, err := p.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("Search(season_pack, filtered) = %d results, want 1", len(subs))
	}
	if subs[0].Language != "fr" {
		t.Errorf("Language = %q, want fr", subs[0].Language)
	}
}

func TestFactory_delay_and_score_settings(t *testing.T) {
	t.Parallel()
	p, err := Factory(context.Background(), map[string]any{
		"delay_ms":   "100",
		"score_base": "75",
	})
	if err != nil {
		t.Fatal(err)
	}
	mock := p.(*mockProvider)
	if mock.delay != 100*time.Millisecond {
		t.Errorf("delay = %v, want 100ms", mock.delay)
	}
	if mock.scoreBase != 75 {
		t.Errorf("scoreBase = %d, want 75", mock.scoreBase)
	}
}

func TestFactory_negative_delay_ignored(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), map[string]any{"delay_ms": "-5"})
	mock := p.(*mockProvider)
	if mock.delay != 0 {
		t.Errorf("delay = %v, want 0 (negative ignored)", mock.delay)
	}
}

func TestFactory_zero_result_count(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), map[string]any{"result_count": "0"})
	subs, err := p.(*mockProvider).Search(context.Background(), &api.SearchRequest{
		Title:     "Test",
		MediaType: "movie",
		Languages: []string{"en"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 0 {
		t.Errorf("Search(result_count=0) = %d results, want 0", len(subs))
	}
}

func TestSearch_hi_and_forced_flags(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), map[string]any{
		"hearing_impaired": true,
		"forced":           true,
		"result_count":     "1",
	})
	subs, err := p.Search(context.Background(), &api.SearchRequest{
		Title:     "Test",
		MediaType: "movie",
		Languages: []string{"en"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d results, want 1", len(subs))
	}
	if !subs[0].HearingImp {
		t.Error("HearingImp = false, want true")
	}
	if !subs[0].Forced {
		t.Error("Forced = false, want true")
	}
}

func TestSearch_score_decrements(t *testing.T) {
	t.Parallel()
	p, _ := Factory(context.Background(), map[string]any{"result_count": "3", "score_base": "60"})
	subs, err := p.Search(context.Background(), &api.SearchRequest{
		Title:     "Test",
		MediaType: "movie",
		Languages: []string{"en"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 3 {
		t.Fatalf("got %d results, want 3", len(subs))
	}
	wantScores := []int{60, 55, 50}
	for i, want := range wantScores {
		if subs[i].Score != want {
			t.Errorf("subs[%d].Score = %d, want %d", i, subs[i].Score, want)
		}
	}
}

func TestApplyDelay_context_cancelled(t *testing.T) {
	t.Parallel()
	p := &mockProvider{delay: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.applyDelay(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("applyDelay(cancelled) = %v, want context.Canceled", err)
	}
}

func TestSchema_returns_all_fields(t *testing.T) {
	t.Parallel()
	fields := Schema()
	if len(fields) != 12 {
		t.Errorf("Schema() returned %d fields, want 12", len(fields))
	}
	wantKeys := []string{
		"mode", "delay_ms", "result_count", "score_base",
		"languages", "include_hash", "hearing_impaired", "forced",
		"error_message", "download_error", "subtitle_content", "flaky_rate",
	}
	for i, want := range wantKeys {
		if i >= len(fields) {
			break
		}
		if fields[i].Key != want {
			t.Errorf("Schema()[%d].Key = %q, want %q", i, fields[i].Key, want)
		}
	}
}
