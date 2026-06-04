package api

import "testing"

func FuzzLangNameToISO(f *testing.F) {
	f.Add("english")
	f.Add("en")
	f.Add("")
	f.Add("ZZ")
	f.Add("日本語")
	f.Fuzz(func(t *testing.T, name string) {
		result := LangNameToISO(name)
		if result == "" {
			return
		}
		if len(result) != 2 {
			t.Errorf("result=%q is not 2 bytes", result)
		}
		if result[0] < 'a' || result[0] > 'z' || result[1] < 'a' || result[1] > 'z' {
			t.Errorf("result=%q is not lowercase ASCII", result)
		}
	})
}
