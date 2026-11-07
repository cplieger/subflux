//go:build corpus

package subsync

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

// TestPiecewiseEqualsMonoDPOnCutStudy runs the production DP and the study's
// monoDP on every pair of the cut study's plan (CUT_PLAN: a JSON list of
// {name, ref, cand} paths) on the production grid.
func TestPiecewiseEqualsMonoDPOnCutStudy(t *testing.T) {
	plan := os.Getenv("CUT_PLAN")
	if plan == "" {
		t.Skip("CUT_PLAN names the cut study's plan")
	}
	data, err := os.ReadFile(plan)
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, Ref, Cand string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	load := func(t *testing.T, path string) []Span {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		c, err := timing.ParseCues(b)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return sortSpans(DialogueSpans(c))
	}
	g := productionGrid
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			ref, inc := load(t, c.Ref), load(t, c.Cand)
			if maxDuration(inc) > g.hi {
				t.Skipf("a cue longer than %d ms: the DP clamps its end, monoDP does not", g.hi)
			}
			p := productionMono(splitPenaltyPer * float64(min(len(ref), len(inc))))
			_, want := monoDP(ref, inc, g.lo, g.hi, p)
			got, obj, err := newDP(g, ref).run(t.Context(), inc, p.Penalty)
			if err != nil {
				t.Fatal(err)
			}
			if !closeTo(obj, want) {
				t.Fatalf("objective %v, monoDP %v", obj, want)
			}
			if score, err := monoObjective(ref, inc, got, g.lo, g.hi, p); err != nil || !closeTo(score, want) {
				t.Fatalf("the DP's assignment scores %v (%v), objective %v", score, err, want)
			}
		})
	}
}
