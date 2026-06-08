package scoring

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/api"
)

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

var (
	SeasonEpRe   = regexp.MustCompile(`(?i)S\d{1,2}E\d{1,3}`)
	SeasonOnlyRe = regexp.MustCompile(`(?i)S(\d{1,2})(?:E\d{1,3})?`)
)

func ExtractReleaseSeason(releaseName string) int {
	m := SeasonOnlyRe.FindStringSubmatch(releaseName)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// ReleaseNameMatchesTitle checks if a release name's title portion matches
// the requested title.
func ReleaseNameMatchesTitle(reqTitle, releaseName string) bool {
	cleaned := groupTagRe.ReplaceAllString(releaseName, "")
	return releaseNameMatchesTitleWith(reqTitle, releaseName, NormalizeTitle(cleaned))
}

func releaseNameMatchesTitleWith(reqTitle, releaseName, normalizedCleaned string) bool {
	loc := SeasonEpRe.FindStringIndex(releaseName)
	if loc != nil {
		return TitlesMatch(reqTitle, releaseName[:loc[0]])
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

// IsSeasonPack returns true if the release name looks like a season pack.
func IsSeasonPack(releaseName string) bool {
	return SeasonOnlyRe.MatchString(releaseName) && !SeasonEpRe.MatchString(releaseName)
}
