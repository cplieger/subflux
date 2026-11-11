package search_test

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/go/keyenc"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/searchtest"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

const srt = "1\n00:00:01,000 --> 00:00:02,000\nHello\n"

var srtBytes = []byte(srt)

// alignedAs is an Align answer delivering srt at identity timing under v.
func alignedAs(t *testing.T, v timing.Verdict) searchtest.Result {
	t.Helper()
	cues, err := timing.ParseCues(srtBytes)
	if err != nil {
		t.Fatal(err)
	}
	return searchtest.Result{Aligned: syncsched.Aligned{
		Timing: timing.Identity{}, Cues: cues,
		Outcome: timing.Outcome{Verdict: v, Method: timing.MethodReference, Decision: timing.Decision{Action: timing.ActionReference}},
	}}
}

var (
	refusedCut  = searchtest.Result{Err: syncsched.ErrDifferentCut}
	unreadable  = searchtest.Result{Err: timing.ErrFormatUnsupported}
	ep1         = episode(1)
	closedSpell = []string{
		"", "different_cut", "format_unsupported", "different_episode", "episode_count", "order_undetermined",
		"fetch_gone", "fetch_failed", "write_failed",
	}
)

// script sets the aligner's answers, first call first, and returns it to
// script members and read the calls it was asked.
func (r *rig) script(results ...searchtest.Result) *searchtest.Aligner {
	al := searchtest.NewAligner(results...)
	r.alignerMu.Lock()
	r.aligner = al
	r.alignerMu.Unlock()
	return al
}

// run runs inst's work of cause through its deliveries on a fresh engine,
// the clock moved past every provider's scan delay first.
func (r *rig) run(inst statedb.InstanceID, cause statedb.WorkCause) error {
	r.t.Helper()
	return r.runCtx(r.t.Context(), inst, cause)
}

func (r *rig) runCtx(ctx context.Context, inst statedb.InstanceID, cause statedb.WorkCause) error {
	r.t.Helper()
	time.Sleep(time.Hour)
	return search.RunFor(ctx, r.engine(), statedb.Work{Inst: inst, Gen: 1, Cause: cause})
}

// mustRun is run, failing the test on an error.
func (r *rig) mustRun(inst statedb.InstanceID, cause statedb.WorkCause) {
	r.t.Helper()
	if err := r.run(inst, cause); err != nil {
		r.t.Fatalf("run: %v", err)
	}
}

// history is k's download log, newest first.
func (r *rig) history(k media.Key) []statedb.Download {
	r.t.Helper()
	rows, _, err := r.db.History(r.t.Context(), statedb.ItemHistory(k), "", 100)
	if err != nil {
		r.t.Fatal(err)
	}
	return rows
}

// lastRefusal is the refusal noted on k's pair of target and v.
func (r *rig) lastRefusal(k media.Key, target string, v language.Variant) string {
	r.t.Helper()
	rows, err := r.db.Coverage(r.t.Context(), k)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, row := range rows {
		if row.Target == mustTag(target) && row.Variant == v {
			return row.LastRefusal
		}
	}
	r.t.Fatalf("no %s pair of %d in %+v", target, v, rows)
	return ""
}

// subtitles are the subtitle names beside inst's video.
func (r *rig) subtitles(inst statedb.InstanceID) []string {
	r.t.Helper()
	in, _, err := r.db.Instance(r.t.Context(), inst)
	if err != nil {
		r.t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(in.VideoPath))
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".srt") {
			out = append(out, e.Name())
		}
	}
	return out
}

// record is inst's one file record; it fails the test unless there is
// exactly one.
func (r *rig) record(inst statedb.InstanceID) statedb.FileRecord {
	r.t.Helper()
	recs, err := r.db.Files(r.t.Context(), inst)
	if err != nil {
		r.t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Provenance == nil {
		r.t.Fatalf("records %+v, want one delivered", recs)
	}
	return recs[0]
}

// inOrder is rows by when each attempt was fetched: by time, then attempt.
// A delivery writes its row before its run's other attempts, so the log's
// own order is not the attempts'.
func inOrder(rows []statedb.Download) []statedb.Download {
	out := slices.Clone(rows)
	slices.SortStableFunc(out, func(a, b statedb.Download) int {
		if c := a.At.Compare(b.At); c != 0 && a.Run != b.Run {
			return c
		}
		return a.Attempt - b.Attempt
	})
	return out
}

// spellings are rows' refusals in attempt order.
func spellings(rows []statedb.Download) []string {
	out := make([]string, 0, len(rows))
	for _, d := range inOrder(rows) {
		out = append(out, d.Refusal)
	}
	return out
}

// rowOf is the row of the candidate token of provider id; it fails the test
// when there is none.
func rowOf(t *testing.T, rows []statedb.Download, id, token string) statedb.Download {
	t.Helper()
	i := slices.IndexFunc(rows, func(d statedb.Download) bool { return d.Candidate == keyFor(id, token) })
	if i < 0 {
		t.Fatalf("history %+v holds no row of %s's %s", rows, id, token)
	}
	return rows[i]
}

// keyFor is the attempt key 8.3 defines: the SHA-256 of the keyenc join of
// the provider id and the Ref's token.
func keyFor(id, token string) [32]byte { return sha256.Sum256([]byte(keyenc.Join(id, token))) }

func TestEndedFetchRecordsFailed(t *testing.T) {
	t.Run("recorded", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			p := r.provider("p", "en").answer(nil, cand(1, "en")).serve("ref-en-01", nil, srtBytes)
			inst := r.seed(show, nil, ep1)[0]
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p.onFetch = func(fctx context.Context) error {
				cancel()
				<-fctx.Done()
				return fctx.Err()
			}
			if err := r.runCtx(ctx, inst, statedb.Scan); !errors.Is(err, context.Canceled) {
				t.Fatalf("run = %v, want context.Canceled", err)
			}
			rows := r.history(keyOf(t, show, ep1))
			if len(rows) != 1 || rows[0].Refusal != "fetch_failed" || rows[0].Delivered || rows[0].Candidate != keyFor("p", "ref-en-01") {
				t.Errorf("history %+v, want one undelivered fetch_failed row keyed by the candidate", rows)
			}
		})
	})
	t.Run("store failing", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			p := r.provider("p", "en").answer(nil, cand(1, "en")).serve("ref-en-01", nil, srtBytes)
			inst := r.seed(show, nil, ep1)[0]
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p.onFetch = func(fctx context.Context) error {
				if err := r.db.Close(); err != nil {
					t.Error(err)
				}
				cancel()
				<-fctx.Done()
				return fctx.Err()
			}
			err := r.runCtx(ctx, inst, statedb.Scan)
			if err == nil || errors.Is(err, context.Canceled) {
				t.Errorf("run = %v, want the store's error, so the run fails", err)
			}
		})
	})
}

func TestAllAttemptsRefusedNotesDifferentCut(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil, cand(1, "en", withRef("a")), cand(1, "en", withRef("b"))).
			serve("a", nil, srtBytes).serve("b", nil, srtBytes)
		inst := r.seed(show, nil, ep1)[0]
		r.script(refusedCut, refusedCut)
		r.mustRun(inst, statedb.Scan)
		k := keyOf(t, show, ep1)
		if got := spellings(r.history(k)); !slices.Equal(got, []string{"different_cut", "different_cut"}) {
			t.Errorf("rows %q, want two different_cut", got)
		}
		if got := r.lastRefusal(k, "en", language.Standard); got != "different_cut" {
			t.Errorf("last refusal %q, want different_cut", got)
		}
	})
}

func TestNothingRankedNotesSkip(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en").answer(nil).answer(nil, cand(1, "en")).
			serve("ref-en-01", provider.Fail(provider.Transient, "busy"), nil)
		inst := r.seed(show, nil, ep1)[0]
		k := keyOf(t, show, ep1)
		r.mustRun(inst, statedb.Item)
		if got := r.lastRefusal(k, "en", language.Standard); got != "no_candidate" {
			t.Errorf("after an empty answer the last refusal is %q, want no_candidate", got)
		}
		r.mustRun(inst, statedb.Item)
		if got := p.fetches(); !slices.Equal(got, []string{"ref-en-01"}) {
			t.Fatalf("fetches %q, want the one candidate", got)
		}
		if got := r.lastRefusal(k, "en", language.Standard); got != "no_candidate" {
			t.Errorf("a fetch failure noted %q, want the earlier no_candidate kept", got)
		}
	})
}

func TestDeliveryRecordsMachineTranslated(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		machine := func(c *provider.Candidate) { c.MachineTranslated = true }
		r.provider("p", "en").answer(nil, cand(1, "en", machine)).serve("ref-en-01", nil, srtBytes)
		inst := r.seed(show, nil, ep1)[0]
		r.script(alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		if rec := r.record(inst); !rec.Provenance.MachineTranslated {
			t.Errorf("file record %+v, want MachineTranslated", rec.Provenance)
		}
		rows := r.history(keyOf(t, show, ep1))
		if len(rows) != 1 || !rows[0].Delivered || !rows[0].Provenance.MachineTranslated {
			t.Errorf("history %+v, want one delivered machine-translated row", rows)
		}
	})
}

func TestNoRefetchWithinAttemptMemory(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en").answer(nil, cand(1, "en", withRef("a"))).
			answer(nil, cand(1, "en", withRef("a")), cand(1, "en", withRef("b"))).
			serve("a", nil, srtBytes).serve("b", nil, srtBytes)
		inst := r.seed(show, nil, ep1)[0]
		r.script(refusedCut)
		r.mustRun(inst, statedb.Scan)
		r.script(alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		if got := p.fetches(); !slices.Equal(got, []string{"a", "b"}) {
			t.Errorf("fetches %q, want a once, then only b, its release name equal", got)
		}
	})
}

func TestDeliveredAttemptKeyedByCandidate(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en").answer(nil, cand(1, "en", withRef("a"))).serve("a", nil, srtBytes)
		inst := r.seed(show, nil, ep1)[0]
		r.script(alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		k := keyOf(t, show, ep1)
		rows := r.history(k)
		if len(rows) != 1 || !rows[0].Delivered || rows[0].Candidate != keyFor("p", "a") {
			t.Fatalf("history %+v, want one delivered row keyed by the candidate", rows)
		}
		in, _, err := r.db.Instance(t.Context(), inst)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(filepath.Dir(in.VideoPath), r.record(inst).Name)); err != nil {
			t.Fatal(err)
		}
		r.mustRun(inst, statedb.Scan)
		if p.calls() != 2 {
			t.Fatalf("searches %d, want the pair searched again once its file went", p.calls())
		}
		if got := p.fetches(); !slices.Equal(got, []string{"a"}) {
			t.Errorf("fetches %q, want the delivered candidate never fetched again", got)
		}
	})
}

func TestFetchGoneRemembered(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en").answer(nil, cand(1, "en")).serve("ref-en-01", provider.Fail(provider.NotFound, "gone"), nil)
		inst := r.seed(show, nil, ep1)[0]
		r.mustRun(inst, statedb.Scan)
		r.mustRun(inst, statedb.Item)
		if got := p.fetches(); !slices.Equal(got, []string{"ref-en-01"}) {
			t.Errorf("fetches %q, want a 404 never fetched again", got)
		}
		if got := spellings(r.history(keyOf(t, show, ep1))); !slices.Equal(got, []string{"fetch_gone"}) {
			t.Errorf("rows %q, want one fetch_gone", got)
		}
	})
}

func TestFetchTransientRetried(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en").answer(nil, cand(1, "en")).serve("ref-en-01", provider.Fail(provider.Transient, "status 503"), nil)
		inst := r.seed(show, nil, ep1)[0]
		r.mustRun(inst, statedb.Scan)
		r.mustRun(inst, statedb.Scan)
		if got := p.fetches(); !slices.Equal(got, []string{"ref-en-01", "ref-en-01"}) {
			t.Errorf("fetches %q, want a 503 fetched again next scan", got)
		}
		if got := spellings(r.history(keyOf(t, show, ep1))); !slices.Equal(got, []string{"fetch_failed", "fetch_failed"}) {
			t.Errorf("rows %q, want two fetch_failed", got)
		}
	})
}

func TestRefusalSpellingsClosed(t *testing.T) {
	for name, s := range map[string]string{
		"different cut": search.RefusalDifferentCut, "format unsupported": search.RefusalFormatUnsupported,
		"fetch gone": search.RefusalFetchGone, "fetch failed": search.RefusalFetchFailed,
		"write failed": search.RefusalWriteFailed, "different episode": search.RefusalDifferentEpisode,
		"episode count": search.RefusalEpisodeCount, "order undetermined": search.RefusalOrderUndetermined,
	} {
		if !slices.Contains(closedSpell, s) {
			t.Errorf("%s is spelled %q, outside the closed set", name, s)
		}
	}
	fail := func(k provider.FailureKind) func(*scripted) {
		return func(s *scripted) {
			s.serve("ref-en-01", &provider.Failure{Kind: k, Msg: "scripted", RetryAfter: time.Minute}, nil)
		}
	}
	served := func(body []byte) func(*scripted) {
		return func(s *scripted) { s.serve("ref-en-01", nil, body) }
	}
	servedZip := func(members ...string) func(*scripted) {
		return func(s *scripted) { s.serve("ref-en-01", nil, zipOf(t, members...)) }
	}
	syncFailed := func(t *testing.T) searchtest.Result {
		res := alignedAs(t, timing.Unverified)
		res.Err = syncsched.ErrSyncFailed
		return res
	}
	cases := []struct {
		name      string
		serve     func(*scripted)
		align     func(*testing.T) []searchtest.Result
		held      bool
		want      string
		delivered bool
	}{
		{name: "not found", serve: fail(provider.NotFound), want: "fetch_gone"},
		{name: "permanent", serve: fail(provider.Permanent), want: "fetch_gone"},
		{name: "transient", serve: fail(provider.Transient), want: "fetch_failed"},
		{name: "rate limited", serve: fail(provider.RateLimited), want: "fetch_failed"},
		{name: "auth", serve: fail(provider.Auth), want: "fetch_failed"},
		{name: "different cut", serve: served(srtBytes), align: func(*testing.T) []searchtest.Result { return []searchtest.Result{refusedCut} }, want: "different_cut"},
		{name: "unreadable", serve: served(srtBytes), align: func(*testing.T) []searchtest.Result { return []searchtest.Result{unreadable} }, want: "format_unsupported"},
		{name: "archive refused", serve: served([]byte("PK\x03\x04 not a zip")), want: "format_unsupported"},
		{
			name: "a member chosen by number refused", serve: servedZip("Show.S01E01.srt"), want: "different_episode",
			align: func(*testing.T) []searchtest.Result { return []searchtest.Result{refusedCut} },
		},
		{name: "a pack short of the season", serve: servedZip("Show.S01E01.srt", "Show.S01E02.srt"), want: "episode_count"},
		{name: "sync failed delivered", serve: served(srtBytes), align: func(t *testing.T) []searchtest.Result { return []searchtest.Result{syncFailed(t)} }, want: "", delivered: true},
		{
			name: "staged, not chosen", serve: served(srtBytes), held: true, want: "",
			align: func(t *testing.T) []searchtest.Result { return []searchtest.Result{alignedAs(t, timing.Unverified)} },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				p := r.provider("p", "en").answer(nil, cand(1, "en", sameRelease, withRef("ref-en-01")))
				c.serve(p)
				var inst statedb.InstanceID
				if c.held {
					inst = r.seedHeld(textTrack, unverified(rank.Other, false), time.Now())
				} else {
					inst = r.seed(show, nil, ep1)[0]
				}
				if c.align != nil {
					r.script(c.align(t)...)
				}
				r.mustRun(inst, statedb.Scan)
				if got := rowOf(t, r.history(keyOf(t, show, ep1)), "p", "ref-en-01"); got.Refusal != c.want || got.Delivered != c.delivered || !slices.Contains(closedSpell, got.Refusal) {
					t.Errorf("row %q delivered %v, want %q delivered %v", got.Refusal, got.Delivered, c.want, c.delivered)
				}
			})
		})
	}
}

func TestOneDeliveryPerPair(t *testing.T) {
	inRig(t, "default: [{tag: en}, {tag: fr}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en", "fr")
		var cands []provider.Candidate
		for _, ref := range []string{"en1", "en2", "en3"} {
			cands = append(cands, cand(1, "en", withRef(ref)))
			p.serve(ref, nil, srtBytes)
		}
		for _, ref := range []string{"fr1", "fr2"} {
			cands = append(cands, cand(1, "fr", withRef(ref)))
			p.serve(ref, nil, srtBytes)
		}
		p.answer(nil, cands...)
		inst := r.seed(show, nil, ep1)[0]
		r.script(alignedAs(t, timing.Verified), alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		got := p.fetches()
		slices.Sort(got)
		if !slices.Equal(got, []string{"en1", "fr1"}) {
			t.Errorf("fetches %q, want each pair's first candidate only", got)
		}
		if subs := r.subtitles(inst); len(subs) != 2 {
			t.Errorf("subtitles %q, want one per pair", subs)
		}
	})
}

func TestNoConfidenceRetryBeforeSE3(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en").answer(nil, cand(1, "en", withRef("a")), cand(1, "en", withRef("b"))).
			serve("a", nil, srtBytes).serve("b", nil, srtBytes)
		inst := r.seed(show, nil, ep1)[0]
		r.script(alignedAs(t, timing.Unverified), alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		if got := p.fetches(); !slices.Equal(got, []string{"a"}) {
			t.Errorf("fetches %q, want the unverified first attempt delivered with no retry", got)
		}
		if rec := r.record(inst); rec.Provenance.Timing.Verdict != timing.Unverified {
			t.Errorf("delivered verdict %v, want Unverified", rec.Provenance.Timing.Verdict)
		}
	})
}

func TestUnverifiedUpgradeNeedsVerifiedBeforeSE3(t *testing.T) {
	two := func(r *rig) *scripted {
		return r.provider("p", "en").answer(nil, cand(1, "en", sameRelease, withRef("a")), cand(1, "en", sameRelease, withRef("b"))).
			serve("a", nil, srtBytes).serve("b", nil, srtBytes)
	}
	t.Run("the verified attempt replaces the file", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			p := two(r)
			inst := r.seedHeld(textTrack, unverified(rank.Other, false), time.Now())
			r.script(alignedAs(t, timing.Unverified), alignedAs(t, timing.Verified))
			r.mustRun(inst, statedb.Scan)
			if got := p.fetches(); !slices.Equal(got, []string{"a", "b"}) {
				t.Errorf("fetches %q, want a staged, then b", got)
			}
			rec := r.record(inst)
			if rec.Provenance.Timing.Verdict != timing.Verified || rec.Provenance.Release != stem(1) {
				t.Errorf("record %+v, want the verified attempt", rec.Provenance)
			}
			rows := r.history(keyOf(t, show, ep1))
			a, b := rowOf(t, rows, "p", "a"), rowOf(t, rows, "p", "b")
			if a.Delivered || a.Refusal != "" || a.Attempt != 1 || !b.Delivered || b.Attempt != 2 || a.Run != b.Run {
				t.Errorf("attempts %+v and %+v, want a staged as attempt 1 and b delivered as attempt 2 in one run", a, b)
			}
		})
	})
	t.Run("never an unverified one", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			two(r)
			inst := r.seedHeld(textTrack, unverified(rank.Other, false), time.Now())
			held := r.record(inst)
			r.script(alignedAs(t, timing.Unverified), alignedAs(t, timing.Unverified))
			r.mustRun(inst, statedb.Scan)
			if rec := r.record(inst); rec.Name != held.Name || rec.Identity != held.Identity {
				t.Errorf("record %+v, want the held file kept", rec)
			}
		})
	})
	noRef := func(t *testing.T, heldFit rank.Fit, v timing.Verdict, replaced bool) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			r.provider("p", "en").answer(nil, cand(1, "en", sameRelease, withRef("a"))).serve("a", nil, srtBytes)
			inst := r.seedHeld(nil, unverified(heldFit, true), time.Now())
			r.script(alignedAs(t, v))
			r.mustRun(inst, statedb.Item)
			if got := r.record(inst).Provenance.Timing.Method == timing.MethodReference; got != replaced {
				t.Errorf("replaced %v, want %v", got, replaced)
			}
		})
	}
	t.Run("no reference, a strict gain unverified", func(t *testing.T) { noRef(t, rank.Other, timing.Unverified, true) })
	t.Run("no reference, no gain unverified", func(t *testing.T) { noRef(t, rank.SameRelease, timing.Unverified, false) })
	t.Run("no reference, no gain verified", func(t *testing.T) { noRef(t, rank.SameRelease, timing.Verified, true) })
}

func TestRefusedPayloadUsesTrialNeverDelivered(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en")
		var cands []provider.Candidate
		for _, ref := range []string{"a", "b", "c", "d"} {
			cands = append(cands, cand(1, "en", withRef(ref)))
			p.serve(ref, nil, srtBytes)
		}
		p.answer(nil, cands...)
		inst := r.seed(show, nil, ep1)[0]
		r.script(refusedCut, unreadable, refusedCut, alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		if got := p.fetches(); !slices.Equal(got, []string{"a", "b", "c"}) {
			t.Errorf("fetches %q, want three trials, the fourth candidate never fetched", got)
		}
		if subs := r.subtitles(inst); len(subs) != 0 {
			t.Errorf("subtitles %q, want none", subs)
		}
		if got := spellings(r.history(keyOf(t, show, ep1))); !slices.Equal(got, []string{"different_cut", "format_unsupported", "different_cut"}) {
			t.Errorf("rows %q, want the three refusals recorded", got)
		}
	})
}

func TestOnlyDeliveredPublishedAllRecorded(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil, cand(1, "en", withRef("a")), cand(1, "en", withRef("b"))).
			serve("a", nil, srtBytes).serve("b", nil, srtBytes)
		inst := r.seed(show, nil, ep1)[0]
		r.script(refusedCut, alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		if subs := r.subtitles(inst); len(subs) != 1 {
			t.Errorf("subtitles %q, want only the delivered one", subs)
		}
		rows := r.history(keyOf(t, show, ep1))
		a, b := rowOf(t, rows, "p", "a"), rowOf(t, rows, "p", "b")
		if len(rows) != 2 || a.Run != b.Run {
			t.Fatalf("history %+v, want both attempts of one run", rows)
		}
		if a.Delivered || a.Refusal != "different_cut" || a.Attempt != 1 {
			t.Errorf("first attempt %+v, want a refused different_cut", a)
		}
		if !b.Delivered || b.Attempt != 2 {
			t.Errorf("second attempt %+v, want b delivered", b)
		}
	})
}

func TestRefusedCallWritesNoRow(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en").answer(nil, cand(1, "en", withRef("a1")), cand(1, "en", withRef("a2")), cand(1, "en", withRef("a3"))).
			serve("a1", &provider.Failure{Kind: provider.RateLimited, Msg: "slow down", RetryAfter: time.Hour}, nil).
			serve("a2", nil, srtBytes).serve("a3", nil, srtBytes)
		q := r.provider("q", "en").answer(nil, cand(1, "en", withRef("b1")), cand(1, "en", withRef("b2"))).
			serve("b1", nil, srtBytes).serve("b2", nil, srtBytes)
		inst := r.seed(show, nil, ep1)[0]
		r.script(refusedCut, alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		if got := p.fetches(); !slices.Equal(got, []string{"a1"}) {
			t.Errorf("p fetched %q, want a1 only, its pause refusing a2 and a3", got)
		}
		if got := q.fetches(); !slices.Equal(got, []string{"b1", "b2"}) {
			t.Errorf("q fetched %q, want b1 and b2", got)
		}
		rows := r.history(keyOf(t, show, ep1))
		if got := spellings(rows); !slices.Equal(got, []string{"fetch_failed", "different_cut", ""}) {
			t.Errorf("rows %q, want one per admitted fetch", got)
		}
		if b2 := rowOf(t, rows, "q", "b2"); !b2.Delivered || b2.Attempt != 3 {
			t.Errorf("delivered row %+v, want attempt 3, the refused calls using no trial", b2)
		}
	})
}

// rootsChanged has the writer apply the rig's roots as they now stand.
func (r *rig) rootsChanged() {
	r.writer.RootsChanged()
	synctest.Wait()
}

func TestDeliveryWriterRefusals(t *testing.T) {
	cases := []struct {
		hook func(r *rig, inst statedb.InstanceID) error
		want error
		name string
	}{
		{name: "outside every root skips the pair", hook: func(r *rig, _ statedb.InstanceID) error {
			other := r.t.TempDir()
			if err := os.WriteFile(filepath.Join(other, ".root"), nil, 0o644); err != nil {
				return err
			}
			r.roots = []string{other}
			r.rootsChanged()
			return nil
		}},
		{name: "an unavailable root defers", want: mediawrite.ErrRootUnavailable, hook: func(r *rig, _ statedb.InstanceID) error {
			if err := os.Rename(r.dir, r.dir+".gone"); err != nil {
				return err
			}
			r.rootsChanged()
			return nil
		}},
		{name: "a pending write on the name defers", want: mediawrite.ErrPathPending, hook: func(r *rig, inst statedb.InstanceID) error {
			cues, err := timing.ParseCues(srtBytes)
			if err != nil {
				return err
			}
			rec := statedb.FileRecord{Inst: inst, Name: stem(1) + ".en.srt", Tag: mustTag("en"), Timing: timing.Identity{}, Provenance: &statedb.Provenance{Provider: "other"}}
			row := statedb.Download{
				At: time.Now(), Target: rec.Tag, Variant: language.Standard, Items: []media.Key{keyOf(r.t, show, ep1)},
				Inst: inst, Run: statedb.RunID{2}, Attempt: 1, Candidate: [32]byte{7}, Delivered: true,
			}
			return r.db.BeginDeliver(r.t.Context(), statedb.PendingDeliver{Record: rec, Baseline: cues, Attempts: []statedb.Download{row}})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				p := r.provider("p", "en").answer(nil, cand(1, "en", sameRelease)).serve("ref-en-01", nil, srtBytes)
				inst := r.seed(show, nil, ep1)[0]
				p.onFetch = func(context.Context) error {
					if err := c.hook(r, inst); err != nil {
						t.Error(err)
					}
					return nil
				}
				r.script(alignedAs(t, timing.Verified))
				err := r.run(inst, statedb.Scan)
				if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
					t.Fatalf("run = %v, want %v", err, c.want)
				}
				if got := rowOf(t, r.history(keyOf(t, show, ep1)), "p", "ref-en-01"); got.Delivered || got.Refusal != search.RefusalWriteFailed {
					t.Errorf("row %+v, want the attempt recorded undelivered, write_failed", got)
				}
			})
		})
	}
}

func TestRunFetchesCandidateOnce(t *testing.T) {
	inRig(t, "close_varieties: true\ndefault: [{tag: pt-BR}, {tag: pt-PT}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "pt-BR", "pt-PT").answer(nil, cand(1, "pt-PT", sameRelease, withRef("x"))).serve("x", nil, srtBytes)
		inst := r.seed(show, nil, ep1)[0]
		r.script(alignedAs(t, timing.Verified), alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		if got := p.fetches(); !slices.Equal(got, []string{"x"}) {
			t.Errorf("fetches %q, want the candidate both pairs rank fetched once", got)
		}
		if subs := r.subtitles(inst); len(subs) != 1 {
			t.Errorf("subtitles %q, want one", subs)
		}
	})
}

// zipOf is a zip archive of the named members.
func zipOf(t *testing.T, members ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, name := range members {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\n" + name + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func gzipOf(t *testing.T, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPayloadUnpackedForItem(t *testing.T) {
	cases := []struct {
		payload func(t *testing.T) []byte
		name    string
		want    string // the member aligned, "" for a payload refused
		refusal string
		season  int // the season's episodes the root holds
	}{
		{name: "the member claiming the item", want: "Show.S01E02.srt", season: 3, payload: func(t *testing.T) []byte {
			return zipOf(t, "Show.S01E01.srt", "Show.S01E02.srt", "Show.S01E03.srt")
		}},
		{name: "a bare number where nothing claims an episode", want: "02.srt", season: 2, payload: func(t *testing.T) []byte {
			return zipOf(t, "01.srt", "02.srt")
		}},
		{name: "a claim leaves a bare number unread", refusal: "episode_count", season: 2, payload: func(t *testing.T) []byte {
			return zipOf(t, "Show.S01E01.srt", "02.srt")
		}},
		{name: "no member for the item refuses", refusal: "format_unsupported", season: 3, payload: func(t *testing.T) []byte {
			return zipOf(t, "Show.S01E01.srt", "Show.S01E03.srt", "Show.S01E04.srt")
		}},
		{name: "a gzip layer is removed", want: "Show.S01E02.srt", season: 2, payload: func(t *testing.T) []byte {
			return gzipOf(t, zipOf(t, "Show.S01E01.srt", "Show.S01E02.srt"))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				r.provider("p", "en").answer(nil, cand(2, "en", withRef("x"))).serve("x", nil, c.payload(t))
				var items []mediasource.Item
				for n := 1; n <= c.season; n++ {
					items = append(items, episode(n))
				}
				inst := r.seed(show, nil, items...)[1]
				rec := r.script(alignedAs(t, timing.Verified))
				r.mustRun(inst, statedb.Scan)
				asked := rec.Payloads()
				row := rowOf(t, r.history(keyOf(t, show, episode(2))), "p", "x")
				if c.want == "" {
					if len(asked) != 0 || row.Refusal != c.refusal {
						t.Errorf("aligned %d payloads, row %q; want none aligned and %s", len(asked), row.Refusal, c.refusal)
					}
					return
				}
				if len(asked) != 1 || !bytes.Contains(asked[0], []byte(c.want)) {
					t.Errorf("aligned %q (row %q), want the member %s", asked, row.Refusal, c.want)
				}
			})
		})
	}
}

func TestSupersedesOnlyAutomatic(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "an automatic substitute is replaced"
		if manual {
			name = "a manual substitute is kept"
		}
		t.Run(name, func(t *testing.T) {
			inRig(t, "close_varieties: true\ndefault: [{tag: pt-BR}]", func(t *testing.T, r *rig) {
				r.provider("p", "pt-BR", "pt-PT").answer(nil, cand(1, "pt-BR", withRef("x"))).serve("x", nil, srtBytes)
				inst := r.seed(show, textTrack, ep1)[0]
				prov := verified(rank.SameRelease)
				prov.Manual = manual
				r.deliver(inst, keyOf(t, show, ep1), stem(1)+".pt-PT.srt", mustTag("pt-PT"), language.Flags{}, prov, language.Standard, time.Now().Add(-400*day))
				r.script(alignedAs(t, timing.Verified))
				r.mustRun(inst, statedb.Scan)
				want := []string{stem(1) + ".pt-BR.srt"}
				if manual {
					want = []string{stem(1) + ".pt-BR.srt", stem(1) + ".pt-PT.srt"}
				}
				got := r.subtitles(inst)
				slices.Sort(got)
				if !slices.Equal(got, want) {
					t.Errorf("subtitles %q, want %q", got, want)
				}
			})
		})
	}
}

func TestWriteRefusalRetried(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p, inst := r.deliverable()
		p.onFetch = func(context.Context) error {
			r.rootGone()
			return nil
		}
		if err := r.run(inst, statedb.Scan); !errors.Is(err, mediawrite.ErrRootUnavailable) {
			t.Fatalf("run = %v, want ErrRootUnavailable", err)
		}
		p.onFetch = nil
		r.rootBack()
		r.script(alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		if got := p.fetches(); !slices.Equal(got, []string{"ref-en-01", "ref-en-01"}) {
			t.Errorf("fetches %q, want the candidate the writer refused fetched again", got)
		}
		if subs := r.subtitles(inst); len(subs) != 1 {
			t.Errorf("subtitles %q, want the replay delivered", subs)
		}
	})
}
