package settings

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/sonarr"
)

func loadWith(t *testing.T, r Registries, text string) Snapshot {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path, r)
}

func TestSourcesAndScanSections(t *testing.T) {
	r := Registries{Sources: []mediasource.AdapterKind{sonarr.Kind}}
	t.Run("absent sections are no sources and the default schedule", func(t *testing.T) {
		want := mediasource.Schedule{Change: 30 * time.Second, Whole: 24 * time.Hour}
		a, ok := loadWith(t, r, validLanguages).Active()
		if !ok || a.Sources != nil || a.Scan != want {
			t.Errorf("Active = %v (%+v, %+v), want no sources and %+v", ok, a.Sources, a.Scan, want)
		}
	})
	t.Run("both sections parse with the registered kinds", func(t *testing.T) {
		a, ok := loadWith(t, r, validLanguages+
			"sources: [{id: tv, type: sonarr, url: 'http://sonarr:8989', api_key: k}]\n"+
			"scan: {change_interval: 1m, whole_interval: 6h}\n").Active()
		if !ok {
			t.Fatal("not Active")
		}
		if len(a.Sources) != 1 || a.Sources[0].ID != "tv" || a.Sources[0].Type != "sonarr" {
			t.Errorf("Sources = %+v, want the one sonarr entry tv", a.Sources)
		}
		if want := (mediasource.Schedule{Change: time.Minute, Whole: 6 * time.Hour}); a.Scan != want {
			t.Errorf("Scan = %+v, want %+v", a.Scan, want)
		}
	})
	t.Run("an unregistered type and an out-of-range interval fail at their paths", func(t *testing.T) {
		s := loadWith(t, Registries{}, validLanguages+
			"sources: [{id: tv, type: sonarr, url: 'http://sonarr:8989', api_key: k}]\n"+
			"scan: {change_interval: 1s}\n")
		if got, want := failurePaths(t, s), []string{"sources[0].type", "scan.change_interval"}; !slices.Equal(got, want) {
			t.Errorf("paths %v, want %v", got, want)
		}
	})
}
