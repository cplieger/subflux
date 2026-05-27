package auth

import (
	"bytes"
	"errors"
	"testing"

	"pgregory.net/rapid"
)

// Feature: subflux-authentication, Property 6: TOTP secret encryption round-trip
// **Validates: Requirements 2.4, 16.4**
func TestProperty_TOTPSecretEncryptionRoundTrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		plaintext := rapid.SliceOfN(rapid.Byte(), 1, 1024).Draw(t, "plaintext")
		key := rapid.SliceOfN(rapid.Byte(), 32, 32).Draw(t, "key")

		// Encrypt then decrypt must produce original plaintext.
		ciphertext, err := Encrypt(plaintext, key)
		if err != nil {
			t.Fatalf("Encrypt error: %v", err)
		}

		decrypted, err := Decrypt(ciphertext, key)
		if err != nil {
			t.Fatalf("Decrypt error: %v", err)
		}

		if !bytes.Equal(decrypted, plaintext) {
			t.Fatalf("round-trip mismatch: got %x, want %x", decrypted, plaintext)
		}

		// Decrypt with a different key must fail.
		wrongKey := make([]byte, 32)
		copy(wrongKey, key)
		wrongKey[0] ^= 0xFF
		if _, err := Decrypt(ciphertext, wrongKey); err == nil {
			t.Fatalf("Decrypt with wrong key should fail")
		}

		// Encrypt same plaintext twice must produce different ciphertext (random nonce).
		ciphertext2, err := Encrypt(plaintext, key)
		if err != nil {
			t.Fatalf("Encrypt(2) error: %v", err)
		}

		if bytes.Equal(ciphertext, ciphertext2) {
			t.Fatalf("two encryptions of same plaintext produced identical ciphertext")
		}
	})
}

func TestEncrypt_rejects_invalid_key_length(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		keyLen int
	}{
		{name: "empty", keyLen: 0},
		{name: "too_short_16", keyLen: 16},
		{name: "too_long_64", keyLen: 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := make([]byte, tc.keyLen)
			_, err := Encrypt([]byte("hello"), key)
			if err == nil {
				t.Fatalf("Encrypt(key len %d) = nil error, want ErrInvalidKeyLength", tc.keyLen)
			}
			if !errors.Is(err, ErrInvalidKeyLength) {
				t.Fatalf("Encrypt(key len %d) error = %v, want ErrInvalidKeyLength", tc.keyLen, err)
			}
		})
	}
}

func TestDecrypt_rejects_invalid_key_length(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		keyLen int
	}{
		{name: "empty", keyLen: 0},
		{name: "too_short_16", keyLen: 16},
		{name: "too_long_64", keyLen: 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := make([]byte, tc.keyLen)
			_, err := Decrypt([]byte("some-ciphertext"), key)
			if err == nil {
				t.Fatalf("Decrypt(key len %d) = nil error, want ErrInvalidKeyLength", tc.keyLen)
			}
			if !errors.Is(err, ErrInvalidKeyLength) {
				t.Fatalf("Decrypt(key len %d) error = %v, want ErrInvalidKeyLength", tc.keyLen, err)
			}
		})
	}
}

func TestDecrypt_rejects_short_ciphertext(t *testing.T) {
	t.Parallel()
	key := make([]byte, 32)
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "empty", data: []byte{}},
		{name: "one_byte", data: []byte{0x01}},
		{name: "eleven_bytes", data: make([]byte, 11)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Decrypt(tc.data, key)
			if err == nil {
				t.Fatalf("Decrypt(%d bytes) = nil error, want ciphertext too short", len(tc.data))
			}
		})
	}
}

func TestDecrypt_rejects_tampered_ciphertext(t *testing.T) {
	t.Parallel()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	plaintext := []byte("sensitive data")
	ciphertext, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}

	for _, tc := range []struct {
		tamper func([]byte) []byte
		name   string
	}{
		{name: "flip_last_byte", tamper: func(ct []byte) []byte {
			out := make([]byte, len(ct))
			copy(out, ct)
			out[len(out)-1] ^= 0xFF
			return out
		}},
		{name: "flip_middle_byte", tamper: func(ct []byte) []byte {
			out := make([]byte, len(ct))
			copy(out, ct)
			out[len(out)/2] ^= 0xFF
			return out
		}},
		{name: "truncate_one_byte", tamper: func(ct []byte) []byte {
			out := make([]byte, len(ct)-1)
			copy(out, ct)
			return out
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tampered := tc.tamper(ciphertext)
			_, err := Decrypt(tampered, key)
			if err == nil {
				t.Fatalf("Decrypt(tampered[%s]) = nil error, want authentication failure", tc.name)
			}
		})
	}
}
