package release

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/release/netregex"
)

// Parse reads a video string or a subtitle name with Sonarr's tables for an
// Episode and Radarr's for a Movie; any other Kind reads as nothing. It reads
// the name as given, at most MaxNameLen bytes of it: a caller holding a path
// passes its base name.
func Parse(name string, k media.Kind) Parsed {
	var t *arrTables
	switch k {
	case media.Episode:
		t = &tables().episode
	case media.Movie:
		t = &tables().movie
	default:
		return Parsed{}
	}
	name = clamp(name)
	// Source, cut and service read '_' as a space, as the arrs' quality
	// parser does; the group reads the name as written, since the arrs'
	// group lists spell underscores (Koten_Gars).
	spaced := strings.TrimSpace(strings.ReplaceAll(name, "_", " "))
	p := Parsed{source: t.source(spaced), group: t.group(name), cut: cutOf(tables().edition(spaced))}
	p.service = t.service(spaced, p.group, p.source)
	if k == media.Episode {
		_, season := epmarker.Season(name)
		p.pack = season && !epmarker.StatesEpisode(name)
	}
	return p
}

func clamp(name string) string {
	if len(name) <= MaxNameLen {
		return name
	}
	end := MaxNameLen
	for end > 0 && !utf8.RuneStart(name[end]) {
		end--
	}
	return name[:end]
}

// parseTables is every generated entry compiled once, per arr.
type parseTables struct {
	editions       *netregex.Regexp
	episode, movie arrTables
}

// arrTables is one arr's patterns, under the ids patterns.manifest.json
// gives them with this arr's suffix.
type arrTables struct {
	sourceAll, sourceOther, animeBluray, animeWebDL, remux, rawHD *netregex.Regexp
	invalid, anime, exceptionExact, exception, extension          *netregex.Regexp
	release                                                       *groupPort
	clean, website, torrent                                       replacer
	sourceAt, otherAt                                             []Source // by group index, SourceUnknown for a group that names none
	extensions                                                    map[string]bool
	formats                                                       []format
	// exactFirst is Radarr's order: ExceptionReleaseGroupRegexExact before
	// ExceptionReleaseGroupRegex; Sonarr asks them the other way round.
	exactFirst bool
}

// format is one streaming custom format: this arr's file of it, else the
// other arr's.
type format struct {
	label string
	specs []spec
}

type spec struct {
	p                *netregex.Regexp
	kind             string
	sources          []Source
	negate, required bool
}

var tables = sync.OnceValue(func() *parseTables {
	byID := map[string]*symbolEntry{}
	for i := range symbolTable {
		byID[symbolTable[i].id] = &symbolTable[i]
	}
	t := &parseTables{editions: mustCompile(byID, "edition/movie")}
	t.episode = newArrTables(byID, "episode", "sonarr", false)
	t.movie = newArrTables(byID, "movie", "radarr", true)
	return t
})

func newArrTables(byID map[string]*symbolEntry, suffix, arr string, exactFirst bool) arrTables {
	p := func(id string) *netregex.Regexp { return mustCompile(byID, id+"/"+suffix) }
	r := func(id string) replacer { return replacer{re: p(id), with: byID[id+"/"+suffix].replacement} }
	t := arrTables{
		sourceAll: p("source"), sourceOther: p("source-other"), animeBluray: p("anime-bluray"),
		animeWebDL: p("anime-webdl"), remux: p("remux"), rawHD: p("rawhd"),
		release: mustPort(byID, "group/release/"+suffix), invalid: p("group/invalid"), anime: p("group/anime"),
		exceptionExact: p("group/exception-exact"), exception: p("group/exception"),
		clean: r("group/clean"), website: r("prefix/website"), torrent: r("suffix/torrent"),
		extension: p("extension/pattern"), extensions: map[string]bool{}, exactFirst: exactFirst,
	}
	t.sourceAt = sourcesAt(t.sourceAll, byID["source/"+suffix].groups)
	t.otherAt = sourcesAt(t.sourceOther, byID["source-other/"+suffix].groups)
	for _, id := range []string{"extension/media/", "extension/usenet/"} {
		for _, ext := range byID[id+suffix].values {
			t.extensions[ext] = true
		}
	}
	t.formats = newFormats(arr)
	return t
}

// mustCompile panics on a generated entry missing, holding no pattern or
// not compiling; the tests that load the tables rule each out.
func mustCompile(byID map[string]*symbolEntry, id string) *netregex.Regexp {
	e, ok := byID[id]
	if !ok || e.pattern == "" {
		panic("release: no generated pattern " + id)
	}
	re, err := e.compile()
	if err != nil {
		panic("release: " + id + ": " + err.Error())
	}
	return re
}

// mustPort panics on a generated entry missing or not ported by hand.
func mustPort(byID map[string]*symbolEntry, id string) *groupPort {
	e, ok := byID[id]
	if !ok || e.override == nil {
		panic("release: no hand port for " + id)
	}
	return e.override.port
}

// sourcesAt lays groups out by re's group index.
func sourcesAt(re *netregex.Regexp, groups map[string]Source) []Source {
	names := re.SubexpNames()
	at := make([]Source, len(names))
	for i, n := range names {
		at[i] = groups[n]
	}
	return at
}

func newFormats(arr string) []format {
	var ids []string
	files := map[string]*formatEntry{}
	for i := range formatTable {
		f := &formatTable[i]
		if _, seen := files[f.id]; !seen {
			ids = append(ids, f.id)
		}
		if files[f.id] == nil || f.arr == arr {
			files[f.id] = f
		}
	}
	out := make([]format, 0, len(ids))
	for _, id := range ids {
		out = append(out, compileFormat(files[id]))
	}
	return out
}

func compileFormat(f *formatEntry) format {
	cf := format{label: f.label}
	for i := range f.specs {
		s := &f.specs[i]
		c := spec{kind: s.kind, sources: s.sources, negate: s.negate, required: s.required}
		if s.kind != specSource {
			var err error
			if c.p, err = s.compile(); err != nil {
				panic("release: " + f.id + " " + f.arr + ": " + err.Error())
			}
		}
		cf.specs = append(cf.specs, c)
	}
	return cf
}

// source is the arrs' ParseQualityName reduced to the source a name states:
// the steps reading resolution, codec or extension only guess one, so they
// leave SourceUnknown. A remux token with a BluRay source is Remux at every
// size and codec (the arrs answer BluRay at 720p and below or with XviD), and
// raw HD is read before Radarr's unimported BR-DISK, which would make it a
// disc; the unimported MPEG2 and German remux patterns change no family.
func (t *arrTables) source(s string) Source {
	if t.rawHD.MatchString(s) {
		return RawHD
	}
	remux := t.remux.MatchString(s)
	if all := t.sourceAll.FindAll(s); len(all) > 0 {
		switch src := groupSource(all[len(all)-1], t.sourceAt); {
		case src == BluRay && remux:
			return Remux
		case src != SourceUnknown:
			return src
		}
	}
	switch {
	case remux:
		return Remux
	case t.animeBluray.MatchString(s):
		return BluRay
	case t.animeWebDL.MatchString(s):
		return WebDL
	}
	lower := strings.ToLower(s)
	for _, r := range []string{"bluray720p", "bluray1080p", "bluray2160p"} {
		if strings.Contains(lower, r) {
			return BluRay
		}
	}
	if m := t.sourceOther.Find(s, 0); m != nil {
		return groupSource(m, t.otherAt)
	}
	return SourceUnknown
}

// groupSource is the Source of the first group of m that took part and
// names one.
func groupSource(m []int, at []Source) Source {
	for i, src := range at {
		if src != SourceUnknown && m[2*i] >= 0 {
			return src
		}
	}
	return SourceUnknown
}

// group is the arrs' ParseReleaseGroup without PreSubstitutionRegex, which
// is not imported.
func (t *arrTables) group(s string) string {
	s = strings.TrimSpace(s)
	s = t.removeExtension(s)
	s = t.website.replace(s)
	s = t.torrent.replace(s)
	if m := t.anime.Find(s, 0); m != nil {
		g, _ := group(t.anime, s, m, "subgroup")
		return g
	}
	s = t.clean.replace(s)
	exceptions := []*netregex.Regexp{t.exception, t.exceptionExact}
	if t.exactFirst {
		exceptions[0], exceptions[1] = exceptions[1], exceptions[0]
	}
	for _, p := range exceptions {
		if all := p.FindAll(s); len(all) > 0 {
			g, _ := group(p, s, all[len(all)-1], "releasegroup")
			return g
		}
	}
	all := t.release.findAll(s)
	if len(all) == 0 {
		return ""
	}
	g, _ := portGroup(s, all[len(all)-1], "releasegroup")
	// int.TryParse: a group that is an Int32 is a number, not a group.
	if _, err := strconv.ParseInt(g, 10, 32); err == nil || t.invalid.MatchString(g) {
		return ""
	}
	return g
}

// removeExtension is FileExtensions.RemoveFileExtension: the last
// extension goes only when it is a media or usenet one.
func (t *arrTables) removeExtension(s string) string {
	m := t.extension.Find(s, 0)
	if len(m) < 2 || !t.extensions[strings.ToLower(s[m[0]:m[1]])] {
		return s
	}
	return s[:m[0]] + s[m[1]:]
}

// service is the label of the streaming formats that hold, with the arrs'
// rule per specification kind: none required fails, and not all fail.
func (t *arrTables) service(s, group string, src Source) string {
	label := ""
	for i := range t.formats {
		f := &t.formats[i]
		if !f.holds(s, group, src) {
			continue
		}
		if label != "" && label != f.label {
			return ""
		}
		label = f.label
	}
	return label
}

func (f *format) holds(s, group string, src Source) bool {
	type tally struct{ any, requiredFailed bool }
	kinds := map[string]*tally{}
	for i := range f.specs {
		sp := &f.specs[i]
		k := kinds[sp.kind]
		if k == nil {
			k = &tally{}
			kinds[sp.kind] = k
		}
		ok := sp.satisfied(s, group, src) != sp.negate
		k.any = k.any || ok
		k.requiredFailed = k.requiredFailed || sp.required && !ok
	}
	for _, k := range kinds {
		if !k.any || k.requiredFailed {
			return false
		}
	}
	return true
}

func (sp *spec) satisfied(s, group string, src Source) bool {
	switch sp.kind {
	case specSource:
		return slices.Contains(sp.sources, src)
	case specGroup:
		return group != "" && sp.p.MatchString(group)
	}
	return sp.p.MatchString(s)
}
