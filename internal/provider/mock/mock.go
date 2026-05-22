// Package mock provides a configurable mock subtitle provider for functional
// testing. It registers as a normal provider ("mock") and its behavior is
// controlled entirely through config settings:
//
//   - mode: "static" (default), "error", "timeout", "rate_limit", "auth_error",
//     "empty", "slow", "flaky", "season_pack"
//   - delay_ms: artificial latency per Search/Download call (default 0)
//   - result_count: number of results to return in static mode (default 3)
//   - languages: comma-separated language codes to return results for (default: all requested)
//   - error_message: custom error message for error modes
//   - flaky_rate: failure probability 0.0-1.0 for flaky mode (default 0.5)
//   - score_base: base score for returned subtitles (default 50)
//   - include_hash: return hash-matched results (default false)
//   - hearing_impaired: return HI-flagged results (default false)
//   - forced: return forced-flagged results (default false)
//   - download_error: if set, Download returns this error instead of data
//   - subtitle_content: custom SRT content for downloads (default: generated)
//
// The mock provider is only registered when the "mock" provider is enabled
// in config. It never makes network calls.
package mock

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"subflux/internal/api"
	"subflux/internal/provider"
)

const providerName = api.ProviderNameMock

// Factory creates a mock provider from config settings.
func Factory(_ context.Context, settings map[string]any) (api.Provider, error) {
	ps := provider.FromMap(settings)
	_ = ps // mock uses only provider-specific keys from Custom
	p := &mockProvider{
		mode:        provider.SettingString(settings, provider.KeyMode),
		errMsg:      provider.SettingString(settings, provider.KeyErrorMessage),
		dlError:     provider.SettingString(settings, provider.KeyDownloadError),
		srtContent:  provider.SettingString(settings, provider.KeySubtitleContent),
		includeHash: provider.SettingBool(settings, provider.KeyIncludeHash, false),
		hi:          provider.SettingBool(settings, provider.KeyHearingImpaired, false),
		forced:      provider.SettingBool(settings, provider.KeyForced, false),
		resultCount: provider.SettingInt(settings, provider.KeyResultCount, 3),
		scoreBase:   provider.SettingInt(settings, provider.KeyScoreBase, 50),
		flakyRate:   provider.SettingFloat(settings, provider.KeyFlakyRate, 0.5),
	}
	if p.mode == "" {
		p.mode = "static"
	}

	if ms := provider.SettingInt(settings, provider.KeyDelayMs, 0); ms > 0 {
		p.delay = time.Duration(ms) * time.Millisecond
	}

	// Parse language filter.
	if v := provider.SettingString(settings, "languages"); v != "" {
		for l := range strings.SplitSeq(v, ",") {
			l = strings.TrimSpace(l)
			if l != "" {
				p.languages = append(p.languages, l)
			}
		}
	}

	return p, nil
}

// Compile-time interface assertion.
var _ api.Provider = (*mockProvider)(nil)

// mockProvider is a configurable mock subtitle provider.
type mockProvider struct {
	mode        string
	errMsg      string
	dlError     string
	srtContent  string
	languages   []string // if set, only return results for these languages
	delay       time.Duration
	resultCount int
	scoreBase   int
	flakyRate   float64
	includeHash bool
	hi          bool
	forced      bool
}

func (p *mockProvider) Name() api.ProviderID { return providerName }

// Search returns results based on the configured mode.
func (p *mockProvider) Search(ctx context.Context, req *api.SearchRequest) ([]api.Subtitle, error) {
	if err := p.applyDelay(ctx); err != nil {
		return nil, err
	}

	switch p.mode {
	case "error":
		return nil, fmt.Errorf("mock provider error: %s", p.effectiveErrMsg())
	case "timeout":
		return nil, context.DeadlineExceeded
	case "rate_limit":
		return nil, &api.RateLimitError{Msg: "mock rate limit: " + p.effectiveErrMsg()}
	case "auth_error":
		return nil, &api.AuthError{Msg: "mock auth error: " + p.effectiveErrMsg()}
	case "empty":
		return nil, nil
	case "flaky":
		if rand.Float64() < p.flakyRate {
			return nil, fmt.Errorf("mock flaky error: %s", p.effectiveErrMsg())
		}
		return p.generateResults(req), nil
	case "slow":
		// Slow mode adds 5s on top of any configured delay.
		t := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
		return p.generateResults(req), nil
	case "season_pack":
		return p.generateSeasonPackResults(req), nil
	default: // "static"
		return p.generateResults(req), nil
	}
}

// Download returns subtitle data or a configured error.
func (p *mockProvider) Download(ctx context.Context, sub *api.Subtitle) ([]byte, error) {
	if err := p.applyDelay(ctx); err != nil {
		return nil, err
	}
	if p.dlError != "" {
		return nil, fmt.Errorf("mock download error: %s", p.dlError)
	}
	if p.srtContent != "" {
		return []byte(p.srtContent), nil
	}
	return []byte(generateSRT(sub.Language, sub.ReleaseName)), nil
}

func (p *mockProvider) effectiveErrMsg() string {
	if p.errMsg != "" {
		return p.errMsg
	}
	return "simulated failure"
}

func (p *mockProvider) applyDelay(ctx context.Context) error {
	if p.delay <= 0 {
		return nil
	}
	t := time.NewTimer(p.delay)
	select {
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (p *mockProvider) matchesLanguage(lang string) bool {
	if len(p.languages) == 0 {
		return true
	}
	return slices.Contains(p.languages, lang)
}

func (p *mockProvider) generateResults(req *api.SearchRequest) []api.Subtitle {
	var results []api.Subtitle
	for _, lang := range req.Languages {
		if !p.matchesLanguage(lang) {
			continue
		}
		for i := range p.resultCount {
			sub := api.Subtitle{
				Provider:    providerName,
				ID:          fmt.Sprintf("mock-%s-%d", lang, i),
				Language:    lang,
				ReleaseName: p.releaseNameFor(req, i),
				DownloadURL: fmt.Sprintf("mock://download/%s/%d", lang, i),
				MatchedBy:   api.MatchByTitle,
				Title:       req.Title,
				Year:        req.Year,
				Season:      req.Season,
				Episode:     req.Episode,
				Score:       p.scoreBase - i*5,
				HearingImp:  p.hi,
				Forced:      p.forced,
			}
			if p.includeHash && i == 0 {
				sub.MatchedBy = api.MatchByHash
			}
			results = append(results, sub)
		}
	}
	return results
}

func (p *mockProvider) generateSeasonPackResults(req *api.SearchRequest) []api.Subtitle {
	var results []api.Subtitle
	for _, lang := range req.Languages {
		if !p.matchesLanguage(lang) {
			continue
		}
		sub := api.Subtitle{
			Provider:    providerName,
			ID:          fmt.Sprintf("mock-spack-%s", lang),
			Language:    lang,
			ReleaseName: fmt.Sprintf("%s.S%02d.Complete.1080p.WEB-DL", req.Title, req.Season),
			DownloadURL: fmt.Sprintf("mock://download/spack/%s", lang),
			MatchedBy:   api.MatchByTitle,
			Title:       req.Title,
			Year:        req.Year,
			Season:      req.Season,
			Score:       p.scoreBase,
		}
		results = append(results, sub)
	}
	return results
}

func (p *mockProvider) releaseNameFor(req *api.SearchRequest, idx int) string {
	groups := []string{"FLUX", "NTb", "SPARKS", "YTS", "RARBG"}
	sources := []string{"BluRay", "WEB-DL", "HDTV", "WEBRip"}
	codecs := []string{"x264", "x265", "AV1"}

	group := groups[idx%len(groups)]
	source := sources[idx%len(sources)]
	codec := codecs[idx%len(codecs)]

	if req.MediaType == api.MediaTypeEpisode {
		return fmt.Sprintf("%s.S%02dE%02d.1080p.%s.%s-%s",
			strings.ReplaceAll(req.Title, " ", "."),
			req.Season, req.Episode, source, codec, group)
	}
	return fmt.Sprintf("%s.%d.1080p.%s.%s-%s",
		strings.ReplaceAll(req.Title, " ", "."),
		req.Year, source, codec, group)
}

// generateSRT creates a minimal valid SRT file for testing.
func generateSRT(lang, release string) string {
	return fmt.Sprintf(`1
00:00:01,000 --> 00:00:04,000
[Mock subtitle - %s]
Provider: mock | Release: %s

2
00:00:05,000 --> 00:00:08,000
This is a test subtitle generated
by the mock provider for functional testing.

3
00:00:10,000 --> 00:00:15,000
Language: %s
Timestamp: %s
`, lang, release, lang, time.Now().Format(time.RFC3339))
}

// Schema returns the UI schema fields for the mock provider settings page.
func Schema() []api.ProviderSchemaField {
	return []api.ProviderSchemaField{
		{Key: "mode", Label: "Mode", Type: "text",
			Default: "static",
			Help:    "static, error, timeout, rate_limit, auth_error, empty, slow, flaky, season_pack"},
		{Key: "delay_ms", Label: "Delay (ms)", Type: "text",
			Default: "0", Help: "Artificial latency per call"},
		{Key: "result_count", Label: "Result Count", Type: "text",
			Default: "3", Help: "Number of results in static mode"},
		{Key: "score_base", Label: "Base Score", Type: "text",
			Default: "50", Help: "Starting score (decrements by 5 per result)"},
		{Key: "languages", Label: "Languages", Type: "text",
			Help: "Comma-separated language codes to return results for (empty = all)"},
		{Key: "include_hash", Label: "Include Hash Match", Type: "bool",
			Default: "false", Help: "First result uses hash matching"},
		{Key: "hearing_impaired", Label: "Hearing Impaired", Type: "bool",
			Default: "false", Help: "Flag results as HI"},
		{Key: "forced", Label: "Forced", Type: "bool",
			Default: "false", Help: "Flag results as forced"},
		{Key: "error_message", Label: "Error Message", Type: "text",
			Help: "Custom error message for error modes"},
		{Key: "download_error", Label: "Download Error", Type: "text",
			Help: "If set, Download() returns this error"},
		{Key: "subtitle_content", Label: "Subtitle Content", Type: "text",
			Help: "Custom SRT content for downloads (default: auto-generated)"},
		{Key: "flaky_rate", Label: "Flaky Rate", Type: "text",
			Default: "0.5", Help: "Failure probability for flaky mode (0.0-1.0)"},
	}
}
