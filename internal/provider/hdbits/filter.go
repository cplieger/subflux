package hdbits

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/jsonx"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/provider/classify"
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

// idPolicy is the id decode policy: the jsonx strict core with null and
// "" rejected instead of tolerated as 0, and ids required positive —
// id 0 would silently build a getdox.php?id=0&passkey=... download URL.
var idPolicy = func() jsonx.Policy {
	p := jsonx.Strict()
	p.Null = jsonx.Reject
	p.EmptyString = jsonx.Reject
	p.MinValue = 1
	return p
}()

// UnmarshalJSON implements json.Unmarshaler for flexInt. The receiver is
// reset first so a reused decode target never retains a stale id: on error
// it stays 0 (no partial value), and a duplicate key's later tolerated value
// would clear an earlier decode if the policy ever gains a Zero disposition —
// the same invariants the jsonx field types pin (jsonx.StrictInt) and the
// sibling consumers apply.
func (f *flexInt) UnmarshalJSON(data []byte) error {
	*f = 0
	n, err := jsonx.ParseInt64(data, idPolicy)
	if err != nil {
		return fmt.Errorf("flexInt: %w", err)
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
