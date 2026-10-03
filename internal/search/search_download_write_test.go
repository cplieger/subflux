package search

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/cplieger/atomicfile/v4"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/subflux"
)

// fakeMedia answers every write with err and reports blocked as the folder
// Blocked names.
type fakeMedia struct {
	err     func(n int32) error
	blocked string
	writes  atomic.Int32
}

func (f *fakeMedia) WriteFile(context.Context, string, []byte) error {
	n := f.writes.Add(1)
	if f.err == nil {
		return nil
	}
	return f.err(n)
}

func (f *fakeMedia) Blocked(string) (string, bool) { return f.blocked, f.blocked != "" }

func newWriteRig(t *testing.T, media MediaWriter, providers ...*countingProvider) *outcomeRig {
	t.Helper()
	r := newOutcomeRig(t, newRankedConfig(3, "opensubtitles", "subdl"), providers...)
	ps := r.engine.providers
	r.engine = New(ps, WithStore(r.store), WithConfig(r.engine.cfg), WithScorer(fixedScorer{score: 10}),
		WithSyncer(&recordingSyncer{}), WithTracks(noopDetector{}), WithProviderGate(r.gate.binding),
		WithMediaWriter(media))
	return r
}

func (r *outcomeRig) searchErr(t *testing.T) (subflux.SearchResult, error) {
	t.Helper()
	return r.engine.SearchTargets(t.Context(),
		&subflux.SearchRequest{MediaType: subflux.MediaTypeMovie, ImdbID: "tt0111161", ReleaseName: "Movie-GRP"},
		filepath.Join(t.TempDir(), "movie.mkv"), []subflux.SubtitleTarget{{Code: "fr"}})
}

func TestSearchTargets_a_per_target_write_refusal_fails_only_the_target(t *testing.T) {
	t.Parallel()
	media := &fakeMedia{err: func(int32) error {
		return &fs.PathError{Op: "rename", Path: "/m/movie.fr.srt", Err: syscall.EACCES}
	}}
	p := &countingProvider{name: "subdl", results: candidates("subdl", 2)}
	r := newWriteRig(t, media, p)

	result, err := r.searchErr(t)
	if err != nil || result.WriteFailure != nil {
		t.Fatalf("SearchTargets = %v, WriteFailure %v; want both nil for a per-target refusal", err, result.WriteFailure)
	}
	if got := result.Langs[0]; got.Failed != 1 || got.WriteBlocked != 0 {
		t.Errorf("LangOutcome = %+v, want one failed target and none write-blocked", got)
	}
	if n := p.downloads.Load(); n != 1 {
		t.Errorf("downloads = %d, want 1 (no other candidate can land at the same path)", n)
	}
	if r.store.failureCalled || len(r.store.stamps) != 0 {
		t.Errorf("backoff = %v, stamps = %v; want neither", r.store.failureCalled, r.store.stamps)
	}
}

func TestSearchTargets_an_unwritable_folder_stops_and_is_returned(t *testing.T) {
	t.Parallel()
	uerr := &mediawrite.UnwritableError{Folder: "/m", Root: "/m", Op: "write", Err: syscall.EROFS}
	media := &fakeMedia{err: func(int32) error { return uerr }}
	p := &countingProvider{name: "subdl", results: candidates("subdl", 2)}
	r := newWriteRig(t, media, p)

	result, err := r.searchErr(t)
	var got *mediawrite.UnwritableError
	if !errors.As(err, &got) || got != uerr {
		t.Fatalf("SearchTargets error = %v, want the write's *UnwritableError", err)
	}
	if !errors.As(result.WriteFailure, &got) || got != uerr {
		t.Errorf("WriteFailure = %v, want the write's *UnwritableError", result.WriteFailure)
	}
	if l := result.Langs[0]; l.WriteBlocked != 1 || l.Failed != 0 || result.WriteBlocked() != 1 {
		t.Errorf("LangOutcome = %+v, want one write-blocked target and no failure", l)
	}
	if n := p.downloads.Load(); n != 1 {
		t.Errorf("downloads = %d, want 1 (the loop stops at the folder fault)", n)
	}
	if r.store.failureCalled || r.store.successCalled || len(r.store.stamps) != 0 {
		t.Errorf("backoff = %v, success clear = %v, stamps = %v; want none", r.store.failureCalled, r.store.successCalled, r.store.stamps)
	}
}

func TestSearchTargets_a_blocked_folder_queries_no_provider(t *testing.T) {
	t.Parallel()
	media := &fakeMedia{blocked: "/m/tv/Show"}
	p := &countingProvider{name: "subdl", results: candidates("subdl", 1)}
	r := newWriteRig(t, media, p)

	result, err := r.searchErr(t)
	if err != nil || result.WriteFailure != nil {
		t.Fatalf("SearchTargets = %v, WriteFailure %v; want nil for an already-known fault", err, result.WriteFailure)
	}
	l := result.Langs[0]
	if l.Kind != subflux.LangWriteBlocked || l.WriteBlocked != 1 || l.Queried != 0 || result.ProviderQueried() {
		t.Errorf("LangOutcome = %+v, want write_blocked with one target and no query", l)
	}
	if p.searches.Load() != 0 || p.downloads.Load() != 0 || media.writes.Load() != 0 {
		t.Errorf("searches %d, downloads %d, writes %d; want none", p.searches.Load(), p.downloads.Load(), media.writes.Load())
	}
	if r.store.failureCalled || len(r.store.stamps) != 0 {
		t.Errorf("backoff = %v, stamps = %v; want neither", r.store.failureCalled, r.store.stamps)
	}
}

func TestSearchTargets_an_over_cap_subtitle_tries_the_next_candidate(t *testing.T) {
	t.Parallel()
	media := &fakeMedia{err: func(n int32) error {
		if n == 1 {
			return &atomicfile.WriteError{Err: atomicfile.ErrFileTooLarge, Phase: atomicfile.PhaseTempWrite}
		}
		return nil
	}}
	p := &countingProvider{name: "subdl", results: candidates("subdl", 2)}
	r := newWriteRig(t, media, p)

	result, err := r.searchErr(t)
	if err != nil || len(result.Paths()) != 1 || p.downloads.Load() != 2 {
		t.Errorf("SearchTargets = %v with paths %v after %d downloads; want the second candidate saved",
			err, result.Paths(), p.downloads.Load())
	}
}
