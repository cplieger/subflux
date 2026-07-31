package scoring

import (
	"regexp"
	"strings"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/epmarker"
)

// AnyTitleMatches reports whether candidate matches the primary title or any
// alternative title in the request after normalization.
func AnyTitleMatches(req *api.SearchRequest, candidate string) bool {
	if TitlesMatch(req.Title, candidate) {
		return true
	}
	for _, alt := range req.AlternativeTitles {
		if TitlesMatch(alt, candidate) {
			return true
		}
	}
	return false
}

// AnyReleaseNameMatches reports whether releaseName contains any of the request's
// titles (primary or alternatives) after stripping release group tags.
func AnyReleaseNameMatches(req *api.SearchRequest, releaseName string) bool {
	cleaned := groupTagRe.ReplaceAllString(releaseName, "")
	normalizedCleaned := NormalizeTitle(cleaned)

	if releaseNameMatchesTitleWith(req.Title, releaseName, normalizedCleaned) {
		return true
	}
	for _, alt := range req.AlternativeTitles {
		if releaseNameMatchesTitleWith(alt, releaseName, normalizedCleaned) {
			return true
		}
	}
	return false
}

// EpisodeNumberMatch reports whether the subtitle's season/episode pair matches
// the request, including scene and absolute episode numbering alternatives.
func EpisodeNumberMatch(subSeason, subEpisode int, req *api.SearchRequest) bool {
	if matchesPair(subSeason, subEpisode, req.Season, req.Episode) {
		return true
	}
	if req.SceneEpisode > 0 {
		s := req.SceneSeason
		if s <= 0 {
			s = req.Season
		}
		if matchesPair(subSeason, subEpisode, s, req.SceneEpisode) {
			return true
		}
	}
	if req.AbsoluteEpisode > 0 && req.Season != 0 {
		s := req.SceneSeason
		if s <= 0 {
			s = 1
		}
		if matchesPair(subSeason, subEpisode, s, req.AbsoluteEpisode) {
			return true
		}
	}
	return false
}

func matchesPair(subSeason, subEpisode, candSeason, candEpisode int) bool {
	seasonOK := subSeason <= 0 || candSeason <= 0 || subSeason == candSeason
	episodeOK := subEpisode <= 0 || candEpisode <= 0 || subEpisode == candEpisode
	return seasonOK && episodeOK
}

// ExtractReleaseSeason extracts the season number from a release name via the
// S##E## or S## pattern. Returns 0 if no season marker is found.
func ExtractReleaseSeason(releaseName string) int {
	n, ok := epmarker.Season(releaseName)
	if !ok {
		return 0
	}
	return n
}

// releaseNameMatchesTitleWith reports whether the release name's title portion
// matches reqTitle. normalizedCleaned is the caller-hoisted
// NormalizeTitle(release name with group tags stripped): AnyReleaseNameMatches
// computes it once and reuses it across the primary and alternative titles.
func releaseNameMatchesTitleWith(reqTitle, releaseName, normalizedCleaned string) bool {
	if idx := epmarker.FirstIndex(releaseName); idx >= 0 {
		return TitlesMatch(reqTitle, releaseName[:idx])
	}
	a := NormalizeTitle(reqTitle)
	b := normalizedCleaned
	if a == "" || b == "" {
		return true
	}
	idx := strings.Index(b, a)
	if idx < 0 {
		return false
	}
	if idx > 0 && b[idx-1] != ' ' {
		return false
	}
	end := idx + len(a)
	if end >= len(b) {
		return true
	}
	if b[end] != ' ' {
		return true
	}
	rest := strings.TrimSpace(b[end:])
	if rest == "" {
		return true
	}
	first, _, _ := strings.Cut(rest, " ")
	return !sequelIndicators[first]
}

var groupTagRe = regexp.MustCompile(`\[[^\]]*\]`)

var sequelIndicators = map[string]bool{
	"z": true, "gt": true, "super": true, "kai": true,
	"ii": true, "iii": true, "iv": true, "v": true,
	"zero": true, "next": true, "go": true,
}

// TitlesMatch compares two titles after normalization.
func TitlesMatch(requested, candidate string) bool {
	a := NormalizeTitle(requested)
	b := NormalizeTitle(candidate)
	if a == "" || b == "" {
		return true
	}
	return a == b
}

var titleReplacer = strings.NewReplacer(".", " ", "-", " ", "_", " ", ":", " ")

// NormalizeTitle lowercases and replaces common separators with spaces.
func NormalizeTitle(s string) string {
	s = strings.ToLower(s)
	s = titleReplacer.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// IsSeasonPack returns true if the release name looks like a season pack:
// it carries a readable season marker and no episode marker at all.
func IsSeasonPack(releaseName string) bool {
	if _, ok := epmarker.Season(releaseName); !ok {
		return false
	}
	return !epmarker.Present(releaseName)
}
