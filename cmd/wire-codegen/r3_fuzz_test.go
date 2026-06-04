package main

import "testing"

func FuzzPathName(f *testing.F) {
	f.Add("SearchResponse")
	f.Add("UserMeResponse")
	f.Add("OIDCConfig")
	f.Add("")
	f.Add("ABCDef")
	f.Add("aBC")

	f.Fuzz(func(t *testing.T, goName string) {
		result := pathName(goName)
		// pathName converts CamelCase to snake_case; output must be lowercase
		for _, r := range result {
			if r >= 'A' && r <= 'Z' {
				t.Errorf("pathName(%q) = %q contains uppercase", goName, result)
				break
			}
		}
	})
}

func FuzzEnumConstName(f *testing.F) {
	f.Add("MediaType")
	f.Add("ScoreTier")
	f.Add("")
	f.Add("ABC")
	f.Add("aBc")

	f.Fuzz(func(t *testing.T, goName string) {
		result := enumConstName(goName)
		// Must end with "S" suffix
		if len(result) == 0 {
			if goName != "" {
				t.Errorf("enumConstName(%q) returned empty", goName)
			}
			return
		}
		if result[len(result)-1] != 'S' {
			t.Errorf("enumConstName(%q) = %q, does not end with S", goName, result)
		}
	})
}

func FuzzSanitizeVarName(f *testing.F) {
	f.Add("media_type")
	f.Add("score_no_hash")
	f.Add("")
	f.Add("return")
	f.Add("class")
	f.Add("_leading")
	f.Add("a_b_c")

	f.Fuzz(func(t *testing.T, wireName string) {
		result := sanitizeVarName(wireName)
		// Must not be a JS reserved word (those get "Val" suffix)
		reserved := map[string]bool{
			"o": true, "out": true, "v": true, "private": true,
			"public": true, "protected": true, "class": true,
			"return": true, "delete": true, "default": true,
			"export": true, "import": true, "new": true, "this": true,
		}
		if reserved[result] {
			t.Errorf("sanitizeVarName(%q) = %q is still a reserved word", wireName, result)
		}
		// Verify no panic on arbitrary input; semantic checks above suffice.
		_ = result
		// Verify no panic on arbitrary input; semantic checks above suffice.
		// Wire names from Go JSON tags always start with a letter, but we don't
		// restrict fuzz input to that subset - the no-panic guarantee is the point.
		_ = result
	})
}
