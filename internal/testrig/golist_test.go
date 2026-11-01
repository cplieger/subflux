package testrig

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// moduleRoot is the subflux module root, two levels above this package.
const moduleRoot = "../.."

// listedPackage is the part of `go list -json` the module checks read.
type listedPackage struct {
	ImportPath string
	Standard   bool
	Imports    []string
	Module     *struct{ Path string }
	Dir        string
	GoFiles    []string
}

// goList runs `go list -deps -test -json` with args in dir and decodes every
// package it prints.
func goList(t *testing.T, dir string, args ...string) []listedPackage {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", append([]string{"list", "-deps", "-test", "-json"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list in %s: %v\n%s", dir, err, stderr.String())
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listedPackage
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			return pkgs
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
}

// absDir resolves a directory relative to this package for exec.
func absDir(t *testing.T, rel string) string {
	t.Helper()
	dir, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
