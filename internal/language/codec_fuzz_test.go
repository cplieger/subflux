package language

import "testing"

func FuzzTagUnmarshalText_roundtrip(f *testing.F) {
	for _, s := range seedInputs {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		var tag Tag
		if err := tag.UnmarshalText([]byte(in)); err != nil {
			if tag != (Tag{}) {
				t.Fatalf("a refused UnmarshalText(%q) set %q", in, tag)
			}
			return
		}
		if tag.String() != in {
			t.Fatalf("UnmarshalText(%q) accepted non-canonical text as %q", in, tag)
		}
		b, err := tag.MarshalText()
		if err != nil || string(b) != in {
			t.Fatalf("MarshalText of %q = %q, %v", in, b, err)
		}
		if parsed, ok := Parse(in); !ok || parsed != tag {
			t.Fatalf("UnmarshalText(%q) = %q, Parse = %q, %v", in, tag, parsed, ok)
		}
	})
}
