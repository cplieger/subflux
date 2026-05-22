package ffmpeg

import (
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

	if len(candidates) == 0 {
		return nil
	}

	// Prefer non-forced tracks.
	var nonForced []Track
	for _, c := range candidates {
		if !c.Forced {
			nonForced = append(nonForced, c)
		}
	}

	afterForced := candidates
	if len(nonForced) > 0 {
		afterForced = nonForced
	}

	candidates = afterForced

	// Prefer SRT/subrip over ASS/SSA.
	for i := range candidates {
		if candidates[i].CodecName == CodecSubrip || candidates[i].CodecName == CodecSRT {
			return &candidates[i]
		}
	}

	return &candidates[0]
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
		if err != nil {
			return 0
		}
		return f
	}
	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 {
		return 0
	}
	return n / d
}
