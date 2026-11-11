package app

import (
	"testing"

	"github.com/cplieger/subflux/internal/status"
)

func TestActivateRetainsProviderAlerts(t *testing.T) {
	b := boot(t, validLanguages+"providers: {animetosho: {}, gestdown: {}}\n", nil)
	sources := []status.AlertSource{
		status.ProviderAlert("animetosho"), status.ProviderDataAlert("animetosho"),
		status.ProviderAlert("gestdown"), status.ProviderDataAlert("gestdown"),
	}
	for _, src := range sources {
		b.status.Reconcile(src, true, "test")
	}
	saveText(t, b.svc, validLanguages+"providers: {animetosho: {enabled: false}, gestdown: {}}\n")
	want := map[status.AlertSource]bool{sources[0]: false, sources[1]: false, sources[2]: true, sources[3]: true}
	for src, raised := range want {
		if got := hasAlert(b.status, src); got != raised {
			t.Errorf("after AnimeTosho's disabling, %s raised = %v, want %v", src, got, raised)
		}
	}
	b.status.Reconcile(sources[0], true, "later")
	b.status.Reconcile(sources[1], true, "later")
	for _, src := range sources[:2] {
		if hasAlert(b.status, src) {
			t.Errorf("a later raise of %s was kept", src)
		}
	}
}
