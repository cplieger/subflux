// Package confighandlers provides HTTP handlers for configuration CRUD
// operations: get, save, reset, schema, and path validation.
package confighandlers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/cplieger/atomicfile/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/httphelpers"
)

// AlertLog is the narrow interface for alert operations.
type AlertLog interface {
	RecordPersistent(source, msg string)
}

// PathValidationResponse is the JSON response for path validation requests.
type PathValidationResponse struct {
	Error string `json:"error,omitempty"`
	Valid bool   `json:"valid"`
}

// Deps holds all dependencies for the config handler family.
type Deps struct {
	Registry      api.ProviderRegistry
	Alerts        AlertLog
	LoadConfig    api.ConfigLoader
	SchemaFunc    api.SchemaFunc
	NewSonarr     func(baseURL, apiKey string) (api.SonarrClient, error)
	NewRadarr     func(baseURL, apiKey string) (api.RadarrClient, error)
	HotReload     func(ctx context.Context, cfg api.ConfigProvider) error
	State         func() StateView
	ConfigPath    func() string
	Configured    func() bool
	DefaultConfig []byte
}

// StateView provides the live state needed by config handlers.
type StateView struct {
	Cfg api.ConfigProvider
}

// Handler holds all dependencies for the config handler family.
type Handler struct {
	registry      api.ProviderRegistry
	alerts        AlertLog
	loadConfig    api.ConfigLoader
	schemaFunc    api.SchemaFunc
	newSonarr     func(baseURL, apiKey string) (api.SonarrClient, error)
	newRadarr     func(baseURL, apiKey string) (api.RadarrClient, error)
	hotReload     func(ctx context.Context, cfg api.ConfigProvider) error
	state         func() StateView
	configured    func() bool
	configPath    func() string
	defaultConfig []byte

	// saveMu serializes the COMPLETE config-save transaction across every
	// entry point that reads or writes the config file (raw PUT, structured
	// PUT, unconfigured reset): existing-file read + secret merge,
	// canonicalization, old-state comparison + arr pings, activation
	// (hotReload), and persistence. Activation alone is serialized by the
	// server's reload mutex, but without this outer lock two concurrent
	// saves could interleave publish-A, publish-B, write-B, write-A —
	// leaving live state on B and the on-disk file on A with both requests
	// returning 200 — and a structured secret merge could read a stale
	// baseline generation. Holding a lock across the arr pings and
	// activation deliberately trades the no-lock-across-IO guideline for
	// transactional correctness: config saves are admin-rare, and save
	// order must equal activation order must equal persist order.
	saveMu sync.Mutex
}

// New creates a Handler with the given dependencies.
func New(d *Deps) *Handler {
	return &Handler{
		loadConfig:    d.LoadConfig,
		schemaFunc:    d.SchemaFunc,
		defaultConfig: d.DefaultConfig,
		registry:      d.Registry,
		alerts:        d.Alerts,
		newSonarr:     d.NewSonarr,
		newRadarr:     d.NewRadarr,
		hotReload:     d.HotReload,
		state:         d.State,
		configured:    d.Configured,
		configPath:    d.ConfigPath,
	}
}

// maxBodySize references the canonical constant from httphelpers.
const maxBodySize = httphelpers.MaxDefaultBodySize

// HandleGetConfig returns the current config file with secrets redacted.
func (h *Handler) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	configPath := h.configPath()
	data, err := atomicfile.ReadBounded(r.Context(), configPath, maxBodySize)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "read config", "path", configPath)
		return
	}
	data = RedactSecrets(data)
	w.Header().Set("Content-Type", "text/yaml")
	if _, err := w.Write(data); err != nil { // nosemgrep: no-direct-write-to-responsewriter -- raw YAML config, not HTML
		slog.Debug("write response failed", "error", err)
	}
}

// HandleSaveConfig validates, persists, and hot-reloads a new config.
func (h *Handler) HandleSaveConfig(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize+1))
	if err != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, "failed to read body")
		return
	}
	if int64(len(data)) > maxBodySize {
		slog.Warn("config request body too large", "size", len(data))
		api.PayloadTooLargeC(w, r, api.CodeConfigTooLarge, "request body too large")
		return
	}

	// One save transaction at a time, from the existing-file read through
	// persistence (see saveMu).
	h.saveMu.Lock()
	defer h.saveMu.Unlock()

	// Merge secrets from the existing config file (textual, key-name
	// driven: this is the raw-YAML compatibility path; the structured save
	// merges by schema metadata instead — see structured.go).
	data, err = MergeSecrets(data, h.configPath())
	if err != nil {
		// Not the client's fault: the payload relies on keep-semantics
		// secrets and the server could not read its own existing config.
		// Fail closed — no save, no activation; details go to the log.
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "secret merge")
		return
	}

	h.applyConfig(w, r, data)
}

// HandleResetConfig writes the default example config to disk.
// Only allowed when the server is in unconfigured mode.
func (h *Handler) HandleResetConfig(w http.ResponseWriter, r *http.Request) {
	// Reset writes the config file, so it joins the save transaction: the
	// lock is taken before the configured() check so a reset racing a save
	// observes the post-activation state instead of overwriting a config
	// that just activated.
	h.saveMu.Lock()
	defer h.saveMu.Unlock()

	if h.configured() {
		api.ConflictC(w, r, api.CodeConflict, "server is already configured; reset is only available in unconfigured mode")
		return
	}
	if len(h.defaultConfig) == 0 {
		api.InternalErrorC(w, r, errors.New("no default config available"), api.CodeInternalError)
		return
	}

	configPath := h.configPath()
	if err := atomicWriteConfig(r.Context(), configPath, h.defaultConfig); err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "reset config")
		return
	}

	slog.Info("config reset to default example")
	api.WriteJSON(w, map[string]string{api.KeyStatus: "config reset to defaults"})
}

// HandleValidatePath checks whether a filesystem path exists inside the container.
// POST /api/config/validate-path  body: {"path": "/media"}
func (h *Handler) HandleValidatePath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if !httphelpers.DecodeJSONBody(w, r, &req, 4096) {
		return
	}

	p := strings.TrimSpace(req.Path)
	if p == "" {
		api.WriteJSON(w, PathValidationResponse{Error: "path is empty"})
		return
	}
	if !filepath.IsAbs(p) {
		api.WriteJSON(w, PathValidationResponse{Error: "path must be absolute"})
		return
	}
	// Clean the path and reject traversal segments before touching the
	// filesystem. This endpoint is admin-only and read-only (os.Stat),
	// but the explicit guard satisfies CodeQL's go/path-injection rule
	// and prevents the cleaned/uncleaned mismatch a caller might rely on.
	// The check is per SEGMENT, not a substring match: a directory name that
	// merely begins with two dots (e.g. "/media/..extras") is legitimate.
	p = filepath.Clean(p)
	if slices.Contains(strings.Split(p, string(filepath.Separator)), "..") {
		api.WriteJSON(w, PathValidationResponse{Error: "path must not contain a '..' segment"})
		return
	}

	info, err := os.Stat(p)
	if err != nil {
		api.WriteJSON(w, PathValidationResponse{Error: "path does not exist"})
		return
	}
	if !info.IsDir() {
		api.WriteJSON(w, PathValidationResponse{Error: "path is not a directory"})
		return
	}

	api.WriteJSON(w, PathValidationResponse{Valid: true})
}

// HandleConfigSchema returns the full configuration schema for the UI.
func (h *Handler) HandleConfigSchema(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}
	api.WriteJSON(w, h.schemaFunc(api.BuildProviderSchemas(h.registry, string(api.ProviderNameMock))))
}

// --- Internal helpers ---

// pingArrIfChanged pings an arr instance only when its URL or API key
// differs from the current live config.
func (h *Handler) pingArrIfChanged(ctx context.Context, name string,
	newArr api.ArrConfig, oldCfg api.ConfigProvider,
) error {
	if newArr.URL == "" {
		return nil
	}
	if oldCfg != nil {
		var old api.ArrConfig
		if name == "sonarr" {
			old = oldCfg.SonarrConfig()
		} else {
			old = oldCfg.RadarrConfig()
		}
		if newArr.URL == old.URL && newArr.APIKey == old.APIKey {
			return nil
		}
	}
	pinger, err := h.newArrPinger(name, newArr.URL, newArr.APIKey)
	if err != nil {
		return err
	}
	if err := pinger.Ping(ctx); err != nil {
		slog.Warn(name+" connectivity check failed", "error", err)
		return err
	}
	return nil
}

// newArrPinger builds the arr client matching name ("sonarr"/"radarr") for a
// connectivity check. Both role clients expose Ping.
func (h *Handler) newArrPinger(name, baseURL, apiKey string) (interface {
	Ping(context.Context) error
}, error,
) {
	if name == "sonarr" {
		return h.newSonarr(baseURL, apiKey)
	}
	return h.newRadarr(baseURL, apiKey)
}

// atomicWriteConfig writes data to path atomically with 0o600 permissions.
// WithMaxBytes mirrors the read bound: every config read in this package
// (HandleGetConfig, the structured GET, the secret-merge baseline) caps at
// maxBodySize, and MergeSecrets can grow a payload past the request-body
// pre-check, so a file the package's own reads would refuse to load must
// fail the write (ErrFileTooLarge) instead of landing on disk.
func atomicWriteConfig(ctx context.Context, path string, data []byte) error {
	_, err := atomicfile.WriteFile(ctx, path, data,
		atomicfile.WithMode(0o600), atomicfile.WithMaxBytes(maxBodySize))
	return err
}
