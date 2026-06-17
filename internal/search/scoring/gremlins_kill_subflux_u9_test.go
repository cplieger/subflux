package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// Tests in this file kill surviving gremlins mutation-testing mutants in
// internal/search/scoring/identity_match.go (the bulk) plus
// identity_filter.go:78 (unit subflux-u9). Every identifier defined here is
// prefixed gk_subflux_u9_ so it never collides with sibling unit subflux-u8,
// which shares package scoring.

// --- matchesPair: season wildcard / equality (identity_match.go:64) ---
//
// Kills 64:24 (BOUNDARY + NEGATION on subSeason<=0), 64:43 (BOUNDARY +
// NEGATION on candSeason<=0) and 64:61 (NEGATION on subSeason==candSeason).
// The boundary rows feed a 0 season so <=0 (true) differs from the mutated
// <0 / >0 (false); the equality rows use two positive seasons so the == match
// differs from the mutated !=. In every row episodeOK is a wildcard (true) so
// the return reflects seasonOK alone.
func Test_gk_subflux_u9_matchesPair_seasonLogic(t *testing.T) {
	tests := []struct {
		name       string
		subSeason  int
		subEpisode int
		candSeason int
		candEpis   int
		want       bool
	}{
		// subSeason==0 is a wildcard: original <=0 true; mutated <0 / >0 false.
		{"sub season zero is wildcard", 0, 0, 5, 0, true},
		// candSeason==0 is a wildcard: original <=0 true; mutated <0 / >0 false.
		{"cand season zero is wildcard", 5, 0, 0, 0, true},
		// equal positive seasons: original == matches; mutated != drops.
		{"equal positive seasons match", 3, 0, 3, 0, true},
		// different positive seasons: original == drops; mutated != matches.
		{"different positive seasons drop", 3, 0, 4, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesPair(tc.subSeason, tc.subEpisode, tc.candSeason, tc.candEpis)
			if got != tc.want {
				t.Errorf("matchesPair(%d,%d,%d,%d) = %v, want %v",
					tc.subSeason, tc.subEpisode, tc.candSeason, tc.candEpis, got, tc.want)
			}
		})
	}
}

// --- matchesPair: episode wildcard / equality (identity_match.go:65) ---
//
// Kills 65:26 (BOUNDARY + NEGATION on subEpisode<=0), 65:46 (BOUNDARY +
// NEGATION on candEpisode<=0) and 65:65 (NEGATION on subEpisode==candEpisode).
// Mirror of the season test: boundary rows feed a 0 episode; equality rows use
// two positive episodes. seasonOK is a wildcard (true) in every row.
func Test_gk_subflux_u9_matchesPair_episodeLogic(t *testing.T) {
	tests := []struct {
		name       string
		subSeason  int
		subEpisode int
		candSeason int
		candEpis   int
		want       bool
	}{
		// subEpisode==0 is a wildcard: original <=0 true; mutated <0 / >0 false.
		{"sub episode zero is wildcard", 0, 0, 0, 5, true},
		// candEpisode==0 is a wildcard: original <=0 true; mutated <0 / >0 false.
		{"cand episode zero is wildcard", 0, 5, 0, 0, true},
		// equal positive episodes: original == matches; mutated != drops.
		{"equal positive episodes match", 0, 4, 0, 4, true},
		// different positive episodes: original == drops; mutated != matches.
		{"different positive episodes drop", 0, 4, 0, 6, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesPair(tc.subSeason, tc.subEpisode, tc.candSeason, tc.candEpis)
			if got != tc.want {
				t.Errorf("matchesPair(%d,%d,%d,%d) = %v, want %v",
					tc.subSeason, tc.subEpisode, tc.candSeason, tc.candEpis, got, tc.want)
			}
		})
	}
}

// --- EpisodeNumberMatch: scene-numbering branch guard (identity_match.go:42) ---
//
// Kills 42:22 (NEGATION + BOUNDARY on req.SceneEpisode > 0). The aired pair
// deliberately mismatches (episode 5 vs 99) so the result depends on the scene
// branch. With SceneEpisode=5 the branch fires and the scene pair matches
// (true); NEGATION (<=0) would skip it -> false. With SceneEpisode=0 the branch
// must NOT fire (false); BOUNDARY (>=0) would enter it and the 0 candidate
// episode acts as a wildcard -> true.
func Test_gk_subflux_u9_EpisodeNumberMatch_sceneBranchGuard(t *testing.T) {
	tests := []struct {
		name string
		req  api.SearchRequest
		want bool
	}{
		{
			name: "scene episode positive matches when aired differs",
			req:  api.SearchRequest{Season: 1, Episode: 99, SceneSeason: 1, SceneEpisode: 5},
			want: true,
		},
		{
			name: "scene episode zero does not enter scene branch",
			req:  api.SearchRequest{Season: 1, Episode: 99, SceneSeason: 1, SceneEpisode: 0},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EpisodeNumberMatch(1, 5, &tc.req)
			if got != tc.want {
				t.Errorf("EpisodeNumberMatch(1,5,%+v) = %v, want %v", tc.req, got, tc.want)
			}
		})
	}
}

// --- EpisodeNumberMatch: absolute-numbering branch guard (identity_match.go:51) ---
//
// Kills 51:25 (NEGATION + BOUNDARY on req.AbsoluteEpisode > 0) and 51:43
// (NEGATION on req.Season != 0). The aired pair mismatches (episode 10 vs 99)
// so the result depends on the absolute branch (entered only when
// AbsoluteEpisode>0 AND Season!=0). With AbsoluteEpisode=10/Season=1 the branch
// fires and the absolute pair matches (true); AbsoluteEpisode<=0 or Season==0
// would skip it -> false. With AbsoluteEpisode=0 the branch must NOT fire
// (false); BOUNDARY (>=0) would enter it and the 0 candidate episode is a
// wildcard -> true.
func Test_gk_subflux_u9_EpisodeNumberMatch_absoluteBranchGuard(t *testing.T) {
	tests := []struct {
		name string
		req  api.SearchRequest
		want bool
	}{
		{
			name: "absolute episode positive matches when aired differs",
			req:  api.SearchRequest{Season: 1, Episode: 99, AbsoluteEpisode: 10},
			want: true,
		},
		{
			name: "absolute episode zero does not enter absolute branch",
			req:  api.SearchRequest{Season: 1, Episode: 99, AbsoluteEpisode: 0},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EpisodeNumberMatch(1, 10, &tc.req)
			if got != tc.want {
				t.Errorf("EpisodeNumberMatch(1,10,%+v) = %v, want %v", tc.req, got, tc.want)
			}
		})
	}
}

// --- ExtractReleaseSeason: parse-error guard (identity_match.go:80) ---
//
// Kills 80:9 (NEGATION on err != nil). The capture group is always 1-2 ASCII
// digits so strconv.Atoi never errors; the original `if err != nil` never
// fires and the parsed number is returned. Negating it to `err == nil` makes
// the guard fire on every successful parse and return 0 instead. A concrete
// season number therefore differs (5 vs 0, 12 vs 0).
func Test_gk_subflux_u9_ExtractReleaseSeason_returnsParsedNumber(t *testing.T) {
	tests := []struct {
		name    string
		release string
		want    int
	}{
		{"season only", "Show.S05.1080p", 5},
		{"season with episode", "Series.S12E03.720p", 12},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractReleaseSeason(tc.release)
			if got != tc.want {
				t.Errorf("ExtractReleaseSeason(%q) = %d, want %d", tc.release, got, tc.want)
			}
		})
	}
}

// --- releaseNameMatchesTitleWith: empty-operand short circuit (identity_match.go:100) ---
//
// Kills 100:7 (NEGATION on a == "") and 100:18 (NEGATION on b == ""). The
// guard `if a == "" || b == ""` returns true when either normalized title is
// empty. The release-name arg carries no SxxExx so the early SeasonEp path is
// skipped and the normalized comparison runs.
//   - empty b: original b=="" true -> returns true; mutated b!="" makes the
//     guard false and the absent title -> false.
//   - non-empty a not found in b: original guard false -> proceeds and the
//     missing substring -> false; mutated a!="" makes the guard true -> true.
func Test_gk_subflux_u9_releaseNameMatchesTitleWith_emptyGuard(t *testing.T) {
	tests := []struct {
		name       string
		reqTitle   string
		release    string
		normalized string
		want       bool
	}{
		// b == "" branch: empty normalized release -> true.
		{"empty normalized release returns true", "batman", "batman", "", true},
		// a != "" / b != "" both non-empty, title absent -> proceeds to false.
		{"present title absent from release returns false", "zzz", "hello world", "hello world", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := releaseNameMatchesTitleWith(tc.reqTitle, tc.release, tc.normalized)
			if got != tc.want {
				t.Errorf("releaseNameMatchesTitleWith(%q,%q,%q) = %v, want %v",
					tc.reqTitle, tc.release, tc.normalized, got, tc.want)
			}
		})
	}
}

// --- releaseNameMatchesTitleWith: substring-found guard (identity_match.go:104) ---
//
// Kills 104:9 (BOUNDARY + NEGATION on idx < 0). idx is strings.Index(b, a).
//   - prefix match (idx==0): original `idx < 0` false -> proceeds and the
//     match is a valid word-prefix -> true; BOUNDARY `idx <= 0` would treat the
//     valid match as not-found -> false.
//   - title absent (idx==-1): original `idx < 0` true -> false; NEGATION
//     `idx >= 0` is false for -1 -> proceeds and ends up true.
func Test_gk_subflux_u9_releaseNameMatchesTitleWith_indexGuard(t *testing.T) {
	tests := []struct {
		name       string
		reqTitle   string
		release    string
		normalized string
		want       bool
	}{
		// idx == 0 (prefix). <0 false (proceed, true); <=0 would be true (false).
		{"prefix match kept", "batman", "batman begins", "batman begins", true},
		// idx == -1 (absent). <0 true (false); >=0 false (proceed, true).
		{"absent title rejected", "z", "abc", "abc", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := releaseNameMatchesTitleWith(tc.reqTitle, tc.release, tc.normalized)
			if got != tc.want {
				t.Errorf("releaseNameMatchesTitleWith(%q,%q,%q) = %v, want %v",
					tc.reqTitle, tc.release, tc.normalized, got, tc.want)
			}
		})
	}
}

// --- releaseNameMatchesTitleWith: char-before-match boundary (identity_match.go:107) ---
//
// Kills 107:21 (ARITHMETIC_BASE and INVERT_NEGATIVES on idx-1) and 107:25
// (NEGATION on b[idx-1] != ' '). The check `idx > 0 && b[idx-1] != ' '` rejects
// a substring match not preceded by a space.
//   - "x ab" (idx==2, char-before is space): original keeps it -> true. Reading
//     b[idx+1] (the arithmetic / invert-negatives mutation, char 'b') makes the
//     != ' ' true -> false. Flipping != to == also makes the space match the
//     condition -> false. So want=true pins all three at line 107.
//   - "xab" (idx==1, char-before 'x'): original rejects it -> false. Flipping
//     != to == lets it through -> true, so want=false pins 107:25 from the
//     other side.
func Test_gk_subflux_u9_releaseNameMatchesTitleWith_charBeforeMatch(t *testing.T) {
	tests := []struct {
		name       string
		reqTitle   string
		release    string
		normalized string
		want       bool
	}{
		// space before match: original keeps; reading idx+1 or flipping != drops.
		{"space before match keeps it", "ab", "x ab", "x ab", true},
		// non-space before match: original drops; flipping != keeps.
		{"non-space before match drops it", "ab", "xab", "xab", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := releaseNameMatchesTitleWith(tc.reqTitle, tc.release, tc.normalized)
			if got != tc.want {
				t.Errorf("releaseNameMatchesTitleWith(%q,%q,%q) = %v, want %v",
					tc.reqTitle, tc.release, tc.normalized, got, tc.want)
			}
		})
	}
}

// --- releaseNameMatchesTitleWith: end-of-string guard (identity_match.go:111) ---
//
// Kills 111:9 (BOUNDARY + NEGATION on end >= len(b)).
//   - "the batman" (match ends exactly at len(b)==10): original `end >= len(b)`
//     true -> returns true before indexing b[end]. Both BOUNDARY (end > len(b))
//     and NEGATION (end < len(b)) make the guard false at the boundary, so the
//     mutant falls through to b[end] (an out-of-range index) and panics, which
//     fails the test -> killed.
//   - "dragon ball z" (match ends before len(b), trailing sequel indicator "z"):
//     original `end >= len(b)` false -> proceeds and the sequel suffix -> false.
//     NEGATION `end < len(b)` true -> returns true. So want=false is a clean
//     (non-panic) kill of 111:9 NEGATION.
func Test_gk_subflux_u9_releaseNameMatchesTitleWith_endOfString(t *testing.T) {
	tests := []struct {
		name       string
		reqTitle   string
		release    string
		normalized string
		want       bool
	}{
		// match ends exactly at end-of-string: original true; > or < mutants
		// fall through to an out-of-range b[end] and panic (test fails -> kill).
		{"match exactly at end kept", "batman", "the batman", "the batman", true},
		// match before end with a sequel-indicator suffix: original false;
		// `end < len(b)` mutation short-circuits to true.
		{"sequel indicator suffix rejected", "dragon ball", "dragon ball z", "dragon ball z", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := releaseNameMatchesTitleWith(tc.reqTitle, tc.release, tc.normalized)
			if got != tc.want {
				t.Errorf("releaseNameMatchesTitleWith(%q,%q,%q) = %v, want %v",
					tc.reqTitle, tc.release, tc.normalized, got, tc.want)
			}
		})
	}
}

// --- IdentityTitleOK: req.Title guard on the release-name fallback
// (identity_filter.go:78) ---
//
// Kills 78:38 (NEGATION on req.Title != ""). A title-matched sub with an empty
// Title and a non-matching ReleaseName reaches the release-name fallback, which
// fires (returns false) only because every operand of the AND chain is true,
// including req.Title != "". Negating req.Title != "" to req.Title == ""
// collapses the AND chain to false, the fallback is skipped, and the sub is
// kept (true). Distinct from the sibling 78:19 mutant (sub.ReleaseName != "")
// owned by unit subflux-u8.
func Test_gk_subflux_u9_IdentityTitleOK_reqTitleGuardsReleaseFallback(t *testing.T) {
	sub := api.Subtitle{
		MatchedBy:   api.MatchByTitle,
		Title:       "",
		ReleaseName: "Totally.Different.Show.2020.1080p",
	}
	req := api.SearchRequest{Title: "Breaking Bad"}

	got := IdentityTitleOK(&sub, &req)

	if got != false {
		t.Errorf("IdentityTitleOK(title-matched, empty title, non-matching release, req.Title set) = %v, want false", got)
	}
}
