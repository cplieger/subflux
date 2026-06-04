package cliparse

import (
	"slices"
	"testing"
)

func FuzzParseArgs(f *testing.F) {
	f.Add("--lang en --season 1")
	f.Add("--download")
	f.Add("")
	f.Add("--flag")
	f.Add("-- --value")
	f.Fuzz(func(t *testing.T, raw string) {
		var args []string
		if raw != "" {
			// Split on spaces for simplicity
			cur := ""
			for _, c := range raw {
				if c == ' ' {
					if cur != "" {
						args = append(args, cur)
						cur = ""
					}
				} else {
					cur += string(c)
				}
			}
			if cur != "" {
				args = append(args, cur)
			}
		}
		params, dl := ParseArgs(args)
		if params == nil {
			t.Fatal("params should never be nil")
		}
		for k := range params {
			if k == "" {
				t.Error("empty key in params")
			}
		}
		// download flag consistency
		found := slices.Contains(args, "--download")
		if found != dl {
			t.Errorf("download mismatch: found=%v dl=%v", found, dl)
		}
	})
}

func FuzzEditDistance(f *testing.F) {
	f.Add("abc", "abc")
	f.Add("", "hello")
	f.Add("kitten", "sitting")
	f.Add("a", "b")
	f.Fuzz(func(t *testing.T, a, b string) {
		d := editDistance(a, b)
		if d < 0 {
			t.Errorf("editDistance(%q,%q) = %d < 0", a, b, d)
		}
		if editDistance(a, a) != 0 {
			t.Errorf("editDistance(%q,%q) != 0", a, a)
		}
		if a == "" && d != len(b) {
			t.Errorf("editDistance(\"\",%q) = %d, want %d", b, d, len(b))
		}
		// symmetry
		dba := editDistance(b, a)
		if d != dba {
			t.Errorf("editDistance(%q,%q)=%d != editDistance(%q,%q)=%d", a, b, d, b, a, dba)
		}
	})
}

func FuzzValidate(f *testing.F) {
	f.Add("--lang en --season 1")
	f.Add("--unknown foo")
	f.Add("")
	f.Add("--help")
	spec := &Spec{
		Name: "test",
		Flags: []Flag{
			{Name: "lang", Type: "string"},
			{Name: "season", Type: "int"},
			{Name: "timeout", Type: "duration"},
		},
	}
	f.Fuzz(func(t *testing.T, raw string) {
		var args []string
		if raw != "" {
			cur := ""
			for _, c := range raw {
				if c == ' ' {
					if cur != "" {
						args = append(args, cur)
						cur = ""
					}
				} else {
					cur += string(c)
				}
			}
			if cur != "" {
				args = append(args, cur)
			}
		}
		params, _ := ParseArgs(args)
		// Should never panic
		_ = Validate(args, params, spec)
	})
}
