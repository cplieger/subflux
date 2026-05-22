package server

import (
	"net/http"

	"subflux/internal/api"
	"subflux/internal/server/manualops"
)

// downloadStore is the narrow store interface used by manual_download.go.
// It matches manualops.DownloadStore so the field can be passed directly.
type downloadStore interface {
	manualops.DownloadStore
}

// Compile-time assertion: api.Store satisfies downloadStore.
var _ downloadStore = api.Store(nil)

// handleManualSearch delegates to the manualops Handler.
// Kept as a Server method for test backward compatibility.
func (s *Server) handleManualSearch(w http.ResponseWriter, r *http.Request) {
	s.manualH.HandleManualSearch(w, r)
}

// handleClearLock delegates to the manualops Handler.
func (s *Server) handleClearLock(w http.ResponseWriter, r *http.Request) {
	s.manualH.HandleClearLock(w, r)
}

// handleManualDownload delegates to the manualops Handler.
// Kept as a Server method for test backward compatibility.
func (s *Server) handleManualDownload(w http.ResponseWriter, r *http.Request) {
	s.manualH.HandleManualDownload(w, r)
}
