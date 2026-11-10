//go:build corpus

package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/timing"
)

// The harness bar (AC19): the study's 1,540 of 1,657 truth-defined cases
// placed at upTo ModelPiecewise, as a share when the rebuilt set defines
// another count.
const (
	harnessDefined = 1_657
	harnessCorrect = 1_540
)

// archiveRaw serves the harness archive's files at a commit.
const archiveRaw = "https://raw.githubusercontent.com/Subtitle-Archival-Initiative/anime-subtitle-archival/"

// prepareStudy makes dir hold every manifest file: one present must match
// its hash, one absent is fetched from the archive at commit and checked
// before it is written.
func prepareStudy(t *testing.T, dir, commit string, files []harnessFile) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Minute}
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		b, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			b, err = fetchArchive(t, client, commit, f.path)
			if err == nil && digest(b) == f.sha256 {
				err = os.WriteFile(path, b, 0o600)
			}
		}
		if err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		if got := digest(b); got != f.sha256 {
			t.Fatalf("%s: SHA-256 %s, the manifest pins %s", f.name, got, f.sha256)
		}
	}
}

func fetchArchive(t *testing.T, client *http.Client, commit, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, archiveRaw+commit+"/"+(&url.URL{Path: path}).EscapedPath(), http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", req.URL, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestCorpusHarness runs the reference-sync harness (the study's synthetic
// kinds over the anime set the manifest pins, in STUDY_DIR) through the
// production aligner, audio_fallback off then on: AC19's fitting-set bars,
// and the piecewise model's mapping correct on the harness bar's share.
func TestCorpusHarness(t *testing.T) {
	dir := requireEnv(t, "STUDY_DIR", "the harness set's directory, filled from the manifest when empty")
	out := requireEnv(t, "CORPUS_OUT", "the directory each run's TSV is written to")
	commit, files, err := readHarnessManifest("harness-manifest.tsv")
	if err != nil {
		t.Fatal(err)
	}
	prepareStudy(t, dir, commit, files)
	cases := harnessCases(t, dir)
	diag := diagnoseAll(t, cases)
	defined, placed, placedLin := 0, 0, 0
	for i := range cases {
		if cases[i].truth != nil {
			defined++
			placed += b2i(diag[i].pw.bearing > 0 && diag[i].pw.share >= fitBar)
			placedLin += b2i(diag[i].lin.bearing > 0 && diag[i].lin.share >= fitBar)
		}
	}
	t.Logf("%d cases, %d truth-defined: %d placed at ModelPiecewise, %d at ModelLinear", len(cases), defined, placed, placedLin)
	if placed*harnessDefined < harnessCorrect*defined {
		t.Errorf("%d of %d truth-defined cases placed at ModelPiecewise, under %d of every %d", placed, defined, harnessCorrect, harnessDefined)
	}
	for _, audio := range audioRuns {
		t.Run(audioName(audio), func(t *testing.T) {
			s := newStandIn(t, len(cases))
			runs := runFitting(t, rigs(t, s.root, s.videos), s, cases, diag, audio)
			checkFitting(t, runs)
			writeRuns(t, out, "harness", audio, runs)
		})
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

var studySeries = []string{"apollon", "bebop", "erased", "eva", "fz", "sasami", "sg", "snk"}

// harnessDoc is one study file: its document, its events, which of them
// are dialogue, and how many are.
type harnessDoc struct {
	doc      []byte
	events   []timing.Event
	dialogue []bool
	lines    int
}

// dialogueOf marks the events subsync.DialogueSpans keeps, once per start,
// end and text, as the study's loader read only deduplicated dialogue.
func dialogueOf(c timing.Cues) ([]bool, int) {
	left := map[subsync.Span]int{}
	for _, s := range subsync.DialogueSpans(c) {
		left[s]++
	}
	type line struct {
		text       string
		start, end int64
	}
	seen := map[line]bool{}
	evs := c.Events()
	out, n := make([]bool, len(evs)), 0
	for i, e := range evs {
		sp, l := subsync.Span{StartMs: e.StartMs, EndMs: e.EndMs}, line{e.Text, e.StartMs, e.EndMs}
		if left[sp] > 0 && !seen[l] {
			left[sp]--
			seen[l], out[i] = true, true
			n++
		}
	}
	return out, n
}

// harnessCases is the study's case set (sync-study zz_study_test.go
// buildCases and zz_proto_test.go extraCases) over dir: each group 1 and 2
// episode against group 0's as pairCases builds them from the text oracle,
// then wrong episodes and shows.
func harnessCases(t *testing.T, dir string) []corpusCase {
	t.Helper()
	en, _ := language.Parse("en")
	load := func(series string, g, ep int) *harnessDoc {
		m, _ := filepath.Glob(filepath.Join(dir, fmt.Sprintf("%s__g%d__%02d.*", series, g, ep)))
		if len(m) != 1 {
			return nil
		}
		b, err := os.ReadFile(m[0])
		if err != nil {
			t.Fatal(err)
		}
		c, err := timing.ParseCues(b)
		if err != nil {
			return nil
		}
		d := &harnessDoc{doc: b, events: c.Events()}
		if d.dialogue, d.lines = dialogueOf(c); d.lines < 20 {
			return nil
		}
		return d
	}
	var cases []corpusCase
	for si, s := range studySeries {
		for ep := 1; ep <= 12; ep++ {
			ref := load(s, 0, ep)
			if ref == nil {
				continue
			}
			add := func(group, kind string, class caseClass, refDoc, cand []byte, truth []int64) {
				cases = append(cases, corpusCase{
					id: fmt.Sprintf("%s/%02d/%s/%s", s, ep, group, kind), kind: kind, class: class,
					ref: refDoc, cand: cand, truth: truth, tag: en, video: len(cases),
				})
			}
			for g := 1; g <= 2; g++ {
				inc := load(s, g, ep)
				if inc == nil {
					continue
				}
				gs := fmt.Sprintf("g%d", g)
				pairCases(t, func(kind string, class caseClass, refDoc, cand []byte, truth []int64) {
					add(gs, kind, class, refDoc, cand, truth)
				}, ref, inc, oracle(ref, inc))
			}
			if other := load(s, 1, ep%12+1); other != nil {
				add("g1", "c_wrongEpisode", classWrong, ref.doc, other.doc, nil)
			}
			if other := load(s, 2, (ep+5)%12+1); other != nil {
				add("g2", "c_wrongEpisode", classWrong, ref.doc, other.doc, nil)
			}
			oser := studySeries[(si+3)%len(studySeries)]
			if other := load(oser, 1, ep); other != nil {
				add("x:"+oser, "c_wrongShow", classWrong, ref.doc, other.doc, nil)
			}
		}
	}
	return cases
}

// caseSink adds one case of a pair.
type caseSink func(kind string, class caseClass, refDoc, cand []byte, truth []int64)

// pairCases adds the study's cases for inc against ref, whose events pairs
// match: natural, and, when the pairs fit one offset, inc moved onto ref and
// damaged. Every damaged document is the production Render of a Timing.
func pairCases(t *testing.T, add caseSink, ref, inc *harnessDoc, pairs []oraclePair) {
	t.Helper()
	truth := localTruth(inc, pairs)
	defined := len(truth) - countOf(truth, noTruth)
	if 2*defined < inc.lines {
		add("natural_untruthed", classSameCut, ref.doc, inc.doc, nil)
	} else {
		add("natural", classSameCut, ref.doc, inc.doc, truth)
	}
	mode, frac, n := oracleSummary(pairs)
	if n < 30 || frac < 0.85 || 10*defined < 7*inc.lines {
		return
	}
	a, from := render(t, inc.doc, shiftOf(mode))
	tA := make([]int64, len(from))
	evA := events(t, a)
	for k, i := range from {
		tA[k] = noTruth
		if truth[i] != noTruth {
			tA[k] = evA[k].StartMs
		}
	}
	damage(t, add, ref, a, tA, true)
}

// damage adds the study's damaged cases of a, a candidate on ref's timeline
// whose truth is tA: shifts, rate changes, a sparse copy, and the cuts and
// breaks the video lacks. videoSide adds the cuts the reference loses, which
// a real video cannot.
func damage(t *testing.T, add caseSink, ref *harnessDoc, a []byte, tA []int64, videoSide bool) {
	t.Helper()
	refEnd := ref.events[len(ref.events)-1].EndMs
	evA := events(t, a)
	damaged := func(kind string, class caseClass, tm timing.Timing) {
		d, from := render(t, a, tm)
		add(kind, class, ref.doc, d, pick(tA, from))
	}
	damaged("d_fits", classSameCut, timing.Identity{})
	for _, sh := range []int64{3200, -11700, 62000} {
		damaged(fmt.Sprintf("a_shift%+d", sh), classSameCut, shiftOf(sh))
	}
	for _, fr := range []struct {
		name string
		k    float64
	}{{"a_fps25to23976", 25.0 / (24000.0 / 1001.0)}, {"a_fps23976to25", (24000.0 / 1001.0) / 25.0}} {
		damaged(fr.name, classSameCut, timing.Linear{Scale: fr.k})
		damaged(fr.name+"_shift2000", classSameCut, timing.Linear{Scale: fr.k, OffsetMs: 2000})
	}
	damaged("e_sparse4_shift3200", classSameCut, everyFourth(evA, 3200))
	// The video lacks scenes the subtitle has: the reference loses them,
	// and the truth moves with what follows.
	videoLacks := func(kind string, cuts []span) {
		if !videoSide {
			return
		}
		r, _ := render(t, ref.doc, removal(cuts))
		tr := make([]int64, len(tA))
		for k, v := range tA {
			tr[k] = noTruth
			if v != noTruth {
				tr[k] = throughCuts(v, cuts)
			}
		}
		add(kind, classCut, r, a, tr)
	}
	videoLacks("b_videoLacks90s", []span{{at(refEnd, 0.45), 90_000}})
	damaged("b_subLacks90s", classCut, removal([]span{{at(refEnd, 0.45), 90_000}}))
	videoLacks("b_videoLacks3scenes", []span{{at(refEnd, 0.25), 45_000}, {at(refEnd, 0.55), 60_000}, {at(refEnd, 0.8), 30_000}})
	damaged("b_subLacks20s", classCut, removal([]span{{at(refEnd, 0.5), 20_000}}))
	damaged("f_adBreaks3x120s", classCut, adBreaks(refEnd))
	damaged("f_subLacksRecap60s", classCut, removal([]span{{30_000, 60_000}}))
	videoLacks("f_videoLacksRecap60s", []span{{30_000, 60_000}})
}

func countOf(s []int64, v int64) int {
	n := 0
	for _, x := range s {
		if x == v {
			n++
		}
	}
	return n
}

func at(endMs int64, frac float64) int64 { return int64(float64(endMs) * frac) }

// span is [at, at+length) ms.
type span struct{ at, length int64 }

// removal removes the cues starting inside each cut, sorted and disjoint,
// and pulls later cues earlier by the cuts before them.
func removal(cuts []span) timing.Timing {
	var segs []timing.Segment
	var from, shift int64
	for _, c := range cuts {
		segs = append(segs,
			timing.Segment{FromMs: from, ToMs: c.at, OffsetMs: -shift},
			timing.Segment{FromMs: c.at, ToMs: c.at + c.length, Drop: true})
		from, shift = c.at+c.length, shift+c.length
	}
	return timing.Piecewise{Scale: 1, Segments: append(segs, timing.Segment{FromMs: from, OffsetMs: -shift})}
}

// throughCuts maps a time onto the timeline cuts removed, noTruth inside one.
func throughCuts(ms int64, cuts []span) int64 {
	var shift int64
	for _, c := range cuts {
		if ms >= c.at && ms < c.at+c.length {
			return noTruth
		}
		if ms >= c.at+c.length {
			shift += c.length
		}
	}
	return ms - shift
}

// adBreaks inserts three 120 s breaks, at a quarter, half and three
// quarters of endMs, that the video lacks.
func adBreaks(endMs int64) timing.Timing {
	segs := []timing.Segment{{FromMs: 0, ToMs: at(endMs, 0.25)}}
	for i, f := range []float64{0.25, 0.5, 0.75} {
		to := int64(0)
		if i < 2 {
			to = at(endMs, f+0.25)
		}
		segs = append(segs, timing.Segment{FromMs: at(endMs, f), ToMs: to, OffsetMs: int64(i+1) * 120_000})
	}
	return timing.Piecewise{Scale: 1, Segments: segs}
}

// everyFourth keeps every fourth distinct start of evs, shifted by ms.
func everyFourth(evs []timing.Event, ms int64) timing.Timing {
	starts := make([]int64, 0, len(evs))
	for _, e := range evs {
		starts = append(starts, e.StartMs)
	}
	slices.Sort(starts)
	starts = slices.Compact(starts)
	var segs []timing.Segment
	for k := range starts {
		from, to := starts[k], int64(0)
		if k == 0 {
			from = 0
		}
		if k+1 < len(starts) {
			to = starts[k+1]
		}
		keep := k%4 == 0
		if n := len(segs); n > 0 && !keep && segs[n-1].Drop {
			segs[n-1].ToMs = to
			continue
		}
		seg := timing.Segment{FromMs: from, ToMs: to, Drop: !keep}
		if keep {
			seg.OffsetMs = ms
		}
		segs = append(segs, seg)
	}
	return timing.Piecewise{Scale: 1, Segments: segs}
}

// shiftOf is a constant offset of ms in canonical form.
func shiftOf(ms int64) timing.Timing {
	if ms == 0 {
		return timing.Identity{}
	}
	return timing.Shift{Ms: ms}
}

// render is doc under tm through timing.Render and, per event of the
// result, the index of the doc event it came from: Render keeps the order
// and the text, so each output event is the next input event with its text
// at its mapped start.
func render(t *testing.T, doc []byte, tm timing.Timing) ([]byte, []int) {
	t.Helper()
	c, err := timing.ParseCues(doc)
	if err != nil {
		t.Fatal(err)
	}
	b, err := timing.Render(c, tm)
	if err != nil {
		t.Fatalf("Render under %#v: %v", tm, err)
	}
	in, out := c.Events(), events(t, b)
	from := make([]int, 0, len(out))
	for i, e := range in {
		k := len(from)
		if k == len(out) {
			break
		}
		want, kept := placedAt(tm, e.StartMs)
		if kept && out[k].Text == e.Text && abs(out[k].StartMs-max(want, 0)) <= 10 {
			from = append(from, i)
		}
	}
	if len(from) != len(out) {
		t.Fatalf("Render's %d events follow %d of the input's", len(out), len(from))
	}
	return b, from
}

func events(t *testing.T, doc []byte) []timing.Event {
	t.Helper()
	c, err := timing.ParseCues(doc)
	if err != nil {
		t.Fatal(err)
	}
	return c.Events()
}

func pick(truth []int64, from []int) []int64 {
	out := make([]int64, len(from))
	for k, i := range from {
		out[k] = truth[i]
	}
	return out
}

// The study's text oracle (sync-study zz_load_test.go): cue pairs matched
// by word Jaccard at least 0.5 with a 0.2 margin, independent of the
// aligner.
var (
	wordRe = regexp.MustCompile(`[a-z']{3,}`)
	tagRe  = regexp.MustCompile(`\{[^}]*\}|<[^>]*>|\\N|\\n`)
	stop   = map[string]bool{
		"the": true, "and": true, "you": true, "that": true, "this": true, "what": true, "are": true, "for": true, "with": true,
		"have": true, "was": true, "not": true, "but": true, "it's": true, "don't": true, "i'm": true, "your": true, "just": true,
		"can": true, "all": true,
	}
)

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range wordRe.FindAllString(strings.ToLower(tagRe.ReplaceAllString(s, " ")), -1) {
		if !stop[w] {
			m[w] = true
		}
	}
	return m
}

// oraclePair is a candidate event matched to a reference event and the
// offset, reference minus candidate.
type oraclePair struct {
	inc int
	off int64
}

// oracle matches inc's dialogue to ref's.
func oracle(ref, inc *harnessDoc) []oraclePair {
	rw := make([]map[string]bool, len(ref.events))
	for i, e := range ref.events {
		if ref.dialogue[i] {
			rw[i] = words(e.Text)
		}
	}
	var pairs []oraclePair
	for i, e := range inc.events {
		iw := words(e.Text)
		if !inc.dialogue[i] || len(iw) < 2 {
			continue
		}
		best, second, bestJ := 0.0, 0.0, -1
		for j := range rw {
			if len(rw[j]) < 2 {
				continue
			}
			inter := 0
			for w := range iw {
				if rw[j][w] {
					inter++
				}
			}
			if inter < 2 {
				continue
			}
			switch jac := float64(inter) / float64(len(iw)+len(rw[j])-inter); {
			case jac > best:
				second, best, bestJ = best, jac, j
			case jac > second:
				second = jac
			}
		}
		if bestJ >= 0 && best >= 0.5 && best-second >= 0.2 {
			pairs = append(pairs, oraclePair{inc: i, off: ref.events[bestJ].StartMs - e.StartMs})
		}
	}
	return pairs
}

// oracleSummary is the pairs' modal offset, the share within 1 s of it, and
// their count.
func oracleSummary(p []oraclePair) (mode int64, frac float64, n int) {
	if len(p) == 0 {
		return 0, 0, 0
	}
	h := map[int64]int{}
	for _, x := range p {
		b := int64(math.Round(float64(x.off) / 200))
		h[b]++
		h[b-1]++
		h[b+1]++
	}
	var bestB int64
	bestC := -1
	for _, k := range slices.Sorted(maps.Keys(h)) {
		if h[k] > bestC {
			bestC, bestB = h[k], k
		}
	}
	var in []int64
	for _, x := range p {
		if abs(x.off-bestB*200) <= 1000 {
			in = append(in, x.off)
		}
	}
	slices.Sort(in)
	mode = in[len(in)/2]
	c := 0
	for _, x := range p {
		if abs(x.off-mode) <= 1000 {
			c++
		}
	}
	return mode, float64(c) / float64(len(p)), len(p)
}

// localTruth is, per candidate dialogue event, its start plus the median
// offset of the 9 nearest matched pairs within 90 s, kept where at least 5
// are and their median deviation is at most 400 ms.
func localTruth(doc *harnessDoc, pairs []oraclePair) []int64 {
	inc := doc.events
	out := make([]int64, len(inc))
	for i := range out {
		out[i] = noTruth
	}
	if len(pairs) < 5 {
		return out
	}
	type near struct{ dist, off int64 }
	ds := make([]near, len(pairs))
	for i := range inc {
		if !doc.dialogue[i] {
			continue
		}
		at := inc[i].StartMs
		for k, p := range pairs {
			ds[k] = near{abs(inc[p.inc].StartMs - at), p.off}
		}
		slices.SortStableFunc(ds, func(a, b near) int { return int(a.dist - b.dist) })
		var offs []int64
		for _, x := range ds[:min(9, len(ds))] {
			if x.dist <= 90_000 {
				offs = append(offs, x.off)
			}
		}
		if len(offs) < 5 {
			continue
		}
		slices.Sort(offs)
		med := offs[len(offs)/2]
		dev := make([]int64, len(offs))
		for k, o := range offs {
			dev[k] = abs(o - med)
		}
		slices.Sort(dev)
		if dev[len(dev)/2] > 400 {
			continue
		}
		out[i] = at + med
	}
	return out
}
