package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

func BenchmarkAuthenticate(b *testing.B) {
	store := newFakeSessionStore()
	ctx := context.Background()

	user := &api.User{Username: "bench", Role: api.RoleUser, Enabled: true}
	if err := store.CreateUser(ctx, user); err != nil {
		b.Fatal(err)
	}

	// Set up a valid session.
	token := "bench-session-token-value"
	hash := SessionHash(token)
	if err := store.CreateSession(ctx, &api.Session{
		UserID:       user.ID,
		TokenHash:    hash,
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
	}); err != nil {
		b.Fatal(err)
	}

	// Set up a valid API key.
	apiKeyRaw := "sfx_benchmarkapikey1234567890abcdef"
	apiKeyHash := APIKeyHash(apiKeyRaw)
	if err := store.CreateAPIKey(ctx, &api.Key{
		UserID:  user.ID,
		KeyHash: apiKeyHash,
		Label:   "bench-key",
	}); err != nil {
		b.Fatal(err)
	}

	auth := &Authenticator{
		Store:       store,
		IdleTimeout: 24 * time.Hour,
		AbsTimeout:  7 * 24 * time.Hour,
	}

	b.Run("session_cookie", func(b *testing.B) {
		req, _ := http.NewRequest(http.MethodGet, "/api/test", nil)
		req.AddCookie(&http.Cookie{Name: CookieNameHTTP, Value: token})
		b.ResetTimer()
		for range b.N {
			_, _, _ = auth.Authenticate(req)
		}
	})

	b.Run("api_key_header", func(b *testing.B) {
		req, _ := http.NewRequest(http.MethodGet, "/api/test", nil)
		req.Header.Set(api.HeaderXAPIKey, apiKeyRaw)
		b.ResetTimer()
		for range b.N {
			_, _, _ = auth.Authenticate(req)
		}
	})

	b.Run("no_credentials", func(b *testing.B) {
		req, _ := http.NewRequest(http.MethodGet, "/api/test", nil)
		b.ResetTimer()
		for range b.N {
			_, _, _ = auth.Authenticate(req)
		}
	})
}
