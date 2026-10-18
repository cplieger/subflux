package language

import "github.com/cplieger/langtag/v3"

const unknownScript = "Zzzz"

// Grade judges have as an offer for target: how close it is and whether it
// may serve automatically, ignoring the close-varieties switch (Automatic
// applies it). Either tag zero grades Unrelated.
func (Policy) Grade(target, have Tag) Grade {
	if target == (Tag{}) || have == (Tag{}) {
		return Grade{Closeness: ClosenessUnrelated, Class: ClassUnrelated, Reason: "No language tag"}
	}
	pref := langtag.Prefer(target.t)
	c := closenessOf(pref.Compare(have.t))
	coversScript, coversRegion := target.t.CoversScript(have.t), target.t.CoversRegion(have.t)
	return Grade{
		Closeness: c,
		Class:     classOf(c, coversScript, coversRegion),
		Reason:    reasonOf(pref, c, target, have, coversScript, coversRegion),
	}
}

// Automatic reports whether a grade lets subflux take the offer without a
// person: Exact always, Substitute while close varieties are on.
func (p Policy) Automatic(g Grade) bool {
	return g.Class == Exact || g.Class == Substitute && p.CloseVarieties()
}

// closenessOf maps a langtag tier onto Closeness, OtherScript after
// Intelligible; shared literacy is Unrelated.
func closenessOf(t langtag.Tier) Closeness {
	switch t {
	case langtag.TierIdentical:
		return Identical
	case langtag.TierSameLanguage:
		return SameLanguage
	case langtag.TierOtherRegion:
		return OtherRegion
	case langtag.TierIntelligible:
		return Intelligible
	case langtag.TierOtherScript:
		return OtherScript
	}
	return ClosenessUnrelated
}

// classOf is the only place a closeness becomes a class: no automatic class
// crosses a script, and Exact also needs the offer to state the target's
// region or one inside it.
func classOf(c Closeness, coversScript, coversRegion bool) Class {
	switch {
	case c == ClosenessUnrelated:
		return ClassUnrelated
	case c == OtherScript || !coversScript:
		return ManualOnly
	case c <= SameLanguage && coversRegion:
		return Exact
	}
	return Substitute
}

func reasonOf(pref langtag.Preference, c Closeness, target, have Tag, coversScript, coversRegion bool) string {
	switch {
	case c == ClosenessUnrelated:
		return "Different language"
	case c == OtherScript || !coversScript:
		switch {
		case target.t.Script() == unknownScript || have.t.Script() == unknownScript:
			return "Script unknown"
		case have.t.Language() == target.t.Language() && have.t.StatedScript() == "":
			return "No script stated"
		}
		return "Other script"
	}
	if r, ok := pref.Reason(have.t); ok {
		return r
	}
	if !coversRegion || c == OtherRegion {
		if statesRegion(have.t) {
			return "Other region"
		}
		return "No region stated"
	}
	return "Same language"
}

// statesRegion reports whether t states a region langtag places: a tag stating
// one fails to cover its own region-less form, while ZZ and the user-assigned
// codes count as none (langtag exports no direct predicate).
func statesRegion(t langtag.Tag) bool {
	if t.Region() == "" {
		return false
	}
	bare, ok := langtag.Parse(compose(subtag(t), t.StatedScript(), ""))
	return ok && !t.CoversRegion(bare)
}
