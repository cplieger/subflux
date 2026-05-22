package auth

import (
	"crypto/rand"
	"fmt"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	// RecoveryCodeTotal is the number of recovery codes generated when TOTP
	// is enabled. Exposed so handlers can report it alongside the current
	// unused-count for UI "N of M remaining" messaging.
	RecoveryCodeTotal  = 8
	recoveryCodeLength = 8
)

// recoveryAlphabet is the set of characters used for recovery codes.
const recoveryAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// GenerateTOTPSecret generates a new TOTP secret for the given user.
// It returns the base32-encoded secret string and the otpauth:// URI.
func GenerateTOTPSecret(username, issuer string) (secret, uri string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: username,
		Algorithm:   otp.AlgorithmSHA1,
		Digits:      otp.DigitsSix,
		Period:      30,
	})
	if err != nil {
		return "", "", fmt.Errorf("auth: generate TOTP: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// ValidateTOTPCode validates a TOTP code against the given secret.
// The pquerna/otp library accepts codes within ±1 time step by default.
func ValidateTOTPCode(secret, code string) bool {
	return totp.Validate(code, secret)
}

// GenerateRecoveryCodes generates recovery codes and their SHA-256 hashes.
// Returns the plaintext codes (shown once to the user) and their hashes
// (stored in the database). Recovery codes are verified by hashing the
// user-provided code and matching against the stored hash in SQL — no
// Go-side VerifyRecoveryCode helper is needed.
func GenerateRecoveryCodes() (codes, hashes []string, err error) {
	codes = make([]string, RecoveryCodeTotal)
	hashes = make([]string, RecoveryCodeTotal)

	for i := range RecoveryCodeTotal {
		code, err := randomAlphanumeric(recoveryCodeLength)
		if err != nil {
			return nil, nil, fmt.Errorf("auth: generate recovery code: %w", err)
		}
		codes[i] = code
		hashes[i] = HexSHA256(code)
	}

	return codes, hashes, nil
}

// randomAlphanumeric generates a random string of the given length using
// lowercase alphanumeric characters. Uses rejection sampling to eliminate
// modular bias (256 is not evenly divisible by 36).
func randomAlphanumeric(length int) (string, error) {
	const maxValid = 256 - 256%len(recoveryAlphabet) // 252
	b := make([]byte, length)
	for i := range b {
		for {
			var r [1]byte
			if _, err := rand.Read(r[:]); err != nil {
				return "", err
			}
			if int(r[0]) < maxValid {
				b[i] = recoveryAlphabet[int(r[0])%len(recoveryAlphabet)]
				break
			}
		}
	}
	return string(b), nil
}
