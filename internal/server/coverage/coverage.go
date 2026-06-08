// Package coverage provides pure computation functions for subtitle coverage
// analysis. These functions have no dependency on the server package or HTTP
// handling — they operate solely on api types.
package coverage

import "github.com/cplieger/subflux/internal/api"

// Key identifies a subtitle by language and variant for coverage indexing.
type Key struct{ Lang, Variant string }

// Status tracks whether a media item has a usable subtitle or only
// ignored-codec embedded subs for a given language+variant.
type Status struct{ Usable, IgnoredOnly bool }

// TargetCoverage tracks coverage for a single expected subtitle target.
type TargetCoverage struct {
	Language    string `json:"language"`
	Variant     string `json:"variant"`
	Have        int    `json:"have"`
	HaveIgnored int    `json:"have_ignored"`
	Total       int    `json:"total"`
}

// RuleDefault is the rule name shown when no audio language rule matches.
const RuleDefault = "default"

// RuleNoTargets is the rule name shown when no subtitle targets are configured.
const RuleNoTargets = "no targets"

// IndexSubStatus builds a media_id → Key → Status index from subtitle
// file rows. A subtitle is "usable" if it's external or embedded with a
// non-ignored codec.
func IndexSubStatus(files []api.SubtitleFileRow, ignoredCodecs map[string]bool) map[string]map[Key]*Status {
	idx := make(map[string]map[Key]*Status, len(files))
	for i := range files {
		f := &files[i]
		k := Key{f.Language, f.Variant}
		if idx[f.MediaID] == nil {
			idx[f.MediaID] = make(map[Key]*Status, 4)
		}
		st := idx[f.MediaID][k]
		if st == nil {
			st = &Status{}
			idx[f.MediaID][k] = st
		}
		if f.Source == string(api.ProviderNameEmbedded) && ignoredCodecs[f.Codec] {
			if !st.Usable {
				st.IgnoredOnly = true
			}
		} else {
			st.Usable = true
			st.IgnoredOnly = false
		}
	}
	return idx
}

// ResolveRuleName returns the display rule name for coverage based on
// the audio language and resolved targets.
func ResolveRuleName(audioLang string, targets []api.SubtitleTarget) string {
	if len(targets) == 0 {
		return RuleNoTargets
	}
	if audioLang == "" {
		return RuleDefault
	}
	return audioLang
}

// ExtractSeriesPrefix extracts the series prefix from an episode media ID.
// Episode IDs are "{prefix}s{ss}e{ee}" (e.g. "tvdb-12345-s01e01").
// Returns the prefix including the trailing "-", or "" if the format is unrecognized.
func ExtractSeriesPrefix(epMediaID string) string {
	for i := len(epMediaID) - 1; i >= 1; i-- {
		if epMediaID[i-1] == '-' && epMediaID[i] == 's' {
			return epMediaID[:i]
		}
	}
	return ""
}

// CountEpisodeCoverageGrouped counts coverage from pre-grouped episode subtitle maps.
func CountEpisodeCoverageGrouped(
	episodes []map[Key]*Status,
	targets []api.SubtitleTarget,
	total int,
) []TargetCoverage {
	out := make([]TargetCoverage, 0, len(targets))
	for _, t := range targets {
		variant := t.EffectiveVariant()
		have, haveIgnored := 0, 0
		for _, subs := range episodes {
			if st := subs[Key{t.Code, string(variant)}]; st != nil {
				if st.Usable {
					have++
				} else if st.IgnoredOnly {
					haveIgnored++
				}
			}
		}
		out = append(out, TargetCoverage{
			Language:    t.Code,
			Variant:     string(variant),
			Have:        have,
			HaveIgnored: haveIgnored,
			Total:       total,
		})
	}
	return out
}

// CountMovieCoverage returns target coverage for a single movie.
func CountMovieCoverage(
	subs map[Key]*Status,
	targets []api.SubtitleTarget,
) []TargetCoverage {
	out := make([]TargetCoverage, 0, len(targets))
	for _, t := range targets {
		variant := t.EffectiveVariant()
		have, haveIgnored := 0, 0
		if st := subs[Key{t.Code, string(variant)}]; st != nil {
			if st.Usable {
				have = 1
			} else if st.IgnoredOnly {
				haveIgnored = 1
			}
		}
		out = append(out, TargetCoverage{
			Language:    t.Code,
			Variant:     string(variant),
			Have:        have,
			HaveIgnored: haveIgnored,
			Total:       1,
		})
	}
	return out
}

// DeduplicateFileRows collapses multiple rows with the same
// (media_id, language, variant, source) into one row for display.
func DeduplicateFileRows(rows []api.SubtitleFileRow) []api.SubtitleFileRow {
	type key struct{ mediaID, lang, variant, source string }
	seen := make(map[key]bool, len(rows))
	out := make([]api.SubtitleFileRow, 0, len(rows))
	for i := range rows {
		k := key{rows[i].MediaID, rows[i].Language, rows[i].Variant, rows[i].Source}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, rows[i])
	}
	return out
}
