package fsutil

import (
	"testing"
)

func FuzzValidateAbsClean(f *testing.F) {
	f.Add("/tmp/test.txt")
	f.Add("")
	f.Add("relative/path")
	f.Add("/tmp/../etc/passwd")
	f.Add("/tmp/./foo/../bar")
	f.Add("/..")
	f.Add("/a/b/c/../../d")
	f.Fuzz(func(t *testing.T, path string) {
		// Must not panic.
		clean, err := validateAbsClean(path)
		if err == nil && clean == "" {
			t.Fatal("no error but empty clean path")
		}
	})
}
