package server

import (
	"context"
	"net/http"

	"subflux/internal/api"
	"subflux/internal/server/coveragehandlers"
	"subflux/internal/server/filehandlers"
	"subflux/internal/server/mediahandlers"
)

// Test-compatibility delegates: these forward to the extracted handler packages
// so that existing integration tests in package server continue to compile.
// Production routes use the handler fields directly (s.coverageH, s.fileH, s.mediaH).

func (s *Server) handleCoverageSeries(w http.ResponseWriter, r *http.Request) {
	s.ensureCoverageH()
	s.coverageH.HandleCoverageSeries(w, r)
}

func (s *Server) handleCoverageMovies(w http.ResponseWriter, r *http.Request) {
	s.ensureCoverageH()
	s.coverageH.HandleCoverageMovies(w, r)
}

func (s *Server) handleCoverageDetail(w http.ResponseWriter, r *http.Request) {
	s.ensureCoverageH()
	s.coverageH.HandleCoverageDetail(w, r)
}

func (s *Server) handleScanStates(w http.ResponseWriter, r *http.Request) {
	s.ensureCoverageH()
	s.coverageH.HandleScanStates(w, r)
}

// ensureCoverageH lazily initializes coverageH for tests that construct Server{} directly.
func (s *Server) ensureCoverageH() {
	s.coverageHOnce.Do(func() {
		s.coverageH = coveragehandlers.NewHandler(coveragehandlers.Deps{
			Store: &covStoreProxy{s: s},
			StateFunc: func() *coveragehandlers.LiveState {
				ls := s.state()
				return &coveragehandlers.LiveState{Cfg: ls.cfg, Sonarr: ls.sonarr, Radarr: ls.radarr}
			},
		})
	})
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	s.ensureFileH()
	s.fileH.HandleListFiles(w, r)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	s.ensureFileH()
	s.fileH.HandleDeleteFile(w, r)
}

func (s *Server) handleBulkDeleteFiles(w http.ResponseWriter, r *http.Request) {
	s.ensureFileH()
	s.fileH.HandleBulkDeleteFiles(w, r)
}

func (s *Server) handleHistoryIDs(w http.ResponseWriter, r *http.Request) {
	s.ensureFileH()
	s.fileH.HandleHistoryIDs(w, r)
}

func (s *Server) handleMediaSeries(w http.ResponseWriter, r *http.Request) {
	s.ensureMediaH()
	s.mediaH.HandleMediaSeries(w, r)
}

func (s *Server) handleMediaMovies(w http.ResponseWriter, r *http.Request) {
	s.ensureMediaH()
	s.mediaH.HandleMediaMovies(w, r)
}

func (s *Server) handleMediaEpisodes(w http.ResponseWriter, r *http.Request) {
	s.ensureMediaH()
	s.mediaH.HandleMediaEpisodes(w, r)
}

func (s *Server) deleteExternalFile(ctx context.Context, cfg api.ConfigProvider, mediaType api.MediaType, row *api.SubtitleFileRow) bool { //nolint:unparam // mediaType varies in production via filehandlers
	s.ensureFileH()
	return s.fileH.DeleteExternalFile(ctx, cfg, mediaType, row)
}

// ensureFileH lazily initializes fileH for tests that construct Server{} directly.
// Uses sync.Once to avoid data races when parallel tests call handler delegates.
func (s *Server) ensureFileH() {
	s.fileHOnce.Do(func() {
		s.fileH = filehandlers.NewHandler(filehandlers.Deps{
			Store:     &fileStoreProxy{s: s},
			StateFunc: func() *filehandlers.LiveState { return &filehandlers.LiveState{Cfg: s.state().cfg} },
			Events:    s.events,
		})
	})
}

// fileStoreProxy delegates to s.stores.file, allowing tests to swap the store.
type fileStoreProxy struct{ s *Server }

func (p *fileStoreProxy) GetSubtitleFiles(ctx context.Context, mt api.MediaType, prefix string) ([]api.SubtitleFileRow, error) {
	return p.s.stores.file.GetSubtitleFiles(ctx, mt, prefix)
}
func (p *fileStoreProxy) DeleteSubtitleFile(ctx context.Context, mt api.MediaType, mediaID, lang string, v api.Variant, src api.SubtitleSource, path string) error {
	return p.s.stores.file.DeleteSubtitleFile(ctx, mt, mediaID, lang, v, src, path)
}
func (p *fileStoreProxy) ManualSubtitlePaths(ctx context.Context, mt api.MediaType, mediaID, lang string) ([]string, error) {
	return p.s.stores.file.ManualSubtitlePaths(ctx, mt, mediaID, lang)
}
func (p *fileStoreProxy) ClearManualLock(ctx context.Context, mt api.MediaType, mediaID, lang string) error {
	return p.s.stores.file.ClearManualLock(ctx, mt, mediaID, lang)
}
func (p *fileStoreProxy) HistoryMediaIDs(ctx context.Context, mt api.MediaType, prefix string) ([]string, error) {
	return p.s.stores.file.HistoryMediaIDs(ctx, mt, prefix)
}

// ensureMediaH lazily initializes mediaH for tests that construct Server{} directly.
func (s *Server) ensureMediaH() {
	s.mediaHOnce.Do(func() {
		s.mediaH = mediahandlers.NewHandler(mediahandlers.Deps{
			StateFunc: func() *mediahandlers.LiveState {
				ls := s.state()
				return &mediahandlers.LiveState{Cfg: ls.cfg, Sonarr: ls.sonarr, Radarr: ls.radarr}
			},
			ServerCtx: func() context.Context { return s.ctx },
		})
	})
}

// Type aliases for test compatibility with extracted handler packages.
type seriesCoverage = coveragehandlers.SeriesCoverage
type movieCoverage = coveragehandlers.MovieCoverage
type fileEntry = filehandlers.FileEntry
type seriesItem = mediahandlers.SeriesItem
type movieItem = mediahandlers.MovieItem
type seasonGroup = mediahandlers.SeasonGroup

// covStoreProxy delegates to s.stores.cov, allowing tests to swap the store.
type covStoreProxy struct{ s *Server }

func (p *covStoreProxy) GetSubtitleFiles(ctx context.Context, mt api.MediaType, prefix string) ([]api.SubtitleFileRow, error) {
	return p.s.stores.cov.GetSubtitleFiles(ctx, mt, prefix)
}
func (p *covStoreProxy) GetScanStates(ctx context.Context, mt api.MediaType, prefix string) ([]api.ScanStateRow, error) {
	return p.s.stores.cov.GetScanStates(ctx, mt, prefix)
}
