package search

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// UploadFile is a subtitle file or archive a user brings for Item, to serve
// Target's Variant. Upload keeps Bytes until its job ends, so the caller
// must not change them.
type UploadFile struct {
	Bytes   []byte
	Name    string
	Target  language.Tag
	Item    media.Key
	Variant language.Variant
}

// Upload starts a Download job delivering f for inst, numbered, locked and
// Uploaded with no provider (8.7). With idem set, under Starts' lock on
// (owner, idem), the job started under that key is returned. Before any
// job it refuses ErrInstanceUnknown for an unknown inst or an Item not in
// it, ErrTargetUnconfigured and ErrVariantUnserved for a pair the Item's
// coverage does not list. The job ends as a Download job does, never
// provider_failed, and a refused file writes nothing.
func (e *Engine) Upload(ctx context.Context, owner int64, inst statedb.InstanceID, f UploadFile, idem *status.JobKey) (status.Job, error) { //nolint:gocritic // hugeParam: the file is the caller's value
	unlock, err := e.deps.Starts.lock(ctx, owner, idem)
	if err != nil {
		return status.Job{}, err
	}
	defer unlock()
	if idem != nil {
		if j, ok, kerr := e.deps.Status.Keyed(ctx, owner, *idem); kerr != nil || ok {
			return j, kerr
		}
	}
	err = e.uploadable(ctx, inst, &f)
	if err != nil {
		return status.Job{}, err
	}
	f.Name, _ = provider.BoundText(f.Name)
	job := status.Job{Kind: status.Download, Owner: owner, Key: idem, Scope: strconv.FormatUint(uint64(inst), 10)}
	j, replayed, err := e.begin(ctx, job, nil)
	if err != nil || replayed {
		return j, err
	}
	return e.queue(ctx, j, inst, "aligning", func(ctx context.Context) (status.Outcome, string) {
		return e.uploadOnce(ctx, inst, &f)
	})
}

// uploadable answers Upload's refusals of f for inst, nil for none.
func (e *Engine) uploadable(ctx context.Context, inst statedb.InstanceID, f *UploadFile) error {
	in, found, err := e.deps.Store.Instance(ctx, inst)
	if err != nil {
		return fmt.Errorf("search: read instance %d: %w", inst, err)
	}
	if !found || !slices.Contains(in.Items, f.Item) {
		return ErrInstanceUnknown
	}
	rows, err := e.deps.Store.Coverage(ctx, f.Item)
	if err != nil {
		return fmt.Errorf("search: read %s's coverage: %w", f.Item, err)
	}
	targeted := false
	for _, row := range rows {
		if row.Target == f.Target {
			if row.Variant == f.Variant {
				return nil
			}
			targeted = true
		}
	}
	if targeted {
		return ErrVariantUnserved
	}
	return ErrTargetUnconfigured
}

// uploadOnce aligns f for its item as a manual download's payload and
// delivers it, and answers the job's end.
func (e *Engine) uploadOnce(ctx context.Context, inst statedb.InstanceID, f *UploadFile) (end status.Outcome, cause string) {
	if len(f.Bytes) == 0 || len(f.Bytes) > provider.MaxBody {
		e.log.Info("search: upload refused", "instance", inst, "release", f.Name, "bytes", len(f.Bytes), "refusal", RefusalFormatUnsupported)
		return status.Failed, RefusalFormatUnsupported
	}
	r, ok := e.downloadRun(ctx, inst)
	item := -1
	if ok {
		item = slices.IndexFunc(r.items, func(it runItem) bool { return it.key == f.Item })
	}
	switch {
	case ctx.Err() != nil:
		return status.Interrupted, causeSettingsChanged
	case item < 0:
		return status.Failed, causeWriteFailed
	}
	t := &trial{r: r, want: &want{target: f.Target, variant: f.Variant}, item: item, id: newRunID(), manual: true}
	j := &judged{offer: &offer{cand: &provider.Candidate{Lang: f.Target, Release: f.Name, Flags: f.Variant.Flags()}}}
	var a aligned
	err := e.alignPayload(ctx, t, j, f.Bytes, &a)
	switch {
	case ctx.Err() != nil:
		return status.Interrupted, causeSettingsChanged
	case err != nil:
		return status.Failed, e.writeCause(inst, err)
	case a.refusal != refusalNone:
		e.log.Info("search: upload refused", "instance", inst, "release", f.Name, "refusal", a.refusal, "err", a.cause)
		return status.Failed, a.refusal
	}
	a.flags = f.Variant.Flags()
	row := statedb.Download{
		At: time.Now(), Target: f.Target, Variant: f.Variant, Items: r.inst.Items, Inst: inst, Run: t.id, Attempt: 1,
		Provenance: statedb.Provenance{Release: f.Name, Timing: a.Outcome, Facts: a.facts, Manual: true, Uploaded: true},
	}
	err = e.deliver(ctx, t, j, &row, &a)
	switch {
	case err == nil:
		return status.Succeeded, ""
	case ctx.Err() != nil:
		return status.Interrupted, causeSettingsChanged
	}
	return status.Failed, e.writeCause(inst, err)
}
