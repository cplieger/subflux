package config

import (
	"log/slog"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// Compile-time assertions: *Config satisfies all config sub-interfaces.
var (
	_ api.ScoringConfig          = (*Config)(nil)
	_ api.LanguageResolver       = (*Config)(nil)
	_ api.ArrConfigProvider      = (*Config)(nil)
	_ api.ProviderConfigProvider = (*Config)(nil)
	_ api.ServerConfig           = (*Config)(nil)
	_ api.PathValidator          = (*Config)(nil)
	_ api.SearchConfigProvider   = (*Config)(nil)
	_ api.AuthConfigProvider     = (*Config)(nil)
	_ api.UIConfigProvider       = (*Config)(nil)
)

// ResolveTargetsWithFallback implements the full language resolution chain:
// 1. Match originalLanguage against rules
// 2. If no match, try each audio track language against rules
// 3. If still no match, return default targets
func (c *Config) ResolveTargetsWithFallback(originalLang string, audioLangs []string) []api.SubtitleTarget {
	// Priority 1: original language from arr API.
	if originalLang != "" {
		if targets := c.matchRule(originalLang); targets != nil {
			slog.Debug("ResolveTargetsWithFallback: matched original language",
				"original_lang", originalLang, "targets", len(targets))
			return targets
		}
	}

	// Priority 2: audio tracks from the file.
	for _, audio := range audioLangs {
		if targets := c.matchRule(audio); targets != nil {
			slog.Debug("ResolveTargetsWithFallback: matched audio track",
				"audio_lang", audio, "targets", len(targets))
			return targets
		}
	}

	// Priority 3: default.
	if c.cachedDefaultTargets != nil {
		return c.cachedDefaultTargets
	}
	return targetsToAPI(c.Languages.Default)
}

// computeLangCodes returns a deduplicated list of subtitle language codes
// from rules and defaults, preserving first-seen order.
func computeLangCodes(rules []AudioRule, defaults []yamlSubtitleTarget) []string {
	seen := make(map[string]struct{})
	var codes []string
	for _, rule := range rules {
		for _, t := range rule.Subtitles {
			if _, ok := seen[t.Code]; !ok {
				codes = append(codes, t.Code)
				seen[t.Code] = struct{}{}
			}
		}
	}
	for _, t := range defaults {
		if _, ok := seen[t.Code]; !ok {
			codes = append(codes, t.Code)
			seen[t.Code] = struct{}{}
		}
	}
	return codes
}

// LanguageCodes returns a deduplicated list of all subtitle language codes
// across all rules and the default. Used for full library scans where
// audio language isn't known upfront.
func (c *Config) LanguageCodes() []string {
	if c.cachedLangCodes != nil {
		return c.cachedLangCodes
	}
	// Fallback for configs not loaded via LoadFromBytes (e.g. tests).
	return computeLangCodes(c.Languages.Rules, c.Languages.Default)
}

// ProvidersForTarget returns which providers to use for a subtitle target.
func (c *Config) ProvidersForTarget(t *api.SubtitleTarget, allProviders []api.ProviderID) []api.ProviderID {
	if len(t.Providers) > 0 {
		slog.Debug("ProvidersForTarget: using include list",
			"lang", t.Code, "providers", t.Providers)
		return t.Providers
	}
	if len(t.Exclude) > 0 {
		excludeSet := make(map[api.ProviderID]struct{}, len(t.Exclude))
		for _, e := range t.Exclude {
			excludeSet[e] = struct{}{}
		}
		var filtered []api.ProviderID
		for _, p := range allProviders {
			if _, excluded := excludeSet[p]; !excluded {
				filtered = append(filtered, p)
			}
		}
		slog.Debug("ProvidersForTarget: applied exclude list",
			"lang", t.Code, "excluded", t.Exclude, "remaining", filtered)
		return filtered
	}
	slog.Debug("ProvidersForTarget: using all providers",
		"lang", t.Code, "count", len(allProviders))
	return allProviders
}

// MinScoreForTarget returns the minimum score for a target,
// falling back to the global min_score.
// The mediaType parameter is part of the ConfigProvider interface
// for future per-media-type score overrides; currently unused.
func (c *Config) MinScoreForTarget(t *api.SubtitleTarget, _ api.MediaType) int {
	if t.MinScore != nil {
		return *t.MinScore
	}
	return c.SearchCfg.MinScore
}

// SonarrConfig returns the Sonarr connection config.
// Returns empty config when disabled.
func (c *Config) SonarrConfig() api.ArrConfig {
	if !c.Sonarr.isEnabled() {
		return api.ArrConfig{}
	}
	return arrConfig(c.Sonarr)
}

// RadarrConfig returns the Radarr connection config.
// Returns empty config when disabled.
func (c *Config) RadarrConfig() api.ArrConfig {
	if !c.Radarr.isEnabled() {
		return api.ArrConfig{}
	}
	return arrConfig(c.Radarr)
}

// arrConfig builds an ArrConfig with bidirectional URL fallback.
func arrConfig(y yamlArrConfig) api.ArrConfig {
	url := y.URL
	pub := y.PublicURL
	if url == "" {
		url = pub
	}
	if pub == "" {
		pub = url
	}
	return api.ArrConfig{URL: url, APIKey: y.APIKey, PublicURL: pub}
}

// ServerPort returns the fixed HTTP server port.
func (c *Config) ServerPort() int { return ServerPort }

// PollInterval returns the configured arr history poll interval.
func (c *Config) PollInterval() time.Duration { return c.PollIntervalCfg.D }

// LoggingLevel returns the configured log level.
func (c *Config) LoggingLevel() api.LogLevel { return c.Logging.Level }

// LoggingFormat returns the configured log format.
func (c *Config) LoggingFormat() api.LogFormat { return c.Logging.Format }

// LanguageRulesForUI returns the raw (unexpanded) language rules for the UI.
func (c *Config) LanguageRulesForUI() api.LanguageRulesJSON {
	result := api.LanguageRulesJSON{}
	for _, rule := range c.Languages.Rules {
		jr := api.AudioRuleJSON{Audio: rule.Audio}
		for i := range rule.Subtitles {
			jr.Subtitles = append(jr.Subtitles, yamlTargetToJSON(&rule.Subtitles[i]))
		}
		result.Rules = append(result.Rules, jr)
	}
	for i := range c.Languages.Default {
		result.Default = append(result.Default, yamlTargetToJSON(&c.Languages.Default[i]))
	}
	return result
}

// yamlTargetToJSON converts a yamlSubtitleTarget to api.SubtitleTargJSON for the UI.
func yamlTargetToJSON(t *yamlSubtitleTarget) api.SubtitleTargJSON {
	providers := make([]string, 0, len(t.Providers))
	for _, p := range t.Providers {
		providers = append(providers, string(p))
	}
	exclude := make([]string, 0, len(t.Exclude))
	for _, e := range t.Exclude {
		exclude = append(exclude, string(e))
	}
	return api.SubtitleTargJSON{
		MinScore:  t.MinScore,
		Code:      t.Code,
		Variant:   t.Variant,
		Variants:  t.Variants,
		Providers: providers,
		Exclude:   exclude,
	}
}

// matchRule returns the subtitle targets for a matching audio language rule,
// or nil if no rule matches. Does not fall back to defaults.
func (c *Config) matchRule(audioLang string) []api.SubtitleTarget {
	if c.cachedRuleTargets != nil {
		targets, ok := c.cachedRuleTargets[audioLang]
		if !ok {
			return nil
		}
		if targets == nil {
			return []api.SubtitleTarget{}
		}
		return targets
	}
	if c.ruleIndex != nil {
		idx, ok := c.ruleIndex[audioLang]
		if !ok {
			return nil
		}
		targets := targetsToAPI(c.Languages.Rules[idx].Subtitles)
		if targets == nil {
			return []api.SubtitleTarget{}
		}
		return targets
	}
	// Fallback for configs not loaded via LoadFromBytes (e.g. tests).
	for _, rule := range c.Languages.Rules {
		if rule.Audio == audioLang {
			targets := targetsToAPI(rule.Subtitles)
			if targets == nil {
				return []api.SubtitleTarget{}
			}
			return targets
		}
	}
	return nil
}
