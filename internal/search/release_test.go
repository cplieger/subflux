package search

import (
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search/release"
	"github.com/cplieger/subflux/internal/subflux"
	"pgregory.net/rapid"
)

// noPriority is a no-op provider priority function for tests.
func noPriority(_ subflux.ProviderID) int { return 99 }

func TestParseName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  release.Info
	}{
		{
			"empty string",
			"",
			release.Info{},
		},
		{
			"bluray source",
			"Movie.2024.BluRay.1080p.x264-GROUP",
			release.Info{
				Source: "bluray", VideoCodec: "h264", ReleaseGroup: "GROUP",
			},
		},
		{
			"web-dl source",
			"Show.S01E01.WEB-DL.720p.h265.AAC-Team",
			release.Info{
				Source: "webdl", VideoCodec: "h265",
				ReleaseGroup: "Team",
			},
		},
		{
			"webrip source",
			"Movie.2023.WEBRip.2160p.DTS-HD.MA-GRP",
			release.Info{
				Source:       "webrip",
				ReleaseGroup: "GRP",
			},
		},
		{
			"hdtv source",
			"Show.S02E05.HDTV.480p.XviD-LOL",
			release.Info{
				Source: "hdtv", VideoCodec: "xvid", ReleaseGroup: "LOL",
			},
		},
		{
			"4k uhd",
			"Movie.2024.UHD.BluRay.x265-GRP",
			release.Info{
				Source: "bluray", VideoCodec: "h265", ReleaseGroup: "GRP",
			},
		},
		{
			"streaming service AMZN",
			"Show.S01E01.AMZN.WEB-DL.1080p-GRP",
			release.Info{
				Source: "webdl", StreamingService: "AMZN", ReleaseGroup: "GRP",
			},
		},
		{
			"HDR10 detection",
			"Movie.2024.2160p.BluRay.HDR10.x265-GRP",
			release.Info{
				Source: "bluray", VideoCodec: "h265", HDR: "hdr10",
				ReleaseGroup: "GRP",
			},
		},
		{
			"Dolby Vision",
			"Movie.2024.2160p.WEB-DL.DV.x265-GRP",
			release.Info{
				Source: "webdl", VideoCodec: "h265", HDR: "dv",
				ReleaseGroup: "GRP",
			},
		},
		{
			"HDR10+ in release name",
			"Movie.2024.2160p.BluRay.HDR10+.x265-GRP",
			release.Info{
				Source: "bluray", VideoCodec: "h265", HDR: "hdr10+",
				ReleaseGroup: "GRP",
			},
		},
		{
			"edition directors cut",
			"Movie.2024.Directors.Cut.BluRay.1080p-GRP",
			release.Info{
				Source: "bluray", Edition: "directors.cut", ReleaseGroup: "GRP",
			},
		},
		{
			"streaming service NF",
			"Show.S01E01.NF.WEB-DL.1080p.EAC3-GRP",
			release.Info{
				Source: "webdl", StreamingService: "NF",
				ReleaseGroup: "GRP",
			},
		},
		{
			"truehd in name ignored",
			"Movie.2024.BluRay.2160p.TrueHD.x265-GRP",
			release.Info{
				Source: "bluray", VideoCodec: "h265",
				ReleaseGroup: "GRP",
			},
		},
		{
			"dvd source",
			"Movie.2024.DVDRip.XviD-GRP",
			release.Info{
				Source: "dvd", VideoCodec: "xvid",
				ReleaseGroup: "GRP",
			},
		},
		{
			"remux source",
			"Movie.2024.Remux.1080p.AVC.DTS-HD.MA-GRP",
			release.Info{
				Source: "remux", VideoCodec: "h264",
				ReleaseGroup: "GRP",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := release.ParseName(tt.input); got != tt.want {
				t.Errorf("release.ParseName(%q):\n got %+v\nwant %+v",
					tt.input, got, tt.want)
			}
		})
	}
}

// --- buildMatches ---

func TestBuildMatches_release_attribute_comparison(t *testing.T) {
	t.Parallel()
	video := &subflux.VideoInfo{
		MediaType:    "movie",
		ReleaseGroup: "Movie.2024.BluRay.1080p.x264-GROUP",
	}
	sub := &subflux.Subtitle{
		ReleaseName: "Movie.2024.BluRay.1080p.x264-GROUP",
	}

	matches := buildMatches(video, sub)

	if !matches.Source {
		t.Error("buildMatches: source not matched")
	}
	if !matches.VideoCodec {
		t.Error("buildMatches: video_codec not matched")
	}
	if !matches.ReleaseGroup {
		t.Error("buildMatches: release_group not matched")
	}
}

func TestBuildMatches_streaming_service_match(t *testing.T) {
	t.Parallel()
	video := &subflux.VideoInfo{
		MediaType:    "movie",
		ReleaseGroup: "Movie.2024.AMZN.WEB-DL.1080p.x264-GROUP",
	}
	sub := &subflux.Subtitle{
		ReleaseName: "Movie.2024.AMZN.WEB-DL.1080p.x264-GROUP",
	}

	matches := buildMatches(video, sub)

	if !matches.StreamingService {
		t.Error("buildMatches: streaming_service not matched")
	}
	if !matches.Source {
		t.Error("buildMatches: source not matched")
	}
}

func TestBuildMatches_different_releases_no_match(t *testing.T) {
	t.Parallel()
	video := &subflux.VideoInfo{
		MediaType:    "movie",
		ReleaseGroup: "Movie.2024.BluRay.1080p.x264-GROUP",
	}
	sub := &subflux.Subtitle{
		ReleaseName: "Movie.2024.WEB-DL.720p.x265-OTHER",
	}

	matches := buildMatches(video, sub)

	if matches.Source {
		t.Error("buildMatches: source should not match (bluray vs web)")
	}
	if matches.VideoCodec {
		t.Error("buildMatches: video_codec should not match (h264 vs h265)")
	}
	if matches.ReleaseGroup {
		t.Error("buildMatches: release_group should not match")
	}
}

func TestBuildMatches_imdb_episode_sets_series_imdb(t *testing.T) {
	t.Parallel()
	video := &subflux.VideoInfo{MediaType: "episode"}
	sub := &subflux.Subtitle{MatchedBy: "imdb"}
	matches := buildMatches(video, sub)
	if !matches.SeriesIMDB {
		t.Error("buildMatches(imdb, episode): series_imdb_id not set")
	}
	if matches.IMDB {
		t.Error("buildMatches(imdb, episode): imdb_id should not be set")
	}
}

func TestBuildMatches_imdb_movie_sets_imdb_id(t *testing.T) {
	t.Parallel()
	video := &subflux.VideoInfo{MediaType: "movie"}
	sub := &subflux.Subtitle{MatchedBy: "imdb"}
	matches := buildMatches(video, sub)
	if !matches.IMDB {
		t.Error("buildMatches(imdb, movie): imdb_id not set")
	}
	if matches.SeriesIMDB {
		t.Error("buildMatches(imdb, movie): series_imdb_id should not be set")
	}
}

// --- videoInfoFromRequest ---

func Test_videoInfoFromRequest(t *testing.T) {
	t.Parallel()
	req := &subflux.SearchRequest{
		MediaType:   "episode",
		Title:       "Breaking Bad",
		Year:        2008,
		Season:      1,
		Episode:     1,
		ReleaseName: "Breaking.Bad.S01E01.1080p.BluRay.x264-GRP",
		ImdbID:      "tt0903747",
	}

	info := videoInfoFromRequest(req)

	want := subflux.VideoInfo{
		MediaType:    "episode",
		ReleaseGroup: "Breaking.Bad.S01E01.1080p.BluRay.x264-GRP",
	}
	if info != want {
		t.Errorf("videoInfoFromRequest():\n got %+v\nwant %+v", info, want)
	}
}

// --- buildMatches (combined match) ---

func TestBuildMatches(t *testing.T) {
	t.Parallel()
	video := &subflux.VideoInfo{
		MediaType:    "movie",
		ReleaseGroup: "Movie.2024.BluRay.1080p.x264-GRP",
	}

	matches := buildMatches(video, &subflux.Subtitle{
		ReleaseName: "Movie.2024.BluRay.1080p.x264-GRP",
		MatchedBy:   "imdb",
	})

	if !matches.IMDB {
		t.Error("buildMatches: imdb_id not set")
	}
	if !matches.Source {
		t.Error("buildMatches: source not set")
	}
}

// --- scoreResults ---

func TestScoreResults_sorted_descending(t *testing.T) {
	t.Parallel()
	sc := scorer.New(&subflux.DefaultScores)
	video := &subflux.VideoInfo{
		MediaType:    "movie",
		ReleaseGroup: "Movie.2024.BluRay.1080p.x264-GRP",
	}
	subs := []subflux.Subtitle{
		{ReleaseName: "Movie.2024.WEB-DL.720p-OTHER", MatchedBy: "title"},
		{ReleaseName: "Movie.2024.BluRay.1080p.x264-GRP", MatchedBy: "hash"},
		{ReleaseName: "Movie.2024.BluRay.1080p.x264-GRP", MatchedBy: "imdb"},
	}

	scored := scoreResults(sc, video, subs, noPriority)
	if len(scored) != 3 {
		t.Fatalf("scoreResults() returned %d, want 3", len(scored))
	}

	// Verify descending order.
	for i := 1; i < len(scored); i++ {
		if scored[i].score > scored[i-1].score {
			t.Errorf("scoreResults() not sorted: score[%d]=%d > score[%d]=%d",
				i, scored[i].score, i-1, scored[i-1].score)
		}
	}
}

func TestScoreResults_tiebreaker_by_provider_priority(t *testing.T) {
	t.Parallel()
	sc := scorer.New(&subflux.DefaultScores)
	video := &subflux.VideoInfo{
		MediaType:    "movie",
		ReleaseGroup: "Movie.2024.BluRay.1080p.x264-GRP",
	}
	// Two subs with identical release names and matched_by produce the same score.
	// The tiebreaker should sort by provider priority (lower = more trusted).
	subs := []subflux.Subtitle{
		{Provider: "yify", ReleaseName: "Movie.2024.BluRay.1080p.x264-GRP", MatchedBy: "imdb"},
		{Provider: "os", ReleaseName: "Movie.2024.BluRay.1080p.x264-GRP", MatchedBy: "imdb"},
	}

	priority := func(prov subflux.ProviderID) int {
		switch prov {
		case "os":
			return 1 // More trusted.
		case "yify":
			return 5 // Less trusted.
		default:
			return 99
		}
	}

	scored := scoreResults(sc, video, subs, priority)
	if len(scored) != 2 {
		t.Fatalf("scoreResults() returned %d, want 2", len(scored))
	}
	// Scores should be equal.
	if scored[0].score != scored[1].score {
		t.Fatalf("scoreResults() scores differ: %d vs %d, want equal for tiebreaker test",
			scored[0].score, scored[1].score)
	}
	// Provider with lower priority number (os=1) should come first.
	if scored[0].sub.Provider != "os" {
		t.Errorf("scoreResults() tiebreaker: first provider = %q, want %q (lower priority wins)",
			scored[0].sub.Provider, "os")
	}
	if scored[1].sub.Provider != "yify" {
		t.Errorf("scoreResults() tiebreaker: second provider = %q, want %q",
			scored[1].sub.Provider, "yify")
	}
}

// --- Property-based tests ---

func TestParseName_never_panics(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.String().Draw(t, "name")
		_ = release.ParseName(name)
	})
}

func TestParseName_scene_names_never_panic(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Generate strings that look like scene release names.
		name := rapid.StringMatching(
			`[A-Za-z0-9._-]{5,80}`).Draw(t, "scene_name")
		info := release.ParseName(name)

		// All normalized fields should be lowercase or empty.
		for _, field := range []string{
			info.Source, info.VideoCodec,
			info.HDR, info.Edition,
		} {
			if field != strings.ToLower(field) {
				t.Errorf("field %q is not lowercase", field)
			}
		}
	})
}

// TestParseName_empty_returns_zero verifies the zero-value invariant:
// an empty input always produces a zero-value release.Info.
func TestParseName_empty_returns_zero(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Generate whitespace-only strings (empty, spaces, tabs).
		ws := rapid.StringMatching(`[ \t]{0,5}`).Draw(t, "whitespace")
		info := release.ParseName(ws)

		// Whitespace-only strings won't match any regex, so all fields
		// should be empty/false (same as zero value).
		if info != (release.Info{}) {
			t.Errorf("release.ParseName(%q) = %+v, want zero value", ws, info)
		}
	})
}

// --- Mutant-killing tests for scoreResults MatchedBy flags ---

// Kills CONDITIONALS_NEGATION in scoreResults (MatchedBy == matchByHash → !=).
// Verifies that hash-matched subs get HashVerifiable=true and non-hash subs get false.
func TestScoreResults_hash_flag_exact_values(t *testing.T) {
	t.Parallel()
	sc := scorer.New(&subflux.DefaultScores)
	release := "Movie.2024.BluRay.1080p.x264-GRP"
	video := &subflux.VideoInfo{
		MediaType:    "movie",
		ReleaseGroup: release,
	}

	// Hash-matched subtitle should score strictly higher than title-matched
	// with the same release name. The difference comes from HashVerifiable.
	hashSubs := []subflux.Subtitle{
		{ReleaseName: release, MatchedBy: "hash"},
	}
	titleSubs := []subflux.Subtitle{
		{ReleaseName: release, MatchedBy: "title"},
	}

	hashScored := scoreResults(sc, video, hashSubs, noPriority)
	titleScored := scoreResults(sc, video, titleSubs, noPriority)

	// If the negation mutant flips == to !=, hash-matched would get
	// HashVerifiable=false and title-matched would get HashVerifiable=true,
	// reversing the score relationship.
	if hashScored[0].score <= titleScored[0].score {
		t.Errorf("hash score (%d) must be > title score (%d); "+
			"hash flag may be inverted", hashScored[0].score, titleScored[0].score)
	}

	// Also verify the absolute difference is significant (not just 1 point).
	diff := hashScored[0].score - titleScored[0].score
	if diff < 10 {
		t.Errorf("hash vs title score diff = %d, want >= 10 (hash bonus should be significant)", diff)
	}
}

// Kills CONDITIONALS_NEGATION in scoreResults (MatchedBy == matchByHash → != for HashVerifiable).
// HashVerifiable should only be true when MatchedBy is "hash".
func TestScoreResults_hash_verifiable_only_for_hash(t *testing.T) {
	t.Parallel()
	sc := scorer.New(&subflux.DefaultScores)
	video := &subflux.VideoInfo{
		MediaType:    "movie",
		ReleaseGroup: "Movie.2024.BluRay.1080p.x264-GRP",
	}

	// Two subs with identical release names but different MatchedBy.
	// The hash-matched one should score higher due to HashVerifiable bonus.
	subs := []subflux.Subtitle{
		{ReleaseName: "Movie.2024.BluRay.1080p.x264-GRP", MatchedBy: "hash"},
		{ReleaseName: "Movie.2024.BluRay.1080p.x264-GRP", MatchedBy: "imdb"},
	}

	scored := scoreResults(sc, video, subs, noPriority)

	// Hash should be first (highest score) after sorting.
	if scored[0].sub.MatchedBy != "hash" {
		t.Errorf("scoreResults() top result MatchedBy = %q, want %q",
			scored[0].sub.MatchedBy, "hash")
	}
	if scored[0].score <= scored[1].score {
		t.Errorf("hash score (%d) must be > imdb score (%d)",
			scored[0].score, scored[1].score)
	}
}

// Kills CONDITIONALS_NEGATION in buildMatches (MatchedBy == matchByIMDB → !=).
// Verifies that IMDB and title matched_by both produce the same release
// attribute score, since identity fields are no longer scored.
func TestScoreResults_matched_by_does_not_affect_release_score(t *testing.T) {
	t.Parallel()
	sc := scorer.New(&subflux.DefaultScores)
	video := &subflux.VideoInfo{
		MediaType:    "movie",
		ReleaseGroup: "Movie.2024.BluRay.1080p.x264-GRP",
	}

	imdbSubs := []subflux.Subtitle{
		{ReleaseName: "Movie.2024.BluRay.1080p.x264-GRP", MatchedBy: "imdb"},
	}
	titleSubs := []subflux.Subtitle{
		{ReleaseName: "Movie.2024.BluRay.1080p.x264-GRP", MatchedBy: "title"},
	}

	imdbScored := scoreResults(sc, video, imdbSubs, noPriority)
	titleScored := scoreResults(sc, video, titleSubs, noPriority)

	// Identity fields no longer scored; same release attributes = same score.
	if imdbScored[0].score != titleScored[0].score {
		t.Errorf("imdb score (%d) != title score (%d); "+
			"identity fields should not affect score",
			imdbScored[0].score, titleScored[0].score)
	}

	// Both should have a positive score from matching release attributes.
	if imdbScored[0].score <= 0 {
		t.Errorf("imdb score = %d, want > 0 for matching release attributes",
			imdbScored[0].score)
	}
}

// The release-group regex either matches with its capture group or does not
// match at all, so ParseName can never observe a submatch slice holding only
// the full match. These two cases pin the behaviour at that boundary: no
// dash-group pattern yields an empty group, and a single-character group is
// still extracted.
func TestParseName_group_regex_no_match_returns_empty(t *testing.T) {
	t.Parallel()
	// Input with no dash-group pattern.
	info := release.ParseName("Movie 2024 BluRay 1080p")
	if info.ReleaseGroup != "" {
		t.Errorf("release.ParseName(no dash).ReleaseGroup = %q, want empty",
			info.ReleaseGroup)
	}
}

func TestParseName_group_regex_single_char_group(t *testing.T) {
	t.Parallel()
	// Single character group name.
	info := release.ParseName("Movie.2024.BluRay-X")
	if info.ReleaseGroup != "X" {
		t.Errorf("release.ParseName(single char group).ReleaseGroup = %q, want %q",
			info.ReleaseGroup, "X")
	}
}

// --- buildMatches: edition, HDR, and season_pack paths ---

func TestBuildMatches_edition_match(t *testing.T) {
	t.Parallel()
	video := &subflux.VideoInfo{
		MediaType:    "movie",
		ReleaseGroup: "Movie.2024.Directors.Cut.BluRay.1080p.x264-GRP",
	}
	sub := &subflux.Subtitle{
		ReleaseName: "Movie.2024.Directors.Cut.BluRay.1080p.x264-GRP",
	}

	matches := buildMatches(video, sub)

	if !matches.Edition {
		t.Error("buildMatches: edition not matched for identical Directors Cut releases")
	}
}

func TestBuildMatches_hdr_match(t *testing.T) {
	t.Parallel()
	video := &subflux.VideoInfo{
		MediaType:    "movie",
		ReleaseGroup: "Movie.2024.2160p.BluRay.HDR10.x265-GRP",
	}
	sub := &subflux.Subtitle{
		ReleaseName: "Movie.2024.2160p.BluRay.HDR10.x265-GRP",
	}

	matches := buildMatches(video, sub)

	if !matches.HDR {
		t.Error("buildMatches: hdr not matched for identical HDR10 releases")
	}
}

func TestBuildMatches_season_pack_episode(t *testing.T) {
	t.Parallel()
	video := &subflux.VideoInfo{
		MediaType:    "episode",
		ReleaseGroup: "Show.S01E01.1080p.BluRay.x264-GRP",
	}
	sub := &subflux.Subtitle{
		ReleaseName: "Show.S01.1080p.BluRay.x264-GRP",
	}

	matches := buildMatches(video, sub)

	if !matches.SeasonPack {
		t.Error("buildMatches: season_pack not set for episode with season-only subtitle")
	}
}

func TestBuildMatches_season_pack_not_set_for_movie(t *testing.T) {
	t.Parallel()
	video := &subflux.VideoInfo{
		MediaType:    "movie",
		ReleaseGroup: "Movie.2024.BluRay.1080p.x264-GRP",
	}
	sub := &subflux.Subtitle{
		ReleaseName: "Movie.S01.BluRay.1080p.x264-GRP",
	}

	matches := buildMatches(video, sub)

	if matches.SeasonPack {
		t.Error("buildMatches: season_pack should not be set for movie media type")
	}
}

// --- release.CompareSource ---

func TestCompareSource_same_family(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		a, b    string
		wantSet bool
	}{
		{"webdl and webrip are both web", "webdl", "webrip", true},
		{"bluray and remux are both bluray", "bluray", "remux", true},
		{"hdtv and sdtv are both tv", "hdtv", "sdtv", true},
		{"cam and telesync are both cam", "cam", "telesync", true},
		{"same source exact match", "dvd", "dvd", true},
		{"different families", "bluray", "webdl", false},
		{"empty a", "", "bluray", false},
		{"empty b", "bluray", "", false},
		{"both empty", "", "", false},
		{"unknown source falls back to raw value match", "custom", "custom", true},
		{"unknown source falls back to raw value mismatch", "custom", "other", false},
		{"one known one unknown", "bluray", "custom", false},
		{"unknown a with known b", "custom", "webdl", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matches := &subflux.MatchSet{}
			release.CompareSource(matches, tt.a, tt.b)
			got := matches.Source
			if got != tt.wantSet {
				t.Errorf("release.CompareSource(%q, %q): source=%v, want %v",
					tt.a, tt.b, got, tt.wantSet)
			}
		})
	}
}

// --- ParseName release-group extraction ---

func TestParseName_group_bracket_at_end(t *testing.T) {
	t.Parallel()
	// The bracket format [Group] at end of release name exercises the m[3] path
	// in ParseName (the second alternative in sonarrReleaseGroupRegex).
	// The regex requires a separator (-._ or space) before the opening bracket.
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"dot separator", "Movie.2024.BluRay.1080p.[GRP]", "GRP"},
		{"space separator", "Movie 2024 BluRay 1080p [GRP]", "GRP"},
		{"dash separator", "Movie.2024.BluRay.1080p-[GRP]", "GRP"},
		{"underscore separator", "Movie_2024_BluRay_1080p_[GRP]", "GRP"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			info := release.ParseName(tt.input)
			if info.ReleaseGroup != tt.want {
				t.Errorf("release.ParseName(%q).ReleaseGroup = %q, want %q",
					tt.input, info.ReleaseGroup, tt.want)
			}
		})
	}
}

func TestParseName_group_anime_format(t *testing.T) {
	t.Parallel()
	// Anime format: [SubGroup] at start.
	info := release.ParseName("[SubTeam] Anime Title - 01 (1080p)")
	if info.ReleaseGroup != "SubTeam" {
		t.Errorf("release.ParseName(anime group).ReleaseGroup = %q, want %q",
			info.ReleaseGroup, "SubTeam")
	}
}

func TestParseName_group_file_extension_stripped(t *testing.T) {
	t.Parallel()
	// File extension should be stripped before group extraction.
	info := release.ParseName("Movie.2024.BluRay-GRP.mkv")
	if info.ReleaseGroup != "GRP" {
		t.Errorf("release.ParseName(with ext).ReleaseGroup = %q, want %q",
			info.ReleaseGroup, "GRP")
	}
}
