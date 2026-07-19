package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/auth/v2"
	authwebauthn "github.com/cplieger/auth/v2/webauthn"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/metrics"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/go-webauthn/webauthn/webauthn"
)

// --- Fixtures ---

// activationTestConfig is a qhMockConfig with the auth/logging knobs the
// activation path consults made settable.
type activationTestConfig struct {
	qhMockConfig

	rpID      string
	logLevel  api.LogLevel
	logFormat api.LogFormat
	oidcCfg   auth.OIDCConfig
	oidcOn    bool
}

var _ api.ConfigProvider = (*activationTestConfig)(nil)

func (c *activationTestConfig) WebAuthnRPID() string { return c.rpID }
func (c *activationTestConfig) OIDCEnabled() bool    { return c.oidcOn }
func (c *activationTestConfig) OIDCConfig() auth.OIDCConfig {
	return c.oidcCfg
}

func (c *activationTestConfig) LoggingLevel() api.LogLevel {
	if c.logLevel == "" {
		return "info"
	}
	return c.logLevel
}

func (c *activationTestConfig) LoggingFormat() api.LogFormat {
	if c.logFormat == "" {
		return "json"
	}
	return c.logFormat
}

// closableArrClient counts Close calls so tests can assert activation
// releases the outgoing generation's clients.
type closableArrClient struct {
	dummyArrClient

	closed int
}

func (c *closableArrClient) Close() { c.closed++ }

// okWire is a wiring.Func that always succeeds with one stub provider.
func okWire(_ context.Context, _ api.ConfigProvider, _ api.Store, _ search.SearchMetrics) (api.SearchEngine, api.Scorer, []api.Provider, error) {
	return nil, nil, []api.Provider{&stubProvider{name: "mock"}}, nil
}

// newActivationTestServer builds the minimal Server the activation path
// touches: snapshot, wiring, arr factories, metrics, events, alerts, and a
// worker-launch counter injected into the latch seam.
func newActivationTestServer(t *testing.T) (s *Server, workerLaunches *int) {
	t.Helper()
	launches := 0
	s = &Server{
		db:      &qhMockStore{},
		metrics: metrics.New(),
		events:  events.New(0),
		alerts:  activity.NewAlertLog(100),
		wire:    okWire,
		newSonarr: func(_, _ string) (api.SonarrClient, error) {
			return &closableArrClient{}, nil
		},
		newRadarr: func(_, _ string) (api.RadarrClient, error) {
			return &closableArrClient{}, nil
		},
		launchWorkers: func() { launches++ },
		ctx:           context.Background(),
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
	cfg := &activationTestConfig{
		qhMockConfig: qhMockConfig{
			sonarrCfg: api.ArrConfig{URL: "http://sonarr:8989", APIKey: "k"},
		},
		rpID: "subflux.example.com",
	}

	if err := s.activate(context.Background(), cfg, activateHot); err != nil {
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
		cfg:    &activationTestConfig{},
		sonarr: oldSonarr,
		radarr: oldRadarr,
	})

	cfg := &activationTestConfig{
		qhMockConfig: qhMockConfig{
			sonarrCfg: api.ArrConfig{URL: "http://sonarr:8989", APIKey: "k"},
			radarrCfg: api.ArrConfig{URL: "http://radarr:7878", APIKey: "k"},
		},
	}
	if err := s.activate(context.Background(), cfg, activateHot); err != nil {
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
	on := &activationTestConfig{
		rpID:   "subflux.example.com",
		oidcOn: true,
		oidcCfg: auth.OIDCConfig{
			IssuerURL: "https://idp.example.com", ClientID: "id", RedirectURI: "https://x/cb",
		},
	}
	if err := s.activate(context.Background(), on, activateHot); err != nil {
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
	off := &activationTestConfig{}
	if err := s.activate(context.Background(), off, activateHot); err != nil {
		t.Fatalf("activate(off) error = %v", err)
	}
	if s.state().webauthn != nil {
		t.Error("webauthn still present after removing the RP ID")
	}
	if s.state().oidc != nil {
		t.Error("oidc slot still present after disabling OIDC")
	}

	// Re-enable with a different issuer: a FRESH slot, never slotA reused.
	on2 := &activationTestConfig{
		oidcOn: true,
		oidcCfg: auth.OIDCConfig{
			IssuerURL: "https://other.example.com", ClientID: "id", RedirectURI: "https://x/cb",
		},
	}
	if err := s.activate(context.Background(), on2, activateHot); err != nil {
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

	first := &activationTestConfig{}
	if err := s.activate(context.Background(), first, activateHot); err != nil {
		t.Fatalf("activate(first) error = %v", err)
	}
	if len(calls) != 1 || calls[0] != "info/json" {
		t.Fatalf("logSetup calls after first activation = %v, want [info/json]", calls)
	}

	// Identical logging section: no re-setup.
	same := &activationTestConfig{}
	if err := s.activate(context.Background(), same, activateHot); err != nil {
		t.Fatalf("activate(same) error = %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("logSetup re-ran without a logging change: calls = %v", calls)
	}

	// Changed level: re-setup with the new values.
	changed := &activationTestConfig{logLevel: "debug", logFormat: "text"}
	if err := s.activate(context.Background(), changed, activateHot); err != nil {
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
		cfg         activationTestConfig
	}{
		{
			name: "wire failure",
			breakServer: func(s *Server) {
				s.wire = func(context.Context, api.ConfigProvider, api.Store, search.SearchMetrics) (api.SearchEngine, api.Scorer, []api.Provider, error) {
					return nil, nil, nil, errMock
				}
			},
		},
		{
			name: "sonarr construction failure",
			breakServer: func(s *Server) {
				s.newSonarr = func(_, _ string) (api.SonarrClient, error) { return nil, errMock }
			},
			cfg: activationTestConfig{qhMockConfig: qhMockConfig{
				sonarrCfg: api.ArrConfig{URL: "http://sonarr:8989", APIKey: "k"},
			}},
		},
		{
			name: "radarr construction failure",
			breakServer: func(s *Server) {
				s.newRadarr = func(_, _ string) (api.RadarrClient, error) { return nil, errMock }
			},
			cfg: activationTestConfig{qhMockConfig: qhMockConfig{
				radarrCfg: api.ArrConfig{URL: "http://radarr:7878", APIKey: "k"},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, launches := newActivationTestServer(t)

			// Establish a good live snapshot first.
			good := &activationTestConfig{}
			if err := s.activate(context.Background(), good, activateHot); err != nil {
				t.Fatalf("activate(good) error = %v", err)
			}
			before := s.state()

			tt.breakServer(s)
			cfg := tt.cfg
			if err := s.activate(context.Background(), &cfg, activateHot); err == nil {
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
	cfg := &activationTestConfig{}
	s.live.Store(&liveState{cfg: cfg})
	s.configured.Store(true) // WithConfig semantics: configured at construction

	// Cold boot: the one activation Start performs. The former
	// "iff wasUnconfigured" guard computed false here and launched NOTHING.
	if err := s.activate(context.Background(), cfg, activateCold); err != nil {
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
		cfg := &activationTestConfig{}
		if err := s.hotReload(context.Background(), cfg); err != nil {
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

	s.wire = func(context.Context, api.ConfigProvider, api.Store, search.SearchMetrics) (api.SearchEngine, api.Scorer, []api.Provider, error) {
		return nil, nil, nil, errMock
	}
	if err := s.hotReload(context.Background(), &activationTestConfig{}); err == nil {
		t.Fatal("hotReload with failing wire: error = nil, want error")
	}
	if *launches != 0 {
		t.Fatalf("worker launches after failed activation = %d, want 0", *launches)
	}

	s.wire = okWire
	if err := s.hotReload(context.Background(), &activationTestConfig{}); err != nil {
		t.Fatalf("hotReload after fixing wire: error = %v", err)
	}
	if *launches != 1 {
		t.Fatalf("worker launches after failure-then-success = %d, want exactly 1", *launches)
	}
}

func TestWorkerLatch_repeated_identical_put_launches_once(t *testing.T) {
	t.Parallel()
	s, launches := newActivationTestServer(t)

	cfg := &activationTestConfig{}
	for i := range 2 {
		if err := s.hotReload(context.Background(), cfg); err != nil {
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

	good := &activationTestConfig{}
	if err := s.activate(context.Background(), good, activateHot); err != nil {
		t.Fatalf("activate(good) error = %v", err)
	}
	before := s.state()

	bad := &activationTestConfig{rpID: badRPID}
	err := s.activate(context.Background(), bad, activateHot)
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
	cfg := &activationTestConfig{rpID: badRPID}
	s.live.Store(&liveState{cfg: cfg})

	if err := s.activate(context.Background(), cfg, activateCold); err != nil {
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
	fixed := &activationTestConfig{rpID: "subflux.example.com"}
	if err := s.activate(context.Background(), fixed, activateHot); err != nil {
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

func (fakeOIDCStore) CreateOIDCState(context.Context, string, string, string, string) error {
	return nil
}

func (fakeOIDCStore) ConsumeOIDCState(context.Context, string) (string, string, string, error) {
	return "", "", "", errMock
}

func (fakeOIDCStore) GetUserByOIDCSub(context.Context, string, string) (*auth.User, error) {
	return nil, nil
}

func (fakeOIDCStore) GetUserByEmail(context.Context, string) (*auth.User, error) { return nil, nil }

func (fakeOIDCStore) GetUserByUsername(context.Context, string) (*auth.User, error) { return nil, nil }
func (fakeOIDCStore) CreateUser(context.Context, *auth.User) error                  { return nil }
func (fakeOIDCStore) UpdateUser(context.Context, *auth.User) error                  { return nil }

// oidcRedirectLocation drives GET /api/auth/oidc through the handler and
// returns the Location header of the 302.
func oidcRedirectLocation(t *testing.T, h *authhandlers.Handler) string {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(),
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

	oidcCfg := func(issuer string) auth.OIDCConfig {
		return auth.OIDCConfig{
			IssuerURL:   issuer,
			ClientID:    "subflux",
			RedirectURI: "https://subflux.example.com/api/auth/oidc/callback",
		}
	}

	// Activate with issuer A and complete a SUCCESSFUL discovery.
	cfgA := &activationTestConfig{oidcOn: true, oidcCfg: oidcCfg(issuerA.URL)}
	if err := s.activate(context.Background(), cfgA, activateHot); err != nil {
		t.Fatalf("activate(issuer A) error = %v", err)
	}
	if loc := oidcRedirectLocation(t, h); !strings.HasPrefix(loc, issuerA.URL+"/auth") {
		t.Fatalf("redirect after discovery A = %q, want prefix %q", loc, issuerA.URL+"/auth")
	}

	// Edit the issuer. The forever-cached-provider bug served issuer A here.
	cfgB := &activationTestConfig{oidcOn: true, oidcCfg: oidcCfg(issuerB.URL)}
	if err := s.activate(context.Background(), cfgB, activateHot); err != nil {
		t.Fatalf("activate(issuer B) error = %v", err)
	}
	if loc := oidcRedirectLocation(t, h); !strings.HasPrefix(loc, issuerB.URL+"/auth") {
		t.Fatalf("redirect after issuer edit = %q, want prefix %q (fresh slot must re-discover)",
			loc, issuerB.URL+"/auth")
	}

	// Disabling OIDC publishes a nil slot: the endpoint reports unconfigured.
	cfgOff := &activationTestConfig{}
	if err := s.activate(context.Background(), cfgOff, activateHot); err != nil {
		t.Fatalf("activate(oidc off) error = %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(),
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
	s.metrics = metrics.New()
	s.events = events.New(0)
	s.wire = okWire
	s.newSonarr = func(_, _ string) (api.SonarrClient, error) { return dummyArrClient{}, nil }
	s.newRadarr = func(_, _ string) (api.RadarrClient, error) { return dummyArrClient{}, nil }
	s.launchWorkers = func() {}
	s.ctx = context.Background()
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
	if err := authDB.CreatePasskey(context.Background(), oldCred); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}

	// Hot-edit the RP ID to rp-b.example.com.
	cfgB := &activationTestConfig{rpID: "rp-b.example.com"}
	if err := s.activate(context.Background(), cfgB, activateHot); err != nil {
		t.Fatalf("activate(rp-b) error = %v", err)
	}

	// (1) A fresh ceremony begins under the NEW RP ID.
	beginReq := httptest.NewRequestWithContext(context.Background(),
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
	waUser, err := authwebauthn.NewWebAuthnUser(user, nil)
	if err != nil {
		t.Fatalf("NewWebAuthnUser: %v", err)
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
	finishReq := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/auth/webauthn/login/finish", strings.NewReader(string(assertion)))
	finishReq.Header.Set("X-WebAuthn-Session", begin.SessionToken)
	finishRec := httptest.NewRecorder()
	s.authH.HandleWebAuthnLoginFinish(finishRec, finishReq)
	if finishRec.Code != http.StatusUnauthorized {
		t.Errorf("old-RP credential assertion: status = %d, want predictable 401 (body: %s)",
			finishRec.Code, finishRec.Body.String())
	}

	// (3) Password recovery remains available.
	loginReq := httptest.NewRequestWithContext(context.Background(),
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
// built without the test override must fall through to launchWorkerSet (the
// four real goroutines), which requires the full dep set — so this only
// asserts the nil-check dispatch, via a server whose context is already
// cancelled (all four workers exit immediately).
func TestStartWorkers_defaults_to_real_launcher(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.ctx = ctx

	// newTestServer does not build the poller; initHandlers constructs the
	// pieces the real worker set touches, and the cancelled context makes
	// each worker exit immediately.
	s.initHandlers()

	s.startWorkers()
	done := make(chan struct{})
	go func() { s.bgWg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("real worker set did not exit on a cancelled context")
	}
}
