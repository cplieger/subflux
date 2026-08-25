package confighandlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/atomicfile/v3"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/logsafe"
	"github.com/cplieger/subflux/internal/subflux"
	yaml "go.yaml.in/yaml/v3"
)

// maxConnTestBodySize bounds the test request: a kind, a URL and a key.
const maxConnTestBodySize = 4096

// ConnTestResponse is the JSON response for a connection test. It is shaped like
// PathValidationResponse, and for the same reason: a failed test is the normal
// answer to the question being asked, not an HTTP error, so the status stays 200
// and Valid carries the verdict.
type ConnTestResponse struct {
	// Error is the failure, sanitized and capped for display. Where it is not
	// one of the named HTTP answers (describeArrFailure) it is the client's own
	// text, because an operator needs to tell "HTTP 401" from "connection
	// refused" and a vocabulary in front of those two would hide the
	// distinction that makes the test useful.
	Error string `json:"error,omitempty"`
	Valid bool   `json:"valid"`
}

// HandleTestConnection reports whether the remote service a config section points
// at answers at its URL and accepts its API key. It is the same check a config
// save runs before activating a changed endpoint (pingArrIfChanged), reachable on
// its own so the settings UI and the setup wizard can answer "is this right" at
// the field instead of at the save.
//
// POST /api/config/test-connection  body: {"kind":"sonarr","url":"http://sonarr:8989","api_key":"..."}
//
// `kind` is the config section key and the dispatch point, so the surface
// generalizes by growing an arm rather than by growing an endpoint. Today the
// only kinds are the two arrs; the sections that offer a test declare it
// themselves (subflux.SchemaSection.ConnTest), so the client never carries a list.
//
// It deliberately takes NO probe description from the caller — no path, no header
// name, no success rule. That keeps the capability bounded to what a save already
// performs: a GET of one known path on a validated host with one known header. A
// caller-supplied descriptor would turn this into a general-purpose authenticated
// prober, and a caller-supplied "just connect" check would answer green for a
// wrong API key, which is the mistake the test mostly exists to catch.
//
// Deliberately NOT under saveMu: it reads the config file and touches no live
// state, so serializing it behind a save would buy nothing and could block the
// button behind an unrelated write. It also pings UNCONDITIONALLY, where the save
// path skips an unchanged endpoint — an explicit test whose answer depended on
// whether the value had changed would be answering a different question.
//
// Admin-gated by its route group. It adds no capability: an admin can already make
// the server dial an arbitrary host by saving it as an arr URL, and the arr leg is
// deliberately outside the SSRF allowlist because operator-configured private
// addresses (10.x, sonarr:8989) are the normal case. arrapi's own constructor
// validation still applies (absolute http(s) URL, host, no query or fragment), as
// does its same-host redirect policy, so the API key cannot be forwarded off-origin.
//
// The probe runs here rather than in the browser because the question is whether
// SUBFLUX can reach the service. A section's url is the SERVER's address for it —
// the shipped default is a Docker service name, and public_url exists separately
// for browser links precisely because the two differ — so a browser-side test
// would answer a different question, and could not answer it at all over HTTPS or
// for a key the redacting GET never shipped.
func (h *Handler) HandleTestConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}

	var req struct {
		Kind   string `json:"kind"`
		URL    string `json:"url"`
		APIKey string `json:"api_key"`
	}
	if !httpapi.DecodeJSONBody(w, r, &req, maxConnTestBodySize) {
		return
	}

	// An unknown kind is a client bug, not an operator mistake: only sections
	// declaring ConnTest offer a test, and both callers name them from the
	// schema. That makes it the one failure here that is a 400. The set is
	// closed HERE rather than derived from the schema, because a kind is only
	// testable once this handler knows how to probe it.
	kind := strings.TrimSpace(req.Kind)
	if kind != arrSonarr && kind != arrRadarr {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, `kind must be "sonarr" or "radarr"`)
		return
	}

	url := strings.TrimSpace(req.URL)
	if url == "" {
		httpapi.WriteJSON(w, ConnTestResponse{Error: "URL is required"})
		return
	}

	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		apiKey = h.storedArrAPIKey(r.Context(), kind)
	}
	if apiKey == "" {
		httpapi.WriteJSON(w, ConnTestResponse{Error: "API key is required"})
		return
	}

	pinger, err := h.newArrPinger(kind, url, apiKey)
	if err != nil {
		// Construction failure is the URL contract being broken (not
		// absolute, no host, carries a query). That is an answer about the
		// value the operator typed, so it rides the same 200 as a dial
		// failure rather than becoming a 400.
		httpapi.WriteJSON(w, ConnTestResponse{Error: logsafe.Field(err.Error())})
		return
	}
	if err := pinger.Ping(r.Context()); err != nil {
		httpapi.WriteJSON(w, ConnTestResponse{Error: describeArrFailure(err)})
		return
	}

	httpapi.WriteJSON(w, ConnTestResponse{Valid: true})
}

// describeArrFailure renders a failed ping as one line an operator can act on.
//
// An HTTP answer is named, because the raw text buries the only part that
// matters: arrapi leads with its own package name and repeats the status path, so
// a rejected credential reads as "arrapi: /api/v3/system/status: HTTP 401" where
// what the operator needs is which field to go fix. The three arms are the three
// different fixes — the key, the URL's base path, and neither.
//
// Everything else (a dial failure, a timeout, a TLS error) keeps the client's own
// text: "connection refused" and "no such host" are already the diagnosis, and
// paraphrasing them would only lose detail. Classification is on the published
// error TYPE, so a client whose errors this package cannot recognize — including
// a test double — degrades to that same raw text rather than to a wrong claim.
func describeArrFailure(err error) string {
	if status, ok := errors.AsType[*arrapi.StatusError](err); ok {
		switch status.Code {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Sprintf("HTTP %d: the API key was rejected", status.Code)
		case http.StatusNotFound:
			return fmt.Sprintf("HTTP %d: no arr API at this URL — check for a missing or extra base path", status.Code)
		default:
			return fmt.Sprintf("the server at this URL answered HTTP %d", status.Code)
		}
	}
	return logsafe.Field(err.Error())
}

// storedArrAPIKey reads an arr's API key out of the config file on disk, or ""
// when there is none to read. Arr-specific by its path (<kind>.api_key), like
// describeArrFailure: the ENDPOINT generalizes over kinds, the probe for each
// kind does not.
//
// An empty api_key in the request means "test with the key you already have".
// Both callers need it: a saved secret is rendered as an empty field with a
// "saved" placeholder (the redacting GET never ships the value), so the browser
// genuinely cannot send a key the operator has not just retyped, and a test that
// demanded one would fail on precisely the configs that work.
//
// The FILE is the source rather than the live config because the file is what a
// save merges from (mergeExistingSecrets), so the test answers the question the
// operator is actually asking: would saving this work. It also means the wizard
// gets the right answer in unconfigured mode, where there is no live arr config
// to read.
//
// Every read failure yields "" rather than an error: the caller then reports the
// missing key as the user-facing answer it is. Nothing here can distinguish an
// absent file from an unreadable one in a way an operator could act on
// differently, and the save path already fails closed on an unreadable baseline.
func (h *Handler) storedArrAPIKey(ctx context.Context, kind string) string {
	data, err := atomicfile.ReadBounded(ctx, h.configPath(), maxBodySize)
	if err != nil {
		return ""
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return ""
	}
	doc := documentMapping(&root)
	if doc == nil {
		return ""
	}
	node := resolvePath(doc, secretPath{kind, "api_key"})
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(node.Value)
}
