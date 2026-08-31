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
	"strings"
	"sync"

	"github.com/cplieger/atomicfile/v3"
	"github.com/cplieger/pathinside/v2"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/subflux"
)

// ConfigLoader parses and validates config YAML into a candidate config. It is
// declared here because this package is the only caller: HandleSaveConfig and
// the structured save load a candidate, then hand it to HotReload whole.
//
// The candidate is CONCRETE. These handlers read nothing out of it — they parse
// it and pass it on — so the width an interface would state is zero, and a
// zero-method interface is `any` with a name. The composition root supplies
// config.LoadFromBytes; the server carries the func and never calls it.
type ConfigLoader func(data []byte) (*config.Config, error)

// AlertLog is the narrow interface for alert operations.
type AlertLog interface {
	RecordPersistent(source, msg string)
}

// PathValidationResponse is the JSON response for path validation requests.
type PathValidationResponse struct {
	Error string `json:"error,omitempty"`
	Valid bool   `json:"valid"`
}

// arrSonarr and arrRadarr are the config section keys of the two arr instances,
// which double as the `kind` the connection-test endpoint dispatches on. Named
// because the literals recur across the save-time ping and the on-demand test
// (goconst).
const (
	arrSonarr = "sonarr"
	arrRadarr = "radarr"
)

// ArrPinger is the only thing this package asks of an arr client: can it be
// reached. A config save pings Sonarr or Radarr before activating a changed
// endpoint, so a bad URL or key is reported on the save rather than discovered
// by the next scan. ONE method, against the 19 exported methods
// *arrsvc.Sonarr offers and the 16 on *arrsvc.Radarr — nothing here reads a
// series, a movie or a history event.
//
// Exported because the composition root embeds it: server.SonarrClient and
// server.RadarrClient are unions of their consumers' surfaces, and Ping is the
// one method of theirs that no scan, poll or handler path calls, so without a
// name here that union would have to re-list it.
type ArrPinger interface {
	Ping(ctx context.Context) error
}

// Deps holds all dependencies for the config handler family.
type Deps struct {
	Registry      SchemaRegistry
	Alerts        AlertLog
	LoadConfig    ConfigLoader
	SchemaFunc    subflux.SchemaFunc
	NewSonarr     func(baseURL, apiKey string) (ArrPinger, error)
	NewRadarr     func(baseURL, apiKey string) (ArrPinger, error)
	HotReload     func(ctx context.Context, cfg *config.Config) error
	State         func() StateView
	ConfigPath    func() string
	Configured    func() bool
	DefaultConfig []byte
}

// StateView is the LIVE arr configuration the config handlers compare an
// incoming save against, so an unchanged arr is never pinged. 2 of the 37
// values a *config.Config offers.
//
// VALUES, never an interface: the only source is a *config.Config that stays
// nil until activation, and boxing that nil defeats the consumer's nil test.
// The zero view is unconfigured mode, and a zero URL never equals an incoming
// one, so a first-boot save pings instead of comparing.
type StateView struct {
	Sonarr subflux.ArrConfig
	Radarr subflux.ArrConfig
}

// Handler holds all dependencies for the config handler family.
type Handler struct {
	registry      SchemaRegistry
	alerts        AlertLog
	loadConfig    ConfigLoader
	schemaFunc    subflux.SchemaFunc
	newSonarr     func(baseURL, apiKey string) (ArrPinger, error)
	newRadarr     func(baseURL, apiKey string) (ArrPinger, error)
	hotReload     func(ctx context.Context, cfg *config.Config) error
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

// maxBodySize references the canonical constant from api.
const maxBodySize = httpapi.MaxDefaultBodySize

// HandleGetConfig returns the current config file with secrets redacted.
func (h *Handler) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	configPath := h.configPath()
	data, err := atomicfile.ReadBounded(r.Context(), configPath, maxBodySize)
	if err != nil {
		httpapi.InternalErrorC(w, r, err, subflux.CodeInternalError, "stage", "read config", "path", configPath)
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
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "failed to read body")
		return
	}
	if int64(len(data)) > maxBodySize {
		slog.Warn("config request body too large", "size", len(data))
		httpapi.PayloadTooLargeC(w, r, subflux.CodeConfigTooLarge, "request body too large")
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
		httpapi.InternalErrorC(w, r, err, subflux.CodeInternalError, "stage", "secret merge")
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
		httpapi.ConflictC(w, r, subflux.CodeConflict, "server is already configured; reset is only available in unconfigured mode")
		return
	}
	if len(h.defaultConfig) == 0 {
		httpapi.InternalErrorC(w, r, errors.New("no default config available"), subflux.CodeInternalError)
		return
	}

	configPath := h.configPath()
	if err := atomicWriteConfig(r.Context(), configPath, h.defaultConfig); err != nil {
		httpapi.InternalErrorC(w, r, err, subflux.CodeInternalError, "stage", "reset config")
		return
	}

	slog.Info("config reset to default example")
	httpapi.WriteJSON(w, map[string]string{subflux.KeyStatus: "config reset to defaults"})
}

// HandleValidatePath checks whether a filesystem path exists inside the container.
// POST /api/config/validate-path  body: {"path": "/media"}
func HandleValidatePath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if !httpapi.DecodeJSONBody(w, r, &req, 4096) {
		return
	}

	p := strings.TrimSpace(req.Path)
	// atomicfile.ValidatePath is the gate its own writes apply, so a path this
	// endpoint blesses cannot be one the later config write refuses.
	// filepath.IsAbs was the previous stand-in and is a different rule: it
	// accepts an embedded NUL ("/media\x00/tv" is absolute) that every
	// atomicfile write rejects. Messages stay the caller-facing ones rather
	// than the library's, whose text embeds the path. ErrUnsafePath also covers
	// the NUL case, reported here as the absolute-path failure: it cannot be
	// typed into the settings form, so only a crafted request reaches it.
	if err := atomicfile.ValidatePath(p); err != nil {
		msg := "path must be absolute"
		if errors.Is(err, atomicfile.ErrEmptyPath) {
			msg = "path is empty"
		}
		httpapi.WriteJSON(w, PathValidationResponse{Error: msg})
		return
	}
	// Reject traversal in the path AS WRITTEN — before cleaning — then clean
	// before touching the filesystem. This endpoint is admin-only and
	// read-only (os.Stat), but the explicit guard satisfies CodeQL's
	// go/path-injection rule and closes the cleaned/uncleaned mismatch a
	// caller might rely on: the value the settings UI keeps is the one the
	// admin typed, so a path validated only after cleaning would report
	// "/media/tv/../../etc" valid on the strength of a different string
	// (/etc) than the one that ends up in the config.
	// The check is per COMPONENT, not a substring match: a directory name
	// that merely begins with or contains two dots (e.g. "/media/..extras",
	// "/media/a..b") is legitimate and stays valid.
	if pathinside.HasDotDot(p) {
		httpapi.WriteJSON(w, PathValidationResponse{Error: "path must not contain a '..' segment"})
		return
	}
	p = filepath.Clean(p)

	info, err := os.Stat(p)
	if err != nil {
		httpapi.WriteJSON(w, PathValidationResponse{Error: "path does not exist"})
		return
	}
	if !info.IsDir() {
		httpapi.WriteJSON(w, PathValidationResponse{Error: "path is not a directory"})
		return
	}

	httpapi.WriteJSON(w, PathValidationResponse{Valid: true})
}

// HandleConfigSchema returns the full configuration schema for the UI.
func (h *Handler) HandleConfigSchema(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}
	httpapi.WriteJSON(w, h.schemaFunc(BuildProviderSchemas(h.registry, string(subflux.ProviderNameSynthetic))))
}

// --- Internal helpers ---

// pingArrIfChanged pings an arr instance only when its URL or API key
// differs from the current live config. An unconfigured server has no live
// endpoint, so oldArr is zero and every incoming URL counts as a change.
func (h *Handler) pingArrIfChanged(ctx context.Context, name string,
	newArr, oldArr subflux.ArrConfig,
) error {
	if newArr.URL == "" {
		return nil
	}
	if newArr.URL == oldArr.URL && newArr.APIKey == oldArr.APIKey {
		return nil
	}
	pinger, err := h.newArrPinger(name, newArr.URL, newArr.APIKey)
	if err != nil {
		return err
	}
	defer closeArrPinger(pinger)
	if err := pinger.Ping(ctx); err != nil {
		slog.Warn(name+" connectivity check failed", "error", err)
		return err
	}
	return nil
}

// newArrPinger builds the arr client matching name ("sonarr"/"radarr") for a
// connectivity check. Both role clients expose Ping. The client is built for
// ONE ping and is the caller's to close (closeArrPinger).
func (h *Handler) newArrPinger(name, baseURL, apiKey string) (ArrPinger, error) {
	if name == arrSonarr {
		return h.newSonarr(baseURL, apiKey)
	}
	return h.newRadarr(baseURL, apiKey)
}

// closeArrPinger releases the transports a per-ping arr client holds, when
// the concrete type exposes Close. Every client this package builds is built
// for one ping and dropped, so without this each save-time check and each
// press of a Test button strands the client's idle connections — one per
// transport the client keeps, which for the arr-read wrapper is both the
// shipped and the wave transport.
//
// A capability assertion rather than a method on ArrPinger, matching how
// activation closes a replaced live client (server.closeArrClient): ArrPinger
// is embedded in the composition root's SonarrClient/RadarrClient unions,
// where the LIVE client's shutdown belongs to activation and no test double
// implements Close.
func closeArrPinger(p ArrPinger) {
	if closer, ok := p.(interface{ Close() }); ok {
		closer.Close()
	}
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
