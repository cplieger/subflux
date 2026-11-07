package e

import (
	"testing"

	"example.com/confinement/d"
)

func TestName(t *testing.T) {
	if d.Name != "d" {
		t.Fatal(d.Name)
	}
}
