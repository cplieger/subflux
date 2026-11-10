//go:build corpus

package corpus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/timing"
)

// TestCorpusDevBox runs the dev-box corpus in SUBFLUX_CORPUS_DIR, the
// manifest's videos with their own reference track and audio and a track
// of the same video in another language as the candidate, through the
// production aligner and the host's ffmpeg, audio_fallback off then on:
// AC19's fitting-set bars. The candidate is timed to its video, so its own
// times are the truth.
func TestCorpusDevBox(t *testing.T) {
	dir := requireEnv(t, "SUBFLUX_CORPUS_DIR", "the dev-box corpus the manifest names")
	out := requireEnv(t, "CORPUS_OUT", "the directory each run's TSV is written to")
	rows, err := readDevBoxManifest("manifest.tsv")
	if err != nil {
		t.Fatal(err)
	}
	videos := make([]string, len(rows))
	for i, r := range rows {
		for _, f := range [...]struct{ name, sha256 string }{{r.video(), r.videoSHA}, {r.refFile(), r.refSHA}, {r.candFile(), r.candSHA}} {
			b, err := os.ReadFile(filepath.Join(dir, f.name))
			if err != nil {
				t.Fatal(err)
			}
			if got := digest(b); got != f.sha256 {
				t.Fatalf("%s: SHA-256 %s, the manifest pins %s", f.name, got, f.sha256)
			}
		}
		videos[i] = r.video()
	}
	cases := devBoxCases(t, dir, rows)
	diag := diagnoseAll(t, cases)
	for _, audio := range audioRuns {
		t.Run(audioName(audio), func(t *testing.T) {
			runs := runFitting(t, rigs(t, dir, videos), nil, cases, diag, audio)
			checkFitting(t, runs)
			writeRuns(t, out, "devbox", audio, runs)
		})
	}
}

// devBoxCases is, per manifest row, the candidate as is and damaged on its
// side (the video stays as recorded), and one wrong-content case: the
// candidate of the next row whose language differs from this reference's.
func devBoxCases(t *testing.T, dir string, rows []devBoxRow) []corpusCase {
	t.Helper()
	read := func(name string) *harnessDoc {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		c, err := timing.ParseCues(b)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		d := &harnessDoc{doc: b, events: c.Events()}
		d.dialogue, d.lines = dialogueOf(c)
		return d
	}
	parse := func(code string) language.Tag {
		tag, ok := language.Parse(code)
		if !ok {
			t.Fatalf("no language %q", code)
		}
		return tag
	}
	var cases []corpusCase
	for i, r := range rows {
		ref, inc := read(r.refFile()), read(r.candFile())
		if min(ref.lines, inc.lines) < 20 {
			t.Fatalf("case %s: %d and %d dialogue lines, under the corpus floor of 20", r.id, ref.lines, inc.lines)
		}
		tag := parse(r.candLang)
		add := func(kind string, class caseClass, refDoc, cand []byte, truth []int64) {
			cases = append(cases, corpusCase{id: r.id + "/" + kind, kind: kind, class: class, ref: refDoc, cand: cand, truth: truth, tag: tag, video: i})
		}
		truth := make([]int64, len(inc.events))
		for k, e := range inc.events {
			truth[k] = noTruth
			if inc.dialogue[k] {
				truth[k] = e.StartMs
			}
		}
		add("natural", classSameCut, ref.doc, inc.doc, truth)
		damage(t, add, ref, inc.doc, truth, false)
		for k := 1; k < len(rows); k++ {
			o := rows[(i+k)%len(rows)]
			if o.candLang != r.refLang {
				tag = parse(o.candLang)
				add("c_wrongVideo", classWrong, ref.doc, read(o.candFile()).doc, nil)
				break
			}
		}
	}
	return cases
}
