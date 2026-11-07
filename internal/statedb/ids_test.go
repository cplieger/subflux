package statedb

import (
	"encoding/json"
	"testing"
)

func TestParseInstanceIDRoundTrip(t *testing.T) {
	for _, s := range []string{"1", "9", "10", "42", "18446744073709551615"} {
		t.Run(s, func(t *testing.T) {
			id, err := ParseInstanceID(s)
			if err != nil || id.String() != s {
				t.Errorf("ParseInstanceID(%q) = %v, %v; want String %q", s, id, err, s)
			}
		})
	}
}

func TestParseInstanceIDRefusals(t *testing.T) {
	for _, s := range []string{"", "0", "01", "+1", "-1", " 1", "1 ", "0x1", "1a", "18446744073709551616"} {
		t.Run(s, func(t *testing.T) {
			if id, err := ParseInstanceID(s); err == nil {
				t.Errorf("ParseInstanceID(%q) = %d; want a refusal", s, id)
			}
		})
	}
}

func TestInstanceIDText(t *testing.T) {
	t.Run("JSON writes the decimal text", func(t *testing.T) {
		b, err := json.Marshal(map[string]InstanceID{"id": 18446744073709551615})
		if err != nil || string(b) != `{"id":"18446744073709551615"}` {
			t.Errorf("Marshal = %s, %v; want the id as a string", b, err)
		}
	})
	t.Run("JSON reads it back", func(t *testing.T) {
		var v struct{ ID InstanceID }
		if err := json.Unmarshal([]byte(`{"ID":"42"}`), &v); err != nil || v.ID != 42 {
			t.Errorf("Unmarshal = %d, %v; want 42", v.ID, err)
		}
	})
	t.Run("zero is refused both ways", func(t *testing.T) {
		if _, err := InstanceID(0).MarshalText(); err == nil {
			t.Error("MarshalText(0) succeeded")
		}
		id := InstanceID(7)
		if err := id.UnmarshalText([]byte("0")); err == nil || id != 7 {
			t.Errorf("UnmarshalText(0) = %v, id %d; want a refusal leaving 7", err, id)
		}
	})
	t.Run("a number is not an id", func(t *testing.T) {
		var v struct{ ID InstanceID }
		if err := json.Unmarshal([]byte(`{"ID":42}`), &v); err == nil {
			t.Error("Unmarshal of a JSON number succeeded")
		}
	})
}

func TestCoverageText(t *testing.T) {
	for c, text := range map[Coverage]string{Missing: "missing", Substitute: "substitute", Exact: "exact"} {
		t.Run(text, func(t *testing.T) {
			b, err := c.MarshalText()
			if err != nil || string(b) != text {
				t.Errorf("MarshalText(%d) = %q, %v; want %q", c, b, err, text)
			}
			var got Coverage
			if err := got.UnmarshalText([]byte(text)); err != nil || got != c {
				t.Errorf("UnmarshalText(%q) = %d, %v; want %d", text, got, err, c)
			}
		})
	}
	for _, c := range []Coverage{0, Exact + 1} {
		if _, err := c.MarshalText(); err == nil {
			t.Errorf("MarshalText(%d) succeeded", c)
		}
	}
	for _, text := range []string{"", "Exact", "EXACT", "none", "exact "} {
		c := Substitute
		if err := c.UnmarshalText([]byte(text)); err == nil || c != Substitute {
			t.Errorf("UnmarshalText(%q) = %v, %d; want a refusal leaving substitute", text, err, c)
		}
	}
}
