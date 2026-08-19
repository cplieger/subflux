package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/auth/v4"
	authwebauthn "github.com/cplieger/auth/v4/webauthn"
	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/go-webauthn/webauthn/webauthn"
)

// --- Fixtures ---

// activationCfg builds the REAL config the activation path consumes, varying
// only the sections these tests drive. The snapshot holds *config.Config, so
// the fixture is a config and the knobs are YAML.
//
// The base document (testConfig) always configures sonarr and never radarr,
// which is what the snapshot assertions below key on.
type activationCfg struct {
	radarrURL  string
	rpID       string
	oidcIssuer string
	logLevel   string
	logFormat  string
}

// build renders the options into YAML sections and loads them.
func (o activationCfg) build(t *testing.T) *config.Config {
	t.Helper()
	var extra []string
	if o.radarrURL != "" {
		extra = append(extra, "radarr:\n  url: "+strconv.Quote(o.radarrURL)+"\n  api_key: \"k\"")
	}
	auth := ""
	if o.rpID != "" {
		auth += "  webauthn_rp_id: " + strconv.Quote(o.rpID) + "\n"
	}
	if o.oidcIssuer != "" {
		auth += "  oidc_enabled: true\n  oidc:\n" +
			"    issuer_url: " + strconv.Quote(o.oidcIssuer) + "\n" +
			"    client_id: \"subflux\"\n" +
			"    redirect_uri: \"https://subflux.example.com/api/auth/oidc/callback\"\n"
	}
	if auth != "" {
		extra = append(extra, "auth:\n"+auth)
	}
	if o.logLevel != "" || o.logFormat != "" {
		extra = append(extra, "logging:\n  level: "+strconv.Quote(o.logLevel)+
			"\n  format: "+strconv.Quote(o.logFormat))
	}
	return testConfig(t, extra...)
}

// closableArrClient counts Close calls so tests can assert activation
// releases the outgoing generation's clients.
type closableArrClient struct {
	dummyArrClient

	closed int
}

func (c *closableArrClient) Close() { c.closed++ }

// okWire is a wiring.Func that always succeeds with one stub provider.
func okWire(_ context.Context, _ *config.Config, _ search.SearchStore, _ search.SearchMetrics) (*search.Engine, *scorer.Engine, []provider.Provider, error) {
	return nil, nil, []provider.Provider{&stubProvider{name: "mock"}}, nil
}

// newActivationTestServer builds the minimal Server the activation path
// touches: snapshot, wiring, arr factories, metrics, events, alerts, and a
// worker-launch counter injected into the latch seam.
func newActivationTestServer(t *testing.T) (s *Server, workerLaunches *int) {
	t.Helper()
	launches := 0
	s = &Server{
		db:      &qhMockStore{},
		metrics: obs.New(),
		events:  events.New(0),
		alerts:  activity.NewAlertLog(100),
		wire:    okWire,
		newSonarr: func(_, _ string) (SonarrClient, error) {
			return &closableArrClient{}, nil
		},
		newRadarr: func(_, _ string) (RadarrClient, error) {
			return &closableArrClient{}, nil
		},
		launchWorkers: func() { launches++ },
		lifetime:      t.Context(),
	}
	s.live.Store(&liveState{})
	return s, &launches
}

// alertSources returns the source labels of all currently visible alerts.
func alertSources(s *Server) []string {
	var out []string
	for _, a := range s.alerts.VisibleAlerts() {
		out = append(out, a.Source)
	}
	return out
}

func hasAlertSource(s *Server, source string) bool {
	return slices.Contains(alertSources(s), source)
}

// --- Activation table (R4.1) ---

func TestActivate_fresh_publishes_full_snapshot(t *testing.T) {
	t.Parallel()
	s, _ := newActivationTestServer(t)
	cfg := activationCfg{rpID: "subflux.example.com"}.build(t)

	if err := s.activate(t.Context(), cfg, activateHot); err != nil {
		t.Fatalf("activate() error = %v, want nil", err)
	}

	ls := s.state()
	if ls.cfg != cfg {
		t.Error("snapshot cfg not swapped to the candidate")
	}
	if len(ls.providers) != 1 {
		t.Errorf("snapshot providers = %d, want 1", len(ls.providers))
	}
	if ls.sonarr == nil {
		t.Error("snapshot sonarr client not constructed by activation")
	}
	if ls.radarr != nil {
		t.Error("snapshot radarr client constructed despite empty radarr config")
	}
	if ls.webauthn == nil {
		t.Error("snapshot webauthn not assembled from the RP ID")
	}
	if ls.oidc != nil {
		t.Error("snapshot oidc slot non-nil despite OIDC disabled")
	}
	if !s.configured.Load() {
		t.Error("configured flag not set after successful activation")
	}
}

func TestActivate_reactivate_swaps_and_closes_old_arr_clients(t *testing.T) {
	t.Parallel()
	s, _ := newActivationTestServer(t)
	oldSonarr := &closableArrClient{}
	oldRadarr := &closableArrClient{}
	s.live.Store(&liveState{
		cfg:    activationCfg{}.build(t),
		sonarr: oldSonarr,
		radarr: oldRadarr,
	})

	cfg := activationCfg{radarrURL: "http://radarr:7878"}.build(t)
	if err := s.activate(t.Context(), cfg, activateHot); err != nil {
		t.Fatalf("activate() error = %v, want nil", err)
	}

	if oldSonarr.closed != 1 || oldRadarr.closed != 1 {
		t.Errorf("outgoing arr clients closed = (%d, %d), want (1, 1)",
			oldSonarr.closed, oldRadarr.closed)
	}
	ls := s.state()
	if ls.sonarr == oldSonarr || ls.radarr == oldRadarr {
		t.Error("snapshot still references the outgoing arr clients")
	}
}

func TestActivate_auth_edit_swaps_webauthn_and_oidc_slot(t *testing.T) {
	t.Parallel()
	s, _ := newActivationTestServer(t)

	// Enable both capabilities.
	on := activationCfg{rpID: "subflux.example.com", oidcIssuer: "https://idp.example.com"}.build(t)
	if err := s.activate(t.Context(), on, activateHot); err != nil {
		t.Fatalf("activate(on) error = %v", err)
	}
	if s.state().webauthn == nil {
		t.Fatal("webauthn missing after enabling RP ID")
	}
	slotA := s.state().oidc
	if slotA == nil {
		t.Fatal("oidc slot missing after enabling OIDC")
	}

	// Disable both: the snapshot must drop them immediately.
	off := activationCfg{}.build(t)
	if err := s.activate(t.Context(), off, activateHot); err != nil {
		t.Fatalf("activate(off) error = %v", err)
	}
	if s.state().webauthn != nil {
		t.Error("webauthn still present after removing the RP ID")
	}
	if s.state().oidc != nil {
		t.Error("oidc slot still present after disabling OIDC")
	}

	// Re-enable with a different issuer: a FRESH slot, never slotA reused.
	on2 := activationCfg{oidcIssuer: "https://other.example.com"}.build(t)
	if err := s.activate(t.Context(), on2, activateHot); err != nil {
		t.Fatalf("activate(on2) error = %v", err)
	}
	if s.state().oidc == slotA {
		t.Error("oidc slot reused across configs; every activation must mint a fresh slot")
	}
}

func TestActivate_logging_change_reruns_log_setup(t *testing.T) {
	t.Parallel()
	s, _ := newActivationTestServer(t)
	var calls []string
	s.logSetup = func(level, format string) { calls = append(calls, level+"/"+format) }

	first := activationCfg{}.build(t)
	if err := s.activate(t.Context(), first, activateHot); err != nil {
		t.Fatalf("activate(first) error = %v", err)
	}
	if len(calls) != 1 || calls[0] != "info/json" {
		t.Fatalf("logSetup calls after first activation = %v, want [info/json]", calls)
	}

	// Identical logging section: no re-setup.
	same := activationCfg{}.build(t)
	if err := s.activate(t.Context(), same, activateHot); err != nil {
		t.Fatalf("activate(same) error = %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("logSetup re-ran without a logging change: calls = %v", calls)
	}

	// Changed level: re-setup with the new values.
	changed := activationCfg{logLevel: "debug", logFormat: "text"}.build(t)
	if err := s.activate(t.Context(), changed, activateHot); err != nil {
		t.Fatalf("activate(changed) error = %v", err)
	}
	if len(calls) != 2 || calls[1] != "debug/text" {
		t.Fatalf("logSetup calls after logging change = %v, want [... debug/text]", calls)
	}
}

func TestActivate_prepare_failure_preserves_previous_snapshot(t *testing.T) {
	t.Parallel()
	tests := []struct {
		breakServer func(s *Server)
		name        string
		cfg         activationCfg
	}{
		{
			name: "wire failure",
			breakServer: func(s *Server) {
				s.wire = func(context.Context, *config.Config, search.SearchStore, search.SearchMetrics) (*search.Engine, *scorer.Engine, []provider.Provider, error) {
					return nil, nil, nil, errMock
				}
			},
		},
		{
			name: "sonarr construction failure",
			breakServer: func(s *Server) {
				s.newSonarr = func(_, _ string) (SonarrClient, error) { return nil, errMock }
			},
			// The base document already configures sonarr; the broken
			// factory is what fails the candidate.
		},
		{
			name: "radarr construction failure",
			breakServer: func(s *Server) {
				s.newRadarr = func(_, _ string) (RadarrClient, error) { return nil, errMock }
			},
			cfg: activationCfg{radarrURL: "http://radarr:7878"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, launches := newActivationTestServer(t)

			// Establish a good live snapshot first.
			good := activationCfg{}.build(t)
			if err := s.activate(t.Context(), good, activateHot); err != nil {
				t.Fatalf("activate(good) error = %v", err)
			}
			before := s.state()

			tt.breakServer(s)
			if err := s.activate(t.Context(), tt.cfg.build(t), activateHot); err == nil {
				t.Fatal("activate() error = nil, want prepare-phase rejection")
			}

			if s.state() != before {
				t.Error("failed activation mutated the live snapshot; previous snapshot must keep serving")
			}
			if !s.configured.Load() {
				t.Error("failed activation flipped configured off")
			}
			if *launches != 1 {
				t.Errorf("worker launches = %d, want 1 (failed activation must not relaunch)", *launches)
			}
		})
	}
}

// --- Worker cardinality, all four R1.6 directions ---

func TestWorkerLatch_cold_configured_boot_launches_exactly_once(t *testing.T) {
	t.Parallel()
	s, launches := newActivationTestServer(t)
	cfg := activationCfg{}.build(t)
	s.live.Store(&liveState{cfg: cfg})
	s.configured.Store(true) // WithConfig semantics: configured at construction

	// Cold boot: the one activation Start performs. The former
	// "iff wasUnconfigured" guard computed false here and launched NOTHING.
	if err := s.activate(t.Context(), cfg, activateCold); err != nil {
		t.Fatalf("activate(cold) error = %v", err)
	}
	if *launches != 1 {
		t.Fatalf("worker launches after cold configured boot = %d, want exactly 1", *launches)
	}
}

func TestWorkerLatch_unconfigured_boot_then_n_saves_launches_once(t *testing.T) {
	t.Parallel()
	s, launches := newActivationTestServer(t)

	for i := range 3 {
		cfg := activationCfg{}.build(t)
		if err := s.hotReload(t.Context(), cfg); err != nil {
			t.Fatalf("hotReload #%d error = %v", i+1, err)
		}
	}
	if *launches != 1 {
		t.Fatalf("worker launches after 3 saves = %d, want exactly 1", *launches)
	}
}

func TestWorkerLatch_wire_failure_then_successful_save_launches_once(t *testing.T) {
	t.Parallel()
	s, launches := newActivationTestServer(t)

	s.wire = func(context.Context, *config.Config, search.SearchStore, search.SearchMetrics) (*search.Engine, *scorer.Engine, []provider.Provider, error) {
		return nil, nil, nil, errMock
	}
	if err := s.hotReload(t.Context(), activationCfg{}.build(t)); err == nil {
		t.Fatal("hotReload with failing wire: error = nil, want error")
	}
	if *launches != 0 {
		t.Fatalf("worker launches after failed activation = %d, want 0", *launches)
	}

	s.wire = okWire
	if err := s.hotReload(t.Context(), activationCfg{}.build(t)); err != nil {
		t.Fatalf("hotReload after fixing wire: error = %v", err)
	}
	if *launches != 1 {
		t.Fatalf("worker launches after failure-then-success = %d, want exactly 1", *launches)
	}
}

func TestWorkerLatch_repeated_identical_put_launches_once(t *testing.T) {
	t.Parallel()
	s, launches := newActivationTestServer(t)

	cfg := activationCfg{}.build(t)
	for i := range 2 {
		if err := s.hotReload(t.Context(), cfg); err != nil {
			t.Fatalf("hotReload (identical PUT) #%d error = %v", i+1, err)
		}
	}
	if *launches != 1 {
		t.Fatalf("worker launches after repeated identical PUT = %d, want exactly 1", *launches)
	}
}

// --- WebAuthn failure policy: hot save FATAL, cold boot DEGRADED (R1.7) ---

// badRPID fails go-webauthn's config validation (a URL pasted where a bare
// domain belongs — the realistic user mistake).
const badRPID = "http://subflux.example.com"

func TestActivate_webauthn_failure_is_fatal_on_hot_save(t *testing.T) {
	t.Parallel()
	s, launches := newActivationTestServer(t)

	good := activationCfg{}.build(t)
	if err := s.activate(t.Context(), good, activateHot); err != nil {
		t.Fatalf("activate(good) error = %v", err)
	}
	before := s.state()

	bad := activationCfg{rpID: badRPID}.build(t)
	err := s.activate(t.Context(), bad, activateHot)
	if err == nil {
		t.Fatal("hot activation with a bad RP ID: error = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "webauthn") {
		t.Errorf("error %q does not identify the webauthn stage", err)
	}
	if s.state() != before {
		t.Error("rejected save mutated the live snapshot")
	}
	if *launches != 1 {
		t.Errorf("worker launches = %d, want 1", *launches)
	}
}

func TestActivate_webauthn_failure_degrades_on_cold_boot(t *testing.T) {
	t.Parallel()
	s, _ := newActivationTestServer(t)
	cfg := activationCfg{rpID: badRPID}.build(t)
	s.live.Store(&liveState{cfg: cfg})

	if err := s.activate(t.Context(), cfg, activateCold); err != nil {
		t.Fatalf("cold activation with a bad RP ID must degrade, got error %v", err)
	}
	if s.state().webauthn != nil {
		t.Error("webauthn non-nil after degraded construction")
	}
	if !s.configured.Load() {
		t.Error("cold boot with a bad RP ID must still reach configured mode")
	}
	if !hasAlertSource(s, "webauthn") {
		t.Errorf("degraded cold boot must record a persistent webauthn alert; sources = %v", alertSources(s))
	}

	// A later save that fixes the RP ID clears the alert.
	fixed := activationCfg{rpID: "subflux.example.com"}.build(t)
	if err := s.activate(t.Context(), fixed, activateHot); err != nil {
		t.Fatalf("activate(fixed) error = %v", err)
	}
	if hasAlertSource(s, "webauthn") {
		t.Errorf("webauthn alert not cleared by a successful activation; sources = %v", alertSources(s))
	}
}

// --- OIDC issuer edit over httptest: the cache-invalidation proof ---

// fakeIssuer serves a minimal OIDC discovery document whose endpoints are
// derived from its own base URL.
func fakeIssuer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		base := srv.URL
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q}`,
			base, base+"/auth", base+"/token", base+"/keys")
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// fakeOIDCStore satisfies authhandlers.OIDCStore with no-op persistence; the
// redirect handler only needs CreateOIDCState to succeed.
type fakeOIDCStore struct{}

func (fakeOIDCStore) CreateOIDCState(context.Context, auth.OIDCState, auth.OIDCNonce, auth.OIDCCodeVerifier, string) error {
	return nil
}

func (fakeOIDCStore) ConsumeOIDCState(context.Context, auth.OIDCState) (auth.OIDCNonce, auth.OIDCCodeVerifier, string, error) {
	return "", "", "", errMock
}

func (fakeOIDCStore) GetUserByOIDCSub(context.Context, string, string) (*auth.User, bool, error) {
	return nil, false, nil
}

func (fakeOIDCStore) GetUserByUsername(context.Context, string) (*auth.User, bool, error) {
	return nil, false, nil
}
func (fakeOIDCStore) CreateUser(context.Context, *auth.User) error { return nil }

// oidcRedirectLocation drives GET /api/auth/oidc through the handler and
// returns the Location header of the 302.
func oidcRedirectLocation(t *testing.T, h *authhandlers.Handler) string {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/auth/oidc", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleOIDCRedirect(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("HandleOIDCRedirect status = %d, want 302 (body: %s)", rec.Code, rec.Body.String())
	}
	return rec.Header().Get("Location")
}

func TestActivate_oidc_issuer_edit_rediscovers_fresh_slot(t *testing.T) {
	t.Parallel()
	issuerA := fakeIssuer(t)
	issuerB := fakeIssuer(t)

	s, _ := newActivationTestServer(t)
	h := &authhandlers.Handler{
		OidcDB:       fakeOIDCStore{},
		OIDCResolver: s.getOIDC,
	}

	// Activate with issuer A and complete a SUCCESSFUL discovery.
	cfgA := activationCfg{oidcIssuer: issuerA.URL}.build(t)
	if err := s.activate(t.Context(), cfgA, activateHot); err != nil {
		t.Fatalf("activate(issuer A) error = %v", err)
	}
	if loc := oidcRedirectLocation(t, h); !strings.HasPrefix(loc, issuerA.URL+"/auth") {
		t.Errorf("redirect after discovery A = %q, want prefix %q", loc, issuerA.URL+"/auth")
	}

	// Edit the issuer. The forever-cached-provider bug served issuer A here.
	cfgB := activationCfg{oidcIssuer: issuerB.URL}.build(t)
	if err := s.activate(t.Context(), cfgB, activateHot); err != nil {
		t.Fatalf("activate(issuer B) error = %v", err)
	}
	if loc := oidcRedirectLocation(t, h); !strings.HasPrefix(loc, issuerB.URL+"/auth") {
		t.Errorf("redirect after issuer edit = %q, want prefix %q (fresh slot must re-discover)",
			loc, issuerB.URL+"/auth")
	}

	// Disabling OIDC publishes a nil slot: the endpoint reports unconfigured.
	cfgOff := activationCfg{}.build(t)
	if err := s.activate(t.Context(), cfgOff, activateHot); err != nil {
		t.Fatalf("activate(oidc off) error = %v", err)
	}
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/auth/oidc", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleOIDCRedirect(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("redirect with OIDC disabled: status = %d, want 400", rec.Code)
	}
}

// --- WebAuthn live-edit round-trip + RP ID guarded edit (R1.4) ---

// TestActivate_rpid_change_locks_out_old_credential_predictably proves the
// guarded-edit contract: after an RP ID hot edit, (1) new ceremonies run
// under the NEW RP ID, (2) an assertion from a credential scoped to the OLD
// RP ID fails predictably with a clean 401 (never a panic or 500), and (3)
// password login remains available as the recovery path.
func TestActivate_rpid_change_locks_out_old_credential_predictably(t *testing.T) {
	s, authDB := testAuthServer(t)
	// Graft the activation deps onto the auth fixture.
	s.db = &qhMockStore{}
	s.metrics = obs.New()
	s.events = events.New(0)
	s.wire = okWire
	s.newSonarr = func(_, _ string) (SonarrClient, error) { return dummyArrClient{}, nil }
	s.newRadarr = func(_, _ string) (RadarrClient, error) { return dummyArrClient{}, nil }
	s.launchWorkers = func() {}
	s.lifetime = t.Context()
	s.authH.WebAuthnResolver = func() *webauthn.WebAuthn { return s.state().webauthn }

	user := createTestUser(t, authDB, "alice", "correct horse battery staple")

	// A credential enrolled under the OLD RP ID (rp-a.example.com).
	oldCred := &auth.PasskeyCredential{
		UserID:       user.ID,
		Name:         "old-rp key",
		CredentialID: []byte("cred-id-under-rp-a"),
		PublicKey:    []byte("not-a-real-key"),
		CreatedAt:    time.Now(),
	}
	if err := authDB.CreatePasskey(t.Context(), oldCred); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}

	// Hot-edit the RP ID to rp-b.example.com.
	cfgB := activationCfg{rpID: "rp-b.example.com"}.build(t)
	if err := s.activate(t.Context(), cfgB, activateHot); err != nil {
		t.Fatalf("activate(rp-b) error = %v", err)
	}

	// (1) A fresh ceremony begins under the NEW RP ID.
	beginReq := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/api/auth/webauthn/login/begin", http.NoBody)
	beginRec := httptest.NewRecorder()
	s.authH.HandleWebAuthnLoginBegin(beginRec, beginReq)
	if beginRec.Code != http.StatusOK {
		t.Fatalf("login begin under new RP ID: status = %d, want 200 (body: %s)",
			beginRec.Code, beginRec.Body.String())
	}
	var begin struct {
		PublicKey struct {
			PublicKey struct {
				Challenge string `json:"challenge"`
				RPID      string `json:"rpId"`
			} `json:"publicKey"`
		} `json:"publicKey"`
		SessionToken string `json:"session_token"`
	}
	if err := json.Unmarshal(beginRec.Body.Bytes(), &begin); err != nil {
		t.Fatalf("decode begin response: %v", err)
	}
	if begin.PublicKey.PublicKey.RPID != "rp-b.example.com" {
		t.Errorf("assertion challenge rpId = %q, want rp-b.example.com", begin.PublicKey.PublicKey.RPID)
	}

	// (2) Finishing with the OLD RP's credential fails predictably: the
	// assertion carries clientData bound to the old origin, so verification
	// rejects it with a clean 401 envelope.
	waUser, err := authwebauthn.NewUser(user, nil)
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}
	clientData, _ := json.Marshal(map[string]string{
		"type":      "webauthn.get",
		"challenge": begin.PublicKey.PublicKey.Challenge,
		"origin":    "https://rp-a.example.com",
	})
	b64 := base64.RawURLEncoding.EncodeToString
	assertion, _ := json.Marshal(map[string]any{
		"id":    b64(oldCred.CredentialID),
		"rawId": b64(oldCred.CredentialID),
		"type":  "public-key",
		"response": map[string]string{
			"authenticatorData": b64(make([]byte, 37)),
			"clientDataJSON":    b64(clientData),
			"signature":         b64([]byte("sig")),
			"userHandle":        b64(waUser.WebAuthnID()),
		},
	})
	finishReq := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/api/auth/webauthn/login/finish", strings.NewReader(string(assertion)))
	finishReq.Header.Set("X-WebAuthn-Session", begin.SessionToken)
	finishRec := httptest.NewRecorder()
	s.authH.HandleWebAuthnLoginFinish(finishRec, finishReq)
	if finishRec.Code != http.StatusUnauthorized {
		t.Errorf("old-RP credential assertion: status = %d, want predictable 401 (body: %s)",
			finishRec.Code, finishRec.Body.String())
	}

	// (3) Password recovery remains available.
	loginReq := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/api/auth/login", loginBody("alice", "correct horse battery staple"))
	loginRec := httptest.NewRecorder()
	s.authH.HandleLogin(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Errorf("password login after RP ID change: status = %d, want 200 (body: %s)",
			loginRec.Code, loginRec.Body.String())
	}
}

// --- Latch seam sanity ---

// TestStartWorkers_defaults_to_real_launcher guards the nil seam: a Server
// built without the test override must fall through to the real worker set
// (the four goroutines launched by awaitWorkerLaunch on the context handed to
// Start), which requires the full dep set — so this asserts the nil-check
// dispatch and the launch signal reaching the dispatcher, via a context that
// is cancelled the moment the workers are running (all four exit immediately).
func TestStartWorkers_defaults_to_real_launcher(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &qhMockStore{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// newTestServer does not build the poller; initHandlers constructs the
	// pieces the real worker set touches.
	s.initHandlers()

	// The dispatcher stands in for Start: it is the only thing that hands the
	// worker set its lifetime, so the launch is observable only through it.
	launched := make(chan struct{})
	s.bgWg.Go(func() {
		defer close(launched)
		s.awaitWorkerLaunch(ctx)
	})

	s.startWorkers()
	select {
	case <-launched:
	case <-time.After(5 * time.Second):
		t.Fatal("launch signal did not reach awaitWorkerLaunch")
	}

	// Only now cancel, so each worker exits from a live context rather than
	// racing the dispatcher's own ctx.Done arm.
	cancel()
	done := make(chan struct{})
	go func() { s.bgWg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("real worker set did not exit on a cancelled context")
	}
}

// sessionTimeoutSetter is the optional capability activate() probes for to push
// hot-reloaded session timeouts into the auth-store sweeper. The probe is a
// type assertion, so a signature drift on either side silently stops applying
// them; the assertion below is the compile-time check that keeps the two in
// step, and it is why the parameter is auth.SessionTimeouts rather than a pair
// of adjacent durations.
type sessionTimeoutSetter interface {
	SetSessionTimeouts(timeouts auth.SessionTimeouts)
}

var _ sessionTimeoutSetter = (*authstore.Store)(nil)
