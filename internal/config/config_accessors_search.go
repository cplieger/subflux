package config

import "github.com/cplieger/subflux/internal/api"

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

// ProviderConfigs returns the provider configuration map.
func (c *Config) ProviderConfigs() map[api.ProviderID]api.ProviderCfg {
	if c.cachedProviderConfigs != nil {
		return c.cachedProviderConfigs
	}
	// Fallback for configs not loaded via LoadFromBytes (e.g. tests).
	out := make(map[api.ProviderID]api.ProviderCfg, len(c.Providers))
	for k, v := range c.Providers {
		out[k] = api.ProviderCfg{Settings: v.Settings, Enabled: v.Enabled, Priority: v.Priority}
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
