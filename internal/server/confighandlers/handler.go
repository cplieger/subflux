// Package confighandlers provides HTTP handlers for configuration CRUD
// operations: get, save, reset, schema, and path validation.
package confighandlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cplieger/atomicfile/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/httphelpers"
)

// AlertLog is the narrow interface for alert operations.
type AlertLog interface {
	RecordPersistent(source, msg string)
}

// pathValidationResponse is the JSON response for path validation requests.
type pathValidationResponse struct {
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

	configPath := h.configPath()

	// Merge secrets from the existing config file.
	data = MergeSecrets(data, configPath)

	newCfg, err := h.loadConfig(data)
	if err != nil {
		api.BadRequestC(w, r, api.CodeConfigInvalid, "invalid configuration: "+err.Error())
		return
	}

	// Verify arr connectivity when arr config changed or first-time setup.
	oldCfg := h.state().Cfg
	if pingErr := h.pingArrIfChanged(r.Context(), "sonarr", newCfg.SonarrConfig(), oldCfg); pingErr != nil {
		api.BadRequestC(w, r, api.CodeConfigUnreachableArr, "sonarr unreachable: "+pingErr.Error())
		return
	}
	if pingErr := h.pingArrIfChanged(r.Context(), "radarr", newCfg.RadarrConfig(), oldCfg); pingErr != nil {
		api.BadRequestC(w, r, api.CodeConfigUnreachableArr, "radarr unreachable: "+pingErr.Error())
		return
	}

	// Atomic write: temp file + rename prevents corruption on crash.
	if err := atomicWriteConfig(r.Context(), configPath, data); err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "write config")
		return
	}

	if err := h.hotReload(r.Context(), newCfg); err != nil {
		slog.Error("hot reload failed, config saved but not applied", "error", err)
		h.alerts.RecordPersistent("config",
			"Hot reload failed: "+err.Error())
		api.InternalErrorC(w, r, fmt.Errorf("reload failed: %w", err), api.CodeConfigReloadFailed)
		return
	}

	slog.Info("config saved and hot-reloaded")
	api.WriteJSON(w, map[string]string{api.KeyStatus: "saved and applied"})
}

// HandleResetConfig writes the default example config to disk.
// Only allowed when the server is in unconfigured mode.
func (h *Handler) HandleResetConfig(w http.ResponseWriter, r *http.Request) {
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
		api.WriteJSON(w, pathValidationResponse{Error: "path is empty"})
		return
	}
	if !filepath.IsAbs(p) {
		api.WriteJSON(w, pathValidationResponse{Error: "path must be absolute"})
		return
	}
	// Clean the path and reject traversal segments before touching the
	// filesystem. This endpoint is admin-only and read-only (os.Stat),
	// but the explicit guard satisfies CodeQL's go/path-injection rule
	// and prevents the cleaned/uncleaned mismatch a caller might rely on.
	p = filepath.Clean(p)
	if strings.Contains(p, "..") {
		api.WriteJSON(w, pathValidationResponse{Error: "path must not contain '..'"})
		return
	}

	info, err := os.Stat(p)
	if err != nil {
		api.WriteJSON(w, pathValidationResponse{Error: "path does not exist"})
		return
	}
	if !info.IsDir() {
		api.WriteJSON(w, pathValidationResponse{Error: "path is not a directory"})
		return
	}

	api.WriteJSON(w, pathValidationResponse{Valid: true})
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
func atomicWriteConfig(ctx context.Context, path string, data []byte) error {
	_, err := atomicfile.WriteFile(ctx, path, data, atomicfile.WithMode(0o600))
	return err
}
