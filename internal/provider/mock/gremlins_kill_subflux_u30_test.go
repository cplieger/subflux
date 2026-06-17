package mock

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// Kills mock.go:145:24 ARITHMETIC_BASE (time.NewTimer(5 * time.Second), * -> /).
// In "slow" mode the original arms a 5-second timer; against a 200ms context
// the context deadline fires first and Search returns ctx.Err() with no
// results. The "/" mutant computes 5 / time.Second == 0 (integer division),
// arming a 0-duration timer that fires immediately, so Search returns results
// with a nil error well within the 200ms window.
func Test_gk_subflux_u30_SlowModeRespectsTimer(t *testing.T) {
	p := &mockProvider{mode: "slow", resultCount: 1, scoreBase: 50}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req := &api.SearchRequest{MediaType: api.MediaTypeMovie, Title: "X", Year: 2020, Languages: []string{"en"}}

	subs, err := p.Search(ctx, req)
	if err == nil {
		t.Errorf("slow-mode Search err = nil, want a context-deadline error (the 5s timer must outlast the 200ms ctx)")
	}
	if subs != nil {
		t.Errorf("slow-mode Search subs = %v, want nil", subs)
	}
}
