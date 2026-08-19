package config

import (
	"maps"

	"github.com/cplieger/subflux/internal/api"
)

// Adaptive returns the adaptive search config.
func (c *Config) Adaptive() api.AdaptiveConfig {
	return api.AdaptiveConfig{
		Enabled:           c.AdaptiveCfg.Enabled,
		InitialDelay:      c.AdaptiveCfg.InitialDelay.D,
		MaxDelay:          c.AdaptiveCfg.MaxDelay.D,
		BackoffMultiplier: c.AdaptiveCfg.BackoffMultiplier,
		MaxAttempts:       c.AdaptiveCfg.MaxAttempts,
	}
}

// Search returns the search config.
func (c *Config) Search() api.SearchConfig {
	return api.SearchConfig{
		ScanInterval:           c.SearchCfg.ScanInterval.D,
		ProviderTimeout:        c.SearchCfg.ProviderTimeout.D,
		ScanDelay:              c.SearchCfg.ScanDelay.D,
		MinScore:               c.SearchCfg.MinScore,
		UpgradeEnabled:         c.SearchCfg.UpgradeEnabled,
		UpgradeWindowDays:      c.SearchCfg.UpgradeWindowDays,
		ExcludeArrTags:         c.SearchCfg.ExcludeArrTags,
		DownloadMaxAttempts:    c.SearchCfg.DownloadMaxAttempts,
		MaxProviderConcurrency: c.SearchCfg.MaxProviderConcurrency,
		MaxSSEClients:          c.SearchCfg.MaxSSEClients,
	}
}

// PostProcessConfig returns the post-processing configuration.
func (c *Config) PostProcessConfig() api.PostProcessConfig {
	return api.PostProcessConfig{
		StripHI:          c.PostProcessing.StripHI,
		StripTags:        c.PostProcessing.StripTags,
		NormalizeUTF8:    c.PostProcessing.NormalizeUTF8,
		CleanWhitespace:  c.PostProcessing.CleanWhitespace,
		NormalizeEndings: c.PostProcessing.NormalizeEndings,
		RemoveEmpty:      c.PostProcessing.RemoveEmpty,
	}
}

// SyncConfig returns the sync configuration.
func (c *Config) SyncConfig() api.SyncConfig {
	sc := api.SyncConfig{
		SyncSubtitles:     c.PostProcessing.SyncSubtitles,
		AudioSyncFallback: c.PostProcessing.AudioSyncFallback,
		SyncMinConfidence: c.PostProcessing.SyncMinConfidence,
	}
	if sc.SyncMinConfidence == 0 {
		sc.SyncMinConfidence = api.DefaultSyncMinConfidence
	}
	return sc
}

// EmbeddedPolicy returns the typed embedded subtitle codec policy from the
// top-level embedded_subtitles section. This is the single config source for
// the search engine's ignored-codec decision (the detector takes no settings
// and always returns every track).
func (c *Config) EmbeddedPolicy() api.EmbeddedPolicy {
	return api.EmbeddedPolicy{
		IgnorePGS:    c.EmbeddedSubtitles.IgnorePGS,
		IgnoreVobSub: c.EmbeddedSubtitles.IgnoreVobSub,
		IgnoreASS:    c.EmbeddedSubtitles.IgnoreASS,
	}
}

// ProviderConfigs returns the provider configuration map. The map, and every
// Settings map inside it, is a copy: a caller that adds a provider or edits a
// setting is editing its own copy, not the live configuration every subsequent
// scan reads. The copy is deep because a shallow one would change nothing —
// the aliasing that matters is the inner Settings map, which both the cached
// and the recomputed path used to hand out directly.
func (c *Config) ProviderConfigs() map[api.ProviderID]api.ProviderCfg {
	src := c.cachedProviderConfigs
	if src == nil {
		// Fallback for configs not loaded via LoadFromBytes (e.g. tests).
		src = make(map[api.ProviderID]api.ProviderCfg, len(c.Providers))
		for k, v := range c.Providers {
			src[k] = api.ProviderCfg{Settings: v.Settings, Enabled: v.Enabled, Priority: v.Priority}
		}
	}
	out := make(map[api.ProviderID]api.ProviderCfg, len(src))
	for k, v := range src {
		v.Settings = maps.Clone(v.Settings)
		out[k] = v
	}
	return out
}

// ProviderPriority returns the priority for a provider (lower = higher trust).
// Returns api.DefaultProviderPriority for unconfigured or zero-priority providers.
func (c *Config) ProviderPriority(name api.ProviderID) int {
	if p, ok := c.Providers[name]; ok && p.Priority > 0 {
		return p.Priority
	}
	return api.DefaultProviderPriority
}

// Scores returns custom weights or the defaults.
func (c *Config) Scores() api.Scores {
	if c.Scoring.Weights != nil {
		return *c.Scoring.Weights
	}
	return api.DefaultScores
}
