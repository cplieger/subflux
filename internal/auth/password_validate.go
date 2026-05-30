package auth

import (
	"context"
	"crypto/sha1" //nolint:gosec // SHA-1 required by HIBP k-anonymity API (not for security)
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// PasswordMinLengthMultiFactor is the minimum password length when password
// login is not the sole sufficient factor (i.e. basic auth is disabled, so a
// password cannot authenticate on its own). NIST 800-63B Rev 4 allows shorter
// memorized secrets when they are not independently sufficient.
const PasswordMinLengthMultiFactor = 8

// PasswordMinLengthSolo is the minimum password length when password login is
// enabled and thus a sole sufficient factor. Per NIST 800-63B Rev 4 guidance.
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

// ValidatePasswordContext rejects passwords that trivially embed the username
// or the application name. Breach lists miss these context-specific weak
// passwords; NIST 800-63B Rev 4 recommends a context-specific blocklist.
func ValidatePasswordContext(password, username string) error {
	lower := strings.ToLower(password)
	if strings.Contains(lower, "subflux") {
		return errors.New("password must not contain the application name")
	}
	if len(username) >= 4 && strings.Contains(lower, strings.ToLower(username)) {
		return errors.New("password must not contain your username")
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
	// Add-Padding: true requests HIBP pad every response to 800-1000 entries
	// regardless of the real bucket size. Without padding, an attacker
	// observing the encrypted TLS response size can correlate it with the
	// hash-prefix bucket — leaking partial info about the password's SHA-1
	// prefix. Padding entries always have count=0 and are filtered below.
	// See https://www.troyhunt.com/enhancing-pwned-passwords-privacy-with-padding
	req.Header.Set("Add-Padding", "true")

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
		if len(parts) != 2 || !strings.EqualFold(parts[0], suffix) {
			continue
		}
		// Discard padding entries (count == 0). Per HIBP docs, padded
		// entries always have a 0 count and must be ignored to avoid a
		// synthetic suffix accidentally matching the user's hash.
		count, convErr := strconv.Atoi(strings.TrimSpace(parts[1]))
		if convErr != nil || count == 0 {
			continue
		}
		return true, nil
	}

	return false, nil
}
