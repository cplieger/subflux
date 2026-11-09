package arr

import (
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/subflux/internal/language"
)

// IMDb reads "tt" followed by decimal digits; anything else is 0, unknown.
func IMDb(id string) uint64 {
	digits, ok := strings.CutPrefix(id, "tt")
	if !ok || digits == "" || strings.TrimLeft(digits, "0123456789") != "" {
		return 0
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// OriginalLang is language.FromName of a language the arr names, an
// original or a metadata language; zero when absent or unknown.
func OriginalLang(l *arrapi.Language) language.Tag {
	if l == nil {
		return language.Tag{}
	}
	t, _ := language.FromName(l.Name)
	return t
}

// AudioLangs maps each audio track's language, in track order, through
// language.FromName, which reads the English names and the ISO 639-2 codes
// both API generations send, dropping unknown entries and duplicates.
func AudioLangs(mi *arrapi.MediaInfo) []language.Tag {
	if mi == nil {
		return nil
	}
	var out []language.Tag
	for _, name := range mi.AudioLanguages {
		t, ok := language.FromName(name)
		if !ok || t == (language.Tag{}) || slices.Contains(out, t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// AltTitles drops empty titles and case-insensitive duplicates of primary
// and of each other, keeping the first of each in order.
func AltTitles(primary string, titles []string) []string {
	seen := []string{strings.ToLower(primary)}
	var out []string
	for _, t := range titles {
		folded := strings.ToLower(t)
		if t == "" || slices.Contains(seen, folded) {
			continue
		}
		seen = append(seen, folded)
		out = append(out, t)
	}
	return out
}
