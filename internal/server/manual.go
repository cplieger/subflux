package server

import (
	"context"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/manualops"
)

// manualStore documents the api.Store methods used by manual search/download handlers.
type manualStore interface {
	DownloadedRefs(ctx context.Context, mediaType api.MediaType, mediaID, language string) ([]api.DownloadedRef, error)
	ClearManualLock(ctx context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) error
}

// Compile-time assertion: api.Store satisfies manualStore.
var _ manualStore = api.Store(nil)

// manualLiveState converts the server's liveState to manualops.LiveState.
func (s *Server) manualLiveState(ls *liveState) *manualops.LiveState {
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

// lookupMediaTitle wraps manualops.LookupMediaTitle, adapting from the
// server's liveState to manualops.LiveState.
func lookupMediaTitle(ctx context.Context, ls *liveState, mediaType api.MediaType, arrID int) string {
	return manualops.LookupMediaTitle(ctx, &manualops.LiveState{
		Cfg: ls.cfg, Engine: ls.engine, Sonarr: ls.sonarr, Radarr: ls.radarr, Providers: ls.providers,
	}, mediaType, arrID)
}

// lookupMovieMediaID wraps manualops.LookupMovieMediaID with the server's
// manualLiveState adapter.
func (s *Server) lookupMovieMediaID(ctx context.Context, ls *liveState, arrID int) string {
	return manualops.LookupMovieMediaID(ctx, s.manualLiveState(ls), arrID)
}

// lookupEpisodeMediaID wraps manualops.LookupEpisodeMediaID with the server's
// manualLiveState adapter.
func (s *Server) lookupEpisodeMediaID(ctx context.Context, ls *liveState, seriesID, season, episode int) string {
	return manualops.LookupEpisodeMediaID(ctx, s.manualLiveState(ls), seriesID, season, episode)
}

// resolveMediaIDs wraps manualops.ResolveMediaIDs with the server's
// manualLiveState adapter.
func (s *Server) resolveMediaIDs(ctx context.Context, ls *liveState,
	mediaType api.MediaType, arrID, season, episode int,
) (mediaID, title string) {
	return manualops.ResolveMediaIDs(ctx, s.manualLiveState(ls), mediaType, arrID, season, episode)
}
