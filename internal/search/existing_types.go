package search

import "github.com/cplieger/subflux/internal/api"

// Subtitle source identifiers for coverage tracking.
const sourceExternal = api.SourceExternal

// --- Subtitle types ---

// existingSubs describes what subtitles already exist for a video file.
type existingSubs struct {
	// IgnoredCodecs is the set of embedded codecs that should be treated
	// as "present but not usable" (e.g. PGS, VobSub when the user wants
	// text-based subs). hasSubtitle returns false for these so the search
	// engine will look for alternatives online.
	IgnoredCodecs map[string]bool

	// Embedded subtitle tracks with full variant info from native parsing.
	Embedded []embeddedSub

	// External subtitle files found on disk, with variant info.
	External []externalSub
}

// embeddedSub represents an embedded subtitle track detected via native parsing.
type embeddedSub struct {
	Lang   string
	Codec  string
	HI     bool
	Forced bool
}

// externalSub represents an external subtitle file found on disk.
type externalSub struct {
	Path   string
	Lang   string
	HI     bool
	Forced bool
}

// --- Subtitle lookup methods ---

// hasSubtitle checks if a usable subtitle exists for the given target.
// Embedded tracks with codecs in IgnoredCodecs are skipped.
func (e *existingSubs) hasSubtitle(lang string, variant api.Variant) bool {
	if e.hasExternalSubtitle(lang, variant) {
		return true
	}
	for _, emb := range e.Embedded {
		if emb.Lang != lang || !matchesVariant(emb.HI, emb.Forced, variant) {
			continue
		}
		if e.IgnoredCodecs[emb.Codec] {
			continue
		}
		return true
	}
	return false
}

// hasExternalSubtitle checks if an external subtitle file exists for the target.
func (e *existingSubs) hasExternalSubtitle(lang string, variant api.Variant) bool {
	for _, ext := range e.External {
		if ext.Lang == lang && matchesVariant(ext.HI, ext.Forced, variant) {
			return true
		}
	}
	return false
}

// --- Variant matching ---

// matchesVariant reports whether the hi/forced flags match the requested variant.
// Recognized variants: "hi", "forced", and "standard" (or empty) means
// a regular subtitle (neither HI nor forced).
func matchesVariant(hi, forced bool, variant api.Variant) bool {
	switch variant {
	case api.VariantHI:
		return hi
	case api.VariantForced:
		return forced
	case api.VariantStandard, "":
		return !hi && !forced
	default:
		return !hi && !forced
	}
}
