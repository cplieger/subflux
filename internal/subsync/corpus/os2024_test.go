//go:build corpus

package corpus

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/timing"
)

// TestCorpusOS2024 runs OpenSubtitles2024's multilingual test set (Tiedemann
// and Luo 2026, LREC; https://www.opensubtitles.org/) in OS2024_DIR, each
// language's subtitle against the English one of its movie, through the
// production aligner, audio_fallback off then on: AC19's fitting-set bars.
// Its sentence alignments, synchronised across every language, stand in for
// the harness's text oracle.
func TestCorpusOS2024(t *testing.T) {
	dir := requireEnv(t, "OS2024_DIR", "a directory per movie of the set's untokenized documents, <lang>.xml, and its alignments, <pivot>-<lang>.xml")
	out := requireEnv(t, "CORPUS_OUT", "the directory each run's TSV is written to")
	cases := os2024Cases(t, dir)
	diag := diagnoseAll(t, cases)
	for _, audio := range audioRuns {
		t.Run(audioName(audio), func(t *testing.T) {
			s := newStandIn(t, len(cases))
			runs := runFitting(t, rigs(t, s.root, s.videos), s, cases, diag, audio)
			checkFitting(t, runs)
			writeRuns(t, out, "os2024", audio, runs)
		})
	}
}

// opusDoc is one OPUS subtitle document as SRT: its cues and, per sentence
// id, the cue its first word lies in.
type opusDoc struct {
	*harnessDoc
	sentenceCue map[string]int
}

// os2024Cases builds pairCases for every language of every movie in dir
// against the movie's English document, and one wrong-movie case per pair:
// the language's document of the next movie.
func os2024Cases(t *testing.T, dir string) []corpusCase {
	t.Helper()
	movies, err := filepath.Glob(filepath.Join(dir, "*", "en.xml"))
	if err != nil || len(movies) == 0 {
		t.Fatalf("%s holds no <movie>/en.xml", dir)
	}
	for i := range movies {
		movies[i] = filepath.Dir(movies[i])
	}
	slices.Sort(movies)
	langs := os2024Langs(t, movies)
	var cases []corpusCase
	for mi, m := range movies {
		ref := readOpus(t, filepath.Join(m, "en.xml"))
		for _, lang := range langs {
			tag, ok := language.Parse(lang)
			if !ok {
				t.Fatalf("no language %q", lang)
			}
			add := func(kind string, class caseClass, refDoc, cand []byte, truth []int64) {
				cases = append(cases, corpusCase{
					id: filepath.Base(m) + "/" + lang + "/" + kind, kind: kind, class: class,
					ref: refDoc, cand: cand, truth: truth, tag: tag, video: len(cases),
				})
			}
			inc := readOpus(t, filepath.Join(m, lang+".xml"))
			pairCases(t, add, ref.harnessDoc, inc.harnessDoc, opusPairs(t, m, lang, ref, inc))
			other := readOpus(t, filepath.Join(movies[(mi+1)%len(movies)], lang+".xml"))
			add("c_wrongMovie", classWrong, ref.doc, other.doc, nil)
		}
	}
	return cases
}

// os2024Langs is every language each movie holds besides English.
func os2024Langs(t *testing.T, movies []string) []string {
	t.Helper()
	var langs []string
	for i, m := range movies {
		docs, _ := filepath.Glob(filepath.Join(m, "*.xml"))
		var here []string
		for _, d := range docs {
			if l := strings.TrimSuffix(filepath.Base(d), ".xml"); !strings.Contains(l, "-") && l != "en" {
				here = append(here, l)
			}
		}
		slices.Sort(here)
		if i > 0 && !slices.Equal(here, langs) {
			t.Fatalf("%s holds %v, the first movie %v", m, here, langs)
		}
		langs = here
	}
	if len(langs) == 0 {
		t.Fatal("no language besides en")
	}
	return langs
}

var opusTime = regexp.MustCompile(`^T\d+[A-Z]*([SE])$`)

// readOpus reads an untokenized OPUS document: a cue runs from a start time
// to the next end time, so a sentence spanning two subtitle blocks joins
// them, and holds the text between.
func readOpus(t *testing.T, path string) *opusDoc {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	type cue struct {
		text       strings.Builder
		start, end int64
	}
	var cues []*cue
	var open *cue
	sentenceCue := map[string]int{}
	sentence, placed := "", false
	dec := xml.NewDecoder(f)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		switch tk := tok.(type) {
		case xml.StartElement:
			switch tk.Name.Local {
			case "s":
				sentence, placed = attr(tk, "id"), false
			case "time":
				m := opusTime.FindStringSubmatch(attr(tk, "id"))
				ms, ok := srtClock(attr(tk, "value"))
				switch {
				case m == nil || !ok:
					t.Fatalf("%s: time %q = %q", path, attr(tk, "id"), attr(tk, "value"))
				case m[1] == "S" && open == nil:
					open = &cue{start: ms}
					cues = append(cues, open)
				case m[1] == "E" && open != nil:
					open.end, open = ms, nil
				}
			}
		case xml.CharData:
			text := strings.TrimSpace(string(tk))
			if text == "" || open == nil {
				continue
			}
			if open.text.Len() > 0 {
				open.text.WriteByte('\n')
			}
			open.text.WriteString(text)
			if sentence != "" && !placed {
				sentenceCue[sentence], placed = len(cues)-1, true
			}
		}
	}
	// The SRT holds the timed cues with text; index is each cue's event.
	var b bytes.Buffer
	index := make([]int, len(cues))
	n := 0
	for i, c := range cues {
		index[i] = -1
		if c.text.Len() == 0 || c.end <= c.start {
			continue
		}
		index[i] = n
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, srtStamp(c.start), srtStamp(c.end), c.text.String())
	}
	for s, i := range sentenceCue {
		if index[i] < 0 {
			delete(sentenceCue, s)
			continue
		}
		sentenceCue[s] = index[i]
	}
	c, err := timing.ParseCues(b.Bytes())
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	d := &harnessDoc{doc: b.Bytes(), events: c.Events()}
	d.dialogue, d.lines = dialogueOf(c)
	if len(d.events) != n {
		t.Fatalf("%s: %d cues written, %d parsed", path, n, len(d.events))
	}
	return &opusDoc{harnessDoc: d, sentenceCue: sentenceCue}
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

var srtTime = regexp.MustCompile(`^(\d+):(\d\d):(\d\d)[,.](\d{3})$`)

func srtClock(v string) (int64, bool) {
	m := srtTime.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, false
	}
	var p [4]int64
	for i := range p {
		p[i], _ = strconv.ParseInt(m[i+1], 10, 64)
	}
	return ((p[0]*60+p[1])*60+p[2])*1000 + p[3], true
}

func srtStamp(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3_600_000, ms/60_000%60, ms/1000%60, ms%1000)
}

// opusPairs is, per alignment link with both sides, the candidate event of
// its first sentence and the offset to the reference event of its first:
// the movie's alignments all link from one pivot language, and the set
// synchronises them, so link i of pivot-en and of pivot-lang align the same
// text.
func opusPairs(t *testing.T, movie, lang string, ref, inc *opusDoc) []oraclePair {
	t.Helper()
	enSide, langSide := pivotSide(t, movie, "en"), pivotSide(t, movie, lang)
	if len(enSide) != len(langSide) {
		t.Fatalf("%s: %d en links, %d %s links", movie, len(enSide), len(langSide), lang)
	}
	var pairs []oraclePair
	for i := range enSide {
		if len(enSide[i]) == 0 || len(langSide[i]) == 0 {
			continue
		}
		r, okR := ref.sentenceCue[enSide[i][0]]
		c, okC := inc.sentenceCue[langSide[i][0]]
		if okR && okC {
			pairs = append(pairs, oraclePair{inc: c, off: ref.events[r].StartMs - inc.events[c].StartMs})
		}
	}
	return pairs
}

// pivotSide is lang's sentence ids per link of the movie's alignments: the
// target side of <pivot>-<lang>.xml, or the source side of any when lang is
// the pivot.
func pivotSide(t *testing.T, movie, lang string) [][]string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(movie, "*-*.xml"))
	if len(files) == 0 {
		t.Fatalf("%s holds no alignment", movie)
	}
	for _, f := range files {
		pivot, to, _ := strings.Cut(strings.TrimSuffix(filepath.Base(f), ".xml"), "-")
		switch lang {
		case to:
			return xcesSide(t, f, 1)
		case pivot:
			return xcesSide(t, f, 0)
		}
	}
	t.Fatalf("%s holds no alignment for %s", movie, lang)
	return nil
}

// xcesSide is one side, 0 the source and 1 the target, of every link of an
// XCES alignment, in order.
func xcesSide(t *testing.T, path string, side int) [][]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Links []struct {
			Targets string `xml:"xtargets,attr"`
		} `xml:"linkGrp>link"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	out := make([][]string, len(doc.Links))
	for i, l := range doc.Links {
		sides := strings.Split(l.Targets, ";")
		if len(sides) != 2 {
			t.Fatalf("%s: link %q", path, l.Targets)
		}
		out[i] = strings.Fields(sides[side])
	}
	return out
}
