package search

import "github.com/cplieger/subflux/internal/subflux"

const sourceExternal = subflux.SourceExternal

// existingSubs describes what subtitles already exist for a video file.
type existingSubs struct {
	// IgnoredCodecs is the set of embedded codecs treated as "present but
	// not usable" (e.g. PGS, VobSub when the user wants text-based subs).
	// hasSubtitle returns false for these so the engine looks for
	// alternatives online.
	IgnoredCodecs map[string]bool

	Embedded []embeddedSub
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

// hasSubtitle checks if a usable subtitle exists for the given target.
// Embedded tracks with codecs in IgnoredCodecs are skipped.
func (e *existingSubs) hasSubtitle(lang string, variant subflux.Variant) bool {
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

func (e *existingSubs) hasExternalSubtitle(lang string, variant subflux.Variant) bool {
	for _, ext := range e.External {
		if ext.Lang == lang && matchesVariant(ext.HI, ext.Forced, variant) {
			return true
		}
	}
	return false
}

// matchesVariant reports whether the hi/forced flags match the requested
// variant. Recognized variants: "hi", "forced", and "standard" (or empty)
// means a regular subtitle (neither HI nor forced).
func matchesVariant(hi, forced bool, variant subflux.Variant) bool {
	switch variant {
	case subflux.VariantHI:
		return hi
	case subflux.VariantForced:
		return forced
	case subflux.VariantStandard, "":
		return !hi && !forced
	default:
		return !hi && !forced
	}
}
