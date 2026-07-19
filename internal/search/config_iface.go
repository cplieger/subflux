package search

import "github.com/cplieger/subflux/internal/api"

// SearchCfg is the narrow configuration interface consumed by the search engine.
// Only the methods actually called by search are declared here.
// The concrete config.Config satisfies this via structural typing.
//
//nolint:revive // name is established API; renaming would break consumers
type SearchCfg interface {
	// Scores returns the scoring weights. Must return non-zero values;
	// zero scores disable all attribute matching.
	Scores() api.Scores
	// Search returns the top-level search configuration (concurrency, etc.).
	Search() api.SearchConfig
	// Adaptive returns the adaptive search configuration.
	Adaptive() api.AdaptiveConfig
	// SyncConfig returns subtitle sync/timing configuration.
	SyncConfig() api.SyncConfig
	// PostProcessConfig returns post-processing settings.
	PostProcessConfig() api.PostProcessConfig
	// ProvidersForTarget returns provider names allowed for this target.
	// Empty slice means no providers will be searched for this target.
	ProvidersForTarget(t *api.SubtitleTarget, allProviders []api.ProviderID) []api.ProviderID
	// MinScoreForTarget returns the minimum acceptable score for a target.
	// Returns 0 to accept any score.
	MinScoreForTarget(t *api.SubtitleTarget, mediaType api.MediaType) int
	// ProviderPriority returns the priority of a provider by name.
	// Returns 0 for unknown providers (lowest priority = tried last in tiebreakers).
	ProviderPriority(name api.ProviderID) int
	// EmbeddedPolicy returns the typed embedded subtitle codec policy
	// (top-level embedded_subtitles config section).
	EmbeddedPolicy() api.EmbeddedPolicy
}
