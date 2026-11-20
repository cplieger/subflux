package scan_test

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/cplieger/go/httpwire"
	"github.com/cplieger/go/httpwire/httpwiretest"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/scan"
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
			if got := codeOf(arm.err, scan.Owner, status.Owner); got != arm.want {
				t.Errorf("%s: code %q, want %q", arm.name, got, arm.want)
			}
		}

		err := start(ownerFull, scan.Request{Owner: 1, Source: "gone"})
		if _, ok := errors.AsType[*mediasource.SourceError](err); !ok {
			t.Errorf("Start naming an unknown source = %v, want a *SourceError", err)
		}
		if got := codeOf(err, scan.Owner, status.Owner); got != "" {
			t.Errorf("the *SourceError matched an owner code %q, want it left to the handler", got)
		}
	})
}

func TestOwnerSentinels(t *testing.T) {
	httpwiretest.Sentinels(t, scan.Owner)
}

// codeOf is the code of the first entry of owners' Codes that err matches by
// errors.Is, "" when none does: the lookup httpwire's answer mapping makes.
func codeOf(err error, owners ...*httpwire.Owner) string {
	for _, o := range owners {
		for _, c := range o.Codes {
			if errors.Is(err, c.Err) {
				return c.Code
			}
		}
	}
	return ""
}
