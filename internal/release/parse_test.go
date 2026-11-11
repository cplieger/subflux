package release

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/media"
	"github.com/dlclark/regexp2/v2"
)

var sourceNames = []string{
	"SourceUnknown", "BluRay", "Remux", "BDRip", "BRRip", "WebDL", "WebRip", "HDTV", "SDTV", "PDTV", "DSR",
	"TVRip", "RawHD", "DVDSource", "DVDR", "Cam", "Telesync", "Telecine", "Screener", "Regional", "Workprint",
}

var cutNames = []string{
	"Theatrical", "Extended", "Directors", "Ultimate", "Final", "Assembly", "Recut", "Despecialized",
	"Special", "Unrated", "Uncut", "Uncensored", "FanEdit",
}

func (c Cut) names() string {
	var out []string
	for i, n := range cutNames {
		if c&(1<<i) != 0 {
			out = append(out, n)
		}
	}
	return strings.Join(out, " ")
}

// label is a Parsed as testdata/golden/labels.json spells it.
func label(p Parsed) [4]string {
	return [4]string{sourceNames[p.Source()], p.Group(), p.Service(), p.Cut().names()}
}

func TestSourceFamilyTable(t *testing.T) {
	want := map[Source]Family{
		SourceUnknown: FamilyUnknown,
		BluRay:        Disc, Remux: Disc, BDRip: Disc, BRRip: Disc,
		WebDL: Web, WebRip: Web,
		HDTV: TV, SDTV: TV, PDTV: TV, DSR: TV, TVRip: TV, RawHD: TV,
		DVDSource: DVD, DVDR: DVD,
		Cam: PreRelease, Telesync: PreRelease, Telecine: PreRelease, Screener: PreRelease, Regional: PreRelease, Workprint: PreRelease,
	}
	if len(want) != len(sourceNames) {
		t.Fatalf("the table covers %d sources, the type has %d", len(want), len(sourceNames))
	}
	for s, f := range want {
		if got := s.Family(); got != f {
			t.Errorf("%s.Family() = %d, want %d", sourceNames[s], got, f)
		}
	}
	if got := Source(len(sourceNames)).Family(); got != FamilyUnknown {
		t.Errorf("an out-of-range Source has family %d, want FamilyUnknown", got)
	}
}

// pinned are labels asserted by hand, independent of labels.json, so an
// UPDATE_GOLDEN run cannot bless a regression on them.
var pinned = []struct {
	name string
	kind media.Kind
	want [4]string
}{
	{"Movie_2020_BluRay_x264-GRP", media.Movie, [4]string{"BluRay", "GRP", "", ""}},
	{"Show.S01.720p.HDTV.x264-E02", media.Episode, [4]string{"HDTV", "", "", ""}},
	{"Show.S01E01.720p.HDTV.x264-1A2B3C4D", media.Episode, [4]string{"HDTV", "", "", ""}},
	{"Show.S01E01.HDTV.x264-1080p", media.Episode, [4]string{"HDTV", "", "", ""}},
	{"Show.S01E01.HS.1080p.BluRay.x264-GRP", media.Episode, [4]string{"BluRay", "GRP", "", ""}},
	{"Show.S01E01.DCU.1080p.BluRay.x264-GRP", media.Episode, [4]string{"BluRay", "GRP", "", ""}},
	{"Show.S01E01.MY5.1080p.BluRay.x264-GRP", media.Episode, [4]string{"BluRay", "GRP", "", ""}},
	{"Show.S01E01.DCU.1080p.WEB-DL.x264-GRP", media.Episode, [4]string{"WebDL", "GRP", "DCU", ""}},
	{"Movie.2020.Extended.Cut.1080p.BluRay.x264-GRP", media.Movie, [4]string{"BluRay", "GRP", "", "Extended"}},
}

// goldenLabels is testdata/golden/labels.json: per kind, each corpus name's
// label.
type goldenLabels map[string]map[string][4]string

const updateGolden = "UPDATE_GOLDEN=1 go test ./internal/release/ -run '^TestGoldenLabels$'"

func TestGoldenLabels(t *testing.T) {
	for _, c := range pinned {
		t.Run("pinned "+c.name, func(t *testing.T) {
			if got := label(Parse(c.name, c.kind)); got != c.want {
				t.Errorf("Parse = %q, want %q", got, c.want)
			}
		})
	}
	got := goldenLabels{"episode": {}, "movie": {}}
	for _, name := range corpusNames(t) {
		got["episode"][name] = label(Parse(name, media.Episode))
		got["movie"][name] = label(Parse(name, media.Movie))
	}
	encoded := encodeLabels(t, got)
	const file = "testdata/golden/labels.json"
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(file, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	stored, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v; record it with %s", err, updateGolden)
	}
	if bytes.Equal(stored, encoded) {
		return
	}
	var want goldenLabels
	if err := json.Unmarshal(stored, &want); err != nil {
		t.Fatalf("%s: %v; re-record it with %s", file, err, updateGolden)
	}
	for _, kind := range []string{"episode", "movie"} {
		for _, name := range slices.Sorted(maps.Keys(got[kind])) {
			if w, ok := want[kind][name]; !ok || w != got[kind][name] {
				t.Errorf("%s %q: Parse = %q, golden %q", kind, name, got[kind][name], w)
			}
		}
		for name := range want[kind] {
			if _, ok := got[kind][name]; !ok {
				t.Errorf("%s %q: in the golden file, not in the corpus", kind, name)
			}
		}
	}
	t.Errorf("labels differ from %s; review the diff, then re-record with %s", file, updateGolden)
}

// encodeLabels writes one name per line, sorted, so a re-recording's diff
// reads as the labels that moved.
func encodeLabels(t *testing.T, g goldenLabels) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("{")
	for i, kind := range []string{"episode", "movie"} {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "\n  %q: {", kind)
		for j, name := range slices.Sorted(maps.Keys(g[kind])) {
			if j > 0 {
				b.WriteString(",")
			}
			b.WriteString("\n    ")
			b.Write(compact(t, name))
			b.WriteString(": ")
			b.Write(compact(t, g[kind][name]))
		}
		b.WriteString("\n  }")
	}
	b.WriteString("\n}\n")
	return b.Bytes()
}

func compact(t *testing.T, v any) []byte {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// corpusNames is the golden names and every upstream fixture case's name.
func corpusNames(t *testing.T) []string {
	var golden struct{ Names []string }
	readJSON(t, "testdata/golden/names.json", &golden)
	names := map[string]bool{}
	for _, n := range golden.Names {
		names[n] = true
	}
	for _, f := range readFixtures(t) {
		for _, m := range f.Methods {
			for _, c := range m.Cases {
				if n, ok := c.str(0); ok {
					names[n] = true
				}
			}
		}
	}
	return slices.Sorted(maps.Keys(names))
}

type fixtureFile struct {
	Upstream string
	Methods  []struct {
		Name  string
		Body  []string
		Cases []fixtureCase
	}
}

// fixtureCase is one [TestCase] row: each argument a JSON string, null or
// {"code": text}.
type fixtureCase []json.RawMessage

func (c fixtureCase) str(i int) (string, bool) {
	var s string
	if i >= len(c) || json.Unmarshal(c[i], &s) != nil {
		return "", false
	}
	return s, true
}

func readFixtures(t *testing.T) map[string]fixtureFile {
	t.Helper()
	var files map[string]fixtureFile
	readJSON(t, "testdata/golden/fixtures.json", &files)
	return files
}

// Each arr Quality's source as the Source labels it stands for: Sonarr's by
// Quality member (Qualities/Quality.cs), Radarr's by QualitySource and
// Modifier. Bluray at 720p and below also takes Remux, which the arrs file
// as Bluray at those sizes; RAWHD also takes HDTV, since MPEG2Regex, which
// turns an HDTV capture into RAWHD, is not imported.
var (
	tvSources   = []Source{HDTV, SDTV, PDTV, DSR, TVRip}
	discSources = []Source{BluRay, BDRip, BRRip}
	smallDisc   = []Source{BluRay, BDRip, BRRip, Remux}

	sonarrQualities = map[string][]Source{
		"Unknown": {SourceUnknown},
		"SDTV":    tvSources, "HDTV720p": tvSources, "HDTV1080p": tvSources, "HDTV2160p": tvSources,
		"RAWHD":     {RawHD, HDTV},
		"WEBDL480p": {WebDL}, "WEBDL720p": {WebDL}, "WEBDL1080p": {WebDL}, "WEBDL2160p": {WebDL},
		"WEBRip480p": {WebRip}, "WEBRip720p": {WebRip}, "WEBRip1080p": {WebRip}, "WEBRip2160p": {WebRip},
		"Bluray480p": smallDisc, "Bluray576p": smallDisc, "Bluray720p": smallDisc,
		"Bluray1080p": discSources, "Bluray2160p": discSources,
		"Bluray1080pRemux": {Remux}, "Bluray2160pRemux": {Remux},
		"DVD": {DVDSource},
	}
	radarrQualities = map[string][]Source{
		"UNKNOWN": {SourceUnknown}, "CAM": {Cam}, "TELESYNC": {Telesync}, "TELECINE": {Telecine}, "WORKPRINT": {Workprint},
		"TV": tvSources, "TV RAWHD": {RawHD, HDTV},
		"DVD": {DVDSource}, "DVD REMUX": {DVDR}, "DVD SCREENER": {Screener}, "DVD REGIONAL": {Regional},
		"WEBDL": {WebDL}, "WEBRIP": {WebRip},
		"BLURAY": discSources, "BLURAY small": smallDisc, "BLURAY REMUX": {Remux}, "BLURAY BRDISK": {BluRay},
	}
)

// qualityAssertion reads the Quality, or Radarr's QualitySource, Resolution
// and Modifier, that a ParseAndVerifyQuality call names.
func qualityAssertion(args []string) ([]Source, string, bool) {
	var source, modifier, resolution string
	for _, tok := range args {
		if q, ok := strings.CutPrefix(tok, "Quality."); ok {
			srcs, known := sonarrQualities[q]
			return srcs, "Quality." + q, known
		}
		if v, ok := strings.CutPrefix(tok, "QualitySource."); ok {
			source = v
		}
		if v, ok := strings.CutPrefix(tok, "Modifier."); ok {
			modifier = v
		}
		if v, ok := strings.CutPrefix(tok, "Resolution."); ok {
			resolution = v
		}
	}
	key := strings.TrimSpace(source + " " + modifier)
	if key == "BLURAY" && slices.Contains([]string{"R480p", "R576p", "R720p"}, resolution) {
		key = "BLURAY small"
	}
	srcs, known := radarrQualities[key]
	return srcs, "QualitySource." + key, known && source != ""
}

// unasserted are the upstream methods whose assertions no label carries:
// revision and version numbers.
var unasserted = []string{"should_be_able_to_parse_repack", "should_be_able_to_parse_anime_version"}

// fixtureExceptions is testdata/fixture_exceptions.json: per fixture id,
// the case names Parse answers differently on purpose, each with the label
// it gives instead and why.
type fixtureExceptions map[string]map[string]struct{ Want, Reason string }

// assertion is what one fixture case asserts: a Source set, a group or an
// edition.
type assertion struct {
	sources        []Source
	group, edition string
	kind           string
}

// assertions reads each case of a fixture method from what its body calls;
// a method it cannot read fails the test, so a new upstream method is
// classified before it counts.
func assertions(t *testing.T, id, name string, body []string, c fixtureCase) (assertion, bool) {
	expected := func() string { s, _ := c.str(1); return s }
	switch {
	case slices.Contains(body, "ParseAndVerifyQuality"):
		srcs, what, known := qualityAssertion(body[slices.Index(body, "ParseAndVerifyQuality")+1:])
		if !known {
			t.Errorf("%s %s: %s has no row in sonarrQualities or radarrQualities", id, name, what)
		}
		return assertion{kind: "source", sources: srcs}, known
	case slices.Contains(body, "result.SourceDetectionSource.Should"):
		i := slices.Index(body, "result.SourceDetectionSource.Should")
		for _, tok := range body[i+1:] {
			switch tok {
			case "QualityDetectionSource.Name":
				return assertion{kind: "named source"}, true
			case "QualityDetectionSource.Unknown", "QualityDetectionSource.Extension":
				return assertion{kind: "source", sources: []Source{SourceUnknown}}, true
			}
		}
	case slices.Contains(body, "Parser.ReleaseGroupParser.ParseReleaseGroup"):
		return assertion{kind: "group", group: expected()}, true
	case slices.Contains(body, "parsed.Edition.Should"):
		return assertion{kind: "edition", edition: expected()}, true
	case slices.Contains(unasserted, name):
		return assertion{}, false
	}
	t.Errorf("%s %s: asserts nothing TestFixtureAssertions reads; classify it", id, name)
	return assertion{}, false
}

// TestFixtureAssertions checks Parse against the labels the arrs' own tests
// assert, so re-recording labels.json cannot hide a regression.
func TestFixtureAssertions(t *testing.T) {
	var except fixtureExceptions
	readJSON(t, "testdata/fixture_exceptions.json", &except)
	used := map[string]bool{}
	files := readFixtures(t)
	for _, id := range slices.Sorted(maps.Keys(files)) {
		f := files[id]
		kind := media.Episode
		if f.Upstream == "radarr" {
			kind = media.Movie
		}
		t.Run(strings.ReplaceAll(id, "/", " "), func(t *testing.T) {
			for _, m := range f.Methods {
				for _, c := range m.Cases {
					name, ok := c.str(0)
					a, read := assertions(t, id, m.Name, m.Body, c)
					if !read {
						continue
					}
					if !ok {
						t.Errorf("%s: a case whose first argument is not a name", m.Name)
						continue
					}
					got, want := fixtureAnswer(name, kind, a), fixtureWant(a)
					if e, listed := except[id][name]; listed {
						used[id+" "+name] = true
						if got != e.Want {
							t.Errorf("%s %q: Parse gives %s, the exception says %s (%s)", m.Name, name, got, e.Want, e.Reason)
						}
						continue
					}
					if !slices.Contains(want, got) {
						t.Errorf("%s %q: Parse gives %s, upstream asserts %v", m.Name, name, got, want)
					}
				}
			}
		})
	}
	for id, names := range except {
		for name := range names {
			if !used[id+" "+name] {
				t.Errorf("exception %s %q matches no fixture case", id, name)
			}
		}
	}
}

func fixtureAnswer(name string, k media.Kind, a assertion) string {
	p := Parse(name, k)
	switch a.kind {
	case "group":
		return "group " + p.Group()
	case "edition":
		return "edition " + tables().edition(spacedName(name))
	case "named source":
		if p.Source() == SourceUnknown {
			return "source SourceUnknown"
		}
		return "named source"
	}
	return "source " + sourceNames[p.Source()]
}

func fixtureWant(a assertion) []string {
	switch a.kind {
	case "group":
		return []string{"group " + a.group}
	case "edition":
		return []string{"edition " + a.edition}
	case "named source":
		return []string{"named source"}
	}
	var out []string
	for _, s := range a.sources {
		out = append(out, "source "+sourceNames[s])
	}
	return out
}

// spacedName is the text Parse reads cuts from.
func spacedName(name string) string {
	return strings.TrimSpace(strings.ReplaceAll(clamp(name), "_", " "))
}

func TestCutTokensEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want Cut
	}{
		{"Movie.2012.Extended.Cut.1080p", "Movie 2012 Extended Cut 1080p", CutExtended},
		{"Movie.2012.Directors.Cut", "Movie 2012 Director's Cut", CutDirectors},
		{"Movie_2012_Directors_Cut", "Movie 2012 Director s Cut", CutDirectors},
		{"Movie.2012.Fan.Edit", "Movie 2012 FanEdit", CutFanEdit},
		{"Movie.2012.Extended.Directors.Cut.Fan.Edit", "Movie (Extended Directors Cut Fan Edit) 2012", CutExtended | CutDirectors | CutFanEdit},
		{"Movie.2012.Recut.Final.Cut", "Movie 2012 Recut Final Cut", CutRecut | CutFinal},
		{"Movie.2012.Extended.Theatrical.Version.IMAX", "Movie 2012 Extended Theatrical Version", CutExtended | CutTheatrical},
		{"Movie.2012.Special.Edition.Remastered", "Movie 2012 Special Edition", CutSpecial},
		{"Movie.2012.Uncut", "Movie 2012 UNCUT", CutUncut},
		{"Movie.2012.IMAX", "Movie 2012 Remastered", 0},
	}
	for _, c := range cases {
		t.Run(c.a, func(t *testing.T) {
			a, b := Parse(c.a, media.Movie).Cut(), Parse(c.b, media.Episode).Cut()
			if a != c.want || b != c.want {
				t.Errorf("cuts %q and %q, want %q for both", a.names(), b.names(), c.want.names())
			}
		})
	}
}

// TestEditionMatchesReportEditionRegex checks the edition step against
// Radarr's ReportEditionRegex, ^.+? before EditionRegex, under regexp2.
func TestEditionMatchesReportEditionRegex(t *testing.T) {
	var upstream map[string]upstreamPattern
	readJSON(t, "testdata/upstream.json", &upstream)
	re := regexp2.MustCompile(`^.+?`+upstream["edition/movie"].Pattern, regexp2.IgnoreCase)
	for _, name := range append(corpusNames(t), oracleNames...) {
		s := spacedName(name)
		want := ""
		m, err := re.FindStringMatch(s)
		if err != nil {
			t.Fatal(err)
		}
		if m != nil {
			want = strings.ReplaceAll(m.GroupByName("edition").String(), ".", " ")
		}
		if got := tables().edition(s); got != want {
			t.Errorf("%q: edition %q, ReportEditionRegex %q", s, got, want)
		}
	}
}

func TestParseReadsMaxNameLen(t *testing.T) {
	prefix := strings.Repeat("a", MaxNameLen-len("é")+1) // é straddles the cut
	name := prefix + "é.BluRay.x264-GRP"
	if got := Parse(name, media.Movie); got != (Parsed{}) {
		t.Errorf("Parse read past MaxNameLen: %+v", got)
	}
	if got := clamp(name); !utf8.ValidString(got) || len(got) > MaxNameLen {
		t.Errorf("clamp gave %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
}

func TestParseNeedsAKind(t *testing.T) {
	if got := Parse("Show.S01E01.1080p.AMZN.WEB-DL-GRP", 0); got != (Parsed{}) {
		t.Errorf("Parse with no Kind = %+v, want the zero Parsed", got)
	}
}

func TestParseSeasonPack(t *testing.T) {
	for _, c := range []struct {
		name string
		k    media.Kind
		want bool
	}{
		{"Show.S04.Complete.1080p.BluRay-GRP", media.Episode, true},
		{"Show.Season.2.720p.WEB-DL-GRP", media.Episode, true},
		{"Show.S01E02.1080p.WEB-DL-GRP", media.Episode, false},
		{"Show.S01.E05.720p.HDTV-GRP", media.Episode, false},
		{"Show - 01x08 - Title.HDTV-GRP", media.Episode, false},
		{"Show.S01E99999999999999999999.HDTV-GRP", media.Episode, false},
		{"Show.1080p.WEB-DL-GRP", media.Episode, false},
		{"[Group] Show - 05 [BD 1080p HEVC DTS5.1]", media.Episode, false},
		{"[Group] Show - 105 [1080p x265 Opus2.0]", media.Episode, false},
		{"Show.E05.1080p.WEB-DL.DTS5.1-GRP", media.Episode, false},
		{"Mars2.Show.1080p.WEB-DL-GRP", media.Episode, false},
		{"[SubsPlease] Mushoku Tensei S2 - 05 (1080p) [ABCD1234].mkv", media.Episode, false},
		{"[SubsPlease] Show S2 - 05v2 (1080p).ass", media.Episode, false},
		{"Show.S02.EP05.1080p", media.Episode, false},
		{"Show.S02.Ep.05", media.Episode, false},
		{"Show.Season.2.Episode.5", media.Episode, false},
		{"Show.1080p.WEB-DL-S3RL", media.Episode, false},
		{"Show S02 Complete 10 bit-GRP", media.Episode, true},
		{"Show.S01.Complete.BluRay-E02", media.Episode, false},
		{"Show.S01.720p.HDTV.x264-E02", media.Episode, false},
		{"Show.S02.H.264-E05", media.Episode, false},
		{"Show.S01.DDP5.1-E02", media.Episode, false},
		{"Movie.S04.2012.1080p.BluRay-GRP", media.Movie, false},
		{"Show S01 Vol.1-6 [1080p]", media.Episode, true},
		{"Show.S01.Disc.1-4", media.Episode, true},
		{"[Grp] Show S2 [BD Vol.1-3] [1080p]", media.Episode, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Parse(c.name, c.k).SeasonPack(); got != c.want {
				t.Errorf("SeasonPack = %v, want %v", got, c.want)
			}
		})
	}
}

// TestParseSeasonPackRangeNotations: a season and an episode range is one
// claim, an episode set and no pack, whatever notation states it.
func TestParseSeasonPackRangeNotations(t *testing.T) {
	for _, twins := range [][2]string{
		{"Show.S02.E01-E12.1080p.WEB-DL-GRP", "Show.Season.2.E01-E12.1080p.WEB-DL-GRP"},
		{"Show.S02E01-E02.1080p.WEB-DL-GRP", "Show.Season.2.E01E02.1080p.WEB-DL-GRP"},
		{"[Grp] Show S2 EP01-EP12 [1080p]", "[Grp] Show S2 EP01-12 [1080p]"},
		{"Show.S02E05-E06.1080p.WEB-DL-GRP", "[Grp] Show S2 - 05-06 [1080p]"},
		{"Show.S02E01-E12.1080p.WEB-DL-GRP", "[Grp] Show S2 - 01 ~ 12 [1080p]"},
		{"Show.S02.E01-E60.1080p.WEB-DL-GRP", "Show.Season.2.E01-E60.1080p.WEB-DL-GRP"},
		{"Show.S02E01-E60.1080p.WEB-DL-GRP", "[Grp] Show S2 EP01-EP60 [1080p]"},
		{"Show.S02.E05-E03.1080p.WEB-DL-GRP", "Show.Season.2.E05-E03.1080p.WEB-DL-GRP"},
		{"[Grp] Show S2 - 01-12 [1080p]", "[Grp] Show S2 [01-12] [1080p]"},
		{"[Grp] Show S2 - 01-12 [1080p]", "[Grp] Show S2 (01-12) [1080p]"},
		{"[Grp] Show S2 - 01 ~ 12 [1080p]", "[Grp] Show S2 01 ~ 12 [1080p]"},
		{"Show.Season.2.E01-E12.1080p.WEB-DL-GRP", "[Grp] Show Season 2 [01-12] [1080p]"},
	} {
		t.Run(twins[1], func(t *testing.T) {
			for _, name := range twins {
				if Parse(name, media.Episode).SeasonPack() {
					t.Errorf("%q: SeasonPack = true, want false", name)
				}
			}
		})
	}
}

func TestServiceOfTwoFormatsIsNone(t *testing.T) {
	one := Parse("Show.S01E01.1080p.AMZN.WEB-DL-GRP", media.Episode)
	if one.Service() != "AMZN" {
		t.Fatalf("one service: %q, want AMZN", one.Service())
	}
	if got := Parse("Show.S01E01.1080p.AMZN.NF.WEB-DL-GRP", media.Episode).Service(); got != "" {
		t.Errorf("two services: %q, want none", got)
	}
}

// FuzzParse_bounded: labels in their sets, a group a substring of the name,
// and nothing past MaxNameLen read.
func FuzzParse_bounded(f *testing.F) {
	for _, c := range pinned {
		f.Add(c.name, uint8(c.kind))
	}
	f.Add("[SubGroup] Show - 01 [1080p].mkv", uint8(media.Episode))
	f.Add("Movie.2020.Extended.Cut.2160p.UHD.BluRay.Remux-GRP", uint8(media.Movie))
	f.Fuzz(func(t *testing.T, name string, kind uint8) {
		k := media.Kind(kind % 3)
		p := Parse(name, k)
		if int(p.Source()) >= len(sourceNames) {
			t.Errorf("source %d outside the set", p.Source())
		}
		if p.Cut()>>len(cutNames) != 0 {
			t.Errorf("cut %b sets a bit outside the set", p.Cut())
		}
		if !strings.Contains(name, p.Group()) {
			t.Errorf("group %q is not in the name", p.Group())
		}
		if s := p.Service(); s != "" && !slices.ContainsFunc(formatTable, func(f formatEntry) bool { return f.label == s }) {
			t.Errorf("service %q is no format's label", s)
		}
		if long := name + strings.Repeat(".", MaxNameLen); Parse(long+".2160p.BluRay.Extended.Cut-GRP", k) != Parse(long, k) {
			t.Errorf("Parse reads past MaxNameLen")
		}
	})
}
