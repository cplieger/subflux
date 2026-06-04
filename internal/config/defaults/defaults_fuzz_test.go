package defaults

import (
	"testing"
	"time"
)

func FuzzFormatDuration(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(time.Second))
	f.Add(int64(time.Minute))
	f.Add(int64(time.Hour))
	f.Add(int64(24 * time.Hour))
	f.Add(int64(730 * time.Hour))
	f.Add(int64(30 * time.Second))
	f.Add(int64(-time.Hour))

	f.Fuzz(func(t *testing.T, ns int64) {
		d := time.Duration(ns)
		result := FormatDuration(d)
		if result == "" {
			t.Fatal("FormatDuration returned empty string")
		}
	})
}
