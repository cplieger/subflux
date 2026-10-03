package confighandlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/logsafe"
	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/subflux"
)

// maxConnTestBodySize bounds the test request: a kind and one section's settings
// map. A provider section is the widest, and the largest ships six fields.
const maxConnTestBodySize = 8192

// ConnTestResponse is the JSON response for a connection test. It is shaped like
// PathValidationResponse, and for the same reason: a failed test is the normal
// answer to the question being asked, not an HTTP error, so the status stays 200
// and Valid carries the verdict.
type ConnTestResponse struct {
	// Error is the failure, sanitized and capped for display. Unnamed answers
	// keep the client's own text: an operator needs to tell "HTTP 401" from
	// "connection refused", and a vocabulary in front of those would hide it.
	Error string `json:"error,omitempty"`
	// Message says what a passing provider check did to a credential disable
	// recorded against that provider; empty when there was none.
	Message string `json:"message,omitempty"`
	Valid   bool   `json:"valid"`
}

// ProviderAuthClearer clears a provider's credential disable when a passing
// check used the settings the disable was recorded under.
type ProviderAuthClearer interface {
	ClearIfMatches(ctx context.Context, id subflux.ProviderID, settings map[string]any) providergate.ClearResult
}

// connTestRequest is one section's own settings, keyed by its schema — what a
// SAVE sends. Nothing here describes a probe: kind picks the arm, the arm asks.
type connTestRequest struct {
	Settings map[string]string `json:"settings"`
	Kind     string            `json:"kind"`
}

// HandleTestConnection reports whether the remote service a config section
// points at accepts the credentials it is configured with.
//
// POST /api/config/test-connection
// body: {"kind":"sonarr","settings":{"url":"http://sonarr:8989","api_key":"…"}}
//
// `kind` is the section key, which for a provider is its name, and the only
// thing dispatched on; an unknown kind is a client bug and the one 400 here.
func (h *Handler) HandleTestConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}

	var req connTestRequest
	if !httpapi.DecodeJSONBody(w, r, &req, maxConnTestBodySize) {
		return
	}

	kind := strings.TrimSpace(req.Kind)
	switch {
	case kind == arrSonarr || kind == arrRadarr:
		h.testArrConnection(w, r, kind, req.Settings)
	case h.registry.CredentialCheck(subflux.ProviderID(kind)):
		h.testProviderCredentials(w, r, subflux.ProviderID(kind), req.Settings)
	default:
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "kind is not a testable config section")
	}
}

// testArrConnection pings one arr with the URL and key the request carries,
// falling back to the stored key when the field is empty.
func (h *Handler) testArrConnection(w http.ResponseWriter, r *http.Request,
	kind string, settings map[string]string,
) {
	url := strings.TrimSpace(settings["url"])
	if url == "" {
		httpapi.WriteJSON(w, ConnTestResponse{Error: "URL is required"})
		return
	}

	apiKey := strings.TrimSpace(settings["api_key"])
	if apiKey == "" {
		apiKey = h.storedSecret(r.Context(), secretPath{kind, "api_key"})
	}
	if apiKey == "" {
		httpapi.WriteJSON(w, ConnTestResponse{Error: "API key is required"})
		return
	}

	pinger, err := h.newArrPinger(kind, url, apiKey)
	if err != nil {
		// A broken URL contract is an answer about the value the operator
		// typed, so it rides the same 200 as a dial failure, not a 400.
		httpapi.WriteJSON(w, ConnTestResponse{Error: logsafe.Field(err.Error())})
		return
	}
	defer closeArrPinger(pinger)
	if err := pinger.Ping(r.Context()); err != nil {
		httpapi.WriteJSON(w, ConnTestResponse{Error: describeArrFailure(err)})
		return
	}

	httpapi.WriteJSON(w, ConnTestResponse{Valid: true})
}

// testProviderCredentials asks one provider whether its credentials are
// accepted, with every empty secret resolved from the config file.
func (h *Handler) testProviderCredentials(w http.ResponseWriter, r *http.Request,
	name subflux.ProviderID, settings map[string]string,
) {
	resolved := h.resolveProviderSecrets(r.Context(), name, settings)
	if err := h.registry.CheckCredentials(r.Context(), name, resolved); err != nil {
		httpapi.WriteJSON(w, ConnTestResponse{Error: describeCredentialFailure(err)})
		return
	}
	// Detached so a client that walks away cannot leave the clear half done.
	cleared := h.providerAuth.ClearIfMatches(context.WithoutCancel(r.Context()), name,
		h.registry.Normalize(name, resolved))
	httpapi.WriteJSON(w, ConnTestResponse{Valid: true, Message: clearMessage[cleared]})
}

var clearMessage = map[providergate.ClearResult]string{
	providergate.Cleared:  "credentials accepted; provider re-enabled",
	providergate.Mismatch: "credentials accepted; save to apply them and re-enable the provider",
}

// resolveProviderSecrets turns the submitted settings into the map a factory
// takes, filling every schema-declared secret the request left empty from disk.
//
// Keyed by the provider's own schema rather than by the submitted keys, so a
// request cannot ask for a value at a path the schema does not declare. Values
// stay strings; the typed accessors read those as YAML's native forms.
func (h *Handler) resolveProviderSecrets(ctx context.Context,
	name subflux.ProviderID, settings map[string]string,
) map[string]any {
	out := make(map[string]any, len(settings))
	for key, value := range settings {
		out[key] = value
	}
	_, fields := h.registry.Schema(name)
	for i := range fields {
		f := &fields[i]
		if !f.Secret || strings.TrimSpace(settings[f.Key]) != "" {
			continue
		}
		if stored := h.storedSecret(ctx, secretPath{"providers", string(name), "settings", f.Key}); stored != "" {
			out[f.Key] = stored
		}
	}
	return out
}

// describeArrFailure renders a failed ping as one line an operator can act on.
//
// An HTTP answer is named because arrapi's raw text buries the only part that
// matters ("arrapi: /api/v3/system/status: HTTP 401"), and the three arms are
// three different fixes: the key, the URL's base path, and neither. Everything
// else keeps the client's own text, since "connection refused" already is the
// diagnosis. Classification is on the published error TYPE, so an error this
// package cannot recognize degrades to that raw text rather than a wrong claim.
func describeArrFailure(err error) string {
	if status, ok := errors.AsType[*arrapi.StatusError](err); ok {
		switch status.Code {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Sprintf("HTTP %d: the API key was rejected", status.Code)
		case http.StatusNotFound:
			return fmt.Sprintf("HTTP %d: no arr API at this URL; check for a missing or extra base path", status.Code)
		default:
			return fmt.Sprintf("the server at this URL answered HTTP %d", status.Code)
		}
	}
	return logsafe.Field(err.Error())
}

// describeCredentialFailure renders a failed provider check as one line naming
// which of the two remedies applies.
//
// A refused credential is a field to go fix, an unreachable service is not, and
// the banner is the only place the operator learns which. Classification is on
// *subflux.AuthError, which a provider returns for a refusal and nothing else, so
// an unrecognized failure reads as the weaker claim rather than accusing a
// working key.
func describeCredentialFailure(err error) string {
	if authErr, ok := errors.AsType[*subflux.AuthError](err); ok {
		return "the credentials were rejected: " + logsafe.Field(authErr.Msg)
	}
	return "could not reach the service: " + logsafe.Field(err.Error())
}

// storedSecret reads one schema-declared secret out of the config file on disk,
// or "" when there is none to read.
//
// An empty secret in the request means "test with the value you already have":
// a saved secret renders as an empty field, so the browser cannot send a value
// the operator has not just retyped. The FILE rather than the live config,
// because it is what a save merges from and it is readable in unconfigured mode.
// Every read failure yields "" so the caller reports the missing value.
func (h *Handler) storedSecret(ctx context.Context, path secretPath) string {
	value, _ := h.storedScalar(ctx, path)
	return value
}
