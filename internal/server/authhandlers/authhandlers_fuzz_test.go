package authhandlers

import (
	"net/http"
	"testing"
)

func FuzzClientIP(f *testing.F) {
	f.Add("192.168.1.1:8080")
	f.Add("[::1]:443")
	f.Add("127.0.0.1:0")
	f.Add("not-an-address")
	f.Add("")
	f.Add(":")
	f.Add("[fe80::1%25eth0]:80")

	f.Fuzz(func(t *testing.T, addr string) {
		r := &http.Request{RemoteAddr: addr}
		// ClientIP must not panic on arbitrary input.
		_ = ClientIP(r)
	})
}

func FuzzShardedCeremonyMap(f *testing.F) {
	f.Add("token-abc", "token-def")
	f.Add("", "x")
	f.Add("aaaa", "aaaa")

	f.Fuzz(func(t *testing.T, key1, key2 string) {
		sm := NewShardedCeremonyMap[string]()
		sm.Store(key1, "v1")
		sm.Store(key2, "v2")

		if v, ok := sm.Load(key1); ok && v != "v1" && key1 != key2 {
			t.Errorf("unexpected value for key1: %q", v)
		}

		sm.LoadAndDelete(key1)
		if _, ok := sm.Load(key1); ok && key1 != key2 {
			t.Error("key1 still present after delete")
		}
	})
}
