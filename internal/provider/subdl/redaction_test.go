package subdl

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/auth/v6"
	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/server"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// echoKey is a placeholder API key holding a space, so the upstream text can
// spell it in a form the single-line normalization turns back into the key.
const echoKey = "placeholder api-key"

// Not parallel: it swaps slog's default logger.
func TestSearch_an_echoed_key_reaches_no_error_log_record_or_alert(t *testing.T) {
	var logs bytes.Buffer
	testsupport.SwapDefaultLogger(t, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))

	body := `{"status":false,"error":"Invalid API key placeholder api-key, also placeholder\napi-key"}`
	p := &Provider{
		apiKey: echoKey,
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})},
	}

	hasher, err := auth.NewHasher(auth.Argon2Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatalf("Setup: NewHasher: %v", err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := testsupport.NewAuthRecordStore()
	gate, err := providergate.Open(t.Context(), providergate.Config{
		Store: store, Hasher: hasher, Metrics: testsupport.NopGateMetrics{}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Setup: providergate.Open: %v", err)
	}
	alerts := activity.NewAlertLog(16)
	hook := server.NewProviderGateHook(alerts, func(id subflux.ProviderID) string { return string(id) },
		func(events.ProviderOp, subflux.ProviderID) {})
	var reasons []string
	gate.SetOnChange(func(ev providergate.Event) {
		reasons = append(reasons, ev.Reason)
		hook(ev)
	})
	b := gate.Bind(map[subflux.ProviderID]map[string]any{providerName: {"api_key": echoKey}})
	gate.Activate(b)
	gate.Reconcile(t.Context())

	req := &subflux.SearchRequest{MediaType: subflux.MediaTypeMovie, ImdbID: "tt0111161", Languages: []string{"en"}}
	searchErrs := testsupport.DisableThroughLadder(t, b, providerName, providergate.OpSearch, func() error {
		_, searchErr := p.Search(t.Context(), req)
		return searchErr
	}, func(d time.Duration) { now = now.Add(d) })

	st := gate.Status()[providerName]
	if !st.Disabled {
		t.Fatalf("Status(subdl) = %+v, want disabled after three refusals", st)
	}
	surfaces := map[string][]string{
		"provider error":   searchErrs,
		"log":              {logs.String()},
		"status reason":    {st.DisabledReason},
		"persisted reason": {store.Record(providerName).LastError},
		"event reason":     reasons,
	}
	for _, a := range alerts.VisibleAlerts() {
		surfaces["alert"] = append(surfaces["alert"], a.Message)
	}
	if len(surfaces["alert"]) == 0 {
		t.Fatal("no alert raised for the disable")
	}
	for name, texts := range surfaces {
		for _, text := range texts {
			if strings.Contains(text, "placeholder") {
				t.Errorf("%s = %q, carries the API key", name, text)
			}
		}
	}
}
