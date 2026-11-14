package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
)

// alertLines is the exposition's alerts_active samples.
func alertLines(m *obs.Metrics) []string {
	rec := httptest.NewRecorder()
	m.Handler()(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	var out []string
	for line := range strings.Lines(rec.Body.String()) {
		if strings.HasPrefix(line, "subflux_alerts_active{") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func TestAlertsActiveFollowsStatus(t *testing.T) {
	st, err := status.New(t.Context(), statustest.NewMemPersister())
	if err != nil {
		t.Fatal(err)
	}
	m := obs.New()
	go func() { _ = alertMetrics(t.Context(), st, m) }()
	shows := func(want ...string) {
		t.Helper()
		var got []string
		ok := eventually(func() bool {
			got = alertLines(m)
			return strings.Join(got, "\n") == strings.Join(want, "\n")
		})
		if !ok {
			t.Errorf("alerts_active = %q, want %q", got, want)
		}
	}

	st.Reconcile(status.AlertConfig, true, "diverged")
	st.Reconcile(status.ProviderAlert("a"), true, "down")
	st.Reconcile(status.ProviderAlert("b"), true, "down")
	shows(`subflux_alerts_active{id="",kind="config"} 1`,
		`subflux_alerts_active{id="a",kind="provider"} 1`,
		`subflux_alerts_active{id="b",kind="provider"} 1`)
	st.Reconcile(status.ProviderAlert("a"), true, "still down")
	st.Reconcile(status.ProviderAlert("a"), false, "")
	shows(`subflux_alerts_active{id="",kind="config"} 1`,
		`subflux_alerts_active{id="b",kind="provider"} 1`)
	st.RetainAlerts(status.ProviderAlerts, nil)
	shows(`subflux_alerts_active{id="",kind="config"} 1`)

	t.Run("active_series_never_deleted", func(t *testing.T) {
		st, err := status.New(t.Context(), statustest.NewMemPersister())
		if err != nil {
			t.Fatal(err)
		}
		g := &deleteLog{alertGauge: obs.New()}
		go func() { _ = alertMetrics(t.Context(), st, g) }()
		st.Reconcile(status.AlertConfig, true, "diverged")
		for i := range 200 {
			st.Reconcile(status.MediaRootAlert("/media/tv"), i%2 == 0, "unreadable")
		}
		st.Reconcile(status.MediaRootAlert("/media/tv"), true, "unreadable")
		if !eventually(func() bool { return g.set.Load() > 0 && g.lastSet.Load() == "media_root" }) {
			t.Fatal("the loop never showed the last raise")
		}
		if n := g.configDeleted.Load(); n != 0 {
			t.Errorf("the config series was deleted %d times while its alert stayed active", n)
		}
	})
}

// deleteLog counts deletes of the config series and records the last kind
// set.
type deleteLog struct {
	alertGauge
	lastSet       atomic.Value
	set           atomic.Int32
	configDeleted atomic.Int32
}

func (d *deleteLog) SetAlertActive(kind, id string) {
	d.alertGauge.SetAlertActive(kind, id)
	d.lastSet.Store(kind)
	d.set.Add(1)
}

func (d *deleteLog) DeleteAlertActive(kind, id string) {
	if kind == "config" {
		d.configDeleted.Add(1)
	}
	d.alertGauge.DeleteAlertActive(kind, id)
}
