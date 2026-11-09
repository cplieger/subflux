// Package seriesorder joins a source's episode list to TVDB's aired order
// through SkyHook's lists of the same series, and keeps the lifecycle of each
// root's SkyHook evidence, the readings its keys come from, which statedb
// stores with the root record (media-sources design 4.4).
package seriesorder

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/skyhook"
)

// Store is what the service reads; *statedb.Store implements it. Policy is
// the policy in force, whose targets are title languages of every root.
type Store interface {
	RootEvidence(ctx context.Context, src media.SourceID, root mediasource.NativeRef) (mediasource.RootEvidence, bool, error)
	Policy() language.Policy
}

// shows is the SkyHook read the service makes.
type shows interface {
	Show(ctx context.Context, tvdbID int, lang, order string) (skyhook.Show, bool, error)
}

var _ shows = (*skyhook.Client)(nil)

// Service is the aired join and the search read of each root's evidence. A
// root joins only inside its source's scan loop, so its evidence has one
// writer at a time; mu never covers a request.
type Service struct {
	hungUntil time.Time // guarded by mu
	sky       shows
	store     Store
	log       *slog.Logger
	roots     map[rootID]*rootState // guarded by mu
	mu        sync.Mutex
}

type rootID struct {
	src  media.SourceID
	root mediasource.NativeRef
}

func idOf(q *mediasource.JoinQuery) rootID { return rootID{root: q.Root, src: q.Source} }

// New is the service over sky and store; a nil log is slog.Default.
func New(sky *skyhook.Client, store Store, log *slog.Logger) *Service {
	return newService(sky, store, log)
}

func newService(sky shows, store Store, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{sky: sky, store: store, log: log, roots: map[rootID]*rootState{}}
}

// Join answers one Joined per listed episode of q, in order, from the newest
// reading q's list agrees with, and the evidence the root carries after it,
// fetching first when design 4.4's rules call for it; it writes nothing.
// Under another order than aired, a list that agrees with no reading is an
// error wrapping mediasource.ErrAiredOrderUnavailable, with no evidence, as is
// ctx's deadline passing in the root's read under any order. A store failure
// is the store's error and a cancelled ctx its error; SkyHook is asked within
// joinBudget, and a passed deadline, ctx's or the budget's, is a failed fetch.
func (s *Service) Join(ctx context.Context, q mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) { //nolint:gocritic // hugeParam: mediasource.OrderJoin passes the query by value
	l := newListing(&q)
	kept, err := s.kept(ctx, &q)
	switch {
	case errors.Is(err, context.DeadlineExceeded) && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return nil, nil, &unavailableError{reason: outOfTimeReason, cause: err}
	case err != nil:
		return nil, nil, err
	}
	sky, cancel := context.WithTimeoutCause(ctx, joinBudget, errHung)
	defer cancel()
	hung := s.hung(time.Now())
	langs := titleLanguages(q.Language, targetTags(s.store.Policy()))
	if !hung {
		for i := range kept {
			s.complete(sky, &q, &kept[i], langs)
		}
	}
	due := s.dueFetch(idOf(&q), &l, kept, time.Now())
	var r mediasource.SeriesEvidence
	if due {
		r, err = s.refresh(sky, &q, kept, langs, hung)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, nil, ctx.Err()
	}
	budgetPassed := errors.Is(context.Cause(sky), errHung)
	if budgetPassed {
		s.markHung(time.Now())
	}
	switch {
	case err == nil, errors.Is(err, errHung):
	case budgetPassed:
		err = fmt.Errorf("%w: %w", errHung, err)
	default:
		err = fmt.Errorf("SkyHook (skyhook.sonarr.tv) did not answer: %w; it is asked again at the next pass", err)
	}
	switch {
	case !due:
		return s.choose(&l, kept, false, nil)
	case err != nil:
		s.with(idOf(&q), func(st *rootState) {
			if !st.fetchWarned {
				st.fetchWarned = true
				s.log.Warn("no new SkyHook reading for a series",
					"source", q.Source, "root", q.Root.ID, "series", q.TVDB, "err", err)
			}
		})
		return s.choose(&l, kept, false, err)
	}
	s.with(idOf(&q), func(st *rootState) { st.fetchWarned = false })
	return s.choose(&l, append(slices.Clone(kept), r), true, nil)
}

// refresh is a new reading of q's series with its title languages after the
// newest of kept; errHung, asking nothing, while hung.
func (s *Service) refresh(ctx context.Context, q *mediasource.JoinQuery, kept []mediasource.SeriesEvidence, langs []string, hung bool) (mediasource.SeriesEvidence, error) {
	if hung {
		return mediasource.SeriesEvidence{}, errHung
	}
	var after time.Time
	if n := len(kept); n > 0 {
		after = kept[n-1].Catalogue.Fetched
	}
	r, err := s.fetch(ctx, q, after)
	if err == nil {
		s.complete(ctx, q, &r, langs)
	}
	return r, err
}

// hung reports whether SkyHook is barred at now.
func (s *Service) hung(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return now.Before(s.hungUntil)
}

func (s *Service) markHung(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hungUntil = now.Add(hungFor)
}

// kept is the root's stored readings, oldest first; evidence for another
// series, left by a corrected series id, counts as none.
func (s *Service) kept(ctx context.Context, q *mediasource.JoinQuery) ([]mediasource.SeriesEvidence, error) {
	ev, found, err := s.store.RootEvidence(ctx, q.Source, q.Root)
	if err != nil || !found || ev.Bound.Catalogue.TVDB != q.TVDB {
		return nil, err
	}
	kept := []mediasource.SeriesEvidence{ev.Bound}
	if ev.Held != nil {
		kept = append(kept, *ev.Held)
	}
	return kept, nil
}
