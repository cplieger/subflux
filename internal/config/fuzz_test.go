package config

import (
	"context"
	"testing"
)

func FuzzLoadFromBytes(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("sonarr:\n  url: http://localhost:8989\n  api_key: abc123\n"))
	f.Add([]byte("poll_interval: 5m\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic regardless of input.
		_, _ = LoadFromBytes(context.Background(), data)
	})
}

func FuzzParseDuration(f *testing.F) {
	f.Add("5m")
	f.Add("1h")
	f.Add("7D")
	f.Add("3M")
	f.Add("1Y")
	f.Add("")
	f.Add("invalid")
	f.Fuzz(func(t *testing.T, s string) {
		// Must not panic regardless of input.
		_, _ = ParseDuration(s)
	})
}
