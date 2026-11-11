package search_test

import (
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/searchtest"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

func TestManualPackNeverRefusedByCount(t *testing.T) {
	const rel = "Futurama.S02.1080p.WEB-DL.DDP5.1.H.264-FLUX"
	pack := addic7ed()
	for _, c := range []struct {
		align   searchtest.Result
		name    string
		cause   string
		outcome status.Outcome
	}{
		{name: "the own order's member is delivered", align: alignedAs(t, timing.Verified), outcome: status.Succeeded},
		{name: "its timing refusal is another episode", align: refusedCut, outcome: status.Failed, cause: search.RefusalDifferentEpisode},
	} {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				r.provider("p", "en").answer(nil, packCand(rel)).serve(rel, nil, zipOf(t, pack...))
				inst := r.seedFuturama(5)
				e := r.engine()
				h := handleOf(t, r.candidates(e, inst, "en"))
				rec := r.script(c.align)
				j := r.downloaded(e, h, language.Standard)
				if j.Outcome != c.outcome || j.Cause != c.cause {
					t.Errorf("job %v %q, want %v %q", j.Outcome, j.Cause, c.outcome, c.cause)
				}
				if got := alignedMembers(rec, pack); !slices.Equal(got, []string{pack[4]}) {
					t.Errorf("aligned %q, want the member the series' own order names, 2x05", got)
				}
			})
		})
	}
}
