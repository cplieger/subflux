package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestBinaryLinksNoRegexp2 keeps the .NET-flavour regexp2 engine, the
// release patterns' test oracle, out of the server binary.
func TestBinaryLinksNoRegexp2(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for dep := range strings.Lines(string(out)) {
		if strings.Contains(dep, "dlclark/regexp2") {
			t.Errorf("the binary links %s", strings.TrimSpace(dep))
		}
	}
}
