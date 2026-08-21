package subflux

import (
	"encoding/json"
	"testing"
)

// TestManualLockEntry_marshalsTheKeyFlat pins the wire shape ManualLockEntry
// has always had. The quad now arrives by embedding ManualLockKey rather than
// as four repeated fields, and encoding/json only promotes an embedded struct's
// fields when the embed itself carries no json name — so a stray tag on the
// embed (or a rename of the key's own tags) would nest the quad under an object
// and break the TS client and the wiregen decoder without any Go call site
// changing.
func TestManualLockEntry_marshalsTheKeyFlat(t *testing.T) {
	t.Parallel()

	entry := ManualLockEntry{
		MediaType: MediaTypeEpisode,
		MediaID:   "tt0903747-s01e01",
		Language:  "fr",
		Variant:   VariantForced,
		Count:     2,
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	const want = `{"media_type":"episode","media_id":"tt0903747-s01e01",` +
		`"language":"fr","variant":"forced","count":2}`
	if got := string(raw); got != want {
		t.Errorf("json.Marshal(ManualLockEntry) = %s, want %s", got, want)
	}
}

// TestManualLockEntry_unmarshalsTheFlatWireShape asserts the same shape decodes
// back onto the embedded key, which is what the clear-lock request body relies
// on (it decodes straight into a ManualLockKey).
func TestManualLockEntry_unmarshalsTheFlatWireShape(t *testing.T) {
	t.Parallel()

	const wire = `{"media_type":"movie","media_id":"tmdb-27205",` +
		`"language":"en","variant":"hi","count":3}`

	var got ManualLockEntry
	if err := json.Unmarshal([]byte(wire), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	want := ManualLockEntry{
		MediaType: MediaTypeMovie,
		MediaID:   "tmdb-27205",
		Language:  "en",
		Variant:   VariantHI,
		Count:     3,
	}
	if got != want {
		t.Errorf("decoded = %+v, want %+v", got, want)
	}
}

func TestConfigDrift_Empty(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		drift ConfigDrift
		want  bool
	}{
		{name: "zero value is empty", drift: ConfigDrift{}, want: true},
		{name: "removed languages not empty", drift: ConfigDrift{RemovedLanguages: []string{"fr"}}, want: false},
		{name: "removed providers not empty", drift: ConfigDrift{RemovedProviders: []ProviderID{"opensubtitles"}}, want: false},
		{name: "adaptive disabled not empty", drift: ConfigDrift{AdaptiveDisabled: true}, want: false},
		{name: "all fields set not empty", drift: ConfigDrift{RemovedLanguages: []string{"fr"}, RemovedProviders: []ProviderID{"os"}, AdaptiveDisabled: true}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.drift.Empty()
			if got != tt.want {
				t.Errorf("ConfigDrift.Empty() = %v, want %v", got, tt.want)
			}
		})
	}
}
