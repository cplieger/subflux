package media

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSourceIDText(t *testing.T) {
	refused := map[string]string{
		"empty":     "",
		"uppercase": "Sonarr",
		"33 bytes":  strings.Repeat("a", 33),
		"underline": "a_b",
		"LF":        "a\nb",
	}
	for name, s := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			id := SourceID("kept")
			err := id.UnmarshalText([]byte(s))
			if err == nil || err.Error() != errSourceID.Error() || id != "kept" {
				t.Errorf("UnmarshalText(%q) = %v, id %q; want %q leaving the id", s, err, id, errSourceID)
			}
			if b, err := SourceID(s).MarshalText(); err == nil {
				t.Errorf("MarshalText(%q) = %q; want a refusal", s, b)
			}
		})
	}
	t.Run("sonarr-4k round-trips through encoding/json", func(t *testing.T) {
		type entry struct{ ID SourceID }
		b, err := json.Marshal(entry{ID: "sonarr-4k"})
		if err != nil || string(b) != `{"ID":"sonarr-4k"}` {
			t.Fatalf("json.Marshal = %s, %v", b, err)
		}
		var out entry
		if err := json.Unmarshal(b, &out); err != nil || out.ID != "sonarr-4k" {
			t.Errorf("json.Unmarshal = %+v, %v", out, err)
		}
		if err := json.Unmarshal([]byte(`{"ID":"Sonarr"}`), &out); err == nil {
			t.Errorf("json.Unmarshal accepted Sonarr")
		}
	})
	t.Run("32 bytes are accepted", func(t *testing.T) {
		var id SourceID
		if err := id.UnmarshalText([]byte(strings.Repeat("9-", 16))); err != nil {
			t.Errorf("UnmarshalText(32 bytes) = %v", err)
		}
	})
}
