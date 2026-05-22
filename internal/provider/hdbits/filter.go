package hdbits

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"subflux/internal/api"
	"subflux/internal/provider"
	"subflux/internal/provider/classify"
)

// --- JSON types (hdbSubtitleItem, flexInt) ---

// hdbSubtitleItem holds the fields needed for subtitle filtering.
// ID uses flexInt because the HDBits API inconsistently returns it as
// either a JSON number or a quoted string depending on the torrent.
type hdbSubtitleItem struct {
	Title    string  `json:"title"`
	Filename string  `json:"filename"`
	Language string  `json:"language"`
	ID       flexInt `json:"id"`
}

// flexInt unmarshals a JSON value that may be either a number or a string.
// JSON null, non-positive values, and non-numeric strings are rejected so
// a malformed/changing API response surfaces as a visible error instead
// of silently materializing as subtitle ID "0".
type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("flexInt: null is not a valid id")
	}
	n, err := provider.ParseFlexInt(data)
	if err != nil {
		return err
	}
	if n <= 0 {
		return fmt.Errorf("flexInt: non-positive id %d", n)
	}
	*f = flexInt(n)
	return nil
}

// --- Filtering and language mapping ---

// hdbExcludedKeywords lists keywords that disqualify a subtitle entry
// (commentary tracks, extras, lyrics). Data-driven for inspectability
// and per-keyword testability.
var hdbExcludedKeywords = []string{"commentary", "extras", "lyrics"}

// keepSubtitle returns true when an HDBits subtitle entry should be
// surfaced to the caller: extension is allowed (or absent) and the
// title/filename do not flag excluded keywords.
func keepSubtitle(s hdbSubtitleItem) bool {
	ext := strings.ToLower(filepath.Ext(s.Filename))
	if ext != "" && !hdbAcceptedExts[ext] {
		return false
	}
	lower := strings.ToLower(s.Title + " " + s.Filename)
	for _, kw := range hdbExcludedKeywords {
		if strings.Contains(lower, kw) {
			return false
		}
	}
	return true
}

// filterSubtitleData converts raw HDBits subtitle data into Subtitle values,
// applying language and commentary/extras filters. Pure function.
func filterSubtitleData(data []hdbSubtitleItem, searchReq *api.SearchRequest) []api.Subtitle {
	matchedBy := api.MatchByIMDB
	if searchReq.MediaType == api.MediaTypeEpisode {
		matchedBy = api.MatchByTVDB
	}
	var subs []api.Subtitle
	for _, s := range data {
		lang := hdbLangToISO(s.Language)
		if lang == "" || !slices.Contains(searchReq.Languages, lang) {
			continue
		}
		if !keepSubtitle(s) {
			continue
		}
		subs = append(subs, api.Subtitle{
			Provider:    providerName,
			ID:          strconv.Itoa(int(s.ID)),
			Language:    lang,
			ReleaseName: s.Title,
			MatchedBy:   matchedBy,
			Season:      searchReq.Season,
			Episode:     searchReq.Episode,
		})
	}
	return subs
}

// hdbLangOverrides maps HDBits-specific country-code aliases to their
// canonical ISO 639-1 codes. Standard codes are validated against
// classify.LangRegistry directly, eliminating ~48 identity entries.
var hdbLangOverrides = map[string]string{
	"br": "pb", "gr": "el", "cz": "cs", "se": "sv", "dk": "da",
	"kr": "ko", "cn": "zh", "il": "he", "si": "sl",
}

func hdbLangToISO(code string) string {
	if v, ok := hdbLangOverrides[code]; ok {
		return v
	}
	if _, ok := classify.LangRegistry[code]; ok {
		return code
	}
	return ""
}
