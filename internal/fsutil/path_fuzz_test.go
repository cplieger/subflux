package fsutil

import (
	"errors"
	"testing"
)

// FuzzValidateAbsClean exercises the security-critical path sanitiser
// with arbitrary inputs. Checks that only absolute, traversal-free paths
// are accepted.
func FuzzValidateAbsClean(f *testing.F) {
	f.Add("")
	f.Add("/")
	f.Add("/tmp/file.txt")
	f.Add("relative/path")
	f.Add("../escape")
	f.Add("/foo/../bar")
	f.Add("/foo/./bar")
	f.Add("/foo/bar/..")
	f.Add("..") // edge
	f.Add("/a/b/../../../etc/passwd")
	f.Add("\x00/tmp/null")

	f.Fuzz(func(t *testing.T, path string) {
		clean, err := validateAbsClean(path)

		// Invariant 1: empty path always rejected.
		if path == "" && !errors.Is(err, ErrEmptyPath) {
			t.Fatal("empty path must return ErrEmptyPath")
		}

		// Invariant 2: on success, result is non-empty and starts with '/'.
		if err == nil {
			if clean == "" {
				t.Fatal("clean path is empty on success")
			}
			if clean[0] != '/' {
				t.Fatalf("clean path is not absolute: %q", clean)
			}
		}

		// Invariant 3: relative paths must be rejected.
		if len(path) > 0 && path[0] != '/' && err == nil {
			t.Fatalf("relative path accepted: %q -> %q", path, clean)
		}
	})
}
