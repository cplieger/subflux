package release

import "testing"

// TestParsed gives the package a test binary, whose main imports it.
func TestParsed(t *testing.T) {
	if (Parsed{Group: "g"}).Group != "g" {
		t.Fatal("group lost")
	}
}
