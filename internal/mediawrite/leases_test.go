package mediawrite_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/statedb"
)

// TestLeaseOrderNoDeadlock runs Deliver-shaped lease sets (the new name and
// the names it supersedes), Delete-shaped ones (one name) and List-shaped
// ones (the instance, then the names it reconciles) of four instances over
// shared paths, each naming its paths in a random order.
func TestLeaseOrderNoDeadlock(t *testing.T) {
	t.Parallel()
	b := filepath.Join(t.TempDir(), "b")
	w := newWriter(t, mediawrite.Deps{Store: openStore(t), Roots: rootsOf(b)}, nil)
	paths := make([]string, 6)
	for i := range paths {
		paths[i] = fmt.Sprintf("Show/E01.%d.srt", i)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var held atomic.Int32
	var mu sync.Mutex
	inside := map[string]bool{}
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Go(func() {
			r := rand.New(rand.NewPCG(uint64(g), 1))
			for range 200 {
				set := slices.Clone(paths)
				r.Shuffle(len(set), func(i, j int) { set[i], set[j] = set[j], set[i] })
				set = set[:1+r.IntN(3)]
				unlock, err := lease(ctx, w, statedb.InstanceID(1+g%4), b, set, g/4%2 == 0)
				if err != nil {
					t.Errorf("lease(%q): %v", set, err)
					return
				}
				held.Add(1)
				mu.Lock()
				for _, p := range set {
					if inside[p] {
						t.Errorf("%s leased twice at once", p)
					}
					inside[p] = true
				}
				mu.Unlock()
				mu.Lock()
				for _, p := range set {
					delete(inside, p)
				}
				mu.Unlock()
				unlock()
			}
		})
	}
	wg.Wait()
	if got := held.Load(); got != 16*200 {
		t.Errorf("%d lease sets held, want %d", got, 16*200)
	}
}

// lease takes inst and set as one write does, or as List does: the
// instance alone, then the paths.
func lease(ctx context.Context, w *mediawrite.Writer, inst statedb.InstanceID, root string, set []string, listShaped bool) (func(), error) {
	if !listShaped {
		return mediawrite.Lease(ctx, w, inst, root, set...)
	}
	unlockInst, err := mediawrite.Lease(ctx, w, inst, root)
	if err != nil {
		return nil, err
	}
	unlockPaths, err := mediawrite.LockPaths(ctx, w, root, set...)
	if err != nil {
		unlockInst()
		return nil, err
	}
	return func() { unlockPaths(); unlockInst() }, nil
}
