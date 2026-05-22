package release

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"subflux/internal/api"
)

// Edition regex follows standard scene naming conventions.
var editionRe = regexp.MustCompile(
	`(?i)\b(director'?s?[-. ]?cut|extended[-. ]?(?:cut|edition)?|` +
		`unrated|uncut|theatrical|imax|remastered|criterion|` +
		`special[-. ]?edition|anniversary[-. ]?edition|` +
		`collector'?s?[-. ]?edition|limited[-. ]?edition)\b`)

// Info holds metadata extracted from a release/scene name.
type Info struct {
	Source           string
	VideoCodec       string
	ReleaseGroup     string
	StreamingService string
	Edition          string
	HDR              string
}

// ParseReleaseName extracts metadata from a scene/release name.
func ParseReleaseName(name string) Info {
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

	info.ReleaseGroup = ParseReleaseGroup(name)

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

// ParseReleaseGroup extracts the release group from a release name.
func ParseReleaseGroup(name string) string {
	stripped := FileExtRe.ReplaceAllString(name, "")

	if m := CompiledAnimeReleaseGroup.FindStringSubmatch(stripped); len(m) > 1 {
		return m[1]
	}

	if m := CompiledReleaseGroup.FindStringSubmatch(stripped); m != nil {
		if len(m) > 1 && m[1] != "" {
			return m[1]
		}
		if len(m) > 3 && m[3] != "" {
			return m[3]
		}
	}

	return ""
}

// SourceFamily maps granular source labels to their family for comparison.
var SourceFamily = map[string]string{
	NormWebDL:    "web",
	NormWebRip:   "web",
	NormBluray:   "bluray",
	NormRemux:    "bluray",
	NormHDTV:     "tv",
	NormSDTV:     "tv",
	NormDVD:      "dvd",
	NormCam:      NormCam,
	NormTelesync: NormCam,
	NormTelecine: NormCam,
	NormHDRip:    "hdrip",
}

// CompareSource checks if two sources are in the same family.
func CompareSource(matches *api.MatchSet, a, b string) {
	if a == "" || b == "" {
		return
	}
	if SourceOrFamily(a) == SourceOrFamily(b) {
		matches.Source = true
	}
}

// SourceOrFamily returns the source family for comparison.
func SourceOrFamily(src string) string {
	if f, ok := SourceFamily[src]; ok {
		return f
	}
	return src
}
