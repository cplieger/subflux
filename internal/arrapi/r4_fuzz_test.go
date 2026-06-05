package arrapi

import "testing"

func FuzzCommandNameValid(f *testing.F) {
	f.Add("RescanSeries")
	f.Add("RescanMovie")
	f.Add("")
	f.Add("Unknown")
	f.Add("rescanSeries")

	f.Fuzz(func(t *testing.T, name string) {
		c := CommandName(name)
		result := c.Valid()
		// Property: only the two known commands are valid
		want := name == "RescanSeries" || name == "RescanMovie"
		if result != want {
			t.Errorf("CommandName(%q).Valid() = %v, want %v", name, result, want)
		}
	})
}
