package syncsched

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/status"
)

// TestGlobalProposalBoundAtAdmit: a reservation admitted and not yet held
// counts toward MaxProposals, so two admits racing status.Begin at one
// below the bound cannot both pass.
func TestGlobalProposalBoundAtAdmit(t *testing.T) {
	l := newLedger()
	now := time.Now()
	for i := range MaxProposals - 1 {
		o := int64(i/MaxProposalsPerOwner + 1)
		if err := l.admit(o, true, now); err != nil {
			t.Fatalf("reservation %d: %v", i, err)
		}
		l.hold(status.JobID(strconv.Itoa(i)), &proposalKey{owner: o})
		l.jobDone(o)
	}
	if err := l.admit(1_000, true, now); err != nil {
		t.Fatalf("the last reservation: %v", err)
	}
	if err := l.admit(1_001, true, now); !errors.Is(err, ErrSyncCapacity) {
		t.Errorf("a reservation past MaxProposals before the last one held: %v, want ErrSyncCapacity", err)
	}
	l.withdraw(1_000, true)
	if err := l.admit(1_001, true, now); err != nil {
		t.Errorf("after a withdrawn reservation: %v, want room for one", err)
	}
}
