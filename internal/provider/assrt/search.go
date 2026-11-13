package assrt

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/subtitleext"
)

const (
	// maxHits is the most sub/search answers at once; Search never pages.
	maxHits = 15
	// minQuery is the shortest text sub/search accepts.
	minQuery = 3
)

type hit struct {
	Lang *struct {
		List map[string]bool `json:"langlist"`
	} `json:"lang"`
	Filename string   `json:"filename"`
	Files    fileList `json:"filelist"`
	ID       int64    `json:"id"`
}

type file struct {
	F string `json:"f"`
}

// fileList is a hit's filelist, which Assrt answers as {} when it is empty
// (testdata/search-inception.json).
type fileList []file

func (l *fileList) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("{}")) {
		*l = nil
		return nil
	}
	var fs []file
	err := json.Unmarshal(b, &fs)
	*l = fs
	return err
}

type searchAnswer struct {
	Sub struct {
		Subs []hit `json:"subs"`
	} `json:"sub"`
}

// Search is one sub/search with filelist=1, which lists each hit's archive
// members: the video string as a file name, else the first title.
func (a *adapter) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) { //nolint:gocritic // hugeParam: provider.Adapter takes the Query by value, so concurrent adapters never share one
	if q.Kind != media.Episode && q.Kind != media.Movie {
		return nil, &provider.Skipped{Cause: "not an episode or a movie"}
	}
	if !slices.ContainsFunc(q.Langs, func(t language.Tag) bool { return len(languages.Wires(t)) > 0 }) {
		return nil, &provider.Skipped{Cause: "no asked language is Chinese or English"}
	}
	v := url.Values{"cnt": {strconv.Itoa(maxHits)}, "filelist": {"1"}}
	switch {
	case utf8.RuneCountInString(q.Video) >= minQuery:
		v.Set("q", q.Video)
		v.Set("is_file", "1")
	case len(q.Titles) > 0 && utf8.RuneCountInString(q.Titles[0]) >= minQuery:
		v.Set("q", q.Titles[0])
	default:
		return nil, &provider.Skipped{Cause: "no video name or title of 3 or more characters"}
	}
	var r searchAnswer
	if err := a.call(ctx, "/sub/search", v.Encode(), &r); err != nil {
		return nil, err
	}
	return candidates(r.Sub.Subs, q.Langs), nil
}

// candidates are one per subtitle file of each hit per asked tag: a file
// whose language segment names a mapped tag is that tag's alone, any other
// takes the hit's langlist. The first kind come first, since the wrapper
// keeps the first provider.MaxAnswer.
func candidates(hits []hit, langs []language.Tag) []provider.Candidate {
	var named, listed []provider.Candidate
	for i := range hits {
		n, l := offer(&hits[i], langs)
		named, listed = append(named, n...), append(listed, l...)
	}
	return append(named, listed...)
}

// offer is h's candidates in langs, those its files' language segments name
// apart from those its langlist gives.
func offer(h *hit, langs []language.Tag) (named, listed []provider.Candidate) {
	var list map[string]bool
	if h.Lang != nil {
		list = h.Lang.List
	}
	tags := hitTags(list)
	for _, f := range files(h) {
		if tag, ok := segmentTag(f); ok {
			if slices.Contains(langs, tag) {
				named = append(named, candidate(h.ID, f, tag))
			}
			continue
		}
		for _, tag := range tags {
			if slices.Contains(langs, tag) {
				listed = append(listed, candidate(h.ID, f, tag))
			}
		}
	}
	return named, listed
}

// files are a hit's subtitle files: its archive's members, or the file
// itself when it is no archive. A hit in another archive format (7z) has
// none, since the engine cannot open it.
func files(h *hit) []string {
	if h.ID <= 0 {
		return nil
	}
	switch strings.ToLower(path.Ext(h.Filename)) {
	case ".zip", ".rar":
		var out []string
		for _, f := range h.Files {
			if subtitleext.ArchiveInput(f.F) {
				out = append(out, f.F)
			}
		}
		return out
	}
	if subtitleext.ArchiveInput(h.Filename) {
		return []string{h.Filename}
	}
	return nil
}

// candidate is file f of subtitle id in tag, matched by its name.
func candidate(id int64, f string, tag language.Tag) provider.Candidate {
	base := path.Base(strings.ReplaceAll(f, `\`, "/"))
	return provider.Candidate{
		Ref:       provider.NewRef(strconv.FormatInt(id, 10) + "/" + f),
		Lang:      tag,
		Release:   base,
		Flags:     provider.Flags(language.Flags{}, base),
		MatchedBy: rank.Title,
	}
}
