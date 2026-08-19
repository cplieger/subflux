package search

import "github.com/cplieger/subflux/internal/api"

// SearchCfg is what the scan engine reads out of the configuration: the scoring
// weights and floors it ranks candidates by, the provider allow-list and
// priority order it queries in, the concurrency and retry limits it paces
// itself with, and the sync/post-process settings it applies to a download.
// 9 of the 28 values the configuration offers — the widest consumer surface in
// the app, and still short of the whole by the auth, logging, server-runtime,
// media-path and UI halves a search never reads.
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
	// Sync returns subtitle sync/timing configuration.
	Sync() api.SyncConfig
	// PostProcess returns post-processing settings.
	PostProcess() api.PostProcessConfig
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
