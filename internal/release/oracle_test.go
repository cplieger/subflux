package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/release/netregex"
	"github.com/dlclark/regexp2/v2"
)

// upstreamPattern mirrors cmd/release-patterns' testdata/upstream.json row.
type upstreamPattern struct {
	Pattern     string   `json:"pattern"`
	Options     []string `json:"options"`
	Replacement string   `json:"replacement"`
	Spec        string   `json:"spec"`
}

func readJSON(t testing.TB, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func specKey(f *formatEntry, i int) string { return f.id + " " + f.arr + " " + strconv.Itoa(i) }

func TestGeneratedTableMatchesManifest(t *testing.T) {
	var m struct {
		Symbols []struct {
			ID, Override string
		}
		CustomFormats []struct {
			ID    string
			Files map[string]struct{}
		} `json:"custom_formats"`
	}
	readJSON(t, "patterns.manifest.json", &m)
	var upstream map[string]upstreamPattern
	readJSON(t, "testdata/upstream.json", &upstream)

	want := map[string]bool{}
	for _, s := range m.Symbols {
		want[s.ID] = s.Override != ""
	}
	got := map[string]bool{}
	for i := range symbolTable {
		e := &symbolTable[i]
		if _, dup := got[e.id]; dup {
			t.Errorf("%s generated twice", e.id)
		}
		got[e.id] = checkOverride(t, e.id, e.override, upstream[e.id].Pattern)
	}
	if !maps.Equal(got, want) {
		t.Errorf("symbol table %v, manifest %v (id: overridden)", got, want)
	}

	wantFormats := map[string]bool{}
	for _, f := range m.CustomFormats {
		for arr := range f.Files {
			wantFormats[f.ID+" "+arr] = true
		}
	}
	gotFormats := map[string]bool{}
	for i := range formatTable {
		gotFormats[formatTable[i].id+" "+formatTable[i].arr] = true
	}
	if !maps.Equal(gotFormats, wantFormats) {
		t.Errorf("format table %v, manifest %v", gotFormats, wantFormats)
	}
}

// checkOverride checks an override was written from the upstream text the
// snapshot holds, and reports whether there is one.
func checkOverride(t *testing.T, id string, o *override, pattern string) bool {
	t.Helper()
	if o == nil {
		return false
	}
	sum := sha256.Sum256([]byte(pattern))
	if hex.EncodeToString(sum[:]) != o.upstream {
		t.Errorf("%s: its override was written from sha256 %s, upstream is %x", id, o.upstream, sum)
	}
	return true
}

// oracleCase is one imported pattern, or the hand port, beside its upstream
// original under regexp2, which compiles .NET syntax with .NET semantics.
type oracleCase struct {
	re   *netregex.Regexp
	port *groupPort
	orig *regexp2.Regexp
	id   string
	repl string
}

var oracleCases = sync.OnceValues(func() ([]oracleCase, error) {
	b, err := os.ReadFile("testdata/upstream.json")
	if err != nil {
		return nil, err
	}
	var upstream map[string]upstreamPattern
	if err := json.Unmarshal(b, &upstream); err != nil {
		return nil, err
	}
	var cases []oracleCase
	add := func(c oracleCase) error {
		o := upstream[c.id]
		re, err := regexp2.Compile(o.Pattern, dotnetOptions(o.Options))
		if err != nil {
			return err
		}
		c.orig, c.repl = re, o.Replacement
		cases = append(cases, c)
		return nil
	}
	for i := range symbolTable {
		e := &symbolTable[i]
		c := oracleCase{id: e.id}
		switch {
		case e.override != nil:
			c.port = e.override.port
		case e.pattern != "":
			if c.re, err = e.compile(); err != nil {
				return nil, err
			}
		default:
			continue
		}
		if err := add(c); err != nil {
			return nil, err
		}
	}
	for i := range formatTable {
		f := &formatTable[i]
		for i := range f.specs {
			s := &f.specs[i]
			if s.kind == specSource {
				continue
			}
			re, err := s.compile()
			if err != nil {
				return nil, err
			}
			if err := add(oracleCase{id: specKey(f, i), re: re}); err != nil {
				return nil, err
			}
		}
	}
	return cases, nil
})

func dotnetOptions(names []string) regexp2.RegexOptions {
	opts := regexp2.None
	for _, n := range names {
		switch n {
		case "IgnoreCase":
			opts |= regexp2.IgnoreCase
		case "IgnorePatternWhitespace":
			opts |= regexp2.IgnorePatternWhitespace
		}
	}
	return opts
}

// compare reports how the executor's answers for s differ from the
// original's (every match with its groups, IsMatch and Replace), or ""
// when they agree.
func (c oracleCase) compare(t testing.TB, s string) string {
	var diffs []string
	if got, want := c.matches(s), c.dotnetMatches(t, s); got != want {
		diffs = append(diffs, "matches "+got+", want "+want)
	}
	if c.re == nil {
		return strings.Join(diffs, "; ")
	}
	ok, err := c.orig.MatchString(s)
	if err != nil {
		t.Fatalf("%s: regexp2: %v", c.id, err)
	}
	if c.re.MatchString(s) != ok {
		diffs = append(diffs, "IsMatch "+boolText(!ok))
	}
	want, err := c.orig.Replace(s, c.repl, -1, -1)
	if err != nil {
		t.Fatalf("%s: regexp2: %v", c.id, err)
	}
	if got := c.re.Replace(s, c.repl); got != want {
		diffs = append(diffs, "Replace "+strconv.Quote(got)+", want "+strconv.Quote(want))
	}
	return strings.Join(diffs, "; ")
}

func (c oracleCase) matches(s string) string {
	var all [][]int
	if c.port != nil {
		all = c.port.findAll(s)
	} else {
		all = c.re.FindAll(s)
	}
	parts := make([]string, 0, len(all))
	for _, m := range all {
		parts = append(parts, c.describe(s, m))
	}
	return strings.Join(parts, " ")
}

func (c oracleCase) dotnetMatches(t testing.TB, s string) string {
	var parts []string
	m, err := c.orig.FindStringMatch(s)
	for m != nil && err == nil {
		parts = append(parts, c.describeDotnet(m))
		m, err = c.orig.FindNextMatch(m)
	}
	if err != nil {
		t.Fatalf("%s: regexp2: %v", c.id, err)
	}
	return strings.Join(parts, " ")
}

func boolText(b bool) string {
	if b {
		return "match"
	}
	return "no match"
}

// describe spells a match as its span and every named group both sides
// have, so the two engines' answers compare as text.
func (c oracleCase) describe(s string, m []int) string {
	var b strings.Builder
	b.WriteString(span(m[0], m[1]))
	for _, name := range c.groupNames() {
		var v string
		var ok bool
		if c.port != nil {
			v, ok = portGroup(s, m, name)
		} else {
			v, ok = group(c.re, s, m, name)
		}
		if ok {
			b.WriteString(" " + name + "=" + v)
		}
	}
	return b.String()
}

func (c oracleCase) describeDotnet(m *regexp2.Match) string {
	var b strings.Builder
	i, n := m.ByteRange()
	b.WriteString(span(i, i+n))
	for _, name := range c.groupNames() {
		if g := m.GroupByName(name); g != nil && len(g.Captures) > 0 {
			b.WriteString(" " + name + "=" + g.String())
		}
	}
	return b.String()
}

func (c oracleCase) groupNames() []string {
	have := groupNames
	if c.re != nil {
		have = c.re.SubexpNames()
	}
	var names []string
	for _, n := range c.orig.GetGroupNames() {
		if n != "" && slices.Contains(have, n) {
			names = append(names, n)
		}
	}
	return names
}

func span(i, j int) string { return "[" + strconv.Itoa(i) + "," + strconv.Itoa(j) + ")" }

// oracleNames are names aimed at each pattern's edges: where a lookaround,
// a backtracking branch, a Unicode class or .NET's $ decides.
var oracleNames = []string{
	"Movie.2020.BD", "Movie.2020.BD.1080p", "Movie.2020.BD25", "Movie BD", "BD",
	"Show.S01E01.AMZN.WEB.Rip", "Show.S01E01.AMZN.WEB.RiPx", "Show.S01E01.AMZN.WEB.x264", "Show.S01E01.NF.WEB-", "Show.S01E01.DP WEB ri",
	"Show.S01E01.AMZN.WEB.", "Show.S01E01.ATVP.WEB-DL", "Show / WEB / x", "HDTV / WEB / x",
	"Movie.2020.DVD-R", "Movie.2020.DVD-r9", "Movie.2020.DVD-", "Movie.2020.DVD", "Movie.2020.DVD.x264", "Movie.2020.DVDRip", "Movie.2020.DVD-5",
	"Movie.Special.Cut", "Movie.Special.Cutting", "Movie.Final)Cut", "Movie.Extended.Special.Cutx", "Movie (Special Edition)",
	"Movie.Directors.Cut.Extended", "Movie.25th.Anniversary.Edition", "Movie.Ultimate.Rekall.Version.IMAX", "Movie.Open.Matte", "Movie.3in1",
	"Movie.Recut.Final.Cut", "Movie.Signaturex.Cut", "Movie.Theatrical.Cut.Uncut",
	"Show.S01E01.hbo.max.web-dl", "Show.S01E01.HBO.WEB-DL", "Show.S01E01.MAX.WEB-DL", "Show.S01E01.hbo-max.WEB-DL",
	"Movie.DTS-HD.MA.WEB-DL", "Movie.DTSHDMA.WEB-DL", "Movie.MA.WEB-DL", "Movie.YKW.1080p.WEB-DL",
	"Movie [iT] WEB-DL", "Movie [iT+]", "Movie iT]", "Movie +iT]", "iT]", "[iT", "[iT+",
	"Show.S01E01.iT.WEB-DL", "Show.S01E01.iTunes.WEBRip", "Show.S01E01.CNLP.WEB-DL", "Show.S01E01.Play.WEB-DL",
	"[SubGroup] Show - 01 [1080p]", "[ SubGroup] Show", "[Sub Group ] Show", "[a]b] Show", "[a] Show", "[] Show", "[x]",
	"www.Naruto-Kun.org - Show - 01", "[www.Naruto-Kun.org] Show - 01", "Naruto-Kun.org - Show", "www.example.com - Show - 01", "[ www.site.tv ] Show",
	"Show [Joy]", "Show (Joy)", "Show.Joy]", "Show [Joy] [Joy]", "Show [Joy][Joy]", "Show [-ZR-]", "Movie Tigole)", "Movie.[Anna]",
	"Movie.2020.1080p.BluRay.x264-GRP", "Movie.2020.1080p.BluRay.x264-GRP-PART", "Movie.2020-GRP.1080p", "Movie.2020.WEB-DL", "Movie.2020.x264-DTS-HD",
	"Movie.2020.x264-GRP-GRP", "Movie.2020.x264-GRÜP", "Movie.2020.x264-GRP-ES", "Movie.2020.x264-12", "Movie.2020.x264-tt1234567", "Movie.2020.x264-tmdb-123",
	"Movie.2020-1080p", "Movie.2020.x264 [GRP]", "Movie.2020.x264-GRP.mkv", "Movie.2020.x264-GRPx", "Movie-AsRequested", "Movie-GRP-postbot-xpost",
	"Show.S01E01-RP", "Show S01E01 Title-Obfuscated", "Movie.2020.x264-E.N.D", "Movie.2020.YTS.MX", "Movie.2020.KRaLiMaRKo",
	"Movie.2020.Raw-HD", "Movie.2020.RawHD.1080p", "Movie.2020.BD-Remux", "Movie.2020.Remux.1080p", "Movie.2020[Remux]",
	"Show bd1080", "Show (bd)", "Show [bd]", "Show.bd.", "Show [WEB] 1080p", "Show (WEB 1080p", "Movie.2020.HD.TV", "Movie.2020.SD-TV",
	"Movie.2020.x264.ext", "Movie.2020.x264.mp4", "Movie [rarbg]", "Movie.2020.R5", "Movie.2020.TS.x264", "Movie.2020.HDCAM", "Movie.2020.WP",
	"Movie.2020.WEB", "Movie.2020.web", "Movie.2020.1080p.Hybrid-WEB-x", "Movie.2020.WEB.2160p", "Movie.2020.WEB-DLMux",
	"Movie.2020.BD\n", "Movie BD\nx", "Movie.Special\nCut", "Movie.Special)Edition", "Movie [rarbg]\n.mkv", "Movie.2020.WEB\n",
	"www.ab--naruto-kun.com - Show - 01", "[www.ab--Naruto-Kun.org] Show", "www.naruto-kun.com.au - Show", "(www.x--naruto-kun.tv) Show",
	"Movie.2020.x264 [GRP]\n", "Movie.2020.x264-GRP\n", "Movie.2020.x264-GRP_", "Movie.2020.x264-GRP\u200d", "Movie.2020.x264-٢٠",
	"éAMZNé.1080p.WEB-DL", "Show.AMZNé.WEB-DL", "Show.\u0301AMZN.WEB-DL", "Show.AMZN\u0301.WEB-DL", "Show.\u200dAMZN.WEB-DL", "Show.ǅAMZN.WEB-DL",
	"Movie.2020.١٠٨٠p.BluRay", "Movie.2020.x264-١٢", "Show.S١E١.HDTV", "Movie\u00a0BluRay\u00a0x264-GRP", "Movie\u2003WEB\u2003DL", "Movie.HD\u3000TV",
	"Show.S01E01.ＡＭＺＮ.WEB-DL", "Movie.Ünrated.Cut", "Movie.Ultimate\u00a0Cut", "[ＳｕｂＧｒｏｕｐ] Show", "[\u00a0Sub] Show", " Show.AMZN.WEB-DL \n",
}

func goldenNames(t testing.TB) []string {
	var golden struct{ Names []string }
	readJSON(t, "testdata/golden/names.json", &golden)
	return append(slices.Clone(oracleNames), golden.Names...)
}

// oracleInputs is each name as given, and as the arrs feed their patterns:
// trimmed, and with '_' read as a space for the quality patterns.
func oracleInputs(name string) []string {
	out := []string{name}
	for _, s := range []string{strings.TrimSpace(name), strings.TrimSpace(strings.ReplaceAll(name, "_", " "))} {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func TestPatternsMatchOracle(t *testing.T) {
	cases, err := oracleCases()
	if err != nil {
		t.Fatal(err)
	}
	names := goldenNames(t)
	for _, c := range cases {
		t.Run(strings.ReplaceAll(c.id, "/", "-"), func(t *testing.T) {
			for _, name := range names {
				for _, s := range oracleInputs(name) {
					if diff := c.compare(t, s); diff != "" {
						t.Errorf("%q: %s", s, diff)
					}
				}
			}
		})
	}
}

// FuzzPatterns_oracle: on any valid UTF-8 name within MaxNameLen, every
// imported pattern and the hand port answer what regexp2 answers.
func FuzzPatterns_oracle(f *testing.F) {
	for _, n := range oracleNames {
		f.Add(n)
	}
	cases, err := oracleCases()
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if len(name) > MaxNameLen || !utf8.ValidString(name) {
			t.Skip()
		}
		for _, s := range oracleInputs(name) {
			for _, c := range cases {
				if diff := c.compare(t, s); diff != "" {
					t.Errorf("%s %q: %s", c.id, s, diff)
				}
			}
		}
	})
}
