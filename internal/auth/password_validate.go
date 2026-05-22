package auth

import (
	"context"
	"crypto/sha1" //nolint:gosec // SHA-1 required by HIBP k-anonymity API (not for security)
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// PasswordMinLengthMultiFactor is the minimum password length when a second
// factor (TOTP, passkey) is configured. NIST 800-63B Rev 4 allows shorter
// passwords when combined with a second factor.
const PasswordMinLengthMultiFactor = 8

// PasswordMinLengthSolo is the minimum password length when password is the
// sole authentication method. Per NIST 800-63B Rev 4 guidance.
const PasswordMinLengthSolo = 15

// hibpRequestTimeout is the HTTP request timeout for the Have I Been Pwned
// k-anonymity API. Kept short to avoid blocking login flows.
const hibpRequestTimeout = 5 * time.Second

// ValidatePasswordLength enforces minimum password length.
// If passwordOnly is true (password is the sole auth method), the minimum
// is 15 characters per NIST 800-63B Rev 4. Otherwise the minimum is 8.
func ValidatePasswordLength(password string, passwordOnly bool) error {
	minLen := PasswordMinLengthMultiFactor
	if passwordOnly {
		minLen = PasswordMinLengthSolo
	}
	if len([]rune(password)) < minLen {
		return fmt.Errorf("password must be at least %d characters", minLen)
	}
	return nil
}

// CheckBreachedPassword checks a password against the Have I Been Pwned
// Passwords API using k-anonymity. Returns true if the password has been
// found in a breach. On network error, returns false with nil error
// (fail open with warning log).
func CheckBreachedPassword(ctx context.Context, client *http.Client, password string) (bool, error) {
	hash := sha1.Sum([]byte(password)) //nolint:gosec // SHA-1 required by HIBP k-anonymity API
	hexHash := fmt.Sprintf("%X", hash)
	prefix := hexHash[:5]
	suffix := hexHash[5:]

	reqCtx, cancel := context.WithTimeout(ctx, hibpRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet,
		"https://api.pwnedpasswords.com/range/"+prefix, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("auth: create HIBP request: %w", err)
	}
	req.Header.Set("User-Agent", "Subflux-Auth")

	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("breached password check failed, allowing password", "error", err)
		return false, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("breached password check: unexpected status, allowing password",
			"status", resp.StatusCode)
		return false, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2 MB cap
	if err != nil {
		slog.Warn("breached password check: read response failed, allowing password", "error", err)
		return false, nil
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], suffix) {
			return true, nil
		}
	}

	return false, nil
}
