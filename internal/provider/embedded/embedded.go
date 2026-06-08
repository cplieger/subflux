// Package embedded detects embedded subtitles in video files.
// Supports all containers and subtitle codecs that ffprobe can parse.
// Delegates stream detection to the subsync package (ffprobe wrapper).
package embedded

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/classify"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
)

const (
	codecASS     = "ass"
	codecMovText = "mov_text"
	codecSSA     = "ssa"
)

const (
	// langUndefined is the ISO 639-2 code for undefined language,
	// used to skip tracks without a language.
	langUndefined = "und"

	// embeddedScore is the fixed score for embedded subtitles.
	// Set above the 0-100 release attribute range so embedded subs
	// always outrank downloaded subs (matches Bazarr behavior).
	embeddedScore = 360
)

// Factory creates an embedded subtitle provider from settings.
func Factory(_ context.Context, settings map[string]any) (api.Provider, error) {
	ps := provider.FromMap(settings)
	ignorePGS := provider.SettingBool(settings, provider.KeyIgnorePGS, false)
	ignoreVobSub := provider.SettingBool(settings, provider.KeyIgnoreVobSub, false)
	ignoreASS := provider.SettingBool(settings, provider.KeyIgnoreASS, false)
	_ = ps // common fields not used by embedded
	return &Provider{
		ignorePGS:    ignorePGS,
		ignoreVobSub: ignoreVobSub,
		ignoreASS:    ignoreASS,
	}, nil
}

// Compile-time interface assertion.
var _ api.Provider = (*Provider)(nil)

// Provider detects embedded subtitles via ffprobe.
type Provider struct {
	ignorePGS    bool
	ignoreVobSub bool
	ignoreASS    bool
}

// Name returns the provider identifier.
func (p *Provider) Name() api.ProviderID { return api.ProviderNameEmbedded }

// Search probes the video file for embedded subtitle streams.
// The video file path is passed via SearchRequest.VideoPath.
func (p *Provider) Search(ctx context.Context, req *api.SearchRequest) ([]api.Subtitle, error) {
	videoPath := req.VideoPath
	if videoPath == "" {
		return nil, nil
	}

	tracks, err := p.detectTracks(ctx, videoPath)
	if err != nil {
		return nil, fmt.Errorf("detect embedded subs: %w", err)
	}

	var results []api.Subtitle
	for _, t := range tracks {
		if !slices.Contains(req.Languages, t.lang) {
			continue
		}

		results = append(results, api.Subtitle{
			Provider:    api.ProviderNameEmbedded,
			ID:          fmt.Sprintf("embedded-%d", t.index),
			Language:    t.lang,
			ReleaseName: fmt.Sprintf("embedded %s stream #%d", t.codec, t.index),
			MatchedBy:   api.MatchByEmbedded,
			HearingImp:  t.hearingImpaired,
			Forced:      t.forced,
			Score:       embeddedScore,
		})
	}

	slog.Info("embedded search complete",
		"results", len(results), "tracks", len(tracks), "path", videoPath)
	return results, nil
}

// Download is a no-op for embedded subtitles.
func (p *Provider) Download(_ context.Context, _ *api.Subtitle) ([]byte, error) {
	return nil, errors.New("embedded subtitles cannot be downloaded separately")
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
// Callers apply their own filtering (e.g. codec ignore settings).
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
	return tracks, nil
}

// detectTracks uses ffprobe to detect subtitle tracks in any container,
// filtering out codecs the user has chosen to ignore.
func (p *Provider) detectTracks(ctx context.Context, path string) ([]subTrack, error) {
	tracks, err := allTracks(ctx, path)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(slices.Clone(tracks), func(t subTrack) bool {
		return p.isIgnoredCodec(t.codec)
	}), nil
}

// codecFilter maps a setting flag to the codecs it suppresses.
type codecFilter struct {
	enabled *bool
	codecs  []string
}

// isIgnoredCodec returns true if the codec should be filtered out based
// on provider settings.
func (p *Provider) isIgnoredCodec(codec string) bool {
	filters := []codecFilter{
		{&p.ignorePGS, []string{"pgs"}},
		{&p.ignoreVobSub, []string{"vobsub"}},
		{&p.ignoreASS, []string{codecASS, codecSSA}},
	}
	for _, f := range filters {
		if !*f.enabled {
			continue
		}
		if slices.Contains(f.codecs, codec) {
			return true
		}
	}
	return false
}

// normalizeTrack converts ffprobe track metadata into a subTrack.
// Returns nil if the track should be skipped (undefined language).
func normalizeTrack(index int, codec, lang, name string, forced, hi bool) *subTrack {
	if lang == "" || lang == langUndefined {
		return nil
	}

	// Extract primary subtag from BCP 47 tags (e.g. "en-US" → "en").
	if i := strings.IndexByte(lang, '-'); i > 0 {
		lang = lang[:i]
	}

	lang2 := classify.Alpha2FromAlpha3(lang)
	if lang2 == "" {
		lang2 = lang
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

// --- ProviderDirect (implements search.TrackDetector via DetectTracks) ---

// ProviderDirect provides direct access to embedded subtitle detection
// without going through the api.Provider interface.
// Returns all tracks (including bitmap) for coverage tracking.
//
// Note: this type satisfies the search.TrackDetector interface, but the
// compile-time assertion lives in search/track_detector.go (search-side)
// rather than here, to keep this provider package decoupled from search/.
type ProviderDirect struct{}

// DetectTracks detects embedded subtitle tracks in a video file.
// Returns all tracks including bitmap formats (PGS, VobSub) for coverage
// tracking. Codec filtering only applies to Search (downloadable subs).
// Implements search.TrackDetector.
func (p ProviderDirect) DetectTracks(ctx context.Context, videoPath string) []api.EmbeddedTrack {
	tracks, err := allTracks(ctx, videoPath)
	if err != nil {
		slog.Debug("embedded track detection failed", "path", videoPath, "error", err)
		return nil
	}
	if len(tracks) == 0 {
		return nil
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
	return result
}
