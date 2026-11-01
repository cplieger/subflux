package language

import "testing"

// TestNoAutomaticGradeCrossesScripts: over every pair of corpus tags, a grade
// whose target does not cover the offer's script is never automatic, with the
// switch off or on.
func TestNoAutomaticGradeCrossesScripts(t *testing.T) {
	tags := corpusTags(t)
	for _, r := range gradeTable {
		tags = append(tags, mustParse(t, r.target), mustParse(t, r.have))
	}
	on := Policy{&policy{closeVarieties: true}}
	for _, target := range tags {
		for _, have := range tags {
			g := on.Grade(target, have)
			if !target.t.CoversScript(have.t) && (on.Automatic(g) || Policy{}.Automatic(g)) {
				t.Errorf("Grade(%q, %q) = %+v is automatic across scripts", target, have, g)
			}
		}
	}
}
