package buildinfo

import "testing"

func TestVersionPrefersStampedTag(t *testing.T) {
	prev := stamped
	t.Cleanup(func() { stamped = prev })
	stamped = ""
	unstamped := Version()
	if unstamped == "" {
		t.Error("an unstamped build has an empty version")
	}
	stamped = "v9.8.7"
	if got := Version(); got != "v9.8.7" {
		t.Errorf("Version() = %q, want the stamped v9.8.7", got)
	}
}
