package subsro

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

type answer struct {
	Items []item `json:"items"`
}

type item struct {
	Description string `json:"description"`
	Title       string `json:"title"`
	Language    string `json:"language"`
	ID          int64  `json:"id"`
	Year        int    `json:"year"`
}

func (a *adapter) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) { //nolint:gocritic // hugeParam: provider.Adapter takes the Query by value, so concurrent adapters never share one
	if q.External.IMDb == 0 {
		return nil, &provider.Skipped{Cause: "no IMDb id"}
	}
	resp, err := a.get(ctx, fmt.Sprintf("%s/search/imdbid/tt%07d", apiURL, q.External.IMDb))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, maxListBody)
	if err != nil {
		return nil, err
	}
	var r answer
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, provider.Fail(provider.Permanent, "decode Subs.ro answer: "+err.Error())
	}
	return candidates(r.Items, q.Langs), nil
}

// candidates are the items in an asked language, each matched by id with
// no Covers, so the engine reads its episode from its name, and with its
// year, so the engine drops an item Subs.ro filed under another title's id
// (testdata/README.md).
func candidates(items []item, langs []language.Tag) []provider.Candidate {
	var out []provider.Candidate
	for i := range items {
		it := &items[i]
		tag, ok := languages.Tag(it.Language)
		if !ok || it.ID <= 0 || !slices.Contains(langs, tag) {
			continue
		}
		release := releaseName(it)
		out = append(out, provider.Candidate{
			Ref:       provider.NewRef(strconv.FormatInt(it.ID, 10)),
			Lang:      tag,
			Release:   release,
			Year:      it.Year,
			Flags:     provider.Flags(language.Flags{}, release, it.Description),
			MatchedBy: rank.ID,
		})
	}
	return out
}

// releasesLead starts a description that lists the item's releases, comma
// separated ("Release(s): breaking.bad.s01e06.720p.brrip.x264-visionx,").
const releasesLead = "Release(s):"

// sezonul is Subs.ro's Romanian season word in a title ("Breaking Bad -
// Sezonul 5"), which epmarker reads only in English.
var sezonul = regexp.MustCompile(`\bSezonul (\d+)\b`)

// releaseName is the name the engine reads an item by: the first release
// its description lists, else its title with the season in English.
func releaseName(it *item) string {
	if rest, ok := strings.CutPrefix(it.Description, releasesLead); ok {
		first, _, _ := strings.Cut(rest, ",")
		if first = strings.TrimSpace(first); first != "" && first != "None" {
			return first
		}
	}
	return sezonul.ReplaceAllString(it.Title, "Season $1")
}
