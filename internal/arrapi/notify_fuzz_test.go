package arrapi

import "testing"

// FuzzCommandNameValid checks that CommandName.Valid accepts exactly the two
// known arr command names and rejects everything else.
func FuzzCommandNameValid(f *testing.F) {
	f.Add("RescanSeries")
	f.Add("RescanMovie")
	f.Add("")
	f.Add("Unknown")
	f.Add("rescanSeries")

	f.Fuzz(func(t *testing.T, name string) {
		got := CommandName(name).Valid()
		want := name == "RescanSeries" || name == "RescanMovie"
		if got != want {
			t.Errorf("CommandName(%q).Valid() = %v, want %v", name, got, want)
		}
	})
}
