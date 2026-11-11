package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/providercalls"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/statedb"
)

// manualSearchTimeout bounds each provider's whole answer to a manual search,
// the funnel's admission and slot wait included: its Search deadline starts
// only once a slot is held.
const manualSearchTimeout = 30 * time.Second

var (
	// ErrInstanceUnknown is an instance the store does not hold, or one
	// with no item.
	ErrInstanceUnknown = errors.New("search: instance unknown")
	// ErrTargetUnconfigured is a tag none of the instance's items targets.
	ErrTargetUnconfigured = errors.New("search: target not configured for this item")
)

// Results is Candidates' answer: the candidates in rank order and one
// answer per enabled provider, so an empty list can say who did not answer.
type Results struct {
	Candidates []Annotated
	Providers  []ProviderAnswer
}

// Annotated is one candidate of a manual search: Automatic when automation
// would take it for one of the target's variants, else WhyNot says why not.
type Annotated struct {
	Handle            Handle
	Provider          provider.ProviderID
	Release           string
	Covers            []media.Numbering
	Lang              language.Tag
	Grade             language.Grade
	Facts             rank.Facts
	Flags             language.Flags
	MatchedBy         rank.MatchedBy
	Fit               rank.Fit
	WhyNot            WhyNot
	Automatic         bool
	MachineTranslated bool
}

// ProviderAnswer is one enabled provider's outcome: Count when Answered (a
// Failed one keeps the candidates it read before failing), Until when
// Paused, Cause, one redacted line, when Failed or NotAsked.
type ProviderAnswer struct {
	Until    time.Time
	Provider provider.ProviderID
	Cause    string
	Count    int
	Answer   Answer
}

// Answer is how a provider answered a manual search.
type Answer uint8

// The answers.
const (
	Answered Answer = iota
	// Paused: a rate pause, a health cooldown or an auth cooldown.
	Paused
	// Disabled: its credential is disabled.
	Disabled
	Failed
	// NotAsked: it lacks what it searches by, or a tag for the target.
	NotAsked
)

var answerText = [...]string{Answered: "answered", Paused: "paused", Disabled: "disabled", Failed: "failed", NotAsked: "not_asked"}

// MarshalText is the answer's wire spelling, refusing any other value.
func (a Answer) MarshalText() ([]byte, error) {
	if int(a) >= len(answerText) {
		return nil, fmt.Errorf("search: no Answer %d", a)
	}
	return []byte(answerText[a]), nil
}

// UnmarshalText reads an answer's wire spelling and refuses every other one.
func (a *Answer) UnmarshalText(b []byte) error {
	for v := range Answer(len(answerText)) {
		if answerText[v] == string(b) {
			*a = v
			return nil
		}
	}
	return fmt.Errorf("search: no Answer %q", b)
}

// Candidates is a manual search of one configured target of inst: every
// enabled provider asked Interactive with the target's ManualQuery tags,
// 8.3's language, identity and coverage checks, at most maxCandidates in
// rank order, each under a fresh handle owner may download. Its refusals
// are ErrInstanceUnknown, ErrTargetUnconfigured and ErrSearchCapacity, the
// last also when the answer would pass the bound at insertion.
func (e *Engine) Candidates(ctx context.Context, owner int64, inst statedb.InstanceID, target language.Tag) (Results, error) {
	r, variants, err := e.manualRun(ctx, inst, target)
	if err != nil {
		return Results{}, err
	}
	if !e.handles.room(owner, 1, time.Now()) {
		return Results{}, ErrSearchCapacity
	}
	answers, provs := e.askAll(ctx, r, target)
	if cerr := ctx.Err(); cerr != nil {
		return Results{}, cerr
	}
	ranked := e.annotate(target, variants, r.offers(answers))
	hs := make([]*held, len(ranked))
	for i, j := range ranked {
		hs[i] = heldOf(r, target, variants, j)
	}
	ids, ierr := e.handles.insert(owner, hs, time.Now())
	if ierr != nil {
		return Results{}, ierr
	}
	out := Results{Candidates: make([]Annotated, len(ranked)), Providers: provs}
	for i, j := range ranked {
		c := j.cand
		out.Candidates[i] = Annotated{
			Handle: ids[i], Provider: j.from.entry.ID, Release: c.Release, Covers: slices.Clone(c.Covers), Lang: c.Lang,
			Grade: j.grade, Facts: j.facts, Flags: c.Flags, MatchedBy: c.MatchedBy, Fit: j.fit, WhyNot: j.why,
			Automatic: j.why == None, MachineTranslated: c.MachineTranslated,
		}
	}
	return out, nil
}

// manualRun reads what a manual search of inst for target asks with: its
// items and their attempt memory, the root's facts, the video string, the
// stored probe's rate and the hash, and the target's variants over the
// items.
func (e *Engine) manualRun(ctx context.Context, inst statedb.InstanceID, target language.Tag) (*run, []language.Variant, error) {
	in, found, err := e.deps.Store.Instance(ctx, inst)
	if err != nil {
		return nil, nil, fmt.Errorf("search: read instance %d: %w", inst, err)
	}
	if !found || len(in.Items) == 0 {
		return nil, nil, ErrInstanceUnknown
	}
	r := &run{now: time.Now(), inst: in, cause: statedb.Item, fetched: map[[32]byte]bool{}}
	if rerr := r.readItems(ctx, e); rerr != nil {
		return nil, nil, rerr
	}
	if len(r.items) == 0 {
		return nil, nil, ErrInstanceUnknown
	}
	for i := range r.items {
		if r.items[i].history, err = e.readHistory(ctx, r.items[i].key, r.now, attemptMemory); err != nil {
			return nil, nil, err
		}
	}
	variants, err := e.variantsOf(ctx, r, target)
	if err != nil {
		return nil, nil, err
	}
	probe, _, err := e.deps.Store.Embedded(ctx, inst)
	if err != nil {
		return nil, nil, fmt.Errorf("search: read instance %d's probe: %w", inst, err)
	}
	r.planQuery(probe)
	if serr := r.readSeries(ctx, e); serr != nil {
		return nil, nil, serr
	}
	video, err := e.deps.Writer.OpenVideo(ctx, inst)
	if err != nil {
		e.log.Warn("search: video cannot be opened, searching without its hash", "instance", inst, "err", err)
		return r, variants, nil
	}
	defer func() { _ = video.Close() }()
	r.query.OSHash = e.hash(inst, video)
	return r, variants, nil
}

// variantsOf is every variant any of r's items configures for target, in
// first-seen order; ErrTargetUnconfigured for none.
func (e *Engine) variantsOf(ctx context.Context, r *run, target language.Tag) ([]language.Variant, error) {
	var out []language.Variant
	for i := range r.items {
		rows, err := e.deps.Store.Coverage(ctx, r.items[i].key)
		if err != nil {
			return nil, fmt.Errorf("search: read %s's coverage: %w", r.items[i].key, err)
		}
		for _, row := range rows {
			if row.Target == target && !slices.Contains(out, row.Variant) {
				out = append(out, row.Variant)
			}
		}
	}
	if len(out) == 0 {
		return nil, ErrTargetUnconfigured
	}
	return out, nil
}

// askAll asks every enabled provider at once, each under its own
// manualSearchTimeout, and answers the answers to read candidates from and
// one ProviderAnswer per enabled provider, in the engine's order.
func (e *Engine) askAll(ctx context.Context, r *run, target language.Tag) ([]answer, []ProviderAnswer) {
	answers := make([]answer, len(e.providers))
	provs := make([]ProviderAnswer, len(e.providers))
	var wg sync.WaitGroup
	for i := range e.providers {
		b := &e.providers[i]
		a := &ask{from: b, langs: e.policy.ManualQuery(target, b.entry.Languages.Tags()), targets: []language.Tag{target}}
		answers[i].ask = a
		if len(a.langs) == 0 {
			provs[i] = ProviderAnswer{Provider: b.entry.ID, Answer: NotAsked, Cause: "its language map has no tag for " + target.String()}
			continue
		}
		wg.Go(func() {
			budget := time.Now().Add(manualSearchTimeout)
			sctx, cancel := context.WithDeadline(ctx, budget)
			defer cancel()
			q := r.query
			q.Langs = a.langs
			cands, err := b.search(sctx, providercalls.Interactive, &q)
			answers[i].cands, answers[i].err = cands, err
			provs[i] = answerOf(ctx, budget, b.entry.ID, len(cands), err)
		})
	}
	wg.Wait()
	return answers, provs
}

// answerOf classifies one provider's answer to a manual search under ctx,
// asked until budget.
func answerOf(ctx context.Context, budget time.Time, id provider.ProviderID, count int, err error) ProviderAnswer {
	a := ProviderAnswer{Provider: id, Count: count}
	var (
		backoff *providercalls.BackoffError
		skipped *provider.Skipped
		fail    *provider.Failure
	)
	switch {
	case err == nil:
		a.Answer = Answered
	case ctx.Err() == nil && !time.Now().Before(budget):
		// The budget, not err or which timer fired: the funnel's Search
		// deadline can end the call first with its own *Failure, and a site's
		// own shorter timeout, back before the budget, keeps its Msg.
		a.Answer, a.Cause = Failed, "no answer within "+strconv.Itoa(int(manualSearchTimeout/time.Second))+" seconds"
	case errors.As(err, &backoff):
		a.Answer, a.Until = Paused, backoff.Until
	case errors.Is(err, providercalls.ErrCredentialDisabled):
		a.Answer = Disabled
	case errors.As(err, &skipped):
		a.Answer, a.Cause = NotAsked, skipped.Cause
	case errors.As(err, &fail):
		a.Answer, a.Cause = Failed, fail.Msg
	default:
		a.Answer, a.Cause = Failed, err.Error()
	}
	return a
}

// annotate keeps the offers 8.3's manual checks pass for target, each
// judged for the first of variants its flags serve, at most maxCandidates
// in rank order; one automation would take but for the pack it refused
// carries that refusal as its WhyNot.
func (e *Engine) annotate(target language.Tag, variants []language.Variant, offers []*offer) []*judged {
	var kept []*judged
	for _, o := range offers {
		i := slices.IndexFunc(variants, o.cand.Flags.Serves)
		if i < 0 {
			continue
		}
		if j, ok := e.judge(&want{target: target, variant: variants[i]}, o); ok {
			if j.why == None {
				j.why = o.packRefused
			}
			kept = append(kept, &j)
		}
	}
	slices.SortStableFunc(kept, compareRank)
	return kept[:min(len(kept), maxCandidates)]
}

// heldOf is j's handle content.
func heldOf(r *run, target language.Tag, variants []language.Variant, j *judged) *held {
	c := j.cand
	return &held{
		from: j.from, variants: variants, release: c.Release, ref: c.Ref, lang: c.Lang, facts: j.facts,
		inst: r.inst.ID, target: target, key: j.key, flags: c.Flags, matched: c.MatchedBy, fit: j.fit,
		machine: c.MachineTranslated,
	}
}
