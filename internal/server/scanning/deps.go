package scanning

import (
	"github.com/cplieger/subflux/internal/api"
)

// The configuration surfaces this package consumes live here rather than beside
// their consuming structs, because there are two of them and they are two
// widths of one concern: an interface goes next to its consumer when a package
// has exactly one, and into this file when the package has several to compare.
//
// A scan pass reads 4 of the 37 values the configuration offers, and the HTTP
// preflight in front of it reads 2 of those 4. Neither reads a scoring weight,
// a provider setting, an auth switch or the server runtime — a scan asks the
// config what to look for and how fast, and asks the engine for everything
// else. ValidatePath is absent for the same reason: a scan resolves its paths
// from the arr and the store, and containment belongs to the packages that hand
// a path to a client or write one to disk.
//
// ScanCfg composes scanHandlerCfg by embedding rather than re-listing the two
// shared methods, so the preflight's surface cannot silently widen to the
// scan's.

// scanHandlerCfg is what the scan HTTP handlers read before any scan starts:
// the pacing config for the per-item delay, and the language targets used to
// answer the preflight. 2 of the 37 values the config offers — these handlers
// resolve the arr item, decide the status code and hand off.
type scanHandlerCfg interface {
	Search() api.SearchConfig
	ResolveTargetsWithFallback(originalLang string, audioLangs []string) []api.SubtitleTarget
}

// ScanCfg is what a scan pass reads: the preflight's two plus the configured
// language list the search request carries, and the adaptive-backoff settings
// the pass seeds its season tracker from. 4 of the 37 values the config offers.
//
// Exported because the scheduler names it: the scheduler reads Search() for its
// own interval and carries the same value straight into LiveState, so a second
// declaration of these methods there could only drift — the reason its Engine
// field already names ScanEngine.
type ScanCfg interface {
	scanHandlerCfg
	LanguageCodes() []string
	Adaptive() api.AdaptiveConfig
}
