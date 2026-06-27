package ffmpeg

import (
	"math"
	"strconv"
	"strings"
)

// Codec name constants for the most commonly referenced subtitle formats.
const (
	CodecASS    = "ass"
	CodecSSA    = "ssa"
	CodecSubrip = "subrip"
	CodecSRT    = "srt"
)

// streamTypeCodes maps codec_type names to ffprobe -select_streams codes.
var streamTypeCodes = map[string]string{
	"subtitle": "s",
	"audio":    "a",
	"video":    "v",
}

func shortStreamType(codecType string) string {
	if code, ok := streamTypeCodes[codecType]; ok {
		return code
	}
	return codecType
}

// textSubtitleCodecs is the set of ffmpeg codec names that represent
// text-based subtitle formats (as opposed to bitmap formats like PGS/VobSub).
var textSubtitleCodecs = map[string]bool{
	CodecSubrip: true, CodecSRT: true, CodecASS: true, CodecSSA: true,
	"mov_text": true, "webvtt": true, "text": true, "ttml": true,
	"stl": true, "realtext": true, "subviewer": true,
	"subviewer1": true, "microdvd": true, "mpl2": true,
	"jacosub": true, "sami": true,
}

// IsTextSubtitleCodec reports whether the ffprobe codec name is a text-based
// subtitle format that can be converted to SRT.
func IsTextSubtitleCodec(codec string) bool {
	return textSubtitleCodecs[codec]
}

// SelectBestSubTrack picks the best text subtitle track from ffprobe output.
func SelectBestSubTrack(tracks []Track, lang, excludeLang string, mapper LangMapper) *Track {
	candidates := gatherTextCandidates(tracks, lang, excludeLang, mapper)
	if len(candidates) == 0 {
		return nil
	}

	candidates = preferNonForced(candidates)

	// Prefer SRT/subrip over ASS/SSA.
	for i := range candidates {
		if candidates[i].CodecName == CodecSubrip || candidates[i].CodecName == CodecSRT {
			return &candidates[i]
		}
	}

	return &candidates[0]
}

// gatherTextCandidates returns the text subtitle tracks that pass the language
// include/exclude filters.
func gatherTextCandidates(tracks []Track, lang, excludeLang string, mapper LangMapper) []Track {
	var candidates []Track
	for _, t := range tracks {
		if !IsTextSubtitleCodec(t.CodecName) {
			continue
		}
		trackLang := NormalizeFFprobeLang(t.Language, mapper)
		if excludeLang != "" && trackLang == excludeLang {
			continue
		}
		if lang != "" && trackLang != lang {
			continue
		}
		candidates = append(candidates, t)
	}
	return candidates
}

// preferNonForced returns only the non-forced tracks when any exist, otherwise
// the candidates unchanged.
func preferNonForced(candidates []Track) []Track {
	var nonForced []Track
	for _, c := range candidates {
		if !c.Forced {
			nonForced = append(nonForced, c)
		}
	}
	if len(nonForced) > 0 {
		return nonForced
	}
	return candidates
}

// NormalizeFFprobeLang normalizes ffprobe language tags to ISO 639-1.
// Handles ISO 639-2/3 codes, BCP 47 tags, and "und"/"undetermined".
func NormalizeFFprobeLang(lang string, mapper LangMapper) string {
	if lang == "" {
		return ""
	}

	lang = strings.ToLower(lang)

	if lang == "und" || lang == "undetermined" {
		return ""
	}

	// Extract primary subtag from BCP 47 (e.g. "en-US" -> "en").
	if i := strings.IndexByte(lang, '-'); i > 0 {
		lang = lang[:i]
	}

	if len(lang) == 2 {
		return lang
	}

	if mapper != nil {
		if alpha2 := mapper(lang); alpha2 != "" {
			return alpha2
		}
	}

	return lang
}

// parseFrameRate parses ffprobe's r_frame_rate fraction (e.g. "24000/1001").
func parseFrameRate(s string) float64 {
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0
		}
		return f
	}
	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 {
		return 0
	}
	// Guard against NaN/Inf results: ParseFloat accepts "NaN"/"Inf" (so a
	// fraction like "NaN/1" or "1e999/1" slips past the d==0 check), and a
	// frame rate that isn't a finite number is meaningless to callers.
	r := n / d
	if math.IsNaN(r) || math.IsInf(r, 0) {
		return 0
	}
	return r
}
