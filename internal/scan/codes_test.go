package scan_test

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/sentineltest"
	"github.com/cplieger/subflux/internal/status"
)

func TestCodesCoverFailureTable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := func(r *rig, req scan.Request) error {
			_, err := r.scanner.Start(t.Context(), req)
			return err
		}
		ownerFull := newRig(t, movieCaps, daily)
		for n := 1; n <= 4; n++ {
			if err := start(ownerFull, refetch(1, n)); err != nil {
				t.Fatal(err)
			}
		}
		sourceFull := newRig(t, movieCaps, daily)
		for n := 1; n <= 16; n++ {
			if err := start(sourceFull, refetch(int64(n), n)); err != nil {
				t.Fatal(err)
			}
		}
		keyed := newRig(t, movieCaps, daily)
		key := func(b byte) *status.JobKey { return &status.JobKey{Key: "K", Fingerprint: [32]byte{b}} }
		if err := start(keyed, scan.Request{Owner: 1, Source: src, Key: key(1)}); err != nil {
			t.Fatal(err)
		}

		for _, arm := range []struct {
			err  error
			name string
			want string
		}{
			{start(ownerFull, refetch(1, 5)), "Start past the owner's queue bound", "scan_capacity"},
			{start(sourceFull, refetch(17, 17)), "Start past the source's queue bound", "scan_capacity"},
			{start(keyed, scan.Request{Owner: 1, Source: src, Key: key(2)}), "Start with a used key and another request", "idempotency_key_reused"},
		} {
			if got := sentineltest.CodeOf(scan.Codes, arm.err); got != arm.want {
				t.Errorf("%s: code %q, want %q", arm.name, got, arm.want)
			}
		}

		err := start(ownerFull, scan.Request{Owner: 1, Source: "gone"})
		if _, ok := errors.AsType[*mediasource.SourceError](err); !ok {
			t.Errorf("Start naming an unknown source = %v, want a *SourceError", err)
		}
		if got := sentineltest.CodeOf(scan.Codes, err); got != "" {
			t.Errorf("the *SourceError matched Codes as %q, want it left to Owner.Typed", got)
		}
	})

	sentineltest.Placed(t, scan.Codes, map[string]error{"ErrScanCapacity": scan.ErrScanCapacity}, nil)
}
