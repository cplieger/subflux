//go:build corpus

package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/timing"
)

// cutMedia is one pair of the user's CUT_PLAN: the cut study's name for it,
// the subtitle standing in for the video's own track, and the candidate.
type cutMedia struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
	Cand string `json:"cand"`
}

// pairID is the committed plan's id for a cut-study pair name.
func pairID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])[:12]
}

// cutCases joins plan to the media at path by pair id: every pair the plan
// runs needs its media, and media the plan does not name means the plan is
// stale. The candidate's language is the name's last part, after the
// ordinal a wrong-film pair carries.
func cutCases(t *testing.T, plan []cutPair, path string) map[string]*corpusCase {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var media []cutMedia
	if err := json.Unmarshal(b, &media); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	expect := map[string]cutExpect{}
	for _, p := range plan {
		expect[p.id] = p.expect
	}
	cases := map[string]*corpusCase{}
	for _, m := range media {
		id := pairID(m.Name)
		e, ok := expect[id]
		switch {
		case !ok:
			t.Fatalf("%s: pair %s (%s) is not in the committed plan", path, id, m.Name)
		case cases[id] != nil:
			t.Fatalf("%s: pair %s (%s) repeated", path, id, m.Name)
		case e == expectExcluded:
			continue
		}
		tag, ok := language.Parse(strings.TrimLeft(m.Name[strings.LastIndexByte(m.Name, '/')+1:], "0123456789"))
		if !ok {
			t.Fatalf("%s: no language in pair name %q", path, m.Name)
		}
		c := &corpusCase{id: id, kind: m.Name, tag: tag}
		if c.ref, err = os.ReadFile(m.Ref); err != nil {
			t.Fatal(err)
		}
		if c.cand, err = os.ReadFile(m.Cand); err != nil {
			t.Fatal(err)
		}
		cases[id] = c
	}
	for _, p := range plan {
		if p.expect != expectExcluded && cases[p.id] == nil {
			t.Fatalf("%s holds no media for pair %s (%s on %s)", path, p.id, p.subtitle, p.video)
		}
	}
	return cases
}

var cutExpectNames = map[cutExpect]string{expectDifferentCut: "different_cut", expectRefuse: "refuse", expectWrongFilm: "wrong_film"}

// TestCorpusCutStudy runs every cut-study pair of the committed plan, its
// subtitles from CUT_PLAN, through the production aligner unflagged and
// flagged, audio_fallback off then on: each forward pair ends ErrDifferentCut
// with no result and no audio, no must-refuse pair is delivered at any
// verdict, and no wrong film is Verified.
func TestCorpusCutStudy(t *testing.T) {
	media := requireEnv(t, "CUT_PLAN", "a JSON list of the cut study's pairs, {name, ref, cand} with subtitle paths")
	out := requireEnv(t, "CORPUS_OUT", "the directory each run's TSV is written to")
	plan, err := readCutPlan("testdata/cutstudy-plan.tsv")
	if err != nil {
		t.Fatal(err)
	}
	byID := cutCases(t, plan, media)
	var cases []corpusCase
	for _, p := range plan {
		if c := byID[p.id]; c != nil {
			c.video = len(cases)
			cases = append(cases, *c)
		}
	}
	diag := diagnoseAll(t, cases)
	at := map[string]int{}
	for i := range cases {
		at[cases[i].id] = i
	}
	for _, audio := range audioRuns {
		t.Run(audioName(audio), func(t *testing.T) {
			s := newStandIn(t, len(cases))
			rs := rigs(t, s.root, s.videos)
			byPair := make([][]cutRun, len(cases))
			for _, cr := range cutRuns(plan, audio) {
				i := at[cr.pair.id]
				byPair[i] = append(byPair[i], cr)
			}
			runs := make([][]caseRun, len(cases))
			// A pair's runs share its video, so one worker runs them in turn.
			parallel(t, len(cases), func(w, i int) error {
				c := &cases[i]
				for _, cr := range byPair[i] {
					run := caseRun{c: c, action: cr.decision.Action, expected: cutExpectNames[cr.pair.expect], extra: cr.pair.subtitle + "_on_" + cr.pair.video, diag: diag[i]}
					var err error
					if run.out, err = rs[w].alignStandIn(t.Context(), s, i, c, cr.decision, audio); err != nil {
						return err
					}
					checkCut(t, &run, cr.pair.expect)
					runs[i] = append(runs[i], run)
				}
				return nil
			})
			writeRuns(t, out, "cutstudy", audio, slices.Concat(runs...))
		})
	}
}

// checkCut asserts AC19's bar for one cut-study run.
func checkCut(t *testing.T, r *caseRun, e cutExpect) {
	t.Helper()
	o := &r.out
	switch e {
	case expectDifferentCut:
		if o.refusal != "different_cut" || o.aligned.Timing != nil || o.aligned.Outcome != (timing.Outcome{}) || o.pcm != 0 {
			t.Errorf("forward pair %s (%s) under %s: refusal %q, timing %v, %d PCM calls; want different_cut, no result, no audio",
				r.c.id, r.c.kind, text(r.action), o.refusal, o.aligned.Timing, o.pcm)
		}
	case expectRefuse:
		if o.delivered() {
			t.Errorf("must-refuse pair %s (%s) under %s delivered %s by %s at %.3f",
				r.c.id, r.c.kind, text(r.action), text(o.aligned.Outcome.Verdict), text(o.aligned.Outcome.Method), o.aligned.Outcome.Confidence)
		}
	case expectWrongFilm:
		if o.delivered() && o.aligned.Outcome.Verdict == timing.Verified {
			t.Errorf("wrong film %s (%s) under %s Verified at %.3f", r.c.id, r.c.kind, text(r.action), o.aligned.Outcome.Confidence)
		}
	}
}
