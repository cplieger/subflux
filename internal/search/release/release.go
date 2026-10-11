package release

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/cplieger/subflux/internal/subflux"
)

var editionRe = regexp.MustCompile(
	`(?i)\b(director'?s?[-. ]?cut|extended[-. ]?(?:cut|edition)?|` +
		`unrated|uncut|theatrical|imax|remastered|criterion|` +
		`special[-. ]?edition|anniversary[-. ]?edition|` +
		`collector'?s?[-. ]?edition|limited[-. ]?edition)\b`,
)

// Info holds metadata extracted from a release/scene name.
type Info struct {
	Source           string
	VideoCodec       string
	ReleaseGroup     string
	StreamingService string
	Edition          string
	HDR              string
}

// ParseName extracts metadata from a scene/release name.
//
// Input is clamped to MaxNameLen bytes before any pattern runs (defense in
// depth for the layer's measured linear-time gate): a longer name is
// treated as its MaxNameLen-byte prefix, matching the provider boundary's
// ClampName semantics, so callers that bypass provider.wrapRetry cannot
// violate the input bound. Provider-path behavior is unchanged (those
// names arrive already clamped).
func ParseName(name string) Info {
	name = ClampName(name)
	if name == "" {
		return Info{}
	}
	var info Info

	info.Source = MatchFirst(CompiledSources, name)
	info.VideoCodec = MatchFirst(CompiledVideoCodecs, name)
	info.HDR = MatchFirst(CompiledHDR, name)
	info.StreamingService = MatchFirst(CompiledStreaming, name)

	if m := editionRe.FindString(name); m != "" {
		info.Edition = strings.ToLower(m)
	}

	info.ReleaseGroup = parseGroup(name)

	if slog.Default().Enabled(context.TODO(), slog.LevelDebug) {
		slog.Debug("parsed release name",
			"input", name,
			"source", info.Source,
			"video_codec", info.VideoCodec,
			"streaming", info.StreamingService, "edition", info.Edition,
			"hdr", info.HDR,
			"group", info.ReleaseGroup)
	}
	return info
}

// parseGroup extracts the release group from a release name.
func parseGroup(name string) string {
	stripped := fileExtRe.ReplaceAllString(name, "")

	if m := compiledAnimeReleaseGroup.FindStringSubmatch(stripped); len(m) > 1 {
		return m[1]
	}

	if m := compiledReleaseGroup.FindStringSubmatch(stripped); m != nil {
		if len(m) > 1 && m[1] != "" {
			return m[1]
		}
		if len(m) > 3 && m[3] != "" {
			return m[3]
		}
	}

	return ""
}

// sourceFamily maps granular source labels to their family for comparison.
var sourceFamily = map[string]string{
	normWebDL:    "web",
	normWebRip:   "web",
	normBluray:   "bluray",
	normRemux:    "bluray",
	normHDTV:     "tv",
	normSDTV:     "tv",
	normDVD:      "dvd",
	normCam:      normCam,
	normTelesync: normCam,
	normTelecine: normCam,
	normHDRip:    "hdrip",
}

// CompareSource checks if two sources are in the same family.
func CompareSource(matches *subflux.MatchSet, a, b string) {
	if a == "" || b == "" {
		return
	}
	if sourceOrFamily(a) == sourceOrFamily(b) {
		matches.Source = true
	}
}

// sourceOrFamily returns the source family for comparison.
func sourceOrFamily(src string) string {
	if f, ok := sourceFamily[src]; ok {
		return f
	}
	return src
}
