package corpus

import (
	"bufio"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/timing"
)

// cutExpect is what the corpus gate requires of one cut-study pair.
type cutExpect uint8

const (
	// expectDifferentCut: a forward pair, the subtitle holding material the
	// video lacks, ends ErrDifferentCut with no result and no audio run.
	expectDifferentCut cutExpect = iota + 1
	// expectRefuse: a must-refuse pair is never delivered, at any verdict.
	expectRefuse
	// expectWrongFilm: a subtitle of another film is never Verified.
	expectWrongFilm
	// expectExcluded: the study found the pair's truth inconsistent; it is
	// not run.
	expectExcluded
)

var expectNames = map[string]cutExpect{
	"different_cut": expectDifferentCut, "refuse": expectRefuse, "wrong_film": expectWrongFilm, "excluded": expectExcluded,
}

// cutSides are the cuts a side of a pair can hold.
var cutSides = []string{"shorter", "longer", "reedit", "film", "other_film"}

// cutPair is one committed cut-study pair: an opaque id, the cut the video
// and the subtitle each hold, and the gate's expectation.
type cutPair struct {
	id, video, subtitle string
	expect              cutExpect
}

// readCutPlan reads the committed plan, refusing an unknown column value or
// a repeated pair.
func readCutPlan(path string) ([]cutPair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() || sc.Text() != "pair\tvideo\tsubtitle\texpect" {
		return nil, fmt.Errorf("%s: missing the header line", path)
	}
	var plan []cutPair
	seen := map[string]bool{}
	for line := 2; sc.Scan(); line++ {
		cols := strings.Split(sc.Text(), "\t")
		if len(cols) != 4 {
			return nil, fmt.Errorf("%s:%d: %d columns, want 4", path, line, len(cols))
		}
		expect, ok := expectNames[cols[3]]
		switch {
		case !ok:
			return nil, fmt.Errorf("%s:%d: unknown expectation %q", path, line, cols[3])
		case !slices.Contains(cutSides, cols[1]) || !slices.Contains(cutSides, cols[2]):
			return nil, fmt.Errorf("%s:%d: unknown cut %q or %q", path, line, cols[1], cols[2])
		case seen[cols[0]]:
			return nil, fmt.Errorf("%s:%d: pair %s repeated", path, line, cols[0])
		}
		seen[cols[0]] = true
		plan = append(plan, cutPair{id: cols[0], video: cols[1], subtitle: cols[2], expect: expect})
	}
	return plan, sc.Err()
}

// decisions are the two fit-fact cases every pair is planned under: an
// unflagged pair and one whose facts flag a different cut.
func decisions(audioFallback bool) (unflagged, flagged timing.Decision) {
	cfg := timing.Config{Sync: true, AudioFallback: audioFallback}
	return timing.Plan(rank.Facts{}, language.Standard, cfg), timing.Plan(rank.Facts{Cut: rank.Differs}, language.Standard, cfg)
}

// cutRun is one run of the cut-study gate: a pair synced under one decision.
type cutRun struct {
	pair     cutPair
	decision timing.Decision
}

// cutRuns expands plan into its runs: every pair not excluded under both
// decisions, so each gate holds unflagged and flagged.
func cutRuns(plan []cutPair, audioFallback bool) []cutRun {
	unflagged, flagged := decisions(audioFallback)
	var runs []cutRun
	for _, p := range plan {
		if p.expect == expectExcluded {
			continue
		}
		for _, d := range []timing.Decision{unflagged, flagged} {
			runs = append(runs, cutRun{pair: p, decision: d})
		}
	}
	return runs
}

func TestCutStudyPlanRunsBothActions(t *testing.T) {
	t.Parallel()
	plan, err := readCutPlan("testdata/cutstudy-plan.tsv")
	if err != nil {
		t.Fatal(err)
	}
	count := map[cutExpect]int{}
	for _, p := range plan {
		count[p.expect]++
	}
	// The cut study's corpus (its REPORT section 1): 32 forward pairs, 30
	// reversed or must-refuse and 2 re-edits, 33 wrong films, 4 excluded.
	if count[expectDifferentCut] != 32 || count[expectRefuse] != 32 || count[expectWrongFilm] != 33 || count[expectExcluded] != 4 {
		t.Errorf("plan holds %d forward, %d must-refuse, %d wrong-film, %d excluded pairs, want 32, 32, 33, 4",
			count[expectDifferentCut], count[expectRefuse], count[expectWrongFilm], count[expectExcluded])
	}
	for audio, unflagged := range map[bool]timing.Action{false: timing.ActionReference, true: timing.ReferenceThenAudio} {
		actions := map[string][]timing.Action{}
		for _, r := range cutRuns(plan, audio) {
			actions[r.pair.id] = append(actions[r.pair.id], r.decision.Action)
		}
		for _, p := range plan {
			got := actions[p.id]
			slices.Sort(got)
			want := []timing.Action{unflagged, timing.RigidReference}
			if p.expect == expectExcluded {
				want = nil
			}
			if !slices.Equal(got, want) {
				t.Errorf("audio_fallback %v: pair %s (%s on %s) runs under %v, want %v", audio, p.id, p.subtitle, p.video, got, want)
			}
		}
	}
}

func TestReadCutPlanRefuses(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"no header":         "abc\tshorter\tlonger\trefuse\n",
		"a short line":      "pair\tvideo\tsubtitle\texpect\nabc\tshorter\trefuse\n",
		"an unknown expect": "pair\tvideo\tsubtitle\texpect\nabc\tshorter\tlonger\tdeliver\n",
		"an unknown cut":    "pair\tvideo\tsubtitle\texpect\nabc\tdirector\tlonger\trefuse\n",
		"a repeated pair":   "pair\tvideo\tsubtitle\texpect\nabc\tshorter\tlonger\trefuse\nabc\tlonger\tshorter\trefuse\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := t.TempDir() + "/plan.tsv"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if plan, err := readCutPlan(path); err == nil {
				t.Errorf("readCutPlan = %v, nil; want a refusal", plan)
			}
		})
	}
}
