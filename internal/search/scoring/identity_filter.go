package scoring

import "github.com/cplieger/subflux/internal/api"

// FilterByIdentity drops results that don't match the requested
// season/episode or show title. Returns the kept results and the
// number of dropped results so the caller can log as appropriate.
func FilterByIdentity(results []api.Subtitle, req *api.SearchRequest) (kept []api.Subtitle, dropped int) {
	if req.Title == "" && req.Season == 0 && req.Episode == 0 {
		return results, 0
	}

	for i := range results {
		if IdentityOK(&results[i], req) {
			kept = append(kept, results[i])
		} else {
			dropped++
		}
	}

	return kept, dropped
}

func IdentityOK(sub *api.Subtitle, req *api.SearchRequest) bool {
	if sub.MatchedBy == api.MatchByHash {
		return true
	}

	if sub.Season > 0 || sub.Episode > 0 {
		if req.MediaType == api.MediaTypeMovie {
			return false
		}
		if !EpisodeNumberMatch(sub.Season, sub.Episode, req) {
			return false
		}
		return IdentityTitleOK(sub, req)
	}

	return validateNoMetadata(sub, req)
}

func validateNoMetadata(sub *api.Subtitle, req *api.SearchRequest) bool {
	if sub.Title != "" && req.Title != "" &&
		!AnyTitleMatches(req, sub.Title) {
		return false
	}

	if sub.ReleaseName != "" && req.Title != "" &&
		!AnyReleaseNameMatches(req, sub.ReleaseName) {
		return false
	}

	if req.MediaType == api.MediaTypeEpisode && req.Season > 0 &&
		sub.ReleaseName != "" {
		if relSeason := ExtractReleaseSeason(sub.ReleaseName); relSeason > 0 {
			if relSeason != req.Season {
				return false
			}
		}
	}

	return true
}

func IdentityTitleOK(sub *api.Subtitle, req *api.SearchRequest) bool {
	if sub.MatchedBy != "" && sub.MatchedBy != api.MatchByTitle {
		return true
	}

	if sub.Title != "" && req.Title != "" {
		episodeMatch := req.EpisodeTitle != "" && TitlesMatch(req.EpisodeTitle, sub.Title)
		if !episodeMatch && !AnyTitleMatches(req, sub.Title) {
			return false
		}
	}

	if sub.MatchedBy == api.MatchByTitle && sub.Title == "" &&
		sub.ReleaseName != "" && req.Title != "" &&
		!AnyReleaseNameMatches(req, sub.ReleaseName) {
		return false
	}

	return true
}
