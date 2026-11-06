package subtitlefile

import (
	"path/filepath"
	"strings"
)

// Stem is a video file name without its extension.
func Stem(videoName string) string {
	return strings.TrimSuffix(videoName, filepath.Ext(videoName))
}

// videoExts and the excluded prefixes are what Sonarr imports as a media
// file, so a sibling the arr manages is a video here and an AppleDouble
// ._Movie.mkv beside Movie.mkv is not:
// https://github.com/Sonarr/Sonarr/blob/ad661cc76cf097295f88652a4e04258f8e8b0b86/src/NzbDrone.Core/MediaFiles/MediaFileExtensions.cs
// https://github.com/Sonarr/Sonarr/blob/ad661cc76cf097295f88652a4e04258f8e8b0b86/src/NzbDrone.Core/MediaFiles/DiskScanService.cs#L75
var videoExts = map[string]bool{
	".3gp": true, ".asf": true, ".asx": true, ".avc": true, ".avi": true, ".bin": true,
	".bivx": true, ".dat": true, ".divx": true, ".dv": true, ".dvr-ms": true, ".fli": true,
	".flv": true, ".ifo": true, ".img": true, ".iso": true, ".m2ts": true, ".m2v": true,
	".m3u": true, ".m4v": true, ".mkv": true, ".mov": true, ".mp4": true, ".mpeg": true,
	".mpg": true, ".nrg": true, ".nsv": true, ".nuv": true, ".ogm": true, ".ogv": true,
	".pva": true, ".qt": true, ".rm": true, ".rmvb": true, ".strm": true, ".svq3": true,
	".ts": true, ".ty": true, ".viv": true, ".vob": true, ".vp3": true, ".webm": true,
	".wmv": true, ".wpl": true, ".wtv": true, ".xvid": true,
}

// IsVideo reports whether name, in any ASCII case, is a video file with a
// non-empty Stem, so Owner never sees the empty stem that would own every
// name beginning with a dot.
func IsVideo(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "._") || strings.HasPrefix(lower, ".unmanic") || Stem(name) == "" {
		return false
	}
	return videoExts[filepath.Ext(lower)]
}

// Owner is the video stem among stems that owns name: the longest one that,
// followed by ".", prefixes it, so Movie.Extended.en.srt belongs to
// Movie.Extended beside Movie. stems holds the Stem of each IsVideo name in
// the folder, so two videos sharing a stem (Movie.mkv, Movie.mp4) appear
// twice and own none of its names. False: no video owns name.
func Owner(stems []string, name string) (string, bool) {
	best, shared, found := "", false, false
	for _, s := range stems {
		if !strings.HasPrefix(name, s+".") {
			continue
		}
		switch {
		case !found || len(s) > len(best):
			best, shared, found = s, false, true
		case s == best:
			shared = true
		}
	}
	if !found || shared {
		return "", false
	}
	return best, true
}
