package search_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/searchtest"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

// hostileRAR is a solid RAR 5 archive of one member, a.srt, whose header
// declares a 4 GiB dictionary (compression information 1<<7 | 0x40 | 15<<10).
const hostileRAR = "Rar!\x1a\a\x01\x00\xdc\xde^5\x03\x01\x00\x04\x9b?*\n\x0f\x02\x02@\x00@ \xc0y\x01\x05a.srt" +
	"UUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUU\x19\xb2:5\x03\x05\x00\x00"

// uploadOf is name's bytes b uploaded for item k as en Standard.
func uploadOf(k media.Key, name string, b []byte) search.UploadFile {
	return search.UploadFile{Item: k, Target: mustTag("en"), Variant: language.Standard, Name: name, Bytes: b}
}

// firstItem is inst's first item.
func (r *rig) firstItem(inst statedb.InstanceID) media.Key {
	r.t.Helper()
	in, found, err := r.db.Instance(r.t.Context(), inst)
	if err != nil || !found || len(in.Items) == 0 {
		r.t.Fatalf("Instance(%d) = %+v, %v, %v", inst, in, found, err)
	}
	return in.Items[0]
}

// uploaded starts e's upload of f for inst and returns the settled job.
func (r *rig) uploaded(e *search.Engine, inst statedb.InstanceID, f search.UploadFile) status.Job {
	r.t.Helper()
	j, err := e.Upload(r.t.Context(), owner, inst, f, nil)
	if err != nil {
		r.t.Fatalf("Upload: %v", err)
	}
	return r.settled(j.ID)
}

// wroteNothing fails the test when inst holds a file record or a subtitle,
// or k a download row.
func (r *rig) wroteNothing(inst statedb.InstanceID, k media.Key) {
	r.t.Helper()
	recs, err := r.db.Files(r.t.Context(), inst)
	if err != nil {
		r.t.Fatal(err)
	}
	if len(recs) != 0 {
		r.t.Errorf("records %+v, want none", recs)
	}
	if subs := r.subtitles(inst); len(subs) != 0 {
		r.t.Errorf("subtitles %q, want none", subs)
	}
	if rows := r.history(k); len(rows) != 0 {
		r.t.Errorf("download rows %+v, want none", rows)
	}
}

func TestUploadRefusesBeforeJob(t *testing.T) {
	k1, k2 := keyOf(t, show, ep1), keyOf(t, show, episode(2))
	for _, c := range []struct {
		want error
		name string
		f    search.UploadFile
		// other: the upload names the second instance plus 100.
		other bool
	}{
		{name: "an unknown instance", other: true, f: uploadOf(k1, "a.srt", srtBytes), want: search.ErrInstanceUnknown},
		{name: "an item of another instance", f: uploadOf(k2, "a.srt", srtBytes), want: search.ErrInstanceUnknown},
		{name: "an unconfigured target", f: search.UploadFile{Item: k1, Target: mustTag("fr"), Variant: language.Standard, Name: "a.srt", Bytes: srtBytes}, want: search.ErrTargetUnconfigured},
		{name: "a variant the target does not list", f: search.UploadFile{Item: k1, Target: mustTag("en"), Variant: language.HI, Name: "a.srt", Bytes: srtBytes}, want: search.ErrVariantUnserved},
	} {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				ids := r.seed(show, nil, ep1, episode(2))
				inst := ids[0]
				if c.other {
					inst = ids[1] + 100
				}
				key := &status.JobKey{Key: "k"}
				if _, err := r.engine().Upload(t.Context(), owner, inst, c.f, key); !errors.Is(err, c.want) {
					t.Errorf("Upload = %v, want %v", err, c.want)
				}
				if j, ok, err := r.st.Keyed(t.Context(), owner, *key); err != nil || ok {
					t.Errorf("Keyed = %+v, %v, %v; want no job begun", j, ok, err)
				}
			})
		})
	}
	over := make([]byte, provider.MaxBody+1)
	copy(over, srt)
	for name, b := range map[string][]byte{"over MaxBody": over, "empty": nil} {
		t.Run(name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				inst := r.seed(show, nil, ep1)[0]
				al := r.script(alignedAs(t, timing.Verified))
				j := r.uploaded(r.engine(), inst, uploadOf(k1, "a.srt", b))
				if j.Outcome != status.Failed || j.Cause != search.RefusalFormatUnsupported {
					t.Errorf("job %v %q, want Failed format_unsupported", j.Outcome, j.Cause)
				}
				if n := len(al.Payloads()); n != 0 {
					t.Errorf("%d payloads aligned, want none", n)
				}
				r.wroteNothing(inst, k1)
			})
		})
	}
}

func TestUploadArchiveCaps(t *testing.T) {
	names := make([]string, 4097)
	for i := range names {
		names[i] = fmt.Sprintf("Show.S01E01.part%04d.srt", i)
	}
	for _, c := range []struct {
		data  func(t *testing.T) []byte
		name  string
		align searchtest.Result
		// aligned: the bytes reach Align, which refuses them.
		aligned bool
	}{
		{name: "a zip of 4,097 entries", data: func(t *testing.T) []byte { return zipOf(t, names...) }, align: alignedAs(t, timing.Verified)},
		{name: "a rar declaring a 4 GiB dictionary", data: func(*testing.T) []byte { return []byte(hostileRAR) }, align: alignedAs(t, timing.Verified)},
		{name: "a file ParseCues cannot read", data: func(*testing.T) []byte { return []byte("not a subtitle") }, align: unreadable, aligned: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				inst := r.seed(show, nil, ep1)[0]
				e := r.engine()
				al := r.script(c.align)
				j := r.uploaded(e, inst, uploadOf(keyOf(t, show, ep1), "upload.bin", c.data(t)))
				if j.Outcome != status.Failed || j.Cause != search.RefusalFormatUnsupported {
					t.Errorf("job %v %q, want Failed format_unsupported", j.Outcome, j.Cause)
				}
				if n := len(al.Payloads()); n != 0 && !c.aligned {
					t.Errorf("%d payloads reached Align, want the archive refused first", n)
				}
				r.wroteNothing(inst, keyOf(t, show, ep1))
			})
		})
	}
}

func TestUploadDeliversLocked(t *testing.T) {
	t.Run("a season pack's member for its item", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			const name = "Futurama S02\r\npack.zip"
			pack := addic7ed()
			inst := r.seedFuturama(5)
			k := r.firstItem(inst)
			e := r.engine()
			al := r.script(alignedAs(t, timing.Verified))
			if j := r.uploaded(e, inst, uploadOf(k, name, zipOf(t, pack...))); j.Outcome != status.Succeeded {
				t.Fatalf("job %v %q, want Succeeded", j.Outcome, j.Cause)
			}
			if got := alignedMembers(al, pack); !slices.Equal(got, []string{pack[4]}) {
				t.Errorf("aligned %q, want 2x05, the member 8.11 picks for the item", got)
			}
			want, _ := provider.BoundText(name)
			rec := r.record(inst)
			p := rec.Provenance
			if !rec.Locked || rec.Ordinal == 0 || !p.Manual || !p.Uploaded || p.Provider != "" {
				t.Errorf("record %+v with provenance %+v, want locked, numbered, manual, uploaded and no provider", rec, p)
			}
			if p.Release != want || strings.ContainsAny(p.Release, "\r\n") {
				t.Errorf("release %q, want the name sanitized to one line, %q", p.Release, want)
			}
			if rows := r.history(k); len(rows) != 1 || !rows[0].Delivered || !rows[0].Provenance.Uploaded {
				t.Errorf("download rows %+v, want the one delivered upload", rows)
			}
		})
	})
	t.Run("the uploaded item of a two-item instance", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			ep2 := episode(2)
			ep2.VideoPath = ep1.VideoPath
			inst := r.seed(show, nil, ep1, ep2)[0]
			e := r.engine()
			al := r.script(alignedAs(t, timing.Verified))
			members := []string{"Show.S01E01.srt", "Show.S01E02.srt"}
			if j := r.uploaded(e, inst, uploadOf(keyOf(t, show, ep2), "pack.zip", zipOf(t, members...))); j.Outcome != status.Succeeded {
				t.Fatalf("job %v %q, want Succeeded", j.Outcome, j.Cause)
			}
			if got := alignedMembers(al, members); !slices.Equal(got, []string{"Show.S01E02.srt"}) {
				t.Errorf("aligned %q, want the uploaded item's member", got)
			}
		})
	})
	t.Run("an Align refusal", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			inst := r.seed(show, nil, ep1)[0]
			e := r.engine()
			r.script(refusedCut)
			j := r.uploaded(e, inst, uploadOf(keyOf(t, show, ep1), "a.srt", srtBytes))
			if j.Outcome != status.Failed || j.Cause != search.RefusalDifferentCut {
				t.Errorf("job %v %q, want Failed different_cut", j.Outcome, j.Cause)
			}
			r.wroteNothing(inst, keyOf(t, show, ep1))
		})
	})
	t.Run("the variant's flags", func(t *testing.T) {
		inRig(t, "default: [{tag: en, variants: [standard, hi]}]", func(t *testing.T, r *rig) {
			inst := r.seed(show, nil, ep1)[0]
			e := r.engine()
			r.script(alignedAs(t, timing.Verified))
			f := uploadOf(keyOf(t, show, ep1), "pack.zip", zipOf(t, "Show.S01E01.hi.forced.srt"))
			f.Variant = language.HI
			if j := r.uploaded(e, inst, f); j.Outcome != status.Succeeded {
				t.Fatalf("job %v %q, want Succeeded", j.Outcome, j.Cause)
			}
			if rec := r.record(inst); rec.Flags != (language.Flags{HI: true}) {
				t.Errorf("flags %+v, want HI's alone, whatever the member's name says", rec.Flags)
			}
		})
	})
}

func TestUploadKeyedReplay(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		inst := r.seed(show, nil, ep1)[0]
		e := r.engine()
		al := r.script(alignedAs(t, timing.Verified))
		f := uploadOf(keyOf(t, show, ep1), "a.srt", srtBytes)
		key := &status.JobKey{Key: "k", Fingerprint: [32]byte{1}}
		var (
			wg   sync.WaitGroup
			ids  [2]status.JobID
			errs [2]error
		)
		for i := range 2 {
			wg.Go(func() {
				j, err := e.Upload(t.Context(), owner, inst, f, key)
				ids[i], errs[i] = j.ID, err
			})
		}
		wg.Wait()
		if errs[0] != nil || errs[1] != nil || ids[0] != ids[1] || ids[0] == "" {
			t.Fatalf("concurrent same-key uploads = %v %v, %v; want one job", ids, errs[0], errs[1])
		}
		if j := r.settled(ids[0]); j.Outcome != status.Succeeded {
			t.Fatalf("job %v %q, want Succeeded", j.Outcome, j.Cause)
		}
		if j, err := r.engine().Upload(t.Context(), owner, inst, f, key); err != nil || j.ID != ids[0] {
			t.Errorf("the resend on the next engine = %v, %v; want job %s", j.ID, err, ids[0])
		}
		if n := len(al.Payloads()); n != 1 {
			t.Errorf("%d payloads aligned, want one delivery", n)
		}
		r.record(inst)
		r.retire()
		if _, found, err := r.db.Instance(t.Context(), inst); err != nil || found {
			t.Fatalf("Instance after the retiring pass = %v, %v; want it gone", found, err)
		}
		if j, err := r.engine().Upload(t.Context(), owner, inst, f, key); err != nil || j.ID != ids[0] {
			t.Errorf("the resend after the instance retired = %v, %v; want job %s", j.ID, err, ids[0])
		}
	})
}

func TestUploadResendRacingRetirement(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.hooked()
		inst := r.seed(show, nil, ep1)[0]
		e := r.engine()
		r.script(alignedAs(t, timing.Verified))
		f := uploadOf(keyOf(t, show, ep1), "a.srt", srtBytes)
		key := &status.JobKey{Key: "k", Fingerprint: [32]byte{1}}
		type answer struct {
			err error
			j   status.Job
		}
		resent := make(chan answer, 1)
		// While the original records its job, the instance retires and the
		// resend arrives: it must wait for the original and find its job.
		p.onInsert = func() {
			p.onInsert = nil
			r.retire()
			go func() {
				j, err := e.Upload(t.Context(), owner, inst, f, key)
				resent <- answer{err: err, j: j}
			}()
			synctest.Wait()
		}
		j, err := e.Upload(t.Context(), owner, inst, f, key)
		if err != nil {
			t.Fatal(err)
		}
		if got := <-resent; got.err != nil || got.j.ID != j.ID {
			t.Errorf("the resend = %v, %v; want job %s", got.j.ID, got.err, j.ID)
		}
		r.settled(j.ID)
	})
}

func TestUploadDuringDrain(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		inst := r.seed(show, nil, ep1)[0]
		e := r.engine()
		r.script(alignedAs(t, timing.Verified))
		search.BeginDrain(e)
		j, err := e.Upload(t.Context(), owner, inst, uploadOf(keyOf(t, show, ep1), "a.srt", srtBytes), nil)
		if err != nil {
			t.Fatal(err)
		}
		if j.State != status.Done || j.Outcome != status.Interrupted || j.Cause != search.CauseSettingsChanged {
			t.Errorf("job %+v, want Interrupted, settings_changed", j)
		}
		r.wroteNothing(inst, keyOf(t, show, ep1))
	})
}

func TestDeliveredJobSucceedsThroughDrain(t *testing.T) {
	for _, via := range []string{"download", "upload"} {
		t.Run(via, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				_, inst, e, h := r.manual()
				r.script(alignedAs(t, timing.Verified))
				stop := r.drain(e)
				// The drain arrives while Deliver runs past its intent, so
				// the file lands under an ended job context.
				search.WrapDeliver(e, func(inner search.DeliverFunc) search.DeliverFunc {
					return func(ctx context.Context, d mediawrite.Delivery) (statedb.FileRecord, error) {
						rec, err := inner(ctx, d)
						go func() { _ = stop() }()
						<-ctx.Done()
						return rec, err
					}
				})
				var (
					j   status.Job
					err error
				)
				if via == "download" {
					j, err = e.Download(t.Context(), owner, h, language.Standard, nil)
				} else {
					j, err = e.Upload(t.Context(), owner, inst, uploadOf(keyOf(t, show, ep1), "a.srt", srtBytes), nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				if j = r.settled(j.ID); j.Outcome != status.Succeeded {
					t.Errorf("job %v %q, want Succeeded: the file was delivered", j.Outcome, j.Cause)
				}
				r.record(inst)
			})
		})
	}
}
