package config

import (
	"maps"

	"github.com/cplieger/subflux/internal/subflux"
)

// Adaptive returns the adaptive search config.
func (c *Config) Adaptive() subflux.AdaptiveConfig {
	return subflux.AdaptiveConfig{
		Enabled:           c.AdaptiveCfg.Enabled,
		InitialDelay:      c.AdaptiveCfg.InitialDelay.D,
		MaxDelay:          c.AdaptiveCfg.MaxDelay.D,
		BackoffMultiplier: c.AdaptiveCfg.BackoffMultiplier,
		MaxAttempts:       c.AdaptiveCfg.MaxAttempts,
	}
}

// Search returns the search config.
func (c *Config) Search() subflux.SearchConfig {
	return subflux.SearchConfig{
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

// PostProcess returns the post-processing configuration.
func (c *Config) PostProcess() subflux.PostProcessConfig {
	return subflux.PostProcessConfig{
		StripHI:          c.PostProcessing.StripHI,
		StripTags:        c.PostProcessing.StripTags,
		NormalizeUTF8:    c.PostProcessing.NormalizeUTF8,
		CleanWhitespace:  c.PostProcessing.CleanWhitespace,
		NormalizeEndings: c.PostProcessing.NormalizeEndings,
		RemoveEmpty:      c.PostProcessing.RemoveEmpty,
	}
}

// Sync returns the sync configuration.
func (c *Config) Sync() subflux.SyncConfig {
	sc := subflux.SyncConfig{
		SyncSubtitles:     c.PostProcessing.SyncSubtitles,
		AudioSyncFallback: c.PostProcessing.AudioSyncFallback,
		SyncMinConfidence: c.PostProcessing.SyncMinConfidence,
	}
	if sc.SyncMinConfidence == 0 {
		sc.SyncMinConfidence = subflux.DefaultSyncMinConfidence
	}
	return sc
}

// EmbeddedPolicy returns the typed embedded subtitle codec policy from the
// top-level embedded_subtitles section. This is the single config source for
// the search engine's ignored-codec decision (the detector takes no settings
// and always returns every track).
func (c *Config) EmbeddedPolicy() subflux.EmbeddedPolicy {
	return subflux.EmbeddedPolicy{
		IgnorePGS:    c.EmbeddedSubtitles.IgnorePGS,
		IgnoreVobSub: c.EmbeddedSubtitles.IgnoreVobSub,
		IgnoreASS:    c.EmbeddedSubtitles.IgnoreASS,
	}
}

// Providers returns the resolved provider configuration map. The map, and every
// Settings map inside it, is a copy: a caller that adds a provider or edits a
// setting is editing its own copy, not the live configuration every subsequent
// scan reads. The copy is deep because a shallow one would change nothing —
// the aliasing that matters is the inner Settings map, which both the cached
// and the recomputed path used to hand out directly.
//
// Named Providers, not ProviderConfigs: the raw YAML field beside it is
// ProvidersCfg, which is the same split SonarrCfg/Sonarr() and RadarrCfg/
// Radarr() already use in this package — the Cfg suffix marks the unparsed
// decode target, and the bare name is the resolved answer callers want. The
// two also differ in TYPE (yamlProviderCfg vs subflux.ProviderCfg), so the field
// was the one claiming a name it could not honour.
func (c *Config) Providers() map[subflux.ProviderID]subflux.ProviderCfg {
	src := c.cachedProviders
	if src == nil {
		// Fallback for configs not loaded via LoadFromBytes (e.g. tests).
		src = make(map[subflux.ProviderID]subflux.ProviderCfg, len(c.ProvidersCfg))
		for k, v := range c.ProvidersCfg {
			src[k] = subflux.ProviderCfg{Settings: v.Settings, Enabled: v.Enabled, Priority: v.Priority}
		}
	}
	out := make(map[subflux.ProviderID]subflux.ProviderCfg, len(src))
	for k, v := range src {
		v.Settings = maps.Clone(v.Settings)
		out[k] = v
	}
	return out
}

// ProviderPriority returns the priority for a provider (lower = higher trust).
// Returns subflux.DefaultProviderPriority for unconfigured or zero-priority providers.
func (c *Config) ProviderPriority(name subflux.ProviderID) int {
	if p, ok := c.ProvidersCfg[name]; ok && p.Priority > 0 {
		return p.Priority
	}
	return subflux.DefaultProviderPriority
}

// Scores returns custom weights or the defaults.
func (c *Config) Scores() subflux.Scores {
	if c.Scoring.Weights != nil {
		return *c.Scoring.Weights
	}
	return subflux.DefaultScores
}
