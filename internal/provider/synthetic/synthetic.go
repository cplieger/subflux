// Package synthetic provides a subtitle provider that answers from generated
// data instead of an upstream service. It is a real, operator-selectable
// provider registered as "synthetic", not a test double: it takes no injected
// behaviour and nothing substitutes it for another provider. Its behaviour is
// controlled entirely through config settings:
//
//   - mode: "static" (default), "error", "timeout", "rate_limit", "auth_error",
//     "empty", "slow", "flaky", "season_pack"
//   - delay_ms: artificial latency per Search/Download call (default 0)
//   - result_count: number of results to return in static mode (default 3)
//   - languages: comma-separated language codes to return results for (default: all requested)
//   - error_message: custom error message for error modes
//   - flaky_rate: failure probability 0.0-1.0 for flaky mode (default 0.5)
//   - include_hash: return hash-matched results (default false)
//   - hearing_impaired: return HI-flagged results (default false)
//   - forced: return forced-flagged results (default false)
//   - download_error: if set, Download returns this error instead of data
//   - subtitle_content: custom SRT content for downloads (default: generated)
//
// It is disabled by default, kept out of the settings UI's provider list, and
// never makes network calls — its purpose is to exercise the search, download,
// error and timeout paths against predictable results, on a dev box or in the
// functional suite, without asking a real upstream for traffic.
package synthetic

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/subflux"
)

const (
	modError      = "error"
	modTimeout    = "timeout"
	modRateLimit  = "rate_limit"
	modAuthError  = "auth_error"
	modEmpty      = "empty"
	modSeasonPack = "season_pack"

	keyMode            = "mode"
	keyDelayMs         = "delay_ms"
	keyResultCount     = "result_count"
	keyLanguages       = "languages"
	keyHearingImpaired = "hearing_impaired"
	keyForced          = "forced"
	keyErrorMessage    = "error_message"
	keyDownloadError   = "download_error"
	keySubtitleContent = "subtitle_content"
	keyFlakyRate       = "flaky_rate"

	fieldBool      = "bool"
	fieldText      = "text"
	keyIncludeHash = "include_hash"
	valFalse       = "false"
)

const providerName = subflux.ProviderNameSynthetic

// Factory creates a synthetic provider from config settings.
func Factory(_ context.Context, settings map[string]any) (provider.Provider, error) {
	ps := provider.FromMap(settings)
	_ = ps // synthetic uses only provider-specific keys from Custom
	p := &syntheticProvider{
		mode:        provider.SettingString(settings, provider.KeyMode),
		errMsg:      provider.SettingString(settings, provider.KeyErrorMessage),
		dlError:     provider.SettingString(settings, provider.KeyDownloadError),
		srtContent:  provider.SettingString(settings, provider.KeySubtitleContent),
		includeHash: provider.SettingBool(settings, provider.KeyIncludeHash, false),
		hi:          provider.SettingBool(settings, provider.KeyHearingImpaired, false),
		forced:      provider.SettingBool(settings, provider.KeyForced, false),
		resultCount: provider.SettingInt(settings, provider.KeyResultCount, 3),
		flakyRate:   provider.SettingFloat(settings, provider.KeyFlakyRate, 0.5),
	}
	if p.mode == "" {
		p.mode = "static"
	}

	if ms := provider.SettingInt(settings, provider.KeyDelayMs, 0); ms > 0 {
		p.delay = time.Duration(ms) * time.Millisecond
	}

	// Parse language filter.
	if v := provider.SettingString(settings, keyLanguages); v != "" {
		for l := range strings.SplitSeq(v, ",") {
			l = strings.TrimSpace(l)
			if l != "" {
				p.languages = append(p.languages, l)
			}
		}
	}

	return p, nil
}

// syntheticProvider answers searches and downloads from generated data.
type syntheticProvider struct {
	mode        string
	errMsg      string
	dlError     string
	srtContent  string
	languages   []string // if set, only return results for these languages
	delay       time.Duration
	resultCount int
	flakyRate   float64
	includeHash bool
	hi          bool
	forced      bool
}

func (p *syntheticProvider) Name() subflux.ProviderID { return providerName }

// Search returns results based on the configured mode.
func (p *syntheticProvider) Search(ctx context.Context, req *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	if err := p.applyDelay(ctx); err != nil {
		return nil, err
	}

	switch p.mode {
	case modError:
		return nil, fmt.Errorf("synthetic provider error: %s", p.effectiveErrMsg())
	case modTimeout:
		return nil, context.DeadlineExceeded
	case modRateLimit:
		return nil, &subflux.RateLimitError{Msg: "synthetic rate limit: " + p.effectiveErrMsg()}
	case modAuthError:
		return nil, &subflux.AuthError{Msg: "synthetic auth error: " + p.effectiveErrMsg()}
	case modEmpty:
		return nil, nil
	case "flaky":
		if rand.Float64() < p.flakyRate { //nolint:gosec // G404: generated fixture data, not crypto
			return nil, fmt.Errorf("synthetic flaky error: %s", p.effectiveErrMsg())
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
	case modSeasonPack:
		return p.generateSeasonPackResults(req), nil
	default: // "static"
		return p.generateResults(req), nil
	}
}

// Download returns subtitle data or a configured error.
func (p *syntheticProvider) Download(ctx context.Context, sub *subflux.Subtitle) ([]byte, error) {
	if err := p.applyDelay(ctx); err != nil {
		return nil, err
	}
	if p.dlError != "" {
		return nil, fmt.Errorf("synthetic download error: %s", p.dlError)
	}
	if p.srtContent != "" {
		return []byte(p.srtContent), nil
	}
	return []byte(generateSRT(sub.Language, sub.ReleaseName)), nil
}

func (p *syntheticProvider) effectiveErrMsg() string {
	if p.errMsg != "" {
		return p.errMsg
	}
	return "simulated failure"
}

func (p *syntheticProvider) applyDelay(ctx context.Context) error {
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

func (p *syntheticProvider) matchesLanguage(lang string) bool {
	if len(p.languages) == 0 {
		return true
	}
	return slices.Contains(p.languages, lang)
}

func (p *syntheticProvider) generateResults(req *subflux.SearchRequest) []subflux.Subtitle {
	var results []subflux.Subtitle
	for _, lang := range req.Languages {
		if !p.matchesLanguage(lang) {
			continue
		}
		for i := range p.resultCount {
			sub := subflux.Subtitle{
				Provider:    providerName,
				ID:          fmt.Sprintf("synthetic-%s-%d", lang, i),
				Language:    lang,
				ReleaseName: p.releaseNameFor(req, i),
				DownloadURL: fmt.Sprintf("synthetic://download/%s/%d", lang, i),
				MatchedBy:   subflux.MatchByTitle,
				Title:       req.Title,
				Year:        req.Year,
				Season:      req.Season,
				Episode:     req.Episode,
				HearingImp:  p.hi,
				Forced:      p.forced,
			}
			if p.includeHash && i == 0 {
				sub.MatchedBy = subflux.MatchByHash
			}
			results = append(results, sub)
		}
	}
	return results
}

func (p *syntheticProvider) generateSeasonPackResults(req *subflux.SearchRequest) []subflux.Subtitle {
	var results []subflux.Subtitle
	for _, lang := range req.Languages {
		if !p.matchesLanguage(lang) {
			continue
		}
		sub := subflux.Subtitle{
			Provider:    providerName,
			ID:          fmt.Sprintf("synthetic-spack-%s", lang),
			Language:    lang,
			ReleaseName: fmt.Sprintf("%s.S%02d.Complete.1080p.WEB-DL", req.Title, req.Season),
			DownloadURL: fmt.Sprintf("synthetic://download/spack/%s", lang),
			MatchedBy:   subflux.MatchByTitle,
			Title:       req.Title,
			Year:        req.Year,
			Season:      req.Season,
		}
		results = append(results, sub)
	}
	return results
}

func (p *syntheticProvider) releaseNameFor(req *subflux.SearchRequest, idx int) string {
	groups := []string{"FLUX", "NTb", "SPARKS", "YTS", "RARBG"}
	sources := []string{"BluRay", "WEB-DL", "HDTV", "WEBRip"}
	codecs := []string{"x264", "x265", "AV1"}

	group := groups[idx%len(groups)]
	source := sources[idx%len(sources)]
	codec := codecs[idx%len(codecs)]

	if req.MediaType == subflux.MediaTypeEpisode {
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
[Synthetic subtitle - %s]
Provider: synthetic | Release: %s

2
00:00:05,000 --> 00:00:08,000
This is a test subtitle generated
by the synthetic provider for functional testing.

3
00:00:10,000 --> 00:00:15,000
Language: %s
Timestamp: %s
`, lang, release, lang, time.Now().UTC().Format(time.RFC3339))
}

// Schema returns the UI schema fields for the synthetic provider settings page.
func Schema() []subflux.ProviderSchemaField {
	return []subflux.ProviderSchemaField{
		{
			Key: keyMode, Label: "Mode", Type: fieldText,
			Default: "static",
			Help:    "static, error, timeout, rate_limit, auth_error, empty, slow, flaky, season_pack",
		},
		{
			Key: keyDelayMs, Label: "Delay (ms)", Type: fieldText,
			Default: "0", Help: "Artificial latency per call",
		},
		{
			Key: keyResultCount, Label: "Result Count", Type: fieldText,
			Default: "3", Help: "Number of results in static mode",
		},
		{
			Key: keyLanguages, Label: "Languages", Type: fieldText,
			Help: "Comma-separated language codes to return results for (empty = all)",
		},
		{
			Key: keyIncludeHash, Label: "Include Hash Match", Type: fieldBool,
			Default: valFalse, Help: "First result uses hash matching",
		},
		{
			Key: keyHearingImpaired, Label: "Hearing Impaired", Type: fieldBool,
			Default: valFalse, Help: "Flag results as HI",
		},
		{
			Key: keyForced, Label: "Forced", Type: fieldBool,
			Default: valFalse, Help: "Flag results as forced",
		},
		{
			Key: keyErrorMessage, Label: "Error Message", Type: fieldText,
			Help: "Custom error message for error modes",
		},
		{
			Key: keyDownloadError, Label: "Download Error", Type: fieldText,
			Help: "If set, Download() returns this error",
		},
		{
			Key: keySubtitleContent, Label: "Subtitle Content", Type: fieldText,
			Help: "Custom SRT content for downloads (default: auto-generated)",
		},
		{
			Key: keyFlakyRate, Label: "Flaky Rate", Type: fieldText,
			Default: "0.5", Help: "Failure probability for flaky mode (0.0-1.0)",
		},
	}
}
