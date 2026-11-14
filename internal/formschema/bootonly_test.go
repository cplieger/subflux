package formschema

import (
	"slices"
	"testing"
)

func TestBootOnlyChanges(t *testing.T) {
	s := Schema{Fields: []Field{
		{Key: "bypass", Kind: Bool, Default: "false", BootOnly: true},
		{Key: "listen", Kind: String, BootOnly: true},
		{Key: "grace", Kind: DurationMs, Default: "30s", BootOnly: true},
		{Key: "tags", Kind: StringList, Default: "[a]", BootOnly: true},
		{Key: "label", Kind: String},
	}}
	cases := []struct {
		name, stored, next string
		want               []string
	}{
		{name: "both absent", want: nil},
		{name: "no stored section, the default written", next: "{bypass: false, grace: 30s, tags: [a]}"},
		{name: "no stored section, turned on", next: "{bypass: true}", want: []string{"bypass"}},
		{name: "kept, in another spelling", stored: "{bypass: True, grace: 30000ms}", next: "{bypass: true, grace: 30s}"},
		{name: "turned off", stored: "{bypass: true}", next: "{bypass: false}", want: []string{"bypass"}},
		{name: "a field without a default set", stored: "{}", next: "{listen: ':80'}", want: []string{"listen"}},
		{name: "a list reordered", stored: "{tags: [a, b]}", next: "{tags: [b, a]}", want: []string{"tags"}},
		{name: "every change, in schema order", stored: "{tags: [b], bypass: true}", next: "{grace: 1m, listen: x}", want: []string{"bypass", "listen", "grace", "tags"}},
		{name: "a field not BootOnly changes freely", stored: "{label: a}", next: "{label: b}"},
		{name: "stored unparsable counts as the default, turned on", stored: "{bypass: maybe}", next: "{bypass: true}", want: []string{"bypass"}},
		{name: "stored unparsable counts as the default, left off", stored: "{bypass: maybe}", next: "{}"},
		{name: "stored null counts as the default", stored: "{bypass: ~, grace: null}", next: "{bypass: false, grace: 30s}"},
		{name: "stored list for a bool counts as the default", stored: "{bypass: [true]}", next: "{}"},
		{name: "stored beside a broken key still compares", stored: "{bypass: true, bogus: 1}", next: "{}", want: []string{"bypass"}},
		{name: "stored repeated key reads the first", stored: "{bypass: true, bypass: false}", next: "{bypass: true}"},
		{name: "stored repeated key, the first unreadable, counts as the default", stored: "{bypass: maybe, bypass: true}", next: "{}"},
		{name: "stored null string counts as absent", stored: "{listen: ~}", next: "{}"},
		{name: "stored alias counts as the default", stored: "{label: &b true, bypass: *b}", next: "{}"},
		{name: "stored anchored value counts as the default", stored: "{bypass: &b true}", next: "{}"},
		{name: "stored non-mapping counts as defaults", stored: "[bypass]", next: "{bypass: true}", want: []string{"bypass"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := s.BootOnlyChanges(section(t, c.stored), section(t, c.next))
			if got := errPaths(errs); !slices.Equal(got, c.want) && len(got)+len(c.want) > 0 {
				t.Errorf("paths %v, want %v", got, c.want)
			}
			for _, e := range errs {
				if e.Message != msgBootOnly {
					t.Errorf("%s: message %q, want %q", e.Path, e.Message, msgBootOnly)
				}
			}
		})
	}
}
