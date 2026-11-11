package search

import (
	"slices"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/release"
)

// offer is one candidate an ask returned, as far as the pair it is judged
// for does not matter.
type offer struct {
	ask   *ask
	from  *bound
	cand  *provider.Candidate
	order int
	key   [32]byte
	facts rank.Facts
	fit   rank.Fit
	ident identity
	// packRefused is why an automatic run refused its pack, None for none.
	packRefused WhyNot
	tried       bool
}

// judged is an offer judged for one wanted pair (8.3).
type judged struct {
	*offer
	grade    language.Grade
	standing standing
	// why is the first manual-only check it fails, None when automation
	// may take it.
	why WhyNot
	// usable: it passes every automatic check, the attempt memory and the
	// upgrade gain included.
	usable bool
}

// pick is one wanted pair's ranked candidates.
type pick struct {
	want *want
	// ranked are the usable candidates in rank order.
	ranked []*judged
	// skip is why nothing ranked, for a missing pair only: None when its
	// best-graded candidate failed only the attempt memory, so the pair's
	// earlier refusal still says why.
	skip WhyNot
}

// offers reads every answer's candidates once: identity, facts, fit and
// whether the attempt memory holds them.
func (r *run) offers(answers []answer) []*offer {
	var out []*offer
	for i := range answers {
		a := &answers[i]
		for j := range a.cands {
			c := &a.cands[j]
			ident, byNumber := identify(&r.subject, c)
			ok := ident == identityOK
			facts := rank.Compare(r.video, r.fps, release.Parse(c.Release, r.subject.kind), c.FPS, c.MatchedBy, ok)
			facts.EpisodeByNumber = byNumber
			key := attemptKey(a.ask.from.entry.ID, c.Ref)
			out = append(out, &offer{
				ask: a.ask, from: a.ask.from, cand: c, order: j, ident: ident, facts: facts,
				fit: rank.FitOf(facts), key: key, tried: r.tried(key), packRefused: r.packRefused(key),
			})
		}
	}
	return out
}

// tried reports whether any item's attempt memory holds key.
func (r *run) tried(key [32]byte) bool {
	return slices.ContainsFunc(r.items, func(it runItem) bool { return it.history.tried[key] })
}

// packRefused is the newest pack refusal any item's attempt memory holds
// for key, None for none.
func (r *run) packRefused(key [32]byte) WhyNot {
	for i := range r.items {
		if why, ok := r.items[i].history.packRefused[key]; ok {
			return why
		}
	}
	return None
}

// judge judges o for w; false drops it from the pair: a language its
// provider was not asked for (C1-F5), a contradicted identity, flags not
// serving the variant or an unrelated language.
func (e *Engine) judge(w *want, o *offer) (judged, bool) {
	c := o.cand
	if o.ident == identityContradicted || !c.Flags.Serves(w.variant) || !slices.Contains(o.ask.langs, c.Lang) {
		return judged{}, false
	}
	g := e.policy.Grade(w.target, c.Lang)
	if g.Class == language.ClassUnrelated {
		return judged{}, false
	}
	j := judged{offer: o, grade: g, standing: standingOf(g, c.MachineTranslated, c.Flags, w.variant, o.fit)}
	j.why = e.whyNot(w.target, &j)
	j.usable = j.why == None && !o.tried && w.qualifies(j.standing, o.fit)
	return j, true
}

// whyNot is the first check of 8.3 j fails that leaves it to a person:
// identity unconfirmed, partial coverage, a language automation may not
// take, a conflict, then a fit below the target's minimum.
func (e *Engine) whyNot(target language.Tag, j *judged) WhyNot {
	switch {
	case j.ident == identityUnconfirmed:
		return IdentityUnconfirmed
	case j.ident == identityPartial:
		return PartialCoverage
	case !e.policy.Automatic(j.grade):
		return LanguageManualOnly
	case j.fit == rank.Conflict && j.facts.Cut == rank.Differs:
		return DifferentCut
	case j.fit == rank.Conflict:
		return PreRelease
	case j.fit < e.minFit(target):
		return BelowMinFit
	}
	return None
}

// minFit is target's minimum fit: its own when the section sets one.
func (e *Engine) minFit(target language.Tag) rank.Fit {
	if f, ok := e.cfg.TargetMinFit[target]; ok {
		return f
	}
	return e.cfg.MinFit
}

// pickOf ranks w's candidates among offers (8.5): the usable ones in rank
// order, and for a missing pair with none the first failing check of its
// best-graded candidate, NoCandidate when none was kept.
func (e *Engine) pickOf(w *want, offers []*offer) pick {
	var kept []*judged
	for _, o := range offers {
		if j, ok := e.judge(w, o); ok {
			kept = append(kept, &j)
		}
	}
	slices.SortStableFunc(kept, compareRank)
	p := pick{want: w}
	for _, j := range kept {
		if j.usable {
			p.ranked = append(p.ranked, j)
		}
	}
	switch {
	case len(p.ranked) > 0 || w.cur != nil:
	case len(kept) == 0:
		p.skip = NoCandidate
	default:
		p.skip = kept[0].why
	}
	return p
}
