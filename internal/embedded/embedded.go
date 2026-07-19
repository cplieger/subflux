// Package embedded detects embedded subtitle tracks in video files.
// Supports all containers and subtitle codecs that ffprobe can parse.
// Delegates stream detection to the subsync package (ffprobe wrapper).
//
// This is local media inspection, not an acquisition source: the package
// deliberately lives outside internal/provider and implements only the
// search.TrackDetector seam. The detector always returns every normalized
// track (including bitmap formats); codec-usability policy is the search
// engine's, resolved from the embedded_subtitles config section.
package embedded

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/provider/classify"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
)

const (
	codecASS     = "ass"
	codecMovText = "mov_text"
	codecSSA     = "ssa"
)

// Detector detects embedded subtitle tracks via ffprobe.
//
// Note: this type satisfies the search.TrackDetector interface, but the
// compile-time assertion lives in internal/wiring (composition root)
// to keep this package decoupled from search/.
type Detector struct{}

// DetectTracks returns all embedded subtitle tracks in the given video
// file, including bitmap formats (PGS, VobSub), normalized to api types.
// Errors (ffprobe failure, corrupt file, timeout) are returned to the
// caller, which owns logging/metrics/fail-open policy; "error" stays
// distinguishable from "no tracks" (nil, nil).
func (Detector) DetectTracks(ctx context.Context, videoPath string) ([]api.EmbeddedTrack, error) {
	tracks, err := allTracks(ctx, videoPath)
	if err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return nil, nil
	}
	result := make([]api.EmbeddedTrack, len(tracks))
	for i, st := range tracks {
		result[i] = api.EmbeddedTrack{
			Index:           st.index,
			Codec:           st.codec,
			Lang:            st.lang,
			Name:            st.name,
			Forced:          st.forced,
			HearingImpaired: st.hearingImpaired,
		}
	}
	return result, nil
}

// --- Track detection via ffprobe ---

// subTrack is a normalized subtitle track from any container format.
type subTrack struct {
	codec           string
	lang            string
	name            string
	index           int
	forced          bool
	hearingImpaired bool
}

// allTracks returns all normalized subtitle tracks from the video file.
func allTracks(ctx context.Context, path string) ([]subTrack, error) {
	ffTracks, err := ffmpeg.ProbeStreams(ctx, path, "subtitle")
	if err != nil {
		return nil, err
	}
	var tracks []subTrack
	for _, t := range ffTracks {
		codec := normalizeCodecName(t.CodecName)
		st := normalizeTrack(t.Index, codec, t.Language, t.Title, t.Forced, t.HearingImpaired)
		if st == nil {
			continue
		}
		tracks = append(tracks, *st)
	}
	if len(tracks) > 0 {
		slog.Debug("embedded tracks detected", "count", len(tracks), "path", path)
	}
	return tracks, nil
}

// normalizeTrack converts ffprobe track metadata into a subTrack.
// Returns nil if the track should be skipped (undefined language).
func normalizeTrack(index int, codec, lang, name string, forced, hi bool) *subTrack {
	// Delegate to the canonical ffprobe-tag normalizer (lowercasing,
	// "und"/"undetermined" → empty, BCP 47 primary subtag, alpha3→alpha2).
	// A hand-rolled copy here previously drifted: it compared "und" before
	// lowercasing, so tags like "UND" or "UNDETERMINED" leaked through as
	// real coverage languages.
	lang2 := ffmpeg.NormalizeFFprobeLang(lang, classify.Alpha2FromAlpha3)
	if lang2 == "" {
		return nil
	}

	if !hi {
		hi = detectHIFromName(name)
	}
	if !forced {
		forced = detectForcedFromName(name)
	}

	return &subTrack{
		index:           index,
		codec:           codec,
		lang:            lang2,
		name:            name,
		forced:          forced,
		hearingImpaired: hi,
	}
}

// --- Name-based flag detection ---

// detectHIFromName checks if the track name indicates hearing-impaired subtitles.
func detectHIFromName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "sdh") ||
		strings.Contains(lower, "hearing impaired") ||
		strings.Contains(lower, "hard of hearing")
}

// detectForcedFromName checks if the track name indicates forced subtitles.
func detectForcedFromName(name string) bool {
	return classify.IsForced(name)
}

// --- Codec name normalization ---

// normalizeCodecName maps ffprobe codec names to the short names used
// throughout subflux (matching the old custom parser output).
func normalizeCodecName(codec string) string {
	if name, ok := ffprobeCodecMap[codec]; ok {
		return name
	}
	return codec
}

var ffprobeCodecMap = map[string]string{
	"subrip":            "srt",
	codecASS:            codecASS,
	codecSSA:            codecSSA,
	"webvtt":            "webvtt",
	codecMovText:        codecMovText,
	"hdmv_pgs_subtitle": "pgs",
	"dvd_subtitle":      "vobsub",
	"dvb_subtitle":      "dvbsub",
	"dvb_teletext":      "teletext",
	"eia_608":           "cea608",
	"ttml":              "ttml",
	"text":              codecMovText,
}
