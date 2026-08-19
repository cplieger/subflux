package arrsvc

import (
	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/langcode"
)

// The Sonarr/Radarr DTOs are consumed directly as github.com/cplieger/arrapi
// types throughout subflux; there are no api.* aliases for them. This file
// keeps the subtitle-app helpers that read those arr types, beside the client
// that fetches them. arrapi's types are not subflux-local, so these are free
// functions rather than methods.

// OriginalLangCode returns the ISO 639-1 code for an arr original-language
// reference, or "" when the reference is nil or the name is unmapped.
func OriginalLangCode(lang *arrapi.Language) string {
	if lang == nil {
		return ""
	}
	return langcode.FromName(lang.Name)
}

// AudioLanguages parses a file's MediaInfo audio-languages string into
// deduplicated ISO 639-1 codes. Returns nil when mi is nil or empty.
func AudioLanguages(mi *arrapi.MediaInfo) []string {
	if mi == nil || mi.AudioLanguages == "" {
		return nil
	}
	return langcode.ParseAudioList(mi.AudioLanguages)
}

// SeasonEpisodeFileCount returns the number of episode files for a specific
// season, using Sonarr's per-season statistics. Returns 0 if the season is not
// found or statistics are unavailable.
func SeasonEpisodeFileCount(s *arrapi.Series, seasonNum int) int {
	if s == nil {
		return 0
	}
	for i := range s.Seasons {
		if s.Seasons[i].SeasonNumber == seasonNum && s.Seasons[i].Statistics != nil {
			return s.Seasons[i].Statistics.EpisodeFileCount
		}
	}
	return 0
}

// HasExcludeTag reports whether any of the item's tags are in the exclude set.
func HasExcludeTag(tags []int, excludeIDs map[int]struct{}) bool {
	for _, id := range tags {
		if _, ok := excludeIDs[id]; ok {
			return true
		}
	}
	return false
}
