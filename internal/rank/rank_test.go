package rank

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/release"
)

func movie(name string) release.Parsed { return release.Parse(name, media.Movie) }

// cutName is a BluRay movie name carrying edition words.
func cutName(edition string) release.Parsed {
	return movie("Movie.2010." + strings.ReplaceAll(edition, " ", ".") + ".1080p.BluRay.x264-GRP")
}

func TestCutCompatibility(t *testing.T) {
	for _, c := range []struct {
		video, sub string
		want       Tri
	}{
		{"Directors Cut Unrated", "Extended Unrated", Differs},
		{"Theatrical Unrated", "Extended Unrated", Differs},
		{"Unrated", "Uncut", Differs},
		{"Extended Unrated", "Extended", Same},
		{"Unrated", "Extended", Differs},
		{"Special Edition", "Directors Cut", Differs},
		{"Uncut", "Theatrical", Differs},
		{"Extended", "", Unknown},
		{"", "Unrated", Unknown},
		{"Uncensored", "Uncensored", Same},
		{"Directors Cut", "Directors Cut Unrated", Same},
		{"Extended Unrated", "Unrated", Differs},
	} {
		t.Run(c.video+" vs "+c.sub, func(t *testing.T) {
			v, s := cutName(c.video), cutName(c.sub)
			if c.video != "" && v.Cut() == 0 || c.sub != "" && s.Cut() == 0 {
				t.Fatalf("a fixture parsed with no cut: %b, %b", v.Cut(), s.Cut())
			}
			if got := Compare(v, 0, s, 0, Title, false).Cut; got != c.want {
				t.Errorf("Cut = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFitOfTable(t *testing.T) {
	for _, c := range []struct {
		name string
		f    Facts
		want Fit
	}{
		{"hash", Facts{Hash: true, Group: Differs, Rate: Differs}, Exact},
		{"cut differs", Facts{Cut: Differs, Group: Same, Source: Same}, Conflict},
		{"one side pre-release", Facts{PreRelease: true, Group: Same}, Conflict},
		{"same group", Facts{Group: Same, Source: Same, Service: Same, Rate: Same}, SameRelease},
		{"same group, unknowns", Facts{Group: Same}, SameRelease},
		{"same group, source differs", Facts{Group: Same, Source: Differs}, Other},
		{"same group, service differs", Facts{Group: Same, Source: Same, Service: Differs}, SameSource},
		{"same group, rate differs", Facts{Group: Same, Source: Same, Rate: Differs}, Other},
		{"same source", Facts{Source: Same, Group: Differs}, SameSource},
		{"same source, rate differs", Facts{Source: Same, Rate: Differs}, Other},
		{"nothing known", Facts{}, Other},
		{"season pack is no grade", Facts{Source: Same, SeasonPack: true}, SameSource},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := FitOf(c.f); got != c.want {
				t.Errorf("FitOf(%+v) = %v, want %v", c.f, got, c.want)
			}
		})
	}
}

// sourceNames holds one movie name per source, by the family the search
// design's table gives it.
var sourceNames = map[release.Family][]string{
	release.Disc: {"Movie.2010.1080p.BluRay.x264-GRP", "Movie.2010.1080p.REMUX-GRP", "Movie.2010.720p.BDRip-GRP", "Movie.2010.BRRip-GRP"},
	release.Web:  {"Movie.2010.1080p.WEB-DL-GRP", "Movie.2010.1080p.WEBRip-GRP"},
	release.TV:   {"Movie.2010.HDTV-GRP", "Movie.2010.SDTV-GRP", "Movie.2010.PDTV-GRP", "Movie.2010.DSR-GRP", "Movie.2010.TVRip-GRP", "Movie.2010.Raw-HD-GRP"},
	release.DVD:  {"Movie.2010.DVDRip-GRP", "Movie.2010.DVDR-GRP"},
	release.PreRelease: {
		"Movie.2010.CAM.x264-GRP", "Movie.2010.TS.x264-GRP", "Movie.2010.TELECINE.x264-GRP",
		"Movie.2010.DVDSCR.x264-GRP", "Movie.2010.REGIONAL.x264-GRP", "Movie.2010.WORKPRINT.x264-GRP",
	},
}

func TestSourceFacts(t *testing.T) {
	families := []release.Family{release.Disc, release.Web, release.TV, release.DVD, release.PreRelease}
	seen := map[release.Source]bool{}
	for _, fa := range families {
		for _, fb := range families {
			for _, a := range sourceNames[fa] {
				for _, b := range sourceNames[fb] {
					want := Differs
					switch {
					case fa == release.PreRelease && fb == release.PreRelease:
						want = Unknown
					case fa == fb:
						want = Same
					}
					va, vb := movie(a), movie(b)
					seen[va.Source()] = true
					f := Compare(va, 0, vb, 0, Title, false)
					if f.Source != want {
						t.Errorf("%s vs %s: Source %v, want %v", a, b, f.Source, want)
					}
					if pre := (fa == release.PreRelease) != (fb == release.PreRelease); f.PreRelease != pre {
						t.Errorf("%s vs %s: PreRelease %v, want %v", a, b, f.PreRelease, pre)
					}
				}
			}
		}
	}
	t.Run("every source has a name", func(t *testing.T) {
		for s := release.BluRay; s <= release.Workprint; s++ {
			if !seen[s] {
				t.Errorf("no fixture parses as source %d", s)
			}
		}
	})
	t.Run("an unknown side", func(t *testing.T) {
		f := Compare(movie("Movie.2010.CAM.x264-GRP"), 0, movie("Movie.2010-GRP"), 0, Title, false)
		if f.Source != Unknown || !f.PreRelease || FitOf(f) != Conflict {
			t.Errorf("got %+v", f)
		}
	})
}

func TestGroupAndService(t *testing.T) {
	for _, c := range []struct {
		name           string
		video, sub     string
		group, service Tri
	}{
		{"group case-insensitive", "Movie.2010.1080p.BluRay.x264-SPARKS", "Movie.2010.1080p.BluRay.x264-sparks", Same, Unknown},
		{"group differs", "Movie.2010.1080p.BluRay.x264-SPARKS", "Movie.2010.1080p.BluRay.x264-AMIABLE", Differs, Unknown},
		{"no group", "Movie.2010.1080p.BluRay.x264-SPARKS", "Movie 2010 1080p BluRay", Unknown, Unknown},
		{"both web, same service", "Movie.2010.1080p.AMZN.WEB-DL-GRP", "Movie.2010.1080p.AMZN.WEBRip-other", Differs, Same},
		{"both web, other service", "Movie.2010.1080p.AMZN.WEB-DL-GRP", "Movie.2010.1080p.NF.WEB-DL-GRP", Same, Differs},
		{"both web, one service", "Movie.2010.1080p.AMZN.WEB-DL-GRP", "Movie.2010.1080p.WEB-DL-GRP", Same, Unknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, s := movie(c.video), movie(c.sub)
			f := Compare(v, 0, s, 0, Title, false)
			if f.Group != c.group || f.Service != c.service {
				t.Errorf("Group %v Service %v, want %v %v (groups %q %q, services %q %q)", f.Group, f.Service, c.group, c.service, v.Group(), s.Group(), v.Service(), s.Service())
			}
		})
	}
	t.Run("a service off the web is not compared", func(t *testing.T) {
		web := release.Parse("Show.S01E01.1080p.CR.WEB-DL-GRP", media.Episode)
		tv := release.Parse("Show.S01E01.1080p.CR.HDTV-GRP", media.Episode)
		if web.Service() != "CR" || tv.Service() != "CR" {
			t.Fatalf("fixture services %q %q, want CR on both", web.Service(), tv.Service())
		}
		for _, pair := range [][2]release.Parsed{{web, tv}, {tv, web}, {tv, tv}} {
			if f := Compare(pair[0], 0, pair[1], 0, Title, false); f.Service != Unknown {
				t.Errorf("Service %v for sources %d and %d", f.Service, pair[0].Source(), pair[1].Source())
			}
		}
	})
}

func TestHashVoidedByCutOrSource(t *testing.T) {
	bluray := cutName("Extended")
	for _, c := range []struct {
		name       string
		sub        release.Parsed
		m          MatchedBy
		identityOK bool
		want       bool
	}{
		{"hash with identity", cutName("Extended"), Hash, true, true},
		{"hash with unknowns", movie("Movie 2010"), Hash, true, true},
		{"no identity", cutName("Extended"), Hash, false, false},
		{"matched by id", cutName("Extended"), ID, true, false},
		{"cut differs", cutName("Theatrical"), Hash, true, false},
		{"source differs", movie("Movie.2010.Extended.1080p.WEB-DL-GRP"), Hash, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := Compare(bluray, 0, c.sub, 0, c.m, c.identityOK)
			if f.Hash != c.want {
				t.Errorf("Hash %v, want %v (%+v)", f.Hash, c.want, f)
			}
		})
	}
}

func TestRateFamilies(t *testing.T) {
	for _, c := range []struct {
		fps  float64
		want RateFamily
	}{
		{25, PAL},
		{50, PAL},
		{25.02, PAL},
		{24000.0 / 1001, Film},
		{23.976, Film},
		{24, Film},
		{30000.0 / 1001, Film},
		{30, Film},
		{48000.0 / 1001, Film},
		{48, Film},
		{60000.0 / 1001, Film},
		{60, Film},
		{0, RateUnknown},
		{-25, RateUnknown},
		{12.5, RateUnknown},
		{25.05, RateUnknown},
		{25.1, RateUnknown},
		{100, RateUnknown},
	} {
		if got := RateFamilyOf(c.fps); got != c.want {
			t.Errorf("RateFamilyOf(%v) = %v, want %v", c.fps, got, c.want)
		}
	}
	bluray := movie("Movie.2010.1080p.BluRay.x264-GRP")
	for _, c := range []struct {
		name     string
		vid, sub float64
		want     Tri
	}{
		{"25 and 23.976 differ", 25, 23.976, Differs},
		{"23.976 and 24 within the family", 23.976, 24, Same},
		{"one side unknown", 25, 0, Unknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Compare(bluray, c.vid, bluray, c.sub, Title, false).Rate; got != c.want {
				t.Errorf("Rate %v, want %v", got, c.want)
			}
		})
	}
}

func TestSeasonPackFromTheSubtitle(t *testing.T) {
	video := release.Parse("Show.S04E02.1080p.BluRay-GRP", media.Episode)
	for _, c := range []struct {
		sub  string
		want bool
	}{
		{"Show.S04.Complete.1080p.BluRay-GRP", true},
		{"Show.S04E02.1080p.BluRay-GRP", false},
	} {
		t.Run(c.sub, func(t *testing.T) {
			if got := Compare(video, 0, release.Parse(c.sub, media.Episode), 0, Title, false).SeasonPack; got != c.want {
				t.Errorf("SeasonPack %v, want %v", got, c.want)
			}
		})
	}
}

func TestEpisodeByNumberIsNotFit(t *testing.T) {
	for _, f := range []Facts{
		{},
		{Hash: true},
		{Cut: Differs},
		{Group: Same, Source: Same},
		{Source: Same, Rate: Same, SeasonPack: true},
	} {
		byNumber := f
		byNumber.EpisodeByNumber = true
		if a, b := FitOf(f), FitOf(byNumber); a != b {
			t.Errorf("FitOf(%+v) = %v, with EpisodeByNumber %v", f, a, b)
		}
	}
	t.Run("the facts row keeps it", func(t *testing.T) {
		want := Facts{Hash: true, Source: Same, Group: Differs, Service: Unknown, Cut: Same, Rate: Differs, PreRelease: true, SeasonPack: true, EpisodeByNumber: true}
		b, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		var got Facts
		if err := json.Unmarshal(b, &got); err != nil || got != want {
			t.Errorf("round trip of %s: %+v, %v", b, got, err)
		}
		if !strings.Contains(string(b), `"Source":"same"`) {
			t.Errorf("Tri not written by name: %s", b)
		}
	})
	t.Run("Compare never sets it", func(t *testing.T) {
		v := release.Parse("Show.S04.Complete.1080p.BluRay-GRP", media.Episode)
		if Compare(v, 0, v, 0, Hash, true).EpisodeByNumber {
			t.Error("Compare set EpisodeByNumber")
		}
	})
}

type textCodec interface {
	MarshalText() ([]byte, error)
}

func TestTextCodecs(t *testing.T) {
	for _, c := range []struct {
		v    textCodec
		want string
	}{
		{Unknown, "unknown"},
		{Same, "same"},
		{Differs, "differs"},
		{Title, "title"},
		{ID, "id"},
		{Hash, "hash"},
		{Conflict, "conflict"},
		{Other, "other"},
		{SameSource, "same_source"},
		{SameRelease, "same_release"},
		{Exact, "exact"},
	} {
		t.Run(c.want, func(t *testing.T) {
			b, err := c.v.MarshalText()
			if err != nil || string(b) != c.want {
				t.Fatalf("MarshalText = %q, %v", b, err)
			}
			var err2 error
			var back any
			switch c.v.(type) {
			case Tri:
				var x Tri
				err2 = x.UnmarshalText(b)
				back = x
			case MatchedBy:
				var x MatchedBy
				err2 = x.UnmarshalText(b)
				back = x
			case Fit:
				var x Fit
				err2 = x.UnmarshalText(b)
				back = x
			}
			if err2 != nil || back != c.v {
				t.Errorf("UnmarshalText(%q) = %v, %v", b, back, err2)
			}
		})
	}
	t.Run("the zero match and fit have no name", func(t *testing.T) {
		if _, err := MatchedBy(0).MarshalText(); err == nil {
			t.Error("MatchedBy(0) marshalled")
		}
		if _, err := Fit(0).MarshalText(); err == nil {
			t.Error("Fit(0) marshalled")
		}
		if _, err := Tri(3).MarshalText(); err == nil {
			t.Error("Tri(3) marshalled")
		}
	})
	t.Run("other spellings are refused", func(t *testing.T) {
		var tri Tri
		var m MatchedBy
		var f Fit
		for _, s := range []string{"", "Same", "same ", "0", "same-source"} {
			if tri.UnmarshalText([]byte(s)) == nil || m.UnmarshalText([]byte(s)) == nil || f.UnmarshalText([]byte(s)) == nil {
				t.Errorf("%q accepted", s)
			}
		}
	})
}

// TestNoNumericScore: the package exports no number but RateTolerance; its
// enums are named types.
func TestNoNumericScore(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			f, err := parser.ParseFile(token.NewFileSet(), n, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
	}
	numeric := []string{"int", "int8", "int16", "int32", "int64", "uint", "uint16", "uint32", "uint64", "float32", "float64", "uintptr"}
	isNum := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && slices.Contains(numeric, id.Name)
	}
	var found []string
	for _, file := range files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				found = append(found, numericSpecs(d, isNum)...)
			case *ast.FuncDecl:
				if d.Name.IsExported() && d.Type.Results != nil {
					for _, r := range d.Type.Results.List {
						if isNum(r.Type) {
							found = append(found, d.Name.Name)
						}
					}
				}
			}
		}
	}
	if !slices.Equal(found, []string{"RateTolerance"}) {
		t.Errorf("exported numbers: %v, want only RateTolerance", found)
	}
}

// numericSpecs names the exported numbers d declares: an untyped or
// numeric-typed const or var, and a numeric struct field.
func numericSpecs(d *ast.GenDecl, isNum func(ast.Expr) bool) []string {
	var found []string
	var typ ast.Expr
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			if s.Type != nil || len(s.Values) > 0 {
				typ = s.Type
			}
			for _, n := range s.Names {
				if n.IsExported() && (typ == nil || isNum(typ)) {
					found = append(found, n.Name)
				}
			}
		case *ast.TypeSpec:
			st, ok := s.Type.(*ast.StructType)
			if !ok || !s.Name.IsExported() {
				continue
			}
			for _, f := range st.Fields.List {
				for _, n := range f.Names {
					if n.IsExported() && isNum(f.Type) {
						found = append(found, s.Name.Name+"."+n.Name)
					}
				}
			}
		}
	}
	return found
}
