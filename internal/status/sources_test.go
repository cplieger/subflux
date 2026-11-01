package status

import (
	"strings"
	"testing"
)

var constructors = []struct {
	make func(string) AlertSource
	kind string
}{
	{ProviderAlert, "provider"},
	{SourceAlert, "source"},
	{SourceOverlapAlert, "source_overlap"},
	{MediaServerAlert, "media_server"},
	{MediaRootAlert, "media_root"},
}

func TestAlertSourceConstructors(t *testing.T) {
	long := "/media/" + strings.Repeat("a", 600)
	values := []string{
		"opensubtitles",
		"",
		strings.Repeat("x", 600),
		"two\nlines",
		"bad\xffutf8",
		long + "/show-one",
		long + "/show-two",
	}
	for _, c := range constructors {
		t.Run(c.kind, func(t *testing.T) {
			seen := map[AlertSource]string{}
			for _, v := range values {
				src := c.make(v)
				if len(src) > MaxAlertSource || !printableLine(string(src)) {
					t.Errorf("%q: source %q is not one printable line within %d bytes", v, src, MaxAlertSource)
				}
				if other, dup := seen[src]; dup {
					t.Errorf("%q and %q share source %q", v, other, src)
				}
				seen[src] = v
				if again := c.make(v); again != src {
					t.Errorf("%q: source %q then %q", v, src, again)
				}
				if k, ok := AlertKind(src); !ok || k != c.kind {
					t.Errorf("AlertKind(%q) = %q, %v; want %q", src, k, ok, c.kind)
				}
			}
		})
	}
	t.Run("a short value reads as written", func(t *testing.T) {
		if got := ProviderAlert("opensubtitles"); got != "provider:opensubtitles" {
			t.Errorf("ProviderAlert = %q", got)
		}
	})
}

var fixedSources = map[AlertSource]string{
	AlertConfig:         "config",
	AlertStore:          "store",
	AlertJobRecords:     "job_records",
	AlertBackup:         "backup",
	AlertAuthDisabled:   "auth_disabled",
	AlertAdminSocket:    "admin_socket",
	AlertRestartPending: "restart_pending",
	AlertPendingWrites:  "pending_writes",
}

func TestAlertKind(t *testing.T) {
	if len(fixedKinds) != len(fixedSources) {
		t.Errorf("fixedKinds %v; want the %d fixed sources", fixedKinds, len(fixedSources))
	}
	for f, name := range fixedSources {
		if k, ok := AlertKind(f); !ok || k != name || string(f) != name {
			t.Errorf("AlertKind(%q) = %q, %v; want %q", f, k, ok, name)
		}
	}
	forged := []AlertSource{
		"bogus:x",
		"provider:two\nlines",
		AlertSource("provider:" + strings.Repeat("x", 600)),
		"provider#zz",
		AlertSource("provider#" + strings.Repeat("A", 64)),
		"provider",
		"",
	}
	for _, src := range forged {
		if k, ok := AlertKind(src); ok {
			t.Errorf("AlertKind(%q) = %q; want no constructor's source", src, k)
		}
	}
}
