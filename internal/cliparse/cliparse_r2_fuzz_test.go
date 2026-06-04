package cliparse

import "testing"

func FuzzSuggestName(f *testing.F) {
	f.Add("serch", "search,status,state,scan")
	f.Add("", "search,status")
	f.Add("xyz", "")
	f.Add("scor", "score,scan,search")
	f.Add("locsk", "locks,lang,providers")

	f.Fuzz(func(t *testing.T, input, candidatesRaw string) {
		var candidates []string
		if candidatesRaw != "" {
			cur := ""
			for _, c := range candidatesRaw {
				if c == ',' {
					if cur != "" {
						candidates = append(candidates, cur)
						cur = ""
					}
				} else {
					cur += string(c)
				}
			}
			if cur != "" {
				candidates = append(candidates, cur)
			}
		}
		name, ok := SuggestName(input, candidates)
		if ok && name == "" {
			t.Error("SuggestName returned ok=true but empty name")
		}
		if !ok && name != "" {
			t.Error("SuggestName returned ok=false but non-empty name")
		}
	})
}

func FuzzHelpRequested(f *testing.F) {
	f.Add("--help")
	f.Add("-h")
	f.Add("--lang en --help")
	f.Add("")
	f.Add("--helper")

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
		got := HelpRequested(args)
		// Verify: if --help or -h is literally one of the args, result must be true
		for _, a := range args {
			if a == "--help" || a == "-h" {
				if !got {
					t.Errorf("HelpRequested(%v) = false, want true", args)
				}
				return
			}
		}
		if got {
			t.Errorf("HelpRequested(%v) = true, but no --help/-h found", args)
		}
	})
}
