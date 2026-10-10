package server

import "github.com/cplieger/subflux/internal/server/manualops"

// manualLiveState converts the server's liveState to manualops.LiveState; it
// is the only conversion.
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
