package hdbits

import (
	"bytes"
	"log/slog"
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

// Not parallel: it swaps slog's default logger.
func TestSearch_an_echoed_username_reaches_no_error_log_record_or_alert(t *testing.T) {
	var logs bytes.Buffer
	testsupport.SwapDefaultLogger(t, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))

	p := routedProvider(t, map[string]string{
		"/api/torrents": `{"status":5,"message":"bad account placeholder user, also placeholder\nuser, key ` + testPasskey + `"}`,
	}, nil)
	p.username = "placeholder user"

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
	b := gate.Bind(map[subflux.ProviderID]map[string]any{
		providerName: {settingUsername: p.username, settingPasskey: testPasskey},
	})
	gate.Activate(b)
	gate.Reconcile(t.Context())

	searchErrs := testsupport.DisableThroughLadder(t, b, providerName, providergate.OpSearch, func() error {
		_, searchErr := p.Search(t.Context(), episodeRequest())
		return searchErr
	}, func(d time.Duration) { now = now.Add(d) })

	st := gate.Status()[providerName]
	if !st.Disabled {
		t.Fatalf("Status(hdbits) = %+v, want disabled after three refusals", st)
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
				t.Errorf("%s = %q, carries the username or passkey", name, text)
			}
		}
	}
}
