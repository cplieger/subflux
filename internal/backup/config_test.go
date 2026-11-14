package backup

import (
	"testing"
	"time"

	"go.yaml.in/yaml/v4"
)

func section(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

const storeDir = "/config"

func TestBackupDefaults(t *testing.T) {
	c, errs := ParseConfig(section(t, "enabled: true\n"), storeDir)
	if want := (Config{enabled: true, frequency: 24 * time.Hour, retention: 7, path: "/config/backups"}); len(errs) > 0 || c != want {
		t.Errorf("ParseConfig(enabled: true) = %+v, %v; want %+v", c, errs, want)
	}
	if c, errs := ParseConfig(nil, storeDir); len(errs) > 0 || c.enabled {
		t.Errorf("an absent section = %+v, %v; want backups off", c, errs)
	}
	for name, tc := range map[string]struct {
		src, path string
	}{
		"a relative path":        {"path: backups\n", "backup.path"},
		"a trailing slash":       {"path: /srv/backups/\n", "backup.path"},
		"a dot-dot part":         {"path: /srv/../config\n", "backup.path"},
		"the store's own folder": {"path: /config\n", "backup.path"},
		"a 30-minute frequency":  {"frequency: 30m\n", "backup.frequency"},
		"a 31-day frequency":     {"frequency: 744h\n", "backup.frequency"},
		"no copy kept":           {"retention: 0\n", "backup.retention"},
		"366 copies":             {"retention: 366\n", "backup.retention"},
	} {
		t.Run(name, func(t *testing.T) {
			c, errs := ParseConfig(section(t, "enabled: true\n"+tc.src), storeDir)
			if len(errs) != 1 || errs[0].Path != tc.path || c != (Config{}) {
				t.Errorf("ParseConfig = %+v, %v; want the zero Config and one field error at %s", c, errs, tc.path)
			}
		})
	}
	if _, errs := ParseConfig(section(t, "path: /config/backups\n"), storeDir); len(errs) != 0 {
		t.Errorf("a folder under the store's own = %v, want it accepted", errs)
	}
}
