package seriesorder

import (
	"context"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// Evidence returns the Bound reading statedb last stored for src's root, the
// one its keys came from, in one read; false with none. It asks SkyHook
// nothing, so a title language the reading lacks is absent.
func (s *Service) Evidence(ctx context.Context, src media.SourceID, root mediasource.NativeRef) (mediasource.SeriesEvidence, bool, error) {
	ev, found, err := s.store.RootEvidence(ctx, src, root)
	if err != nil || !found {
		return mediasource.SeriesEvidence{}, false, err
	}
	return ev.Bound, true, nil
}
