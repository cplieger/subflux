package episodematch

import (
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"testing"
)

// corpus is testdata/corpus.json: packs of member names with the TVDB
// episode each names ("NOMATCH" for none, "NOTITLE" for an untitled name),
// and every candidate episode's titles per language.
type corpus struct {
	Candidates map[string]struct {
		Episodes map[string]map[string]any `json:"episodes"`
	} `json:"candidates"`
	Packs []struct {
		Kind    string `json:"kind"`
		Lang    string `json:"lang"`
		Members []struct {
			Name  string `json:"name"`
			Truth any    `json:"truth"`
		} `json:"members"`
		Show int `json:"show"`
	} `json:"packs"`
}

func loadCorpus(t testing.TB) corpus {
	t.Helper()
	raw, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var c corpus
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

type tally struct{ n, ok, wrong, untitled int }

// TestAssignmentCorpus runs every pack against its candidates in ascending
// and in descending id order: an exact tie between two episodes (Game of
// Thrones' "You Win or You Die" and its special) must refuse under both,
// not fall to whichever column the assignment meets first. same's floor is
// the measured 427 less Futurama's three episodes listed twice under two
// numbers, whose copies the duplicate rule leaves unassigned.
func TestAssignmentCorpus(t *testing.T) {
	c := loadCorpus(t)
	for _, descending := range []bool{false, true} {
		t.Run(map[bool]string{false: "ascending", true: "descending"}[descending], func(t *testing.T) {
			checkCorpus(t, &c, descending)
		})
	}
}

func checkCorpus(t *testing.T, c *corpus, descending bool) {
	got := map[string]*tally{}
	for _, p := range c.Packs {
		eps := c.Candidates[strconv.Itoa(p.Show)].Episodes
		ids := make([]string, 0, len(eps))
		for id := range eps {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		if descending {
			slices.Reverse(ids)
		}
		langs := []string{"eng"}
		if p.Kind == "xlang" {
			langs = []string{p.Lang, "eng"}
		}
		titles := make([][]string, len(ids))
		for j, id := range ids {
			for _, l := range langs {
				if s, ok := eps[id][l].(string); ok {
					titles[j] = append(titles[j], s)
				}
			}
		}
		names := make([]string, len(p.Members))
		for i, m := range p.Members {
			names[i] = m.Name
		}
		read := ReadPack(names, -1)
		assigned := assignTitles(read, titles)
		key := p.Kind
		if (key == "same" || key == "notitle") && p.Show%2 == 0 {
			key += "-dev"
		}
		r := got[key]
		if r == nil {
			r = &tally{}
			got[key] = r
		}
		for i, m := range p.Members {
			r.n++
			if read[i].Title == "" {
				r.untitled++
			}
			want := ""
			switch v := m.Truth.(type) {
			case float64:
				want = strconv.Itoa(int(v))
			case string:
				want = v
			}
			switch {
			case assigned[i] >= 0 && ids[assigned[i]] == want:
				r.ok++
			case assigned[i] < 0 && (want == "NOMATCH" || want == "NOTITLE" && read[i].Title == ""):
				r.ok++
			case assigned[i] >= 0:
				r.wrong++
			}
		}
	}
	for _, c := range []struct {
		kind       string
		n, atLeast int
	}{{"same", 432, 424}, {"prod", 301, 298}, {"xlang", 614, 614}} {
		r := got[c.kind]
		if r == nil || r.n != c.n || r.ok < c.atLeast || r.wrong > 0 {
			t.Errorf("%s: %+v; want %d members, at least %d assigned, none wrong", c.kind, r, c.n, c.atLeast)
		}
	}
	for _, k := range []string{"notitle", "notitle-dev"} {
		if r := got[k]; r == nil || r.untitled != r.n || r.ok != r.n {
			t.Errorf("%s: %+v; want every name untitled and none assigned", k, r)
		}
	}
}

// TestDuplicateAssignmentRefused: two production X-Men files both named "Old
// Soldiers" under different numbers leave both unassigned, while one alone
// is assigned.
func TestDuplicateAssignmentRefused(t *testing.T) {
	xmen := recorded(t, 76115, "official")
	ser := newSeries(&xmen, 0)
	soldiers := slices.IndexFunc(ser.episodes, func(e episode) bool { return slices.Contains(e.titles, "Old Soldiers") })
	both := []string{
		"X-Men The Animated Series (1992) - S05E07 - Old Soldiers [DSNP][WEBDL-480p][AAC 2.0][h264]-HONE.mkv",
		"X-Men The Animated Series (1992) - S05E08 - Old Soldiers [DSNP][WEBDL-480p][AAC 2.0][h264]-HONE.mkv",
	}
	if got := ser.assign(ReadPack(both, 5)); !slices.Equal(got, []int{unassigned, unassigned}) {
		t.Errorf("both copies: assigned %v; want neither", got)
	}
	if got := ser.assign(ReadPack(both[:1], 5)); !slices.Equal(got, []int{soldiers}) {
		t.Errorf("one copy: assigned %v; want episode %d", got, soldiers)
	}
}
