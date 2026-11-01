package language

import "testing"

func FuzzFromName(f *testing.F) {
	for _, r := range arrNames(f) {
		f.Add(r.name)
	}
	for _, s := range seedInputs {
		f.Add(s)
	}
	for name := range otherEnglishNames {
		f.Add(name)
	}
	f.Fuzz(func(t *testing.T, name string) {
		tag, ok := FromName(name)
		if !ok {
			if tag != (Tag{}) {
				t.Fatalf("FromName(%q) refused with a non-zero tag %q", name, tag)
			}
			return
		}
		if tag == (Tag{}) {
			t.Fatalf("FromName(%q) answered the zero tag and true", name)
		}
		if again, ok := Parse(tag.String()); !ok || again != tag {
			t.Fatalf("FromName(%q) = %q; Parse of its text = %q, %v", name, tag, again, ok)
		}
	})
}
