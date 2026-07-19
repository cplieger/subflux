package server

import (
	"context"
	"time"

	"github.com/cplieger/atomicfile/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/confighandlers"
	"github.com/cplieger/subflux/internal/server/coveragehandlers"
	"github.com/cplieger/subflux/internal/server/filehandlers"
	"github.com/cplieger/subflux/internal/server/manualops"
	"github.com/cplieger/subflux/internal/server/mediahandlers"
	"github.com/cplieger/subflux/internal/server/polling"
	"github.com/cplieger/subflux/internal/server/previewhandlers"
	"github.com/cplieger/subflux/internal/server/queryhandlers"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/server/serveradapter"
	"github.com/cplieger/subflux/internal/server/synchandlers"
)

// newResolver builds the S7 typed-reference resolver over the store and the
// live (hot-reloadable) config + arr clients. One resolver instance is
// shared by every reference-taking handler family so resolution semantics
// cannot drift between endpoints.
func (s *Server) newResolver() *resolve.Resolver {
	return &resolve.Resolver{
		Store: s.db,
		State: func() *resolve.State {
			ls := s.state()
			st := &resolve.State{}
			if ls.cfg != nil {
				st.Cfg = ls.cfg
			}
			if ls.sonarr != nil {
				st.Sonarr = ls.sonarr
			}
			if ls.radarr != nil {
				st.Radarr = ls.radarr
			}
			return st
		},
	}
}

// initHandlers constructs all handler families on the server.
// Called from New() after options are applied and live state is initialized.
func (s *Server) initHandlers() {
	s.pollCache = polling.NewPollCache(
		func(ctx context.Context, key api.PollKey) (time.Time, error) {
			return s.db.GetPollTimestamp(ctx, key)
		},
		func(ctx context.Context, key api.PollKey, t time.Time) error {
			err := s.db.SetPollTimestamp(ctx, key, t)
			// The poll heartbeat writes every cycle, making it the earliest
			// detector of a disk-full store between daily reconciles; classify
			// the error and raise the persistent operator alert here instead of
			// waiting for the next maintenance window.
			if err != nil {
				(&serveradapter.AlertAdapter{A: s.alerts}).RecordStoreWriteError(err)
			}
			return err
		},
	)
	if s.metrics != nil {
		s.pollCache.SetDirtyGauge(s.metrics.SetPollCursorsDirty)
	}
	s.queryH = queryhandlers.New(queryhandlers.Deps{
		QueryDB:      s.stores.query,
		CovDB:        s.db,
		Metrics:      s.metrics,
		State:        s.queryLiveState,
		Configured:   func() bool { return s.configured.Load() },
		CountMissing: countMissing,
	})
	s.configH = confighandlers.New(&confighandlers.Deps{
		LoadConfig:    s.loadConfig,
		SchemaFunc:    s.schemaFunc,
		DefaultConfig: s.defaultConfig,
		Registry:      s.registry,
		Alerts:        s.alerts,
		NewSonarr:     s.newSonarr,
		NewRadarr:     s.newRadarr,
		HotReload:     s.hotReload,
		State: func() confighandlers.StateView {
			ls := s.state()
			return confighandlers.StateView{Cfg: ls.cfg}
		},
		Configured: func() bool { return s.configured.Load() },
		ConfigPath: configFilePath,
	})
	s.poller = polling.NewPoller(polling.Deps{
		PollCache:  s.pollCache,
		Store:      s.db,
		Metrics:    s.metrics,
		Alerts:     s.alerts,
		Events:     s.events,
		StatsCache: s.queryH.StatsInvalidator(),
	}, s.pollerLiveState)
	s.scanH = s.initScanHandler()
	resolver := s.newResolver()
	s.manualH = s.initManualHandler(resolver)

	s.previewH = previewhandlers.NewHandler(previewhandlers.Deps{
		SubtitleProc: s.subtitleProc,
		FFmpegSem:    s.ffmpegSem,
		PosterClient: s.posterClient,
		Resolve:      resolver,
		StateFunc: func() *previewhandlers.LiveState {
			ls := s.state()
			pls := &previewhandlers.LiveState{}
			if ls.cfg != nil {
				pls.HasSonarr = ls.sonarr != nil
				pls.HasRadarr = ls.radarr != nil
				if ls.sonarr != nil {
					sc := ls.cfg.SonarrConfig()
					pls.SonarrConfig = previewhandlers.ArrConfig{URL: sc.URL, APIKey: sc.APIKey}
				}
				if ls.radarr != nil {
					rc := ls.cfg.RadarrConfig()
					pls.RadarrConfig = previewhandlers.ArrConfig{URL: rc.URL, APIKey: rc.APIKey}
				}
			}
			return pls
		},
		ReadBounded: atomicfile.ReadBounded,
		ServerCtx:   func() context.Context { return s.ctx },
	})

	s.coverageH = coveragehandlers.NewHandler(coveragehandlers.Deps{
		Store: s.db,
		StateFunc: func() *coveragehandlers.LiveState {
			ls := s.state()
			return &coveragehandlers.LiveState{
				Cfg:    ls.cfg,
				Sonarr: ls.sonarr,
				Radarr: ls.radarr,
			}
		},
	})
	s.fileH = filehandlers.NewHandler(filehandlers.Deps{
		Store:   s.db,
		Resolve: resolver,
		StateFunc: func() *filehandlers.LiveState {
			ls := s.state()
			fls := &filehandlers.LiveState{Cfg: ls.cfg}
			if ls.sonarr != nil {
				fls.Sonarr = ls.sonarr
			}
			if ls.radarr != nil {
				fls.Radarr = ls.radarr
			}
			return fls
		},
		Events: s.events,
	})
	s.mediaH = mediahandlers.NewHandler(mediahandlers.Deps{
		StateFunc: func() *mediahandlers.LiveState {
			ls := s.state()
			return &mediahandlers.LiveState{
				Cfg:    ls.cfg,
				Sonarr: ls.sonarr,
				Radarr: ls.radarr,
			}
		},
		ServerCtx: func() context.Context { return s.ctx },
	})

	s.syncH = synchandlers.New(synchandlers.Deps{
		Store:        s.stores.sync,
		SubtitleProc: s.subtitleProc,
		Activity:     s.activity,
		Resolve:      resolver,
	})
}

// initManualHandler constructs the manualops.Handler with the server's dependencies.
func (s *Server) initManualHandler(resolver *resolve.Resolver) *manualops.Handler {
	return manualops.NewHandler(manualops.HandlerDeps{
		// api.Store is a compile-checked superset of manualops.DownloadStore,
		// so the implicit interface conversion needs no assertion.
		DBFunc:   func() manualops.DownloadStore { return s.db },
		Activity: &serveradapter.ActivityAdapter{A: s.activity},
		Alerts:   &serveradapter.AlertAdapter{A: s.alerts},
		Events:   &serveradapter.ManualEventAdapter{E: s.events},
		StateFunc: func() *manualops.LiveState {
			ls := s.state()
			return &manualops.LiveState{
				Cfg: ls.cfg, Engine: ls.engine, Scorer: ls.scorer,
				Sonarr: ls.sonarr, Radarr: ls.radarr,
				SonarrLib: ls.sonarr, RadarrLib: ls.radarr,
				Providers: ls.providers,
			}
		},
		BGTracker:  &s.bgWg,
		ServerCtx:  func() context.Context { return s.ctx },
		Resolve:    resolver,
		DecodeJSON: decodeJSONBodyAny,
	})
}
