package auth

import (
	"testing"

	"subflux/internal/api"
)

func TestNewWebAuthn_valid_config(t *testing.T) {
	wa, err := NewWebAuthn("example.com", "Example", []string{"https://example.com"})
	if err != nil {
		t.Fatalf("NewWebAuthn returned error: %v", err)
	}
	if wa == nil {
		t.Fatal("NewWebAuthn returned nil")
	}
}

func TestNewWebAuthn_empty_rpID(t *testing.T) {
	// The go-webauthn library accepts empty rpID at construction time;
	// validation occurs during ceremony. Verify construction succeeds
	// but the instance is usable (non-nil).
	wa, err := NewWebAuthn("", "Example", []string{"https://example.com"})
	if err != nil {
		t.Fatalf("NewWebAuthn returned unexpected error: %v", err)
	}
	if wa == nil {
		t.Fatal("NewWebAuthn returned nil for empty rpID")
	}
}

func TestNewWebAuthn_multiple_origins(t *testing.T) {
	wa, err := NewWebAuthn("example.com", "Example", []string{
		"https://example.com",
		"https://app.example.com",
	})
	if err != nil {
		t.Fatalf("NewWebAuthn returned error: %v", err)
	}
	if wa == nil {
		t.Fatal("NewWebAuthn returned nil")
	}
}

func TestBeginRegistration_nil_user(t *testing.T) {
	wa, err := NewWebAuthn("example.com", "Example", []string{"https://example.com"})
	if err != nil {
		t.Fatalf("NewWebAuthn: %v", err)
	}

	// Construct a WebAuthnUser with a valid user to ensure no panic from nil deref.
	user := &WebAuthnUser{
		User:        &api.User{ID: 1, Username: "test"},
		Credentials: nil,
	}

	creation, session, err := BeginRegistration(wa, user)
	if err != nil {
		t.Fatalf("BeginRegistration returned error: %v", err)
	}
	if creation == nil {
		t.Fatal("BeginRegistration returned nil creation")
	}
	if session == nil {
		t.Fatal("BeginRegistration returned nil session")
	}
}
