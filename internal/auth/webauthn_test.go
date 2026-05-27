package auth

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"subflux/internal/api"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"pgregory.net/rapid"
)

// Feature: subflux-authentication, Property 9: WebAuthn credential storage round-trip
// **Validates: Requirements 3.5**
func TestProperty_WebAuthnCredentialStorageRoundTrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		db := newFakeSessionStore()
		ctx := context.Background()

		// Create a user to own the passkey.
		user := &api.User{
			Username:     "webauthn-user",
			PasswordHash: "dummy",
			Role:         "admin",
			Enabled:      true,
		}
		if err := db.CreateUser(ctx, user); err != nil {
			rt.Fatalf("CreateUser: %v", err)
		}

		// Generate random credential data.
		credID := rapid.SliceOfN(rapid.Byte(), 16, 64).Draw(rt, "credentialID")
		pubKey := rapid.SliceOfN(rapid.Byte(), 32, 128).Draw(rt, "publicKey")
		signCount := rapid.Uint32().Draw(rt, "signCount")
		name := rapid.StringN(1, 50, -1).Draw(rt, "name")

		// Generate a valid 16-byte AAGUID.
		aaguid := rapid.SliceOfN(rapid.Byte(), 16, 16).Draw(rt, "aaguid")

		// Pick a transport from common values.
		transport := rapid.SampledFrom([]string{
			"", "usb", "nfc", "ble", "internal", "usb,nfc",
		}).Draw(rt, "transport")

		// Generate random flags.
		backupEligible := rapid.Bool().Draw(rt, "backupEligible")
		backupState := rapid.Bool().Draw(rt, "backupState")
		userPresent := rapid.Bool().Draw(rt, "userPresent")
		userVerified := rapid.Bool().Draw(rt, "userVerified")

		// Generate optional raw attestation data.
		rawAttestation := rapid.SliceOfN(rapid.Byte(), 0, 256).Draw(rt, "rawAttestation")
		if len(rawAttestation) == 0 {
			rawAttestation = nil
		}

		cred := &api.PasskeyCredential{
			UserID:          user.ID,
			CredentialID:    credID,
			PublicKey:       pubKey,
			AAGUID:          aaguid,
			AttestationType: "none",
			Transport:       transport,
			SignCount:       signCount,
			Name:            name,
			BackupEligible:  backupEligible,
			BackupState:     backupState,
			UserPresent:     userPresent,
			UserVerified:    userVerified,
			RawAttestation:  rawAttestation,
		}

		// Store in DB.
		if err := db.CreatePasskey(ctx, cred); err != nil {
			rt.Fatalf("CreatePasskey: %v", err)
		}

		// Retrieve by credential ID.
		got, err := db.GetPasskeyByCredentialID(ctx, credID)
		if err != nil {
			rt.Fatalf("GetPasskeyByCredentialID: %v", err)
		}
		if got == nil {
			rt.Fatal("GetPasskeyByCredentialID returned nil")
			return // unreachable; satisfies staticcheck SA5011
		}

		// Verify all fields match exactly.
		if !bytes.Equal(got.CredentialID, credID) {
			rt.Fatalf("CredentialID mismatch: got %x, want %x", got.CredentialID, credID)
		}
		if !bytes.Equal(got.PublicKey, pubKey) {
			rt.Fatalf("PublicKey mismatch: got %x, want %x", got.PublicKey, pubKey)
		}
		if !bytes.Equal(got.AAGUID, aaguid) {
			rt.Fatalf("AAGUID mismatch: got %x, want %x", got.AAGUID, aaguid)
		}
		if got.SignCount != signCount {
			rt.Fatalf("SignCount mismatch: got %d, want %d", got.SignCount, signCount)
		}
		if got.Transport != transport {
			rt.Fatalf("Transport mismatch: got %q, want %q", got.Transport, transport)
		}
		if got.Name != name {
			rt.Fatalf("Name mismatch: got %q, want %q", got.Name, name)
		}
		if got.UserID != user.ID {
			rt.Fatalf("UserID mismatch: got %d, want %d", got.UserID, user.ID)
		}
		if got.AttestationType != "none" {
			rt.Fatalf("AttestationType mismatch: got %q, want %q", got.AttestationType, "none")
		}
		if got.BackupEligible != backupEligible {
			rt.Fatalf("BackupEligible mismatch: got %v, want %v", got.BackupEligible, backupEligible)
		}
		if got.BackupState != backupState {
			rt.Fatalf("BackupState mismatch: got %v, want %v", got.BackupState, backupState)
		}
		if got.UserPresent != userPresent {
			rt.Fatalf("UserPresent mismatch: got %v, want %v", got.UserPresent, userPresent)
		}
		if got.UserVerified != userVerified {
			rt.Fatalf("UserVerified mismatch: got %v, want %v", got.UserVerified, userVerified)
		}
		if !bytes.Equal(got.RawAttestation, rawAttestation) {
			rt.Fatalf("RawAttestation mismatch: got %x, want %x", got.RawAttestation, rawAttestation)
		}
	})
}

// TestPasskeyFriendlyName verifies known/unknown AAGUID mapping and dedup numbering.
func TestPasskeyFriendlyName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		aaguid   string
		want     string
		existing []string
	}{
		{name: "known_aaguid", aaguid: "adce0002-35bc-c60a-648b-0b25f1f05503", existing: nil, want: "Chrome on Mac"},
		{name: "unknown_aaguid", aaguid: "ffffffff-ffff-ffff-ffff-ffffffffffff", existing: nil, want: "Passkey 1"},
		{name: "duplicate_first", aaguid: "adce0002-35bc-c60a-648b-0b25f1f05503", existing: nil, want: "Chrome on Mac"},
		{name: "duplicate_second", aaguid: "adce0002-35bc-c60a-648b-0b25f1f05503", existing: []string{"Chrome on Mac"}, want: "Chrome on Mac 2"},
		{name: "duplicate_third", aaguid: "adce0002-35bc-c60a-648b-0b25f1f05503", existing: []string{"Chrome on Mac", "Chrome on Mac 2"}, want: "Chrome on Mac 3"},
		{name: "unknown_duplicates", aaguid: "ffffffff-ffff-ffff-ffff-ffffffffffff", existing: []string{"Passkey 1"}, want: "Passkey 2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			aaguid := parseAAGUID(tc.aaguid)
			if aaguid == nil {
				t.Fatalf("parseAAGUID(%q) returned nil", tc.aaguid)
			}
			got := PasskeyFriendlyName(aaguid, tc.existing)
			if got != tc.want {
				t.Errorf("PasskeyFriendlyName(%q, %v) = %q, want %q", tc.aaguid, tc.existing, got, tc.want)
			}
		})
	}
}

func TestWebAuthnUser_interface_methods(t *testing.T) {
	t.Parallel()
	tests := []struct {
		user            *api.User
		name            string
		wantName        string
		wantDisplayName string
		creds           []api.PasskeyCredential
		wantCredCount   int
	}{
		{
			name:            "display_name_set",
			user:            &api.User{ID: 42, Username: "alice", DisplayName: "Alice Smith"},
			creds:           nil,
			wantName:        "alice",
			wantDisplayName: "Alice Smith",
			wantCredCount:   0,
		},
		{
			name:            "display_name_empty_falls_back_to_username",
			user:            &api.User{ID: 1, Username: "bob", DisplayName: ""},
			creds:           nil,
			wantName:        "bob",
			wantDisplayName: "bob",
			wantCredCount:   0,
		},
		{
			name: "with_credentials",
			user: &api.User{ID: 7, Username: "carol", DisplayName: "Carol"},
			creds: []api.PasskeyCredential{
				{CredentialID: []byte{1, 2, 3}, PublicKey: []byte{4, 5, 6}},
				{CredentialID: []byte{7, 8, 9}, PublicKey: []byte{10, 11, 12}},
			},
			wantName:        "carol",
			wantDisplayName: "Carol",
			wantCredCount:   2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u := &WebAuthnUser{User: tt.user, Credentials: tt.creds}

			if got := u.WebAuthnName(); got != tt.wantName {
				t.Errorf("WebAuthnName() = %q, want %q", got, tt.wantName)
			}
			if got := u.WebAuthnDisplayName(); got != tt.wantDisplayName {
				t.Errorf("WebAuthnDisplayName() = %q, want %q", got, tt.wantDisplayName)
			}
			gotCreds := u.WebAuthnCredentials()
			if len(gotCreds) != tt.wantCredCount {
				t.Errorf("WebAuthnCredentials() count = %d, want %d", len(gotCreds), tt.wantCredCount)
			}
		})
	}
}

func TestWebAuthnUser_WebAuthnID_encodes_varint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		id   int64
	}{
		{name: "zero", id: 0},
		{name: "positive", id: 42},
		{name: "large", id: 1<<32 - 1},
		{name: "negative", id: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u := &WebAuthnUser{User: &api.User{ID: tt.id}}
			got := u.WebAuthnID()
			if len(got) == 0 {
				t.Fatalf("WebAuthnID() returned empty slice for ID %d", tt.id)
			}
			decoded, n := binary.Varint(got)
			if n <= 0 {
				t.Fatalf("binary.Varint failed on WebAuthnID() output for ID %d", tt.id)
			}
			if decoded != tt.id {
				t.Errorf("WebAuthnID() round-trip: got %d, want %d", decoded, tt.id)
			}
		})
	}
}

func TestFormatAAGUID_edge_cases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		want  string
		input []byte
	}{
		{name: "nil", input: nil, want: ""},
		{name: "empty", input: []byte{}, want: ""},
		{name: "too_short", input: []byte{0x01, 0x02}, want: ""},
		{name: "too_long", input: make([]byte, 17), want: ""},
		{name: "valid_zeros", input: make([]byte, 16), want: "00000000-0000-0000-0000-000000000000"},
		{name: "valid_known", input: parseAAGUID("adce0002-35bc-c60a-648b-0b25f1f05503"), want: "adce0002-35bc-c60a-648b-0b25f1f05503"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatAAGUID(tt.input)
			if got != tt.want {
				t.Errorf("formatAAGUID(%x) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseAAGUID_edge_cases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		wantNil bool
	}{
		{name: "valid", input: "adce0002-35bc-c60a-648b-0b25f1f05503", wantNil: false},
		{name: "all_zeros", input: "00000000-0000-0000-0000-000000000000", wantNil: false},
		{name: "invalid_hex", input: "zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz", wantNil: true},
		{name: "too_short", input: "adce0002-35bc", wantNil: true},
		{name: "empty", input: "", wantNil: true},
		{name: "no_dashes", input: "adce000235bcc60a648b0b25f1f05503", wantNil: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseAAGUID(tt.input)
			if tt.wantNil && got != nil {
				t.Errorf("parseAAGUID(%q) = %x, want nil", tt.input, got)
			}
			if !tt.wantNil && got == nil {
				t.Errorf("parseAAGUID(%q) = nil, want non-nil", tt.input)
			}
		})
	}
}

func TestAPICredentialToWebAuthn_table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		transport      string
		wantTransports int
	}{
		{name: "no_transport", transport: "", wantTransports: 0},
		{name: "single_transport", transport: "internal", wantTransports: 1},
		{name: "multi_transport", transport: "usb,nfc,ble", wantTransports: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cred := &api.PasskeyCredential{
				CredentialID:    []byte{1, 2, 3},
				PublicKey:       []byte{4, 5, 6},
				AAGUID:          make([]byte, 16),
				AttestationType: "none",
				Transport:       tt.transport,
				SignCount:       42,
				BackupEligible:  true,
				BackupState:     false,
				UserPresent:     true,
				UserVerified:    true,
			}
			got := APICredentialToWebAuthn(cred)
			if len(got.Transport) != tt.wantTransports {
				t.Errorf("transport count = %d, want %d", len(got.Transport), tt.wantTransports)
			}
			if !bytes.Equal(got.ID, cred.CredentialID) {
				t.Errorf("CredentialID mismatch: got %x, want %x", got.ID, cred.CredentialID)
			}
			if !bytes.Equal(got.PublicKey, cred.PublicKey) {
				t.Errorf("PublicKey mismatch")
			}
			if got.Authenticator.SignCount != 42 {
				t.Errorf("SignCount = %d, want 42", got.Authenticator.SignCount)
			}
			if !got.Flags.UserPresent {
				t.Error("UserPresent = false, want true")
			}
			if !got.Flags.UserVerified {
				t.Error("UserVerified = false, want true")
			}
			if !got.Flags.BackupEligible {
				t.Error("BackupEligible = false, want true")
			}
			if got.Flags.BackupState {
				t.Error("BackupState = true, want false")
			}
		})
	}
}

func TestAPICredentialToWebAuthn_corrupted_attestation(t *testing.T) {
	t.Parallel()
	cred := &api.PasskeyCredential{
		CredentialID:   []byte{1},
		PublicKey:      []byte{2},
		AAGUID:         make([]byte, 16),
		RawAttestation: []byte("not valid json"),
	}
	// Should not panic; corrupted attestation is non-fatal.
	got := APICredentialToWebAuthn(cred)
	if !bytes.Equal(got.ID, []byte{1}) {
		t.Errorf("CredentialID mismatch after corrupted attestation")
	}
}

func TestWebAuthnCredentialToAPI_table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		wantTransport string
		transports    []protocol.AuthenticatorTransport
	}{
		{name: "no_transports", transports: nil, wantTransport: ""},
		{name: "single", transports: []protocol.AuthenticatorTransport{"internal"}, wantTransport: "internal"},
		{name: "multi", transports: []protocol.AuthenticatorTransport{"usb", "nfc"}, wantTransport: "usb,nfc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			waCred := &webauthn.Credential{
				ID:        []byte{10, 20},
				PublicKey: []byte{30, 40},
				Transport: tt.transports,
				Flags: webauthn.CredentialFlags{
					UserPresent:  true,
					UserVerified: false,
				},
				Authenticator: webauthn.Authenticator{
					AAGUID:    make([]byte, 16),
					SignCount: 99,
				},
			}
			got := WebAuthnCredentialToAPI(waCred, 7, "test-key")
			if got.Transport != tt.wantTransport {
				t.Errorf("Transport = %q, want %q", got.Transport, tt.wantTransport)
			}
			if got.UserID != 7 {
				t.Errorf("UserID = %d, want 7", got.UserID)
			}
			if got.Name != "test-key" {
				t.Errorf("Name = %q, want %q", got.Name, "test-key")
			}
			if got.SignCount != 99 {
				t.Errorf("SignCount = %d, want 99", got.SignCount)
			}
			if !got.UserPresent {
				t.Error("UserPresent = false, want true")
			}
			if got.UserVerified {
				t.Error("UserVerified = true, want false")
			}
		})
	}
}

func TestProperty_CredentialConversionRoundTrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		credID := rapid.SliceOfN(rapid.Byte(), 1, 64).Draw(rt, "credentialID")
		pubKey := rapid.SliceOfN(rapid.Byte(), 1, 128).Draw(rt, "publicKey")
		aaguid := rapid.SliceOfN(rapid.Byte(), 16, 16).Draw(rt, "aaguid")
		signCount := rapid.Uint32().Draw(rt, "signCount")
		transport := rapid.SampledFrom([]string{
			"", "usb", "nfc", "ble", "internal", "usb,nfc",
		}).Draw(rt, "transport")
		backupEligible := rapid.Bool().Draw(rt, "backupEligible")
		backupState := rapid.Bool().Draw(rt, "backupState")
		userPresent := rapid.Bool().Draw(rt, "userPresent")
		userVerified := rapid.Bool().Draw(rt, "userVerified")
		name := rapid.StringN(1, 50, -1).Draw(rt, "name")
		userID := rapid.Int64Range(1, 1000).Draw(rt, "userID")

		original := &api.PasskeyCredential{
			UserID:          userID,
			CredentialID:    credID,
			PublicKey:       pubKey,
			AAGUID:          aaguid,
			AttestationType: "none",
			Transport:       transport,
			SignCount:       signCount,
			Name:            name,
			BackupEligible:  backupEligible,
			BackupState:     backupState,
			UserPresent:     userPresent,
			UserVerified:    userVerified,
		}

		// API -> WebAuthn -> API round-trip.
		waCred := APICredentialToWebAuthn(original)
		roundTripped := WebAuthnCredentialToAPI(&waCred, userID, name)

		if !bytes.Equal(roundTripped.CredentialID, original.CredentialID) {
			rt.Fatalf("CredentialID mismatch: got %x, want %x", roundTripped.CredentialID, original.CredentialID)
		}
		if !bytes.Equal(roundTripped.PublicKey, original.PublicKey) {
			rt.Fatalf("PublicKey mismatch")
		}
		if !bytes.Equal(roundTripped.AAGUID, original.AAGUID) {
			rt.Fatalf("AAGUID mismatch")
		}
		if roundTripped.SignCount != original.SignCount {
			rt.Fatalf("SignCount: got %d, want %d", roundTripped.SignCount, original.SignCount)
		}
		if roundTripped.Transport != original.Transport {
			rt.Fatalf("Transport: got %q, want %q", roundTripped.Transport, original.Transport)
		}
		if roundTripped.BackupEligible != original.BackupEligible {
			rt.Fatalf("BackupEligible: got %v, want %v", roundTripped.BackupEligible, original.BackupEligible)
		}
		if roundTripped.BackupState != original.BackupState {
			rt.Fatalf("BackupState: got %v, want %v", roundTripped.BackupState, original.BackupState)
		}
		if roundTripped.UserPresent != original.UserPresent {
			rt.Fatalf("UserPresent: got %v, want %v", roundTripped.UserPresent, original.UserPresent)
		}
		if roundTripped.UserVerified != original.UserVerified {
			rt.Fatalf("UserVerified: got %v, want %v", roundTripped.UserVerified, original.UserVerified)
		}
	})
}

func TestWebAuthnCredentialToAPI_with_attestation(t *testing.T) {
	t.Parallel()
	waCred := &webauthn.Credential{
		ID:        []byte{1, 2, 3},
		PublicKey: []byte{4, 5, 6},
		Transport: []protocol.AuthenticatorTransport{"internal"},
		Flags: webauthn.CredentialFlags{
			UserPresent:  true,
			UserVerified: true,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:    make([]byte, 16),
			SignCount: 10,
		},
		Attestation: webauthn.CredentialAttestation{
			Object:         []byte(`{"fmt":"none","attStmt":{}}`),
			ClientDataJSON: []byte(`{"type":"webauthn.create"}`),
		},
	}
	got := WebAuthnCredentialToAPI(waCred, 5, "attested-key")
	if got.RawAttestation == nil {
		t.Fatal("WebAuthnCredentialToAPI() RawAttestation = nil, want non-nil when attestation data present")
	}
	if got.UserID != 5 {
		t.Errorf("WebAuthnCredentialToAPI() UserID = %d, want 5", got.UserID)
	}
	if got.Name != "attested-key" {
		t.Errorf("WebAuthnCredentialToAPI() Name = %q, want %q", got.Name, "attested-key")
	}
	if got.Transport != "internal" {
		t.Errorf("WebAuthnCredentialToAPI() Transport = %q, want %q", got.Transport, "internal")
	}
}
