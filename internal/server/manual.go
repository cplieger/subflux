package server

import (
	"context"

	"github.com/cplieger/subflux/internal/server/manualops"
	"github.com/cplieger/subflux/internal/subflux"
)

// manualStore is the two rows manual search and download touch directly from
// the server root: what is already downloaded for a language, and releasing a
// manual lock. Two methods, and it is embedded in Store rather than asserted
// against a wide type, so the composite carries them by construction.
type manualStore interface {
	DownloadedRefs(ctx context.Context, mediaType subflux.MediaType, mediaID, language string) ([]subflux.DownloadedRef, error)
	ClearManualLock(ctx context.Context, key subflux.ManualLockKey) error
}

// manualLiveState converts the server's liveState to manualops.LiveState.
//
// The ONE conversion. initManualHandler used to rebuild this struct inline, field
// for field, while four thin wrappers here called this copy — and those wrappers
// had no production caller at all, so the converter the app actually ran was the
// duplicate and this one existed only for tests. The wrappers are deleted and
// StateFunc calls this.
func manualLiveState(ls *liveState) *manualops.LiveState {
	return &manualops.LiveState{
		Cfg:       ls.cfg,
		Engine:    ls.engine,
		Scorer:    ls.scorer,
		Sonarr:    ls.sonarr,
		Radarr:    ls.radarr,
		SonarrLib: ls.sonarr,
		RadarrLib: ls.radarr,
		Providers: ls.providers,
	}
}
