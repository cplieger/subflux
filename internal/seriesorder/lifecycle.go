package seriesorder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/cplieger/subflux/internal/mediasource"
)

const (
	// freshFor is the age of a root's last successful fetch that asks
	// SkyHook again, since a new order on TVDB moves the index while no
	// listed episode changes.
	freshFor = 7 * 24 * time.Hour
	// titleRetryAfter bars a failed title request per root, reading and
	// language, so an outage costs one request an hour, not one a pass.
	titleRetryAfter = time.Hour
	// joinBudget bounds one Join's SkyHook requests, a few that each answer
	// in under a second, so a whole pass, which has no deadline, waits at
	// most this per root, and a batch root keeps half of scan's 60 s.
	joinBudget = 30 * time.Second
	// hungFor bars every SkyHook request after a Join's budget passed, so a
	// hung SkyHook costs one joinBudget per 10 minutes, not one per root.
	hungFor = 10 * time.Minute
)

// errHung is the fetch failure of a Join whose budget passed, and of every
// Join hungFor after it, which asks SkyHook nothing.
var errHung = fmt.Errorf("SkyHook (skyhook.sonarr.tv) did not answer within %d seconds; it is asked again %d minutes later",
	joinBudget/time.Second, hungFor/time.Minute)

// outOfTimeReason is the reason of a Join whose ctx's deadline passed in the
// root's read: answering own places then would drop the stored readings.
const outOfTimeReason = "SkyHook (skyhook.sonarr.tv) was not asked, since the scan ran out of time reading the root; it is asked again at the next pass"

// rootState is what the service remembers of one source's root between
// Joins; a restart loses it, which costs such a root one more fetch.
type rootState struct {
	fact        *listFact
	titles      map[titleKey]titleMemo
	titleWarned map[string]bool
	// fetchWarned and disagreeWarned hold one WARN per failure or
	// disagreement streak.
	fetchWarned, disagreeWarned bool
}

func (st *rootState) idle() bool {
	return st.fact == nil && len(st.titles) == 0 && len(st.titleWarned) == 0 && !st.fetchWarned && !st.disagreeWarned
}

// listFact is a list a successful fetch disagreed with: asking SkyHook again
// cannot bring it into step until the source refreshes it or the age passes.
type listFact struct {
	at   time.Time
	hash [32]byte
}

type titleKey struct {
	lang    string
	fetched int64 // the reading's Catalogue.Fetched
}

// titleMemo is a completion's outcome for one reading and language, held
// while the root keeps the reading so an unread pass, which stores nothing,
// asks nothing again: set added, else barred until until, or for the reading
// when until is zero.
type titleMemo struct {
	until time.Time
	set   *mediasource.TitleSet
}

func (s *Service) with(id rootID, f func(*rootState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.roots[id]
	if st == nil {
		st = &rootState{titles: map[titleKey]titleMemo{}, titleWarned: map[string]bool{}}
		s.roots[id] = st
	}
	f(st)
}

// dueFetch is design 4.4's fetch rule: a last successful fetch freshFor
// old, or a list no kept reading agrees with that the list fact does not
// hold (no reading kept included).
func (s *Service) dueFetch(id rootID, l *listing, kept []mediasource.SeriesEvidence, now time.Time) bool {
	var fact *listFact
	s.with(id, func(st *rootState) { fact = st.fact })
	var last time.Time
	if n := len(kept); n > 0 {
		last = kept[n-1].Catalogue.Fetched
	}
	if fact != nil && fact.at.After(last) {
		last = fact.at
	}
	if !last.IsZero() && now.Sub(last) >= freshFor {
		return true
	}
	for i := range kept {
		if l.disagreeing(&kept[i]) == 0 {
			return false
		}
	}
	return fact == nil || fact.hash != l.hash
}

// complete adds each of langs r lacks, one aired request per language
// unless a memo answers it, until ctx ends.
func (s *Service) complete(ctx context.Context, q *mediasource.JoinQuery, r *mediasource.SeriesEvidence, langs []string) {
	for _, lang := range langs {
		if ctx.Err() != nil {
			return
		}
		if slices.ContainsFunc(r.Titles, func(t mediasource.TitleSet) bool { return t.Language == lang }) {
			continue
		}
		if set := s.titlesIn(ctx, q, &r.Catalogue, lang); set != nil {
			addTitles(r, *set)
		}
	}
}

// titlesIn is c's title set in lang from its memo or one aired request: nil
// when barred, failed or read against another aired list. A request ctx
// ended leaves no memo, so the next Join asks it again.
func (s *Service) titlesIn(ctx context.Context, q *mediasource.JoinQuery, c *mediasource.Catalogue, lang string) *mediasource.TitleSet {
	id, key := idOf(q), titleKey{lang: lang, fetched: c.Fetched.UnixNano()}
	var memo titleMemo
	var memoed bool
	s.with(id, func(st *rootState) { memo, memoed = st.titles[key] })
	if memoed && (memo.set != nil || memo.until.IsZero() || time.Now().Before(memo.until)) {
		return memo.set
	}
	show, found, err := s.sky.Show(ctx, int(q.TVDB), lang, airedOrder) // #nosec G115 -- a TVDB id is at most 2^53-1
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		s.with(id, func(st *rootState) {
			st.titles[key] = titleMemo{until: time.Now().Add(titleRetryAfter)}
			if !st.titleWarned[lang] {
				st.titleWarned[lang] = true
				s.log.Warn("SkyHook did not answer a series' titles; they are asked again in an hour",
					"source", q.Source, "root", q.Root.ID, "series", q.TVDB, "language", lang, "err", err)
			}
		})
		return nil
	}
	memo = titleMemo{}
	switch {
	case !found:
		memo.set = &mediasource.TitleSet{Language: lang}
	case sameAired(c, show.Episodes):
		ts := titleSet(lang, show.Episodes)
		memo.set = &ts
	}
	s.with(id, func(st *rootState) {
		st.titles[key] = memo
		delete(st.titleWarned, lang)
	})
	return memo.set
}

// addTitles inserts ts in r's ascending languages, never writing a backing
// array r shares with its caller.
func addTitles(r *mediasource.SeriesEvidence, ts mediasource.TitleSet) {
	i, _ := slices.BinarySearchFunc(r.Titles, ts.Language, func(t mediasource.TitleSet, lang string) int {
		switch {
		case t.Language < lang:
			return -1
		case t.Language > lang:
			return 1
		}
		return 0
	})
	r.Titles = slices.Insert(slices.Clone(r.Titles), i, ts)
}

// choose is the lifecycle table's choose step over cands, the kept readings
// then any fetched one, oldest first, and the list fact's transitions.
func (s *Service) choose(l *listing, cands []mediasource.SeriesEvidence, fetched bool, fetchErr error) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
	id := idOf(l.q)
	chosen := -1
	for i := len(cands) - 1; i >= 0; i-- {
		if l.disagreeing(&cands[i]) == 0 {
			chosen = i
			break
		}
	}
	s.with(id, func(st *rootState) {
		switch {
		case chosen >= 0:
			st.fact = nil
		case fetched:
			st.fact = &listFact{at: cands[len(cands)-1].Catalogue.Fetched, hash: l.hash}
		}
	})
	kept := cands
	if fetched {
		kept = cands[:len(cands)-1]
	}
	if chosen < 0 {
		if !l.q.Order.Aired {
			s.prune(id, kept)
			return nil, nil, s.unavailable(l, cands, fetchErr)
		}
		if len(cands) == 0 {
			joined, _ := l.joined(nil)
			s.prune(id, nil)
			return joined, nil, nil
		}
		chosen = len(cands) - 1
	}
	ev := &mediasource.RootEvidence{Bound: cands[chosen]}
	readings := []mediasource.SeriesEvidence{cands[chosen]}
	if last := len(cands) - 1; last > chosen {
		held := cands[last]
		ev.Held = &held
		readings = append(readings, held)
	}
	s.with(id, func(st *rootState) { st.disagreeWarned = false })
	s.prune(id, readings)
	joined, unplaced := l.joined(&cands[chosen])
	if unplaced > 0 {
		s.log.Warn("some episodes have no aired place in SkyHook's lists and stay unidentified",
			"source", l.q.Source, "root", l.q.Root.ID, "series", l.q.TVDB, "order", l.q.Order.Type, "episodes", unplaced)
	}
	return joined, ev, nil
}

// unavailable is the error of a root in another order than aired whose list
// agrees with no reading after every fetch the rules allow.
func (s *Service) unavailable(l *listing, cands []mediasource.SeriesEvidence, fetchErr error) error {
	switch {
	case errors.Is(fetchErr, errHung):
		return &unavailableError{reason: errHung.Error(), cause: fetchErr}
	case fetchErr != nil:
		return &unavailableError{reason: fetchErr.Error(), cause: fetchErr}
	}
	var newest *mediasource.SeriesEvidence
	if n := len(cands); n > 0 {
		newest = &cands[n-1]
	}
	n := l.disagreeing(newest)
	verb := "episodes are"
	if n == 1 {
		verb = "episode is"
	}
	reason := fmt.Sprintf("%d %s listed otherwise than SkyHook (skyhook.sonarr.tv) lists them; a refresh of the series in Sonarr brings them into step", n, verb)
	s.with(idOf(l.q), func(st *rootState) {
		if !st.disagreeWarned {
			st.disagreeWarned = true
			s.log.Warn("a series is listed otherwise than SkyHook lists it, so its aired order cannot be read",
				"source", l.q.Source, "root", l.q.Root.ID, "series", l.q.TVDB, "order", l.q.Order.Type, "episodes", n)
		}
	})
	return &unavailableError{reason: reason}
}

// prune drops the title memos of readings the root no longer keeps, and the
// root's state once it holds nothing.
func (s *Service) prune(id rootID, keep []mediasource.SeriesEvidence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.roots[id]
	if st == nil {
		return
	}
	for k := range st.titles {
		if !slices.ContainsFunc(keep, func(r mediasource.SeriesEvidence) bool { return r.Catalogue.Fetched.UnixNano() == k.fetched }) {
			delete(st.titles, k)
		}
	}
	if st.idle() {
		delete(s.roots, id)
	}
}

// unavailableError wraps mediasource.ErrAiredOrderUnavailable; its text is
// the reason alone, which the Set prefixes with the series' title.
type unavailableError struct {
	cause  error
	reason string
}

func (e *unavailableError) Error() string { return e.reason }

func (e *unavailableError) Unwrap() []error {
	if e.cause == nil {
		return []error{mediasource.ErrAiredOrderUnavailable}
	}
	return []error{mediasource.ErrAiredOrderUnavailable, e.cause}
}
