package subx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

// maxItems is the most items one search answers (the API's limit ceiling);
// an answer holding that many may have left some out.
const maxItems = 200

type answer struct {
	Items []item `json:"items"`
}

// item is one search result; Season and Episode are nil when SubX knows
// none, so a season's item with no Episode is a season pack.
type item struct {
	Season      *int   `json:"season"`
	Episode     *int   `json:"episode"`
	ID          string `json:"id"`
	Title       string `json:"title"`
	IMDb        string `json:"imdb_id"`
	Description string `json:"description"`
	Year        int    `json:"year"`
}

// Search asks by the query's IMDb id, else by its first title and year. A
// movie is one request. An episode asks its season, whose answer holds the
// episode's items and the season's packs; when that answer may be cut at
// maxItems, a second request asks the episode alone.
func (a *adapter) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) { //nolint:gocritic // hugeParam: provider.Adapter takes the Query by value, so concurrent adapters never share one
	v, ok := params(&q)
	if !ok {
		return nil, &provider.Skipped{Cause: "no IMDb id or title"}
	}
	if q.Kind != media.Episode {
		v.Set("video_type", "movie")
		items, err := a.search(ctx, v)
		if err != nil {
			return nil, err
		}
		return candidates(items, &q), nil
	}
	s, e := q.Numbering.Season, q.Numbering.Episode
	if s < 1 || e < 1 {
		return nil, &provider.Skipped{Cause: "SubX numbers episodes from season 1"}
	}
	v.Set("video_type", "episode")
	v.Set("season", strconv.Itoa(s))
	season, err := a.search(ctx, v)
	if err != nil {
		return nil, err
	}
	if len(season) < maxItems {
		return candidates(onEpisode(season, s, e, true), &q), nil
	}
	v.Set("episode", strconv.Itoa(e))
	ep, err := a.search(ctx, v)
	if err != nil {
		return candidates(onEpisode(season, s, e, true), &q), err
	}
	return candidates(append(onEpisode(ep, s, e, false), packs(season, s)...), &q), nil
}

// params are the search's criteria and limit: the IMDb id, else the first
// title with the year SubX accepts (1900 to 2100), both known.
func params(q *provider.Query) (url.Values, bool) {
	v := url.Values{"limit": {strconv.Itoa(maxItems)}}
	switch {
	case q.External.IMDb != 0:
		v.Set("imdb_id", imdbText(q.External.IMDb))
	case len(q.Titles) > 0 && strings.TrimSpace(q.Titles[0]) != "":
		v.Set("title", q.Titles[0])
		if q.Year >= 1900 && q.Year <= 2100 {
			v.Set("year", strconv.Itoa(q.Year))
		}
	default:
		return nil, false
	}
	return v, true
}

func imdbText(id uint64) string { return fmt.Sprintf("tt%07d", id) }

// search sends one search and decodes its items.
func (a *adapter) search(ctx context.Context, v url.Values) ([]item, error) {
	resp, err := a.get(ctx, apiURL+"/search?"+v.Encode())
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
		return nil, provider.Fail(provider.Permanent, "decode SubX answer: "+err.Error())
	}
	return r.Items, nil
}

// onEpisode keeps the items of season s, episode e, and the season's packs
// when withPacks is set, in answer order.
func onEpisode(items []item, s, e int, withPacks bool) []item {
	var out []item
	for _, it := range items {
		if it.Season == nil || *it.Season != s {
			continue
		}
		if (it.Episode != nil && *it.Episode == e) || (it.Episode == nil && withPacks) {
			out = append(out, it)
		}
	}
	return out
}

// packs are items' packs of season s.
func packs(items []item, s int) []item {
	var out []item
	for _, it := range items {
		if it.Season != nil && *it.Season == s && it.Episode == nil {
			out = append(out, it)
		}
	}
	return out
}

// candidates are the items in an asked language. An item filed under the
// query's IMDb id is matched by id; any other, an episode SubX files under
// the episode's own id among them, by its title, with its year.
func candidates(items []item, q *provider.Query) []provider.Candidate {
	var out []provider.Candidate
	for i := range items {
		it := &items[i]
		tag := variant(it.Description)
		if !subtitleID.MatchString(it.ID) || !slices.Contains(q.Langs, tag) {
			continue
		}
		title := baseTitle(it.Title)
		release := sceneName(it.Description, title)
		if release == "" {
			release = it.Title
		}
		c := provider.Candidate{
			Ref:       provider.NewRef(it.ID),
			Lang:      tag,
			Release:   release,
			Year:      it.Year,
			Flags:     provider.Flags(language.Flags{}, release, it.Description),
			MatchedBy: rank.Title,
			Title:     title,
		}
		if q.External.IMDb != 0 && it.IMDb == imdbText(q.External.IMDb) {
			c.MatchedBy, c.Title = rank.ID, ""
		}
		out = append(out, c)
	}
	return out
}

// spain matches a word naming Spain's Spanish, case folded: design 5.3's
// keywords and "peninsular", which uploaders also write. "Castellano
// neutro" is neutral Spanish (testdata/README.md), so that phrase is none.
var (
	spain   = regexp.MustCompile(`(?i)\b(?:españa|ibérico|castellano|gallego|castilla|europeo|peninsular)\b`)
	neutral = regexp.MustCompile(`(?i)\bcastellano\s+neutro\b`)
)

// variant is es-ES when description names Spain's Spanish, else es-419.
func variant(description string) language.Tag {
	if spain.MatchString(neutral.ReplaceAllString(description, "")) {
		return spainSpanish
	}
	return latinSpanish
}

// numbered is a SubX title's year or episode suffix ("Inception (2010)",
// "Breaking Bad S01E03", "Breaking Bad S01").
var numbered = regexp.MustCompile(`^(.+?)\s+(?:\(\d{4}\)|S\d+(?:E\d+)?)$`)

// baseTitle is title less its year or episode suffix.
func baseTitle(title string) string {
	if m := numbered.FindStringSubmatch(title); m != nil {
		return m[1]
	}
	return strings.TrimSpace(title)
}

// sceneName is the first release name description lists for title: a
// token starting with title's words joined by dots ("Breaking.Bad.S01E03.
// HDTV.XviD-LOL"), else "".
func sceneName(description, title string) string {
	words := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) == 0 {
		return ""
	}
	prefix := strings.Join(words, ".") + "."
	for _, tok := range strings.FieldsFunc(description, tokenBreak) {
		if tok = strings.Trim(tok, "."); strings.HasPrefix(strings.ToLower(tok), prefix) {
			return tok
		}
	}
	return ""
}

func tokenBreak(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(`,;:"()[]{}¡!¿?\`, r)
}
